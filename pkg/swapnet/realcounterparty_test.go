package swapnet_test

import (
	"encoding/hex"
	"sync"
	"testing"
	"time"

	"obscura/pkg/swapnet"
	"obscura/pkg/swapsession"
	"obscura/pkg/swapd"
)

// TestMakerOnlyNodeSweeps proves the role-split capability design: a coordinator
// configured with ONLY a MakerCaps (Taker: nil — no funding wallet of its own) can
// still complete its side of a swap, receiving + sweeping the XNO. The taker runs on
// a SEPARATE coordinator with only TakerCaps. This is the real seller/buyer topology
// (seller = maker-only, buyer = taker-only) the in-node path must support.
func TestMakerOnlyNodeSweeps(t *testing.T) {
	sc := newSharedChain(t)
	nano := swapd.NewMockNano()

	makerTr := newPair("maker-addr")
	takerTr := newPair("taker-addr")

	// MAKER-ONLY coordinator: Taker is nil. It has no NewTakerOBX / XNOLocker — it can
	// never lock XNO, only sweep it. New() must accept this (a node can be maker-only).
	makerCoord, err := swapnet.New(swapnet.Config{
		Transport: makerTr, Maker: makerCaps{s: sc, nano: nano},
		Timeout: 20 * time.Second, Fee: testFee, StateDir: t.TempDir(),
		AcceptInit: acceptOffer(testOBX, testXNO),
		// Taker deliberately nil.
	})
	if err != nil {
		t.Fatalf("maker-only coord: %v", err)
	}
	defer makerCoord.Stop()

	// TAKER-ONLY coordinator: Maker is nil. It funds XNO from its own Nano backend.
	takerCoord, err := swapnet.New(swapnet.Config{
		Transport: takerTr, Taker: takerCaps{s: sc, nano: nano},
		Timeout: 20 * time.Second, Fee: testFee, StateDir: t.TempDir(),
		// Maker deliberately nil.
	})
	if err != nil {
		t.Fatalf("taker-only coord: %v", err)
	}
	defer takerCoord.Stop()

	takerTr.route("maker-addr", makerCoord)
	makerTr.route("taker-addr", takerCoord)

	sess, err := takerCoord.Take("maker-addr", testOBX, testXNO, testFee)
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	if err := sess.Wait(); err != nil {
		t.Fatalf("taker session: %v", err)
	}
	if !sess.Succeeded() {
		t.Fatal("taker did not claim the OBX")
	}
	// The maker-only node must still sweep the XNO.
	waitXNO(t, nano, "maker-xno-dest", testXNO)

	// And the taker's Session must surface the ON-CHAIN SwapKey (not empty), the join
	// the trade tape records under.
	if sess.SwapKey() == "" {
		t.Fatal("Session.SwapKey() empty after success — tape cannot join to the on-chain key")
	}
	if _, err := hex.DecodeString(sess.SwapKey()); err != nil {
		t.Fatalf("Session.SwapKey() not hex: %v", err)
	}
}

// TestAcceptInitRejectsFeeMismatch proves the in-band fee agreement: a taker whose
// Init carries a DIFFERENT fee than the maker's configured Fee is rejected by
// HandleInit — the maker funds NOTHING. (The Init fee is carried in-band so both
// sides provably agree on the number a co-signed claim spends.)
func TestAcceptInitRejectsFeeMismatch(t *testing.T) {
	sc := newSharedChain(t)
	nano := swapd.NewMockNano()

	tr := &memTransport{}
	coord, err := swapnet.New(swapnet.Config{
		Transport: tr, Maker: makerCaps{s: sc, nano: nano},
		Timeout: 1 * time.Second, Fee: testFee,
		// AcceptInit by amounts only (the fee guard lives in HandleInit), so we prove
		// the FEE check — not the amount check — is what stops the funding.
		AcceptInit: acceptOffer(testOBX, testXNO),
	})
	if err != nil {
		t.Fatalf("coord: %v", err)
	}
	defer coord.Stop()

	id := swapID(0x55)
	// taker uses a DIFFERENT fee than the maker's configured testFee.
	badTaker := swapsession.NewTaker(id, testOBX, testXNO, testFee+1, takerOBX{s: sc})
	env := &swapnet.Envelope{SwapID: id, Kind: swapnet.KindInit, Payload: badTaker.Init().Serialize()}
	coord.Deliver("peer", env.Serialize())
	time.Sleep(400 * time.Millisecond)

	// HandleInit rejects the fee mismatch BEFORE funding, so the maker must NEVER send
	// a MakerCommit (kind 2) or Funded (kind 3) — i.e. it funds NOTHING on-chain. An
	// advisory KindAbort is acceptable (the session was registered, then refused).
	for _, k := range tr.kinds() {
		if k == swapnet.KindMakerCommit || k == swapnet.KindFunded {
			t.Fatalf("fee-mismatch Init must not fund: maker sent funding-phase kind %d (all=%v)", k, tr.kinds())
		}
	}
}

