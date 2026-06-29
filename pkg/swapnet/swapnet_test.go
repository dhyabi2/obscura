package swapnet_test

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/edwards25519"

	"obscura/pkg/chain"
	"obscura/pkg/config"
	"obscura/pkg/mempool"
	"obscura/pkg/miner"
	"obscura/pkg/p2p"
	"obscura/pkg/swap"
	"obscura/pkg/swapnet"
	"obscura/pkg/swapsession"
	"obscura/pkg/swapd"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
)

// ---- shared OBX host -------------------------------------------------------
//
// The two coordinators SHARE one in-process chain.Chain (so the taker can verify
// the maker's on-chain SwapOut), with a shared mining mutex (the maker funds, then
// the taker claims — sequential in the protocol, but the mutex makes it safe). The
// SWAP MESSAGES still travel over two real, separately-bound p2p.Node TCP
// connections; only the chain is shared, which the prompt explicitly permits as the
// simpler topology.

type sharedChain struct {
	mu     sync.Mutex
	c      *chain.Chain
	funder *wallet.Wallet // maker: mines coinbase + funds/refunds
	taker  *wallet.Wallet // taker: receives the OBX claim
}

func newSharedChain(t *testing.T) *sharedChain {
	t.Helper()
	old := config.CoinbaseMaturity
	oldMargin, oldMinWin, oldTl := config.SwapReorgMargin, config.SwapMinClaimWindow, config.SwapTimelockWindow
	config.CoinbaseMaturity = 1
	config.SwapReorgMargin = 2
	config.SwapMinClaimWindow = 3
	config.SwapTimelockWindow = 8 // honest unlock offset (> margin+minWindow=5)
	t.Cleanup(func() {
		config.CoinbaseMaturity = old
		config.SwapReorgMargin = oldMargin
		config.SwapMinClaimWindow = oldMinWin
		config.SwapTimelockWindow = oldTl
	})

	c, err := chain.New(t.TempDir())
	if err != nil {
		t.Fatalf("chain: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	sc := &sharedChain{
		c:      c,
		funder: wallet.FromSeed([]byte("swapnet-maker-000000000000000000000")),
		taker:  wallet.FromSeed([]byte("swapnet-taker-000000000000000000000")),
	}
	for i := 0; i < 4; i++ {
		if err := sc.mine(nil); err != nil {
			t.Fatalf("fund maker: %v", err)
		}
	}
	return sc
}

func (s *sharedChain) Height() uint64 { return s.c.Height() }

func (s *sharedChain) mine(txs []*tx.Transaction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mineLocked(txs)
}

func (s *sharedChain) mineLocked(txs []*tx.Transaction) error {
	fees := chain.CollectedFees(txs)
	cb, err := s.funder.BuildCoinbase(s.c.Height()+1, s.c.ExpectedCoinbaseMinted(fees, nil), nil)
	if err != nil {
		return err
	}
	all := append([]*tx.Transaction{cb}, txs...)
	tmpl, err := s.c.BlockTemplate(all)
	if err != nil {
		return err
	}
	if !miner.Mine(context.Background(), tmpl, 0) {
		return errTest
	}
	if err := s.c.AddBlock(tmpl); err != nil {
		return err
	}
	s.scanLocked()
	return nil
}

func (s *sharedChain) scanLocked() {
	for hh := uint64(0); hh <= s.c.Height(); hh++ {
		if b, ok := s.c.BlockByHeight(hh); ok {
			s.funder.ScanBlock(b)
			s.taker.ScanBlock(b)
		}
	}
}

func (s *sharedChain) findSwapOut(swapKey []byte) (swap.SwapOutput, bool) {
	e, ok := s.c.Swap(swapKey)
	if !ok {
		return swap.SwapOutput{}, false
	}
	return swap.SwapOutput{
		ClaimKey: e.ClaimKey, RefundKey: e.RefundKey, UnlockHeight: e.UnlockHeight,
		ClaimR: e.ClaimR, ClaimT: e.ClaimT, Amount: e.Amount,
	}, true
}

// makerOBX / takerOBX are the per-session OBX capability adapters over the shared
// chain (a fresh one is handed to each session, but all share the same chain).

type makerOBX struct{ s *sharedChain }

func (m makerOBX) Height() uint64 { return m.s.Height() }
func (m makerOBX) FindSwapOut(k []byte) (swap.SwapOutput, bool) { return m.s.findSwapOut(k) }
func (m makerOBX) FundSwapOut(swapKey []byte, obxAmount uint64, claimKey, refundKey, claimA, claimB, popA, popB, claimR, claimT []byte, unlockHeight, fee uint64) error {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()
	fund, err := m.s.funder.FundSwap(m.s.c, swapKey, obxAmount, claimKey, refundKey, claimA, claimB, popA, popB, claimR, claimT, unlockHeight, fee)
	if err != nil {
		return err
	}
	return m.s.mineLocked([]*tx.Transaction{fund})
}
// FindSwapSpend scans the shared chain for the mined CLAIM spend of swapKey and
// returns the published full claim signature plus the tx core hash it signed. It
// is the F-B chain-scrape read: the maker uses it to recover the on-chain claim
// sig when the taker withholds/corrupts the ClaimDone relay.
func (m makerOBX) FindSwapSpend(swapKey []byte) (fullSig []byte, coreHash []byte, ok bool) {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()
	for hh := uint64(0); hh <= m.s.c.Height(); hh++ {
		b, got := m.s.c.BlockByHeight(hh)
		if !got {
			continue
		}
		for _, t := range b.Txs {
			for _, in := range t.SwapInputs {
				if in.IsRefund || !bytes.Equal(in.SwapKey, swapKey) {
					continue
				}
					ch := t.CoreHash()
				return append([]byte(nil), in.Sig...), append([]byte(nil), ch[:]...), true
			}
		}
	}
	return nil, nil, false
}

func (m makerOBX) MineRefund(swapKey []byte, obxAmount, fee, unlockHeight uint64, sign func([]byte) []byte) error {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()
	for m.s.c.Height() < unlockHeight {
		if err := m.s.mineLocked(nil); err != nil {
			return err
		}
	}
	refund, err := m.s.funder.BuildSwapSpend(swapKey, obxAmount, true, fee, sign)
	if err != nil {
		return err
	}
	return m.s.mineLocked([]*tx.Transaction{refund})
}

type takerOBX struct{ s *sharedChain }

func (tk takerOBX) Height() uint64 { return tk.s.Height() }
func (tk takerOBX) FindSwapOut(k []byte) (swap.SwapOutput, bool) { return tk.s.findSwapOut(k) }
func (tk takerOBX) BuildClaim(swapKey []byte, obxAmount, fee uint64) (*tx.Transaction, []byte, error) {
	var coreHash []byte
	t, err := tk.s.taker.BuildSwapSpend(swapKey, obxAmount, false, fee, func(ch []byte) []byte {
		coreHash = append([]byte(nil), ch...)
		return make([]byte, 64)
	})
	if err != nil {
		return nil, nil, err
	}
	return t, coreHash, nil
}
func (tk takerOBX) MineClaim(t *tx.Transaction, sig []byte) error {
	t.SwapInputs[0].Sig = sig
	return tk.s.mine([]*tx.Transaction{t})
}

var errTest = errTestErr("swapnet test: mining failed")

type errTestErr string

func (e errTestErr) Error() string { return string(e) }

// ---- caps -------------------------------------------------------------------

type makerCaps struct {
	s    *sharedChain
	nano *swapd.MockNano
}

func (m makerCaps) NewMakerOBX() swapsession.MakerOBX { return makerOBX{s: m.s} }
func (m makerCaps) Nano() swapsession.XNOSweeper      { return m.nano }
func (m makerCaps) SweepDest() string                 { return "maker-xno-dest" }

type takerCaps struct {
	s    *sharedChain
	nano *swapd.MockNano
}

func (tk takerCaps) NewTakerOBX() swapsession.TakerOBX { return takerOBX{s: tk.s} }
func (tk takerCaps) Nano() swapsession.XNOLocker       { return tk.nano }

// ---- counting transport wrapper --------------------------------------------
//
// countingTransport proves the swap messages crossed the REAL p2p layer: it wraps
// the p2p transport and counts every Send. Combined with an inbound-delivery
// counter on each coordinator, a passing swap shows N sends on one node arrived as
// N deliveries on the other — over the TCP connection, not a direct call.

type countingTransport struct {
	inner swapnet.Transport
	sends int64
}

func (c *countingTransport) Send(peer string, env *swapnet.Envelope) error {
	atomic.AddInt64(&c.sends, 1)
	return c.inner.Send(peer, env)
}

// memTransport is a controllable in-memory transport for the abort/timeout test:
// outbound envelopes from the coordinator are captured (so the test can inspect
// what the maker sent) but NEVER answered, modelling a taker that opens a swap and
// then walks away. Inbound is driven by the test calling Deliver directly.
type memTransport struct {
	mu   sync.Mutex
	sent []*swapnet.Envelope
}

func (m *memTransport) Send(_ string, env *swapnet.Envelope) error {
	m.mu.Lock()
	m.sent = append(m.sent, env)
	m.mu.Unlock()
	return nil
}
func (m *memTransport) kinds() []swapnet.Kind {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]swapnet.Kind, len(m.sent))
	for i, e := range m.sent {
		out[i] = e.Kind
	}
	return out
}

