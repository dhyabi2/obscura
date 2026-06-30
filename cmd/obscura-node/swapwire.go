package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"filippo.io/edwards25519"

	"obscura/pkg/chain"
	"obscura/pkg/commit"
	"obscura/pkg/config"
	"obscura/pkg/miner"
	"obscura/pkg/p2p"
	"obscura/pkg/swap"
	"obscura/pkg/swapbook"
	"obscura/pkg/swapd"
	"obscura/pkg/swapnet"
	"obscura/pkg/swaprelay"
	"obscura/pkg/swapsession"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
)

// This file wires the trustless XNO<->OBX atomic-swap engine (pkg/swapnet
// Coordinator over pkg/swapsession) into the running node: it adapts the node's
// chain + miner wallet into the MakerOBX / TakerOBX capabilities the swap state
// machines drive, picks a Nano backend (LIVE-ONLY policy: a real client when
// --nano-rpc is set, otherwise the engine REFUSES to start — MockNano is permitted
// only behind the explicit OBX_ALLOW_MOCK_NANO=1 opt-in for the value-less TEST
// chain), gates maker auto-funding to the node's OWN published offers, and routes
// inbound p2p swap envelopes to the coordinator.
//
// HONEST SCOPE (test chain, see MEMORY): the OBX leg mines its OWN blocks through
// a single mine-lock (mirroring the swapnet tests), funding from the node miner
// wallet seed. That is the simplest topology that makes a swap complete in-process;
// it coexists with the main mineLoop because chain.AddBlock is internally locked
// and fork-choice tolerant. A production design would instead inject swap txs into
// the shared mempool and let one miner seal them — noted as a follow-up.

// swapOBXHost owns the OBX-side chain access + funding wallet for swaps, with a
// dedicated mine-lock so swap funding/claim/refund txs are sealed deterministically.
// It is shared by every maker/taker session on this node (a fresh per-session
// adapter is handed out, but all read/mine the same chain + wallet).
type swapOBXHost struct {
	mu      sync.Mutex     // serializes swap-block production
	c       *chain.Chain   // the node's live chain (read for SwapOut / claim scrape)
	w       *wallet.Wallet // node miner wallet (funds OBX, receives claims/refunds)
	node    *p2p.Node      // to broadcast sealed swap blocks to peers
	scanned uint64         // last height scanned into w (so SpendableOutputs is current)
}

func newSwapOBXHost(c *chain.Chain, seed []byte, node *p2p.Node) *swapOBXHost {
	return &swapOBXHost{c: c, w: wallet.FromSeed(seed), node: node}
}

// height returns the current chain height.
func (h *swapOBXHost) height() uint64 { return h.c.Height() }

// scanLocked catches the funding wallet up to the tip so SpendableOutputs reflects
// every confirmed (incl. swap-change + claim) output. Caller holds h.mu.
func (h *swapOBXHost) scanLocked() {
	tip := h.c.Height()
	for hh := h.scanned + 1; hh <= tip; hh++ {
		if b, ok := h.c.BlockByHeight(hh); ok {
			h.w.ScanBlock(b)
			h.scanned = hh
		} else {
			break
		}
	}
}

// mineLocked seals a block carrying txs (with a coinbase to the funding wallet so
// the block is valid), adds it to the chain, rescans, and broadcasts it. Caller
// holds h.mu. Uses the canonical PoW seed so the block is consensus-valid.
func (h *swapOBXHost) mineLocked(txs []*tx.Transaction) error {
	fees := chain.CollectedFees(txs)
	cb, err := h.w.BuildCoinbase(h.c.Height()+1, h.c.ExpectedCoinbaseMinted(fees, nil), nil)
	if err != nil {
		return err
	}
	all := append([]*tx.Transaction{cb}, txs...)
	tmpl, err := h.c.BlockTemplate(all)
	if err != nil {
		return err
	}
	if !miner.MineSeed(context.Background(), tmpl, h.c.PoWSeed(tmpl.Header.Height), 0) {
		return errSwapMine
	}
	if err := h.c.AddBlock(tmpl); err != nil {
		return err
	}
	h.scanLocked()
	if h.node != nil {
		go h.node.BroadcastBlock(tmpl)
	}
	return nil
}