// TestOnMakerDoneJoinsOnChainKey proves the maker-side settlement reconciliation
// hook fires with success=true and the ACTUAL on-chain SwapKey (the maker's Fund tx
// key, which the claim spends) — the join the maker node records its trade under so
// both books agree.
func TestOnMakerDoneJoinsOnChainKey(t *testing.T) {
	sc := newSharedChain(t)
	nano := swapd.NewMockNano()

	makerTr := newPair("maker-addr")
	takerTr := newPair("taker-addr")

	var (
		mu        sync.Mutex
		gotKey    string
		gotOK     bool
		gotCalls  int
	)
	makerCoord, err := swapnet.New(swapnet.Config{
		Transport: makerTr, Maker: makerCaps{s: sc, nano: nano},
		Timeout: 20 * time.Second, Fee: testFee, StateDir: t.TempDir(),
		AcceptInit: acceptOffer(testOBX, testXNO),
		OnMakerDone: func(s swapnet.MakerSettlement, success bool) {
			mu.Lock()
			gotKey, gotOK, gotCalls = s.SwapKey, success, gotCalls+1
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("maker coord: %v", err)
	}
	defer makerCoord.Stop()
	takerCoord, err := swapnet.New(swapnet.Config{
		Transport: takerTr, Taker: takerCaps{s: sc, nano: nano},
		Timeout: 20 * time.Second, Fee: testFee, StateDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("taker coord: %v", err)
	}
	defer takerCoord.Stop()

	takerTr.route("maker-addr", makerCoord)
	makerTr.route("taker-addr", takerCoord)

	sess, err := takerCoord.Take("maker-addr", testOBX, testXNO, testFee)
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	if err := sess.Wait(); err != nil {
		t.Fatalf("taker session: %v", err)
	}
	waitXNO(t, nano, "maker-xno-dest", testXNO)

	// give the maker's deferred hook a moment to fire after the sweep.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		calls := gotCalls
		mu.Unlock()
		if calls > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotCalls != 1 {
		t.Fatalf("OnMakerDone fired %d times, want exactly 1", gotCalls)
	}
	if !gotOK {
		t.Fatal("OnMakerDone success=false on a swept swap")
	}
	if gotKey == "" {
		t.Fatal("OnMakerDone SwapKey empty — cannot join the maker tape to the on-chain key")
	}
	// the maker's reported on-chain SwapKey must equal the taker Session's SwapKey
	// (both read the same Fund tx key) — the cross-node tape join.
	if gotKey != sess.SwapKey() {
		t.Fatalf("maker SwapKey %s != taker SwapKey %s — tape join would diverge", gotKey, sess.SwapKey())
	}
}

// TestOnMakerDoneReleasesOnAbort proves the abort path of the reconciliation hook:
// a taker that stalls (never locks XNO) drives the maker to refund, and OnMakerDone
// must fire with success=false so the node releases the offer reservation rather than
// leaving the offer permanently under-committed.
func TestOnMakerDoneReleasesOnAbort(t *testing.T) {
	sc := newSharedChain(t)
	nano := swapd.NewMockNano()

	tr := &memTransport{}
	var (
		mu      sync.Mutex
		ok      bool
		called  bool
	)
	coord, err := swapnet.New(swapnet.Config{
		Transport: tr, Maker: makerCaps{s: sc, nano: nano},
		Timeout: 500 * time.Millisecond, Fee: testFee, StateDir: t.TempDir(),
		AcceptInit: acceptOffer(testOBX, testXNO),
		OnMakerDone: func(_ swapnet.MakerSettlement, success bool) {
			mu.Lock()
			ok, called = success, true
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("coord: %v", err)
	}
	defer coord.Stop()

	id := swapID(0x66)
	taker := swapsession.NewTaker(id, testOBX, testXNO, testFee, takerOBX{s: sc})
	env := &swapnet.Envelope{SwapID: id, Kind: swapnet.KindInit, Payload: taker.Init().Serialize()}
	coord.Deliver("taker-peer", env.Serialize())

	// the maker funds, times out waiting for XNOLocked, refunds (mines forward to the
	// unlock height — several PoW blocks), then OnMakerDone fires with success=false.
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := called
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if !called {
		t.Fatal("OnMakerDone never fired on the abort/refund path")
	}
	if ok {
		t.Fatal("OnMakerDone success=true on an aborted (refunded) swap")
	}
}