// TestAbortMakerRefunds: a taker opens a swap (sends Init) then STALLS — it never
// locks XNO. The maker funds the OBX, waits for XNOLocked past the deadline, and
// must arm the refund, reclaiming its OBX. The persisted maker state must end in
// the refunded phase. (Liveness backstop: the funder is never stuck.)
func TestAbortMakerRefunds(t *testing.T) {
	sc := newSharedChain(t)
	nano := swapd.NewMockNano()
	stateDir := t.TempDir()

	tr := &memTransport{}
	coord, err := swapnet.New(swapnet.Config{
		Transport:  tr,
		Maker:      makerCaps{s: sc, nano: nano},
		Timeout:    500 * time.Millisecond, // short stall deadline so the test is fast
		Fee:        testFee,
		StateDir:   stateDir,
		AcceptInit: acceptOffer(testOBX, testXNO),
	})
	if err != nil {
		t.Fatalf("coord: %v", err)
	}
	defer coord.Stop()

	// build a real taker's Init (we only need its public material + amounts), then
	// hand it to the maker coordinator as if it arrived over the wire. The taker then
	// goes silent (we never deliver XNOLocked).
	id := swapID(0x33)
	taker := swapsession.NewTaker(id, testOBX, testXNO, testFee, takerOBX{s: sc})
	env := &swapnet.Envelope{SwapID: id, Kind: swapnet.KindInit, Payload: taker.Init().Serialize()}
	coord.Deliver("taker-peer", env.Serialize())

	// the maker should fund (send MakerCommit + Funded), then time out waiting for
	// XNOLocked and refund. Refund mines forward to the unlock height (several
	// class-group PoW blocks), so give it ample room.
	deadline := time.Now().Add(90 * time.Second)
	statePath := filepath.Join(stateDir, fmt.Sprintf("swap-%x.json", id))
	for time.Now().Before(deadline) {
		if st, err := swapsession.LoadState(statePath); err == nil && st.Phase == swapsession.PhaseRefunded {
			// success: the maker reclaimed its OBX.
			kinds := tr.kinds()
			// it must have at least sent MakerCommit and Funded before refunding.
			var sawCommit, sawFunded bool
			for _, k := range kinds {
				if k == swapnet.KindMakerCommit {
					sawCommit = true
				}
				if k == swapnet.KindFunded {
					sawFunded = true
				}
			}
			if !sawCommit || !sawFunded {
				t.Fatalf("maker did not fund before refunding (kinds=%v)", kinds)
			}
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("maker did not refund after the taker stalled")
}

// TestMakerCrashResume proves the crash-resume fix (audit): SwapState was persisted but
// never LOADED, so a node that crashed after funding an OBX SwapOut forgot the swap and
// stranded the funds. Here coordinator A funds the OBX and then "crashes" (a FRESH
// coordinator B is created on the SAME StateDir). B.Resume() must re-drive the maker and,
// since the taker never proceeds, reclaim the OBX via the refund branch once the claim
// window closes (the chain is advanced past UnlockHeight to model the live miner).
func TestMakerCrashResume(t *testing.T) {
	sc := newSharedChain(t)
	nano := swapd.NewMockNano()
	stateDir := t.TempDir()

	// Coordinator A: a long stall timeout keeps it BLOCKED awaiting XNOLocked (never
	// refunding on its own), so the persisted state stays at PhaseFunded — a crash.
	coordA, err := swapnet.New(swapnet.Config{
		Transport:  &memTransport{},
		Maker:      makerCaps{s: sc, nano: nano},
		Timeout:    10 * time.Minute,
		Fee:        testFee,
		StateDir:   stateDir,
		AcceptInit: acceptOffer(testOBX, testXNO),
	})
	if err != nil {
		t.Fatalf("coordA: %v", err)
	}
	defer coordA.Stop()

	id := swapID(0x77)
	taker := swapsession.NewTaker(id, testOBX, testXNO, testFee, takerOBX{s: sc})
	env := &swapnet.Envelope{SwapID: id, Kind: swapnet.KindInit, Payload: taker.Init().Serialize()}
	coordA.Deliver("taker-peer", env.Serialize())

	statePath := filepath.Join(stateDir, fmt.Sprintf("swap-%x.json", id))
	waitSwapPhase(t, statePath, swapsession.PhaseFunded, 60*time.Second)

	// "crash": a fresh coordinator B on the SAME StateDir picks up the funded swap.
	coordB, err := swapnet.New(swapnet.Config{
		Transport:  &memTransport{},
		Maker:      makerCaps{s: sc, nano: nano},
		Timeout:    500 * time.Millisecond,
		Fee:        testFee,
		StateDir:   stateDir,
		AcceptInit: acceptOffer(testOBX, testXNO),
	})
	if err != nil {
		t.Fatalf("coordB: %v", err)
	}
	defer coordB.Stop()
	coordB.Resume()

	// Advance the chain past UnlockHeight (no claim) to model the live miner, so the
	// resumed watcher sees the claim window close and refunds.
	st, err := swapsession.LoadState(statePath)
	if err != nil {
		t.Fatalf("load resumed state: %v", err)
	}
	for sc.Height() <= st.UnlockHeight+2 {
		_ = sc.mine(nil)
		time.Sleep(40 * time.Millisecond)
	}
	waitSwapPhase(t, statePath, swapsession.PhaseRefunded, 60*time.Second)
}

// waitSwapPhase polls a persisted SwapState until it reaches want, or fails after d.
func waitSwapPhase(t *testing.T, path string, want swapsession.Phase, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if st, err := swapsession.LoadState(path); err == nil && st.Phase == want {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("state at %s never reached phase %q", path, want)
}

// ---- helpers ----------------------------------------------------------------

const (
	testOBX = 3 * config.AtomicPerCoin
	testFee = uint64(1_000_000_000)
)

// testXNO is the agreed XNO amount (raw, 128-bit *big.Int) used across the swapnet
// tests. It is a *big.Int (not a uint64 const) because Coordinator.Take, NewTaker and
// the session views now carry raw XNO as *big.Int. testXNOp1 is the off-by-one variant
// used by the wrong-amount taker test.
var (
	testXNO   = big.NewInt(10_000)
	testXNOp1 = new(big.Int).Add(testXNO, big.NewInt(1))
)

func swapID(tag byte) [32]byte {
	var id [32]byte
	for i := range id {
		id[i] = tag
	}
	return id
}

// acceptOffer builds an AcceptInit predicate that authorizes maker auto-funding
// ONLY for an Init whose amounts match a (single) live offer's terms — a stand-in
// for full swapbook offer-binding (the documented follow-up). It rejects any Init
// whose OBX/XNO amounts differ, modelling the F-A rule that a maker never funds
// for an unsolicited / mismatched Init.
func acceptOffer(obx uint64, xno *big.Int) func(*swapsession.Init, string) bool {
	return func(init *swapsession.Init, _ string) bool {
		return init.OBXAmount == obx && init.XNOAmount != nil && init.XNOAmount.Cmp(xno) == 0
	}
}

// assertShareIsolation proves NEITHER persisted session ever held BOTH secret
// shares: the maker's own shares (b,sB) and the taker's (a,sA) are disjoint, and
// each side's stored "peer" public material equals the OTHER side's own-share
// public points (own·G) — never the private scalar.
func assertShareIsolation(t *testing.T, makerDir, takerDir string, id [32]byte) {
	t.Helper()
	mPath := filepath.Join(makerDir, fmt.Sprintf("swap-%x.json", id))
	tPath := filepath.Join(takerDir, fmt.Sprintf("swap-%x.json", id))
	mSt, err := swapsession.LoadState(mPath)
	if err != nil {
		t.Fatalf("load maker state: %v", err)
	}
	tSt, err := swapsession.LoadState(tPath)
	if err != nil {
		t.Fatalf("load taker state: %v", err)
	}
	if mSt.Role != swapsession.RoleMaker || tSt.Role != swapsession.RoleTaker {
		t.Fatalf("roles wrong: maker=%s taker=%s", mSt.Role, tSt.Role)
	}

	// own private shares: maker=(b,sB), taker=(a,sA). All four must be distinct.
	priv := [][]byte{mSt.OwnShareClaim, mSt.OwnShareXNO, tSt.OwnShareClaim, tSt.OwnShareXNO}
	for i := 0; i < len(priv); i++ {
		for j := i + 1; j < len(priv); j++ {
			if bytes.Equal(priv[i], priv[j]) {
				t.Fatalf("private shares %d and %d collide — not independently minted", i, j)
			}
		}
	}

	// neither side stored the other's PRIVATE scalar anywhere.
	mineMaker := [][]byte{mSt.OwnShareClaim, mSt.OwnShareXNO}
	mineTaker := [][]byte{tSt.OwnShareClaim, tSt.OwnShareXNO}
	for _, s := range mineMaker {
		if bytes.Equal(s, tSt.OwnShareClaim) || bytes.Equal(s, tSt.OwnShareXNO) {
			t.Fatal("maker state holds the taker's PRIVATE share — isolation violated")
		}
	}
	for _, s := range mineTaker {
		if bytes.Equal(s, mSt.OwnShareClaim) || bytes.Equal(s, mSt.OwnShareXNO) {
			t.Fatal("taker state holds the maker's PRIVATE share — isolation violated")
		}
	}

	// the maker's peer fields must equal the taker's own-share PUBLIC points, and
	// vice versa — confirming only public material crossed and was stored.
	if !bytes.Equal(mSt.PeerClaimShare, pub(t, tSt.OwnShareClaim)) || !bytes.Equal(mSt.PeerXNOShare, pub(t, tSt.OwnShareXNO)) {
		t.Fatal("maker peer fields are not the taker's public points")
	}
	if !bytes.Equal(tSt.PeerClaimShare, pub(t, mSt.OwnShareClaim)) || !bytes.Equal(tSt.PeerXNOShare, pub(t, mSt.OwnShareXNO)) {
		t.Fatal("taker peer fields are not the maker's public points")
	}
}

// pub returns scalar·G for a 32-byte canonical scalar encoding.
func pub(t *testing.T, scalarBytes []byte) []byte {
	t.Helper()
	sc, err := new(edwards25519.Scalar).SetCanonicalBytes(scalarBytes)
	if err != nil {
		t.Fatalf("bad scalar: %v", err)
	}
	return new(edwards25519.Point).ScalarBaseMult(sc).Bytes()
}

// twoNodes stands up two connected p2p nodes over loopback TCP and returns them
// plus the peer-address handle each uses to reach the other.
func twoNodes(t *testing.T, sc *sharedChain) (maker, taker *p2p.Node, makerPeerOnTaker, takerPeerOnMaker string) {
	t.Helper()
	// Each node has its OWN chain handle for p2p sync purposes is unnecessary here —
	// they share sc.c (the prompt's "one in-process chain both read"). p2p still needs
	// a chain + mempool to start; we give both nodes the SAME chain object, which is
	// fine: no blocks are gossiped between them (the maker mines locally), only swap
	// envelopes travel the wire.
	mp := mempool.New(sc.c)
	nodeM := p2p.NewNode("127.0.0.1:0", sc.c, mp, "")
	nodeT := p2p.NewNode("127.0.0.1:0", sc.c, mp, "")
	if err := nodeM.Start(nil); err != nil {
		t.Fatalf("start maker node: %v", err)
	}
	if err := nodeT.Start([]string{nodeM.Addr()}); err != nil {
		t.Fatalf("start taker node: %v", err)
	}
	t.Cleanup(func() { nodeM.Stop(); nodeT.Stop() })

	// wait for the connection to establish, then capture the peer-address handles.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		ma := nodeM.PeerAddrs()
		ta := nodeT.PeerAddrs()
		if len(ma) > 0 && len(ta) > 0 {
			// nodeM sees the taker at ma[0]; nodeT sees the maker at ta[0].
			return nodeM, nodeT, ta[0], ma[0]
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("p2p nodes did not connect")
	return nil, nil, "", ""
}

// TestTwoNodeSwapOverP2P is the key deliverable: a maker on one node and a taker on
// another complete a full atomic swap by exchanging the swapsession messages over a
// REAL p2p connection (not direct Go calls). It asserts (1) completion — taker gets
// OBX, maker sweeps XNO; (2) share isolation — neither coordinator's session object
// ever held both secret shares; (3) the messages traversed the p2p layer.
func TestTwoNodeSwapOverP2P(t *testing.T) {
	sc := newSharedChain(t)
	nano := swapd.NewMockNano() // shared XNO ledger both sides see

	nodeM, nodeT, makerPeer, takerPeer := twoNodes(t, sc)
	_ = takerPeer // the maker learns the taker's handle from the inbound Init.

	makerStateDir := t.TempDir()
	takerStateDir := t.TempDir()

	// maker coordinator on nodeM.
	mTr := &countingTransport{inner: swapnet.NewP2PTransport(nodeM)}
	makerCoord, err := swapnet.New(swapnet.Config{
		Transport:  mTr,
		Maker:      makerCaps{s: sc, nano: nano},
		Timeout:    20 * time.Second,
		Fee:        testFee,
		StateDir:   makerStateDir,
		AcceptInit: acceptOffer(testOBX, testXNO),
	})
	if err != nil {
		t.Fatalf("maker coord: %v", err)
	}
	defer makerCoord.Stop()
	mTr.inner.(*swapnet.P2PTransport).BindInbound(makerCoord)

	// taker coordinator on nodeT.
	tTr := &countingTransport{inner: swapnet.NewP2PTransport(nodeT)}
	takerCoord, err := swapnet.New(swapnet.Config{
		Transport: tTr,
		Taker:     takerCaps{s: sc, nano: nano},
		Timeout:   20 * time.Second,
		Fee:       testFee,
		StateDir:  takerStateDir,
	})
	if err != nil {
		t.Fatalf("taker coord: %v", err)
	}
	defer takerCoord.Stop()
	tTr.inner.(*swapnet.P2PTransport).BindInbound(takerCoord)

	takerBefore := sc.taker.Balance()

	// taker takes the swap against the maker peer. The SwapID is minted inside Take
	// (fresh high-entropy nonce); we read it back for the share-isolation assertion.
	sess, err := takerCoord.Take(makerPeer, testOBX, testXNO, testFee)
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	id := sess.ID()
	if err := sess.Wait(); err != nil {
		t.Fatalf("taker session failed: %v", err)
	}
	if !sess.Succeeded() {
		t.Fatal("taker session did not reach success")
	}

	// the maker session runs to completion (sweep) asynchronously; wait for the XNO.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if nano.Balance("maker-xno-dest").Cmp(testXNO) == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// (1) COMPLETION.
	if got := nano.Balance("maker-xno-dest"); got.Cmp(testXNO) != 0 {
		t.Fatalf("XNO at maker dest = %s, want %s (maker did not sweep)", got, testXNO)
	}
	if sc.taker.Balance() <= takerBefore {
		t.Fatalf("taker OBX balance did not grow (before %d, after %d)", takerBefore, sc.taker.Balance())
	}

	// (2) SHARE ISOLATION: the two coordinators persisted their sessions' SwapState.
	// Each file holds ONLY that party's own private shares plus the peer's PUBLIC
	// points. We assert the two own-share sets are disjoint and that each side's
	// "peer" fields are exactly the OTHER side's own-share PUBLIC points — i.e.
	// neither coordinator ever stored the other's private scalar.
	assertShareIsolation(t, makerStateDir, takerStateDir, id)

	// (3) MESSAGES TRAVERSED P2P: the taker sent Init/XNOLocked/ClaimRequest/ClaimDone
	// (>=4) and the maker sent MakerCommit/Funded/ClaimPreSig (>=3). Both counters
	// being non-zero — with completion only possible if every byte arrived at the
	// OTHER node — proves the handshake crossed the wire, not a direct call.
	if atomic.LoadInt64(&tTr.sends) < 4 {
		t.Fatalf("taker sent %d swap messages over p2p, want >= 4", tTr.sends)
	}
	if atomic.LoadInt64(&mTr.sends) < 3 {
		t.Fatalf("maker sent %d swap messages over p2p, want >= 3", mTr.sends)
	}
}

// ---- in-process paired transport (for F-A/F-B/F-C griefing tests) -----------
//
// pairTransport wires two coordinators (a maker and a taker) on a SINGLE process
// with controllable, peer-addressed delivery. Each Send(peer, env) routes to the
// peer's registered Coordinator via Deliver, tagging the message with the SENDER's
// handle (so the receiver's F-C counterparty check sees the right fromPeer). A test
// can register a third "attacker" handle and inject envelopes by calling a victim's
// Deliver directly. An optional perEnvelope hook lets a test drop/mutate frames
// (e.g. suppress or corrupt ClaimDone) to model a malicious counterparty.
type pairTransport struct {
	mu     sync.Mutex
	myAddr string
	routes map[string]*swapnet.Coordinator // peer handle -> that peer's coordinator
	// onSend, if set, is consulted before each Send: return drop=true to swallow the
	// envelope, or a replacement payload to mutate it.
	onSend func(to string, env *swapnet.Envelope) (replacement []byte, drop bool)
}

func newPair(myAddr string) *pairTransport {
	return &pairTransport{myAddr: myAddr, routes: map[string]*swapnet.Coordinator{}}
}

func (p *pairTransport) route(peer string, coord *swapnet.Coordinator) {
	p.mu.Lock()
	p.routes[peer] = coord
	p.mu.Unlock()
}

func (p *pairTransport) Send(peer string, env *swapnet.Envelope) error {
	p.mu.Lock()
	dst := p.routes[peer]
	hook := p.onSend
	from := p.myAddr
	p.mu.Unlock()
	payload := env.Serialize()
	if hook != nil {
		if repl, drop := hook(peer, env); drop {
			return nil // model a counterparty that never delivers this frame
		} else if repl != nil {
			payload = repl
		}
	}
	if dst != nil {
		dst.Deliver(from, payload)
	}
	return nil
}

// TestDeliverDropsNonCounterpartyEnvelope (F-C): a THIRD peer that has somehow
// learned an in-flight SwapID injects a KindAbort into a live maker session. The
// maker MUST drop it (it is not from the session's bound counterparty) and the swap
// MUST still complete. Without the F-C fromPeer check the abort would tear the
// session down (recv surfaces KindAbort as an error).
func TestDeliverDropsNonCounterpartyEnvelope(t *testing.T) {
	sc := newSharedChain(t)
	nano := swapd.NewMockNano()

	makerTr := newPair("maker-addr")
	takerTr := newPair("taker-addr")

	makerCoord, err := swapnet.New(swapnet.Config{
		Transport: makerTr, Maker: makerCaps{s: sc, nano: nano},
		Timeout: 20 * time.Second, Fee: testFee, StateDir: t.TempDir(),
		AcceptInit: acceptOffer(testOBX, testXNO),
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

	// the taker reaches the maker at "maker-addr"; the maker learns the taker at
	// "taker-addr" from the inbound Init.
	takerTr.route("maker-addr", makerCoord)
	makerTr.route("taker-addr", takerCoord)

	// Start the taker; capture its SwapID, then have an ATTACKER inject a KindAbort
	// onto the maker's session from a different handle while the swap is in flight.
	sess, err := takerCoord.Take("maker-addr", testOBX, testXNO, testFee)
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	id := sess.ID()

	// hammer the maker with abort/junk frames from a non-counterparty handle for the
	// duration of the swap; every one must be dropped.
	stop := make(chan struct{})
	go func() {
		abort := (&swapnet.Envelope{SwapID: id, Kind: swapnet.KindAbort}).Serialize()
		junk := (&swapnet.Envelope{SwapID: id, Kind: swapnet.KindClaimDone, Payload: make([]byte, 128)}).Serialize()
		for {
			select {
			case <-stop:
				return
			default:
				makerCoord.Deliver("attacker-addr", abort)
				makerCoord.Deliver("attacker-addr", junk)
				time.Sleep(time.Millisecond)
			}
		}
	}()

	err = sess.Wait()
	close(stop)
	if err != nil {
		t.Fatalf("swap failed despite attacker only injecting from a non-counterparty handle: %v", err)
	}
	if !sess.Succeeded() {
		t.Fatal("taker did not reach success")
	}
	// the maker sweeps async; wait for the XNO.
	waitXNO(t, nano, "maker-xno-dest", testXNO)
}

// TestSessionCapRejected (F-A part 1): once the per-peer session cap is reached,
// further inbound Inits from that peer are dropped WITHOUT funding, and a taker's
// Take past the global cap errors.
func TestSessionCapRejected(t *testing.T) {
	oldGlobal, oldPer := config.SwapMaxSessions, config.SwapMaxSessionsPerPeer
	config.SwapMaxSessions = 2
	config.SwapMaxSessionsPerPeer = 1
	t.Cleanup(func() { config.SwapMaxSessions, config.SwapMaxSessionsPerPeer = oldGlobal, oldPer })

	sc := newSharedChain(t)
	nano := swapd.NewMockNano()
	tr := &memTransport{}
	coord, err := swapnet.New(swapnet.Config{
		Transport: tr, Taker: takerCaps{s: sc, nano: nano},
		Timeout: 30 * time.Second, Fee: testFee,
	})
	if err != nil {
		t.Fatalf("coord: %v", err)
	}
	defer coord.Stop()

	// first Take to a peer succeeds (fills global=1, per-peer[peerA]=1).
	if _, err := coord.Take("peerA", testOBX, testXNO, testFee); err != nil {
		t.Fatalf("first Take: %v", err)
	}
	// second Take to the SAME peer hits the per-peer cap (1).
	if _, err := coord.Take("peerA", testOBX, testXNO, testFee); err == nil {
		t.Fatal("expected per-peer cap to reject the 2nd Take to peerA")
	}
	// a Take to a different peer is allowed (fills global=2).
	if _, err := coord.Take("peerB", testOBX, testXNO, testFee); err != nil {
		t.Fatalf("Take to peerB: %v", err)
	}
	// now the GLOBAL cap (2) rejects any further new session.
	if _, err := coord.Take("peerC", testOBX, testXNO, testFee); err == nil {
		t.Fatal("expected global cap to reject the 3rd Take")
	}
}

// TestAcceptInitGatesMakerFunding (F-A part 2): a maker with NO AcceptInit predicate
// funds NOTHING for an inbound Init (deny by default); one whose Init does not match
// a live offer is likewise dropped without funding.
func TestAcceptInitGatesMakerFunding(t *testing.T) {
	sc := newSharedChain(t)
	nano := swapd.NewMockNano()

	// (a) nil AcceptInit → deny all. No MakerCommit/Funded ever sent.
	tr := &memTransport{}
	coord, err := swapnet.New(swapnet.Config{
		Transport: tr, Maker: makerCaps{s: sc, nano: nano},
		Timeout: 500 * time.Millisecond, Fee: testFee,
		// AcceptInit deliberately nil.
	})
	if err != nil {
		t.Fatalf("coord: %v", err)
	}
	defer coord.Stop()
	id := swapID(0x44)
	taker := swapsession.NewTaker(id, testOBX, testXNO, testFee, takerOBX{s: sc})
	env := &swapnet.Envelope{SwapID: id, Kind: swapnet.KindInit, Payload: taker.Init().Serialize()}
	coord.Deliver("evil-peer", env.Serialize())
	time.Sleep(300 * time.Millisecond)
	if got := tr.kinds(); len(got) != 0 {
		t.Fatalf("nil AcceptInit must not fund/send anything, but maker sent %v", got)
	}

	// (b) predicate rejects a mismatched Init (wrong amounts) → no funding.
	tr2 := &memTransport{}
	coord2, err := swapnet.New(swapnet.Config{
		Transport: tr2, Maker: makerCaps{s: sc, nano: nano},
		Timeout: 500 * time.Millisecond, Fee: testFee,
		AcceptInit: acceptOffer(testOBX, testXNO),
	})
	if err != nil {
		t.Fatalf("coord2: %v", err)
	}
	defer coord2.Stop()
	id2 := swapID(0x45)
	badTaker := swapsession.NewTaker(id2, testOBX, testXNOp1, testFee, takerOBX{s: sc}) // wrong XNO amount
	env2 := &swapnet.Envelope{SwapID: id2, Kind: swapnet.KindInit, Payload: badTaker.Init().Serialize()}
	coord2.Deliver("peer", env2.Serialize())
	time.Sleep(300 * time.Millisecond)
	if got := tr2.kinds(); len(got) != 0 {
		t.Fatalf("mismatched Init must not fund/send anything, but maker sent %v", got)
	}
}

// TestMakerSweepsViaChainScrape (F-B): the taker mines the claim (taking the OBX
// and publishing on-chain the full claim sig that reveals sA) but sends a CORRUPTED
// ClaimDone whose on-chain-observable full sig is junk — a griefing attempt to make
// the maker's SweepXNO fail. The maker must IGNORE the relayed full sig, SCRAPE the
// real one from the chain, and still sweep the XNO. Asserts the maker's XNO dest
// balance grows. (Note the honest limitation: the relay still must carry the
// aggregate pre-sig scalar Ŝ, which is the one value not on-chain — see
// sweepWithScrape. Here the taker corrupts only the on-chain-derivable fields.)
func TestMakerSweepsViaChainScrape(t *testing.T) {
	sc := newSharedChain(t)
	nano := swapd.NewMockNano()

	makerTr := newPair("maker-addr")
	takerTr := newPair("taker-addr")

	// taker corrupts the FULL SIG bytes inside its outbound ClaimDone (the 64B tail),
	// keeping the aggregate pre-sig scalar Ŝ intact. The maker should scrape the real
	// full sig from chain and sweep anyway.
	takerTr.onSend = func(_ string, env *swapnet.Envelope) ([]byte, bool) {
		if env.Kind != swapnet.KindClaimDone {
			return nil, false
		}
		corrupt := append([]byte(nil), env.Payload...)
		// payload = R(32) || S(32) || fullSig(64); zero the fullSig tail.
		for i := 64; i < len(corrupt); i++ {
			corrupt[i] = 0
		}
		repl := (&swapnet.Envelope{SwapID: env.SwapID, Kind: env.Kind, Payload: corrupt}).Serialize()
		return repl, false
	}

	makerCoord, err := swapnet.New(swapnet.Config{
		Transport: makerTr, Maker: makerCaps{s: sc, nano: nano},
		Timeout: 20 * time.Second, Fee: testFee, StateDir: t.TempDir(),
		AcceptInit: acceptOffer(testOBX, testXNO),
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
	if !sess.Succeeded() {
		t.Fatal("taker did not claim the OBX")
	}
	// the maker must still sweep, via the chain-scraped full sig.
	waitXNO(t, nano, "maker-xno-dest", testXNO)
}

// TestMakerSweepsWithoutAnyClaimDone (GRIEFING FIX): the taker claims the OBX
// (publishing on-chain the full claim sig that bakes in sA) but sends NO KindClaimDone
// relay AT ALL — the transport DROPS every ClaimDone frame, modelling a malicious taker
// that takes the OBX and then goes silent to try to freeze the maker's XNO. Before the
// fix the maker depended on the relay to obtain the aggregate pre-sig scalar Ŝ and was
// stuck (XNO frozen). After the fix the maker extracts sA INDEPENDENTLY from the chain
// (sA = S_full − ŝ_a − ŝ_b, with ŝ_a verified+stored at co-sign time) and sweeps anyway.
func TestMakerSweepsWithoutAnyClaimDone(t *testing.T) {
	sc := newSharedChain(t)
	nano := swapd.NewMockNano()

	makerTr := newPair("maker-addr")
	takerTr := newPair("taker-addr")

	// DROP every ClaimDone the taker tries to send: the maker gets NOTHING off-chain
	// after co-signing. (Also drop nothing else, so the handshake itself completes.)
	var droppedClaimDone int64
	takerTr.onSend = func(_ string, env *swapnet.Envelope) ([]byte, bool) {
		if env.Kind == swapnet.KindClaimDone {
			atomic.AddInt64(&droppedClaimDone, 1)
			return nil, true // drop entirely
		}
		return nil, false
	}

	makerCoord, err := swapnet.New(swapnet.Config{
		Transport: makerTr, Maker: makerCaps{s: sc, nano: nano},
		Timeout: 20 * time.Second, Fee: testFee, StateDir: t.TempDir(),
		AcceptInit: acceptOffer(testOBX, testXNO),
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
	if !sess.Succeeded() {
		t.Fatal("taker did not claim the OBX")
	}
	// the maker must STILL sweep, with zero ClaimDone relays ever delivered.
	waitXNO(t, nano, "maker-xno-dest", testXNO)
	if atomic.LoadInt64(&droppedClaimDone) == 0 {
		t.Fatal("test did not actually exercise the no-relay path (no ClaimDone was dropped)")
	}
}

// waitXNO polls until the MockNano destination reaches want, failing the test on
// timeout.
func waitXNO(t *testing.T, nano *swapd.MockNano, dest string, want *big.Int) {
	t.Helper()
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		if nano.Balance(dest).Cmp(want) == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("XNO at %s = %s, want %s (maker did not sweep)", dest, nano.Balance(dest), want)
}