type swapMineErr string

func (e swapMineErr) Error() string { return string(e) }

var errSwapMine = swapMineErr("swapwire: failed to mine swap block")

// findSwapOut reads a confirmed SwapOut from the chain (taker's safe-leg check).
func (h *swapOBXHost) findSwapOut(swapKey []byte) (swap.SwapOutput, bool) {
	e, ok := h.c.Swap(swapKey)
	if !ok {
		return swap.SwapOutput{}, false
	}
	return swap.SwapOutput{
		ClaimKey: e.ClaimKey, RefundKey: e.RefundKey, UnlockHeight: e.UnlockHeight,
		ClaimR: e.ClaimR, ClaimT: e.ClaimT, Amount: e.Amount,
	}, true
}

// findSwapSpend scans the chain for the mined CLAIM spend of swapKey, returning the
// published full claim signature + the tx core hash it signed (the maker's
// chain-scrape recovery path). Mirrors the swapnet test adapter.
func (h *swapOBXHost) findSwapSpend(swapKey []byte) (fullSig, coreHash []byte, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for hh := uint64(0); hh <= h.c.Height(); hh++ {
		b, got := h.c.BlockByHeight(hh)
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

// ---- per-session OBX adapters ----------------------------------------------

type makerOBXAdapter struct{ h *swapOBXHost }

func (m makerOBXAdapter) Height() uint64                                { return m.h.height() }
func (m makerOBXAdapter) FindSwapOut(k []byte) (swap.SwapOutput, bool)  { return m.h.findSwapOut(k) }
func (m makerOBXAdapter) FindSwapSpend(k []byte) ([]byte, []byte, bool) { return m.h.findSwapSpend(k) }

func (m makerOBXAdapter) FundSwapOut(swapKey []byte, obxAmount uint64, claimKey, refundKey, claimA, claimB, popA, popB, claimR, claimT []byte, unlockHeight, fee uint64) error {
	m.h.mu.Lock()
	m.h.scanLocked()
	fund, err := m.h.w.FundSwap(m.h.c, swapKey, obxAmount, claimKey, refundKey, claimA, claimB, popA, popB, claimR, claimT, unlockHeight, fee)
	m.h.mu.Unlock()
	if err != nil {
		return err
	}
	// Route the funding tx through the MEMPOOL so the node's single mining loop seals it
	// into the CANONICAL chain. Self-sealing it here (mineLocked) raced the node's
	// concurrent miner: when the miner's own next block won the height, the funding block
	// was a sibling fork and the funding tx — mined directly into the orphaned block,
	// never relayed to any mempool — was lost, so the taker never saw the SwapOut and
	// aborted ("funding not synced"). Maker.Fund returns Funded immediately; the taker's
	// VerifyFundedAndLock poll waits for the mined funding (ErrFundingNotVisible retry).
	// Standalone (no peers / no miner loop) falls back to self-seal.
	if m.h.node != nil {
		return m.h.node.RelayTx(fund)
	}
	m.h.mu.Lock()
	defer m.h.mu.Unlock()
	return m.h.mineLocked([]*tx.Transaction{fund})
}

func (m makerOBXAdapter) MineRefund(swapKey []byte, obxAmount, fee, unlockHeight uint64, sign func([]byte) []byte) error {
	m.h.mu.Lock()
	defer m.h.mu.Unlock()
	for m.h.c.Height() < unlockHeight {
		if err := m.h.mineLocked(nil); err != nil {
			return err
		}
	}
	refund, err := m.h.w.BuildSwapSpend(swapKey, obxAmount, true, fee, sign)
	if err != nil {
		return err
	}
	return m.h.mineLocked([]*tx.Transaction{refund})
}

type takerOBXAdapter struct{ h *swapOBXHost }

func (t takerOBXAdapter) Height() uint64                               { return t.h.height() }
func (t takerOBXAdapter) FindSwapOut(k []byte) (swap.SwapOutput, bool) { return t.h.findSwapOut(k) }

func (t takerOBXAdapter) BuildClaim(swapKey []byte, obxAmount, fee uint64) (*tx.Transaction, []byte, error) {
	var coreHash []byte
	txn, err := t.h.w.BuildSwapSpend(swapKey, obxAmount, false, fee, func(ch []byte) []byte {
		coreHash = append([]byte(nil), ch...)
		return make([]byte, 64) // placeholder sig; the real one is attached in MineClaim
	})
	if err != nil {
		return nil, nil, err
	}
	return txn, coreHash, nil
}

func (t takerOBXAdapter) MineClaim(txn *tx.Transaction, sig []byte) error {
	txn.SwapInputs[0].Sig = sig
	// Do NOT self-seal the claim into a block: a taker is usually not an active miner
	// and can lag the tip (busy with the slow real-XNO lock + connection churn), so a
	// block sealed at its STALE height is orphaned and the claim is lost from every
	// mempool — the maker then never sees a claim to recover sA from, and the XNO leg
	// strands. Instead RELAY the claim tx so the canonical-tip miner — the MAKER, which
	// WANTS it on-chain to sweep — includes it. If this node also mines, its own loop
	// picks it up from the mempool too. Standalone (no peers) falls back to self-mine.
	if t.h.node != nil {
		return t.h.node.RelayTx(txn)
	}
	t.h.mu.Lock()
	defer t.h.mu.Unlock()
	return t.h.mineLocked([]*tx.Transaction{txn})
}

// ---- maker / taker capability bundles --------------------------------------

type nodeMakerCaps struct {
	h         *swapOBXHost
	nano      swapd.NanoClient
	sweepDest string // the nano_ address (or mock ledger key) swept XNO is sent to
}

func (m nodeMakerCaps) NewMakerOBX() swapsession.MakerOBX { return makerOBXAdapter{h: m.h} }
func (m nodeMakerCaps) Nano() swapsession.XNOSweeper      { return m.nano }
func (m nodeMakerCaps) SweepDest() string                 { return m.sweepDest }

type nodeTakerCaps struct {
	h    *swapOBXHost
	nano swapd.NanoClient
}

func (t nodeTakerCaps) NewTakerOBX() swapsession.TakerOBX { return takerOBXAdapter{h: t.h} }
func (t nodeTakerCaps) Nano() swapsession.XNOLocker       { return t.nano }

// mockSweepDest is the opaque ledger key MockNano credits swept XNO to in the
// test/default (no real --nano-rpc) configuration. It is NOT a nano_ address (the
// mock treats destinations opaquely), so it is only ever used with MockNano.
const mockSweepDest = "obx-node-xno-sweep-dest"

// errSwapMockRefused is the sentinel returned when no live Nano RPC is configured
// AND the explicit MockNano opt-in is not set: the LIVE-ONLY policy (see repo
// CLAUDE.md) FORBIDS powering a "real" swap engine with a mock, so we refuse to
// wire the coordinator at all (leaving /swaps/* disabled) rather than silently
// completing swaps against a fake ledger.
var errSwapMockRefused = swapMockRefusedErr("swap engine DISABLED: live Nano RPC required " +
	"(set --nano-rpc to a live endpoint; MockNano is refused by the live-only policy). " +
	"Set OBX_ALLOW_MOCK_NANO=1 to override on the value-less local test chain")

type swapMockRefusedErr string

func (e swapMockRefusedErr) Error() string { return string(e) }

// nanoForSwaps returns the Nano backend the swap engine uses, ENFORCING the
// live-only policy. A real *swapd.NanoRPC (selected via --nano-rpc) is always
// accepted. With no real client the DEFAULT is to REFUSE (errSwapMockRefused) so
// the node does NOT silently run "real" swaps on a mock; MockNano is permitted ONLY
// behind the explicit OBX_ALLOW_MOCK_NANO=1 escape hatch (local/dev/test on the
// value-less chain), and that path logs a loud WARNING. The real client must satisfy
// swapsession's XNOSweeper/XNOLocker (Lock/Confirmed/Sweep/LockInfo), which
// swapd.NanoClient does.
func nanoForSwaps(real swapd.NanoClient) (swapd.NanoClient, string, error) {
	if real != nil {
		return real, "real --nano-rpc client", nil
	}
	if os.Getenv("OBX_ALLOW_MOCK_NANO") == "1" {
		log.Printf("WARNING: OBX_ALLOW_MOCK_NANO=1 — swap engine running on MockNano (NO real XNO moves). " +
			"For the value-less local test chain ONLY; this violates the live-only policy on any real deployment.")
		return swapd.NewMockNano(), "MockNano (OBX_ALLOW_MOCK_NANO=1 test override)", nil
	}
	return nil, "", errSwapMockRefused
}

// wireSwapCoordinator builds and starts the swap Coordinator bound to this node,
// returning it (and the OBX fee it uses) for the RPC layer.
//
// ROLE TOPOLOGY. The Coordinator is given DISTINCT maker and taker capability
// objects so the two roles never share funding state:
//
//   - the MAKER caps (nodeMakerCaps) need only Sweep/Confirmed/LockInfo — the maker
//     RECEIVES XNO and sweeps it to its seed-derived dest; it has NO funded Nano
//     wallet of its own. A seller therefore runs maker-only.
//   - the TAKER caps (nodeTakerCaps) need Lock from the node's OWN funded
//     --nano-wallet/--nano-account — the taker PAYS XNO. A buyer runs taker-only with
//     a funded Nano wallet.
//
// In this in-node build BOTH roles are wired (so one node can play either side
// against another node), but the capability split is real: maker auto-fund is gated
// to this node's own published offers (acceptInitForOwnOffers) and the taker funds
// from the Nano backend. A pure maker-only deployment simply never calls Take; a
// pure taker-only deployment publishes no offers (so AcceptInit matches nothing).
//
// AcceptInit gates maker auto-funding to the node's OWN live published OBX/XNO
// offers AND now RESERVES the matched offer (so a second concurrent Init cannot
// double-fund the same liquidity); the reservation is committed on a swept swap and
// released on abort via OnMakerDone. SwapState is persisted under
// <datadir>/swapstate so a crash mid-swap can resume rather than freeze XNO.
func wireSwapCoordinator(c *chain.Chain, node *p2p.Node, minerSeed []byte, realNano swapd.NanoClient, xnoSweepDestOverride, datadir string) (*swapnet.Coordinator, *swaprelay.Relay, uint64, error) {
	// LIVE-ONLY POLICY (repo CLAUDE.md): refuse to start the swap engine on MockNano
	// by default. Without a real --nano-rpc client (and without the OBX_ALLOW_MOCK_NANO=1
	// escape hatch) this returns errSwapMockRefused, so we skip wiring entirely — main.go
	// logs "swap engine NOT wired" and /swaps/* stays empty while the rest of the node runs.
	nano, nanoDesc, err := nanoForSwaps(realNano)
	if err != nil {
		return nil, nil, 0, err
	}
	host := newSwapOBXHost(c, minerSeed, node)
	makerPub := makerPubFromSeed(minerSeed)

	// Resolve where the maker sweeps the XNO it receives from a settled OBX→XNO swap.
	usingRealNano := realNano != nil
	sweepDest, sweepDesc, err := resolveSweepDest(minerSeed, xnoSweepDestOverride, usingRealNano)
	if err != nil {
		return nil, nil, 0, err
	}

	// In-node SwapState persistence: a real directory under the datadir so a crash
	// mid-swap leaves a resumable record (the ResumeMaker/LoadState machinery) rather
	// than a frozen XNO leg. FAIL LOUDLY (audit #12): without a durable state dir a
	// crash mid-swap can leave locked funds unclaimable, so by default we REFUSE to start
	// the swap subsystem rather than run it non-resumably. Set OBX_SWAP_ALLOW_EPHEMERAL=1
	// to opt into the old in-memory behavior (explicitly accepting the frozen-funds risk).
	stateDir := filepath.Join(datadir, "swapstate")
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		if os.Getenv("OBX_SWAP_ALLOW_EPHEMERAL") == "1" {
			log.Printf("swap: could not create state dir %s (%v); SwapState persistence DISABLED "+
				"(OBX_SWAP_ALLOW_EPHEMERAL=1 — a crash mid-swap may freeze funds)", stateDir, err)
			stateDir = ""
		} else {
			return nil, nil, 0, fmt.Errorf("swap: cannot create state dir %s: %w (a crash mid-swap could "+
				"freeze funds; fix the path/permissions or set OBX_SWAP_ALLOW_EPHEMERAL=1 to run without "+
				"crash-resumable swaps)", stateDir, err)
		}
	}

	// rec tracks the offer reservation taken when AcceptInit authorizes a maker
	// session, keyed by SwapID, so OnMakerDone can commit (success) or release (abort).
	rec := newSwapReconciler(node)

	tr := swapnet.NewP2PTransport(node)
	// Wrap the p2p transport with the browser relay: a browser taker's swap
	// envelopes are injected into THIS coordinator's maker side as if from a
	// "browser:<swapID>" peer, and the maker's replies to that synthetic peer are
	// queued for the browser to long-poll. Real p2p peers fall through unchanged.
	// This is what lets a non-custodial browser taker swap with a node maker.
	relay := swaprelay.New(tr)
	fee := swapOBXFee
	coord, err := swapnet.New(swapnet.Config{
		Transport: relay,
		Maker:     nodeMakerCaps{h: host, nano: nano, sweepDest: sweepDest},
		Taker:     nodeTakerCaps{h: host, nano: nano},
		Timeout:   swapStallTimeout,
		Fee:       fee,
		StateDir:  stateDir,
		AcceptInit: func(init *swapsession.Init, fromPeer string) bool {
			return rec.acceptAndReserve(makerPub, init)
		},
		OnMakerDone: rec.makerDone,
	})
	if err != nil {
		return nil, nil, 0, err
	}
	relay.SetCoordinator(coord) // browser envelopes -> coord.Deliver(browser:<swapID>)
	tr.BindInbound(coord)       // real p2p node.OnSwapSession -> coord.Deliver
	// crash-resume: re-drive any persisted, non-terminal MAKER swaps so a node that
	// crashed mid-swap still sweeps (taker claimed) or refunds (taker stalled) its funded
	// OBX instead of stranding it. No-op when persistence is disabled (StateDir == "").
	coord.Resume()
	stateDesc := stateDir
	if stateDesc == "" {
		stateDesc = "(in-memory; persistence disabled)"
	}
	log.Printf("swap engine ENABLED: Nano=%s, fee=%s %s, XNO sweep dest=%s, state=%s, maker auto-fund RESERVES own published OBX/XNO offers",
		nanoDesc, config.FormatAmount(fee), config.Ticker, sweepDesc, stateDesc)
	return coord, relay, fee, nil
}

// swapReconciler binds the maker-side swap lifecycle to this node's order book:
// it RESERVES the matched offer when AcceptInit authorizes a maker session (so a
// concurrent Init cannot double-fund the same liquidity), then COMMITS the trade on
// a swept swap (joined to the on-chain SwapKey, decrementing the maker's own book)
// or RELEASES the reservation on abort. It is the MAKER-side counterpart to the
// taker's /swaps/take reserve→commit/release flow, so both nodes' books converge.
type swapReconciler struct {
	node *p2p.Node
	mu   sync.Mutex
	// reserved maps a per-session SwapID (hex) -> the offer reservation held for it,
	// pending commit/release at OnMakerDone time.
	reserved map[string][]swapbook.Reservation
}

func newSwapReconciler(node *p2p.Node) *swapReconciler {
	return &swapReconciler{node: node, reserved: map[string][]swapbook.Reservation{}}
}

// acceptAndReserve is the AcceptInit gate: it authorizes maker auto-funding ONLY
// when the Init matches one of this node's live OBX->XNO offers AND it can RESERVE
// the matching XNO capacity in the book. The reservation is recorded under the
// Init's SwapID so makerDone can finalize it. Returning false (no match, or no
// reservable liquidity) funds NOTHING (deny-by-default F-A).
func (r *swapReconciler) acceptAndReserve(makerPub []byte, init *swapsession.Init) bool {
	if !acceptInitForOwnOffers(r.node, makerPub, init) {
		return false // not one of our live offers / mismatched amounts → deny
	}
	// Reserve the matching XNO capacity (maker GIVES OBX, GETS XNO; from the book's
	// taker-orientation API that is Reserve(give=XNO, get=OBX, sizeXNOofferUnits)).
	// Convert the agreed RAW XNO back to offer-units (raw / 1e18) to size the reserve.
	xnoOfferUnits := swapd.XNORawToOfferUnits(init.XNOAmount)
	if xnoOfferUnits == 0 {
		return false
	}
	res, _, _, err := r.node.Reserve("XNO", "OBX", xnoOfferUnits, swapbook.ReserveOpts{})
	if err != nil || len(res) == 0 {
		return false // book could not hold the liquidity → do not fund
	}
	key := hex.EncodeToString(init.SwapID[:])
	r.mu.Lock()
	// If a reservation already exists for this SwapID (duplicate Init), release the
	// new one and keep the first (the coordinator's register() also rejects dup IDs).
	if _, dup := r.reserved[key]; dup {
		r.mu.Unlock()
		r.node.ReleaseReservation(res)
		return false
	}
	r.reserved[key] = res
	r.mu.Unlock()
	return true
}

// makerDone finalizes the reservation when a maker session terminates: COMMIT the
// trade (joined to the on-chain SwapKey) on success, RELEASE it on failure/abort.
func (r *swapReconciler) makerDone(s swapnet.MakerSettlement, success bool) {
	key := hex.EncodeToString(s.SwapID[:])
	r.mu.Lock()
	res := r.reserved[key]
	delete(r.reserved, key)
	r.mu.Unlock()
	if len(res) == 0 {
		return
	}
	if success {
		// Record the maker-side trade keyed by the ACTUAL on-chain SwapKey (the Fund
		// tx key), so this node's book decrements + tape entry match the taker node's.
		// The maker GIVES OBX, GETS XNO → tape orientation give=OBX, get=XNO.
		r.node.CommitTrade(res, "OBX", "XNO", s.SwapKey, "")
		return
	}
	r.node.ReleaseReservation(res)
}

// resolveSweepDest decides where the maker sweeps received XNO and validates it.
//
//   - override set: the operator supplied an explicit nano_ address (external/cold).
//     It MUST decode as a valid nano address; an invalid one is rejected so a real
//     OBX→XNO swap can never sweep proceeds into an unrecoverable destination.
//   - override empty + real Nano: derive a recoverable nano_ receive account from the
//     miner seed (swapd.MinerXNOAccount, domain "Obscura/xno-proceeds/v1"). The
//     operator can spend the swept XNO with the seed-derived secret (wallet follow-up).
//   - override empty + MockNano (test/default): use the opaque mock ledger key so the
//     value-less test chain keeps completing every leg unchanged.
//
// It returns the destination and a human-readable description for the startup log.
func resolveSweepDest(minerSeed []byte, override string, usingRealNano bool) (dest, desc string, err error) {
	if override != "" {
		if _, derr := swapd.DecodeNanoAddress(override); derr != nil {
			return "", "", fmt.Errorf("--xno-sweep-dest %q is not a valid nano address: %w (refusing to enable OBX→XNO offers that would sweep proceeds nowhere recoverable)", override, derr)
		}
		return override, override + " (operator override)", nil
	}
	if !usingRealNano {
		// MockNano default: opaque ledger key, never a real address.
		return mockSweepDest, mockSweepDest + " (MockNano test key)", nil
	}
	_, _, addr, derr := swapd.MinerXNOAccount(minerSeed)
	if derr != nil {
		return "", "", fmt.Errorf("derive miner XNO sweep dest from seed: %w", derr)
	}
	return addr, addr + " (derived from miner seed)", nil
}

// swapStallTimeout is the per-phase stall deadline before a funder arms a refund /
// a taker aborts. Generous because each OBX leg mines real (class-group PoW) blocks
// AND the claim/sweep wait for on-chain confirmation depth (SwapReorgMargin blocks).
// On a fast devnet (1s blocks) that confirmation wait can approach the default 90s,
// so OBX_SWAP_STALL_SEC raises it for test deployments; production keeps 90s.
var swapStallTimeout = func() time.Duration {
	if v := os.Getenv("OBX_SWAP_STALL_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 90 * time.Second
}()

// swapOBXFee is the OBX fee BOTH parties use for swap funding/claim/refund. It is
// well above MinFeePerByte yet far below any takeable amount; not negotiated in-band
// (the offer fixes terms out of band — see swapnet.Config.Fee).
const swapOBXFee = uint64(1_000_000_000) // 0.001 OBX in atomic units

// makerPubFromSeed derives the maker pubkey for this node's published offers from
// the miner seed, matching the auto-liquidity loop's derivation EXACTLY
// (commit.HashToScalar("Obscura/maker/v1", seed)) so AcceptInit recognises Inits
// taken against our own auto-posted offers.
func makerPubFromSeed(seed []byte) []byte {
	sec := commit.HashToScalar([]byte("Obscura/maker/v1"), seed)
	return new(edwards25519.Point).ScalarBaseMult(sec).Bytes()
}

// acceptInitForOwnOffers authorizes maker auto-funding ONLY when the inbound Init's
// amounts match one of THIS node's live published OBX->XNO offers (converted to the
// swap leg's atomic/raw units). This is the deny-by-default F-A binding: a node never
// funds OBX for an unsolicited or mismatched Init.
func acceptInitForOwnOffers(node *p2p.Node, makerPub []byte, init *swapsession.Init) bool {
	initXNO := init.XNOAmount
	if initXNO == nil {
		initXNO = new(big.Int)
	}
	// Reject dust/empty up front: a real fill moves a positive amount on BOTH legs.
	if init.OBXAmount == 0 || initXNO.Sign() <= 0 {
		return false
	}
	for _, o := range node.MakerOffers(makerPub) {
		if o.GiveAsset != "OBX" || o.GetAsset != "XNO" {
			continue
		}
		obxAtomic, xnoRaw := offerLegAmounts(o)
		// Accept the FULL offer OR a same-rate PARTIAL fill within the offer's
		// capacity. Partial fills are the whole point of market/IOC/FOK orders — the
		// taker's /swaps/take reserves a slice, so the maker must honor a slice too,
		// not only an exact full-offer match (which froze EVERY partial swap at init).
		// Capacity: both legs ≤ the offer's legs. Price: the slice must be at EXACTLY
		// the offer's rate (never worse for the maker), checked by cross-multiplication
		// in big.Int to avoid uint64 overflow and integer-division rounding:
		//     init.OBX / init.XNO == obxAtomic / xnoRaw
		//   ⇔ init.OBX · xnoRaw   == obxAtomic · init.XNO
		if init.OBXAmount > obxAtomic || initXNO.Cmp(xnoRaw) > 0 {
			continue // slice exceeds this offer's remaining capacity
		}
		lhs := new(big.Int).Mul(new(big.Int).SetUint64(init.OBXAmount), xnoRaw)
		rhs := new(big.Int).Mul(new(big.Int).SetUint64(obxAtomic), initXNO)
		if lhs.Cmp(rhs) == 0 {
			return true
		}
	}
	return false
}

// offerLegAmounts converts an OBX->XNO offer's human-decimal give/get into the swap
// leg's SETTLEMENT units: OBX in on-chain atomic units (10^12) and XNO in RAW
// (*big.Int, 1 XNO = 1e30 raw). The offer's GetAmount is in offer-units (1e12-scale),
// so the XNO leg is offerUnits × 10^18 = raw via swapd.XNOOfferUnitsToRaw — the SAME
// conversion the RPC /swaps/take path applies, so a taker's Init derived from an offer
// matches the amount this maker checks before auto-funding.
func offerLegAmounts(o *swapbook.Offer) (obxAtomic uint64, xnoRaw *big.Int) {
	exp := 12 - config.AutoLiquidityDecimals["OBX"]
	obxAtomic = o.GiveAmount
	if exp >= 0 {
		obxAtomic = o.GiveAmount * pow10u(exp)
	} else {
		obxAtomic = o.GiveAmount / pow10u(-exp)
	}
	return obxAtomic, swapd.XNOOfferUnitsToRaw(new(big.Int).SetUint64(o.GetAmount))
}
