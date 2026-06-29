// Command obscura-swap runs a trustless XNO<->OBX atomic swap end-to-end. It
// orchestrates the proven swap primitives (adaptor 2-of-2 OBX leg + scriptless XNO
// leg) over the swapd.NanoClient interface, so the SAME orchestration runs against
// MockNano (selftest, no network) or a real Nano node (live).
//
//	obscura-swap selftest
//	    Full XNO->OBX->XNO round trip against MockNano on a local OBX devnet. Proves
//	    the orchestration with no network and no funds.
//
//	obscura-swap live --nano-rpc rainstorm --xno-dest nano_... [--xno-amount-raw N]
//	    Real run: shows a STATUS PANEL, waits for you to send XNO to a joint address,
//	    settles the OBX leg locally, then sweeps the real XNO back to --xno-dest. The
//	    sweep is the LIVE GATE for the from-scratch Nano signer; it also returns your
//	    funds (Nano is feeless).
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"filippo.io/edwards25519"

	"obscura/pkg/chain"
	"obscura/pkg/commit"
	"obscura/pkg/config"
	"obscura/pkg/mempool"
	"obscura/pkg/miner"
	"obscura/pkg/p2p"
	"obscura/pkg/pow"
	"obscura/pkg/swap"
	"obscura/pkg/swapd"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
)

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "selftest":
		runSelfTest()
	case "live":
		runLive(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Print(`obscura-swap — trustless XNO<->OBX atomic swap

  obscura-swap selftest
  obscura-swap live --nano-rpc <preset|url> --xno-dest <nano_...> [--xno-amount-raw N]
                    [--obx-seed <host:port>] [--obx-datadir <path>] [--obx-amount <OBX>]

  --xno-amount-raw N  require AT LEAST N raw XNO (cemented) before settling (else any).
  --obx-amount  <OBX> OBX locked into the swap output (default 3).

  --obx-seed joins the REAL OBX testnet at that seed (P2P): the OBX leg syncs,
  mines, and broadcasts so the swap settles on the shared chain. Omit it to keep
  the original isolated in-process OBX devnet.

`)
	fmt.Print(swapd.NanoPresetList())
	os.Exit(2)
}

// ---- shared primitives ------------------------------------------------------

func pt(s *edwards25519.Scalar) *edwards25519.Point { return new(edwards25519.Point).ScalarBaseMult(s) }

func randKey() []byte { b := make([]byte, 32); _, _ = rand.Read(b); return b }

// mineWith mines one block: funder's coinbase + the given txs, grinds PoW, applies it.
//
// node selects the mining mode:
//   - node == nil  → isolated in-process devnet (selftest / default live). Uses the
//     epoch-0 PoW seed via miner.Mine and does NOT broadcast — behavior is
//     byte-for-byte unchanged from the original devnet path.
//   - node != nil  → SHARED testnet leg. Grinds under the per-epoch consensus seed
//     (c.PoWSeed) exactly like cmd/obscura-node's mineLoop — required because a
//     synced chain may already be past the epoch-0 boundary — and BROADCASTS the
//     mined block to peers so it propagates onto the real network.
func mineWith(c *chain.Chain, funder *wallet.Wallet, txs []*tx.Transaction, node *p2p.Node) error {
	fees := chain.CollectedFees(txs) // coinbase must mint subsidy + the block's tx fees
	cb, err := funder.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(fees, nil), nil)
	if err != nil {
		return fmt.Errorf("coinbase: %w", err)
	}
	all := append([]*tx.Transaction{cb}, txs...)
	tmpl, err := c.BlockTemplate(all)
	if err != nil {
		return fmt.Errorf("template: %w", err)
	}
	if node == nil {
		if !miner.Mine(context.Background(), tmpl, 0) {
			return fmt.Errorf("mining failed")
		}
		return c.AddBlock(tmpl)
	}
	// Shared-network path: same call sequence as cmd/obscura-node's mineLoop
	// (MineSeed under the per-epoch seed, AddBlock, then BroadcastBlock).
	if !miner.MineSeed(context.Background(), tmpl, c.PoWSeed(tmpl.Header.Height), 0) {
		return fmt.Errorf("mining failed")
	}
	if err := c.AddBlock(tmpl); err != nil {
		return err
	}
	go node.BroadcastBlock(tmpl)
	return nil
}

func scanAll(c *chain.Chain, w *wallet.Wallet) {
	for h := uint64(0); h <= c.Height(); h++ {
		if b, ok := c.BlockByHeight(h); ok {
			w.ScanBlock(b)
		}
	}
}

// swapSecrets are the per-swap keys: T=sA·G bridges the chains; the XNO joint key is
// sA+sB; the OBX claim key is A+B (a,b). xnoPub is the joint Nano account pubkey.
type swapSecrets struct {
	sA, sB, a, b *edwards25519.Scalar
	xnoPub       []byte
}

func newSecrets() (swapSecrets, error) {
	s := swapSecrets{sA: commit.RandomScalar(), sB: commit.RandomScalar(), a: commit.RandomScalar(), b: commit.RandomScalar()}
	pub, err := swapd.NanoAccountPub(pt(s.sA).Bytes(), pt(s.sB).Bytes())
	if err != nil {
		return swapSecrets{}, err
	}
	s.xnoPub = pub
	return s, nil
}

// doAtomicSwap runs ONE atomic swap given an already-obtained XNO lockID (the joint
// account holding the locked XNO). funder funds the OBX SwapOut to K=A+B; claimer claims
// it (revealing sA via the adaptor); the recovered sA+sB then sweeps the XNO to sweepDest.
// This is the whole protocol; selftest and live differ only in how the lock is obtained.
// node is nil for the isolated devnet and non-nil for the shared testnet leg; it is
// passed straight through to mineWith so each swap tx is mined+broadcast accordingly.
// The adaptor-secret extraction logic below is identical in both modes.
func doAtomicSwap(c *chain.Chain, nano swapd.NanoClient, funder, claimer *wallet.Wallet,
	obxAmount, fee uint64, sec swapSecrets, lockID, sweepDest string, node *p2p.Node) error {

	K := swap.AggregateKey(pt(sec.a), pt(sec.b))
	T := pt(sec.sA)
	swapKey := randKey()
	unlock := c.Height() + config.SwapTimelockWindow // OBX timelock: refund path opens here if the swap aborts (#10)

	// The aggregate pre-signature nonce R = Ra+Rb and adaptor point T are agreed
	// and committed at FUND time (consensus binds the claim sig to R+T). The same
	// nonces are reused for the claim's pre-signature below.
	ra, rb := commit.RandomScalar(), commit.RandomScalar()
	R := new(edwards25519.Point).Add(pt(ra), pt(rb))
	popA := swap.ProvePossession(sec.a)
	popB := swap.ProvePossession(sec.b)

	// 1) funder locks OBX into the swap output (claim path = 2-of-2 K, refund path = funder b).
	fund, err := funder.FundSwap(c, swapKey, obxAmount, K.Bytes(), pt(sec.b).Bytes(),
		pt(sec.a).Bytes(), pt(sec.b).Bytes(), popA, popB, R.Bytes(), T.Bytes(), unlock, fee)
	if err != nil {
		return fmt.Errorf("fund OBX swap: %w", err)
	}
	if err := mineWith(c, funder, []*tx.Transaction{fund}, node); err != nil {
		return err
	}
	log.Printf("  · OBX swap output funded (%s OBX, claim key K, unlock@%d)", config.FormatAmount(obxAmount), unlock)

	// #7/#8: the OBX is now locked. Any failure BEFORE the claim is mined leaves it
	// unspent and reclaimable — so reclaim it via the refund branch (at UnlockHeight)
	// instead of abandoning it. After the claim is mined the OBX is gone (claimed), so
	// post-claim failures fall through to XNO recovery, NOT refund.
	refundOnFail := func(cause error) error {
		log.Printf("  · settlement failed before claim: %v", cause)
		if rerr := refundOBX(c, funder, sec, swapKey, obxAmount, fee, unlock, node); rerr != nil {
			return fmt.Errorf("%w; AND refund failed: %v (OBX stays locked until height %d, reclaim with refund key b)", cause, rerr, unlock)
		}
		return fmt.Errorf("%w (locked OBX was refunded to the funder)", cause)
	}

	// 2) claimer builds the adaptor-adapted 2-of-2 claim sig — publishing it WILL reveal sA.
	// Reuse the SAME committed nonces ra,rb so the claim sig's nonce equals R+T.
	var pre *commit.AdaptorSig
	claim, err := claimer.BuildSwapSpend(swapKey, obxAmount, false, fee, func(coreHash []byte) []byte {
		p, _ := swap.CoSignClaim(sec.a, sec.b, ra, rb, coreHash, T)
		pre = p
		full, _ := commit.Adapt(p, sec.sA, T)
		return full.Serialize()
	})
	if err != nil {
		return refundOnFail(fmt.Errorf("build OBX claim: %w", err))
	}

	// #12: VERIFY the adaptor extract round-trips to the joint XNO key BEFORE mining the
	// claim — i.e. before sA is revealed on-chain. The mined claim carries this exact sig,
	// so if the recovered key would not control the locked account we abort now: the claim
	// is never broadcast, sA stays secret, and the XNO is untouched. Previously this check
	// ran AFTER the claim was committed, stranding the XNO with no salvage on mismatch.
	fullSig, err := commit.ParseFullSig(claim.SwapInputs[0].Sig)
	if err != nil {
		return refundOnFail(fmt.Errorf("parse claim sig: %w", err))
	}
	recovered, err := commit.Extract(pre, fullSig)
	if err != nil {
		return refundOnFail(fmt.Errorf("pre-mine extract check failed (not revealing sA): %w", err))
	}
	accountSecret := new(edwards25519.Scalar).Add(recovered, sec.sB)
	if got := pt(accountSecret).Bytes(); string(got) != string(sec.xnoPub) {
		return refundOnFail(fmt.Errorf("pre-mine: recovered XNO key does not control the locked joint account — NOT revealing sA (your XNO stays safe in the joint account)"))
	}

	// 3) Safe to reveal: mine the claim (publishes sA on-chain), then sweep the XNO with
	// the already-verified joint key sA+sB.
	if err := mineWith(c, funder, []*tx.Transaction{claim}, node); err != nil {
		return err
	}
	scanAll(c, claimer)
	log.Printf("  · OBX claimed on-chain (claimer balance now %s OBX) — secret sA revealed", config.FormatAmount(claimer.Balance()))
	log.Printf("  · extracted joint XNO key sA+sB; sweeping locked XNO -> %s", sweepDest)
	if err := nano.Sweep(lockID, accountSecret, sweepDest); err != nil {
		return fmt.Errorf("XNO sweep: %w", err)
	}
	log.Printf("  · XNO swept. atomic swap complete.")
	return nil
}

// refundOBX reclaims the funder's locked OBX via the refund branch after UnlockHeight.
// Invoked when a swap stalls/fails AFTER FundSwap but BEFORE the claim is mined (so the
// swap output is still unspent). It advances the chain to the unlock height (mining in
// devnet; waiting on the shared-net miner otherwise), builds a refund spend signed by the
// funder's refund key sec.b (a plain Schnorr full-sig under RefundKey, which consensus
// accepts only at/after UnlockHeight — pkg/swap.SwapOutput.VerifyRefund), and mines it.
// This makes the consensus refund branch actually executable + watched (#7/#8). A real
// two-party flow will arm this on a timer with the negotiated deadline (WS2); here it is
// the funder's safety net for the demo.
func refundOBX(c *chain.Chain, funder *wallet.Wallet, sec swapSecrets, swapKey []byte, obxAmount, fee, unlock uint64, node *p2p.Node) error {
	log.Printf("  · REFUND: reclaiming %s OBX; waiting for unlock height %d (current %d)…", config.FormatAmount(obxAmount), unlock, c.Height())
	for c.Height() < unlock {
		if node == nil {
			if err := mineWith(c, funder, nil, node); err != nil {
				return fmt.Errorf("advance to unlock: %w", err)
			}
		} else {
			time.Sleep(2 * time.Second) // the shared-net miner advances height
		}
	}
	refund, err := funder.BuildSwapSpend(swapKey, obxAmount, true, fee, func(coreHash []byte) []byte {
		return commit.Sign(sec.b, coreHash).Serialize()
	})
	if err != nil {
		return fmt.Errorf("build refund: %w", err)
	}
	if err := mineWith(c, funder, []*tx.Transaction{refund}, node); err != nil {
		return fmt.Errorf("mine refund: %w", err)
	}
	scanAll(c, funder)
	log.Printf("  · REFUND complete: %s OBX reclaimed by the funder at height %d.", config.FormatAmount(obxAmount), c.Height())
	return nil
}

// ---- selftest: full round trip, MockNano, no network ------------------------

func runSelfTest() {
	old := config.CoinbaseMaturity
	config.CoinbaseMaturity = 1
	defer func() { config.CoinbaseMaturity = old }()

	dir, _ := os.MkdirTemp("", "obx-swap-selftest")
	defer os.RemoveAll(dir)
	c, err := chain.New(dir)
	if err != nil {
		log.Fatalf("chain: %v", err)
	}
	defer c.Close()

	user := wallet.FromSeed([]byte("swap-selftest-user-000000000000000"))
	us := wallet.FromSeed([]byte("swap-selftest-us-00000000000000000"))
	// fund both OBX sides so each can play the OBX funder in its direction.
	if err := fund(c, user, 4); err != nil {
		log.Fatal(err)
	}
	if err := fund(c, us, 4); err != nil {
		log.Fatal(err)
	}

	mock := swapd.NewMockNano()
	// 0.00001 XNO = 1e25 raw — a real sub-cent amount that OVERFLOWS uint64 (max ~1.8e19),
	// proving the *big.Int amount path round-trips real raw exactly (not saturated).
	xnoRaw, _ := new(big.Int).SetString("10000000000000000000000000", 10) // 1e25
	obxAmt := 3 * config.AtomicPerCoin
	fee := uint64(1_000_000_000)

	log.Printf("=== SELFTEST: XNO -> OBX (user locks XNO, receives OBX) ===")
	s1, _ := newSecrets()
	lock1, _ := mock.Lock(xnoRaw, s1.xnoPub) // user's XNO lock (simulated)
	if err := doAtomicSwap(c, mock, us, user, obxAmt, fee, s1, lock1, "us-xno", nil); err != nil {
		log.Fatalf("swap 1 (XNO->OBX) FAILED: %v", err)
	}
	if mock.Balance("us-xno").Cmp(xnoRaw) != 0 {
		log.Fatalf("swap 1: us did not receive XNO")
	}

	log.Printf("=== SELFTEST: OBX -> XNO (user locks OBX-equiv via XNO leg, gets XNO back) ===")
	s2, _ := newSecrets()
	lock2, _ := mock.Lock(xnoRaw, s2.xnoPub) // the XNO we now hold is re-locked toward the user
	if err := doAtomicSwap(c, mock, user, us, obxAmt, fee, s2, lock2, "user-xno", nil); err != nil {
		log.Fatalf("swap 2 (OBX->XNO) FAILED: %v", err)
	}
	if mock.Balance("user-xno").Cmp(xnoRaw) != 0 {
		log.Fatalf("swap 2: user did not get XNO back")
	}

	log.Printf("\nSELFTEST PASSED ✓  full XNO->OBX->XNO round trip settled atomically (orchestration verified).")
}

func fund(c *chain.Chain, w *wallet.Wallet, blocks int) error {
	for i := 0; i < blocks; i++ {
		if err := mineWith(c, w, nil, nil); err != nil {
			return err
		}
	}
	scanAll(c, w)
	return nil
}

// ---- live: real Nano node, status panel, sweep back to the user -------------

func runLive(args []string) {
	fs := flagSet(args)
	if fs.rpc == "" {
		log.Fatalf("live needs --nano-rpc <preset|url>")
	}
	cfg, isPreset := swapd.ResolveNanoSelector(fs.rpc)
	if fs.work != "" {
		cfg.WorkURL = fs.work
	}
	nano, err := swapd.NewNanoRPC(cfg)
	if err != nil {
		log.Fatalf("nano: %v", err)
	}
	// The user provides NO address. If --xno-dest is omitted we GENERATE a fresh Nano
	// destination account here (and log its secret so the swept funds are recoverable),
	// so the whole test is self-contained: the user only sends to the joint address we show.
	if fs.xnoDest == "" {
		dsk := commit.RandomScalar()
		dpub := pt(dsk).Bytes()
		addr, derr := swapd.EncodeNanoAddress(dpub)
		if derr != nil {
			log.Fatalf("generate dest: %v", derr)
		}
		fs.xnoDest = addr
		log.Printf("generated destination XNO account: %s", addr)
		log.Printf("  (dest secret key hex: %s — keep this to recover the swept funds)", hex.EncodeToString(dsk.Bytes()))
	} else if _, err := swapd.DecodeNanoAddress(fs.xnoDest); err != nil {
		log.Fatalf("--xno-dest is not a valid nano address: %v", err)
	}

	ver, verr := nano.Version()
	sec, _ := newSecrets()
	jointAddr, _ := swapd.EncodeNanoAddress(sec.xnoPub)
	// Log the joint half-secrets so the locked XNO is recoverable (sweep needs sA+sB)
	// even if the executor crashes before sweeping — fund-safety for the live run.
	log.Printf("joint account %s — recovery half-keys sA=%s sB=%s", jointAddr,
		hex.EncodeToString(sec.sA.Bytes()), hex.EncodeToString(sec.sB.Bytes()))

	// Set up the OBX leg. Two modes:
	//   - default (no --obx-seed): an isolated, in-process devnet (unchanged).
	//   - --obx-seed set: JOIN the real OBX testnet at that seed, sync, and mine
	//     coinbase to `us` on the SHARED chain so the swap txs land on the network.
	us := wallet.FromSeed([]byte("swap-live-us-000000000000000000000"))
	user := wallet.FromSeed([]byte("swap-live-user-0000000000000000000"))
	// #5: size the OBX side and ensure the OBX wallet is PROVABLY fundable (>= obxAmt+fee)
	// BEFORE advertising the joint address — otherwise the user could lock XNO into a swap
	// the OBX side can never fund. (#4: obxAmt is operator-set via --obx-amount, default 3.)
	obxAmt := obxAtomic(fs.obxAmount)
	fee := uint64(1_000_000_000)
	leg := setupOBXLeg(fs, us, obxAmt+fee)
	defer leg.close()
	c := leg.c

	src := "custom URL"
	if isPreset {
		src = "preset " + fs.rpc
	}
	nodeStatus := "REACHABLE (" + ver + ")"
	if verr != nil {
		nodeStatus = "UNREACHABLE (" + verr.Error() + ")"
	}
	// #3: if the operator agreed an amount, require AT LEAST that many raw before
	// settling; the panel reflects the requirement.
	var minRaw *big.Int
	amtNote := "any amount (no minimum enforced — demo)"
	if fs.xnoAmountRaw != "" {
		v, ok := new(big.Int).SetString(fs.xnoAmountRaw, 10)
		if !ok {
			log.Fatalf("--xno-amount-raw must be an integer amount in raw")
		}
		minRaw = v
		amtNote = "at least " + fs.xnoAmountRaw + " raw"
	}

	fmt.Printf(`
┌─────────────────────────  XNO ↔ OBX SWAP — STATUS  ─────────────────────────┐
  Nano RPC ........ %s  [%s]
                    %s
  OBX leg ......... %s, funded, height %d  (OBX leg ready)
  Swap secrets .... generated (sA,sB,a,b); joint key sA+sB derived
  ⚠ EXPERIMENTAL ... this demo plays BOTH sides; there is NO XNO refund and the
                     OBX refund path is not yet wired — do NOT use real value

  ▶ SEND your XNO now (%s) to the JOINT account:

        %s

  After it confirms I will: settle the OBX leg locally, extract the joint
  key, and SWEEP the XNO to your address:  %s
  (Nano is feeless, so your funds return to you; this also validates the
   from-scratch Nano signer against the live network — the LIVE GATE.)
└─────────────────────────────────────────────────────────────────────────────┘

`, cfg.URL, src, nodeStatus, leg.label, c.Height(), amtNote, jointAddr, fs.xnoDest)

	if verr != nil {
		log.Printf("NOTE: version check failed (%v) — proceeding to poll anyway (public RPCs are occasionally slow; the receivable poll + sweep retry on their own).", verr)
	}

	log.Printf("Waiting for your XNO send to %s (polling receivable; Ctrl-C to abort)…", jointAddr)
	lockID := waitForReceivable(nano, jointAddr, minRaw, 30*time.Minute)
	if lockID == "" {
		log.Fatalf("no cemented XNO lock of the agreed amount; no funds moved (any XNO you sent stays in the joint account, recoverable via sA+sB)")
	}
	log.Printf("XNO lock %s cemented. Settling OBX leg and sweeping…", lockID)

	if err := doAtomicSwap(c, nano, us, user, obxAmt, fee, sec, lockID, fs.xnoDest, leg.node); err != nil {
		log.Fatalf("SWAP FAILED after lock: %v\n(your XNO is in the joint account; recover it with sA+sB — keep these logs)", err)
	}
	log.Printf("\nLIVE SWAP COMPLETE ✓  Your XNO was swept back to %s. The Nano signer worked on mainnet.", fs.xnoDest)
}

// obxLeg holds the OBX side of a live swap. node is nil for the isolated devnet and
// the running P2P node for the shared-testnet leg; doAtomicSwap/mineWith key off it.
type obxLeg struct {
	c     *chain.Chain
	node  *p2p.Node // nil ⇒ isolated devnet
	label string    // status-panel label ("local devnet" / "testnet <seed>")
	close func()
}

// setupOBXLeg prepares the OBX side and funds `us` enough to build FundSwap.
//
// Without --obx-seed it reproduces the original isolated devnet exactly: a throwaway
// temp chain, CoinbaseMaturity lowered to 1, 4 self-mined coinbase blocks.
//
// With --obx-seed it JOINS the real OBX testnet: opens a PERSISTENT chain, starts a
// P2P node, dials the seed, syncs, then runs a paced miner (coinbase → `us`) reusing
// cmd/obscura-node's mineLoop pacing/seed/broadcast path until `us` has spendable
// coinbase. CoinbaseMaturity is left at the network default (all nodes must agree).
func setupOBXLeg(fs liveFlags, us *wallet.Wallet, required uint64) *obxLeg {
	if fs.obxSeed == "" {
		// --- isolated in-process devnet (unchanged behavior) -----------------
		old := config.CoinbaseMaturity
		config.CoinbaseMaturity = 1
		dir, _ := os.MkdirTemp("", "obx-swap-live")
		c, err := chain.New(dir)
		if err != nil {
			log.Fatalf("chain: %v", err)
		}
		// #5: fund until provably able to build FundSwap (>= obxAmt+fee).
		for i := 0; us.Balance() < required; i++ {
			if i > 1000 {
				log.Fatalf("fund OBX side: could not reach %s OBX", config.FormatAmount(required))
			}
			if err := fund(c, us, 1); err != nil {
				log.Fatalf("fund OBX side: %v", err)
			}
		}
		return &obxLeg{c: c, node: nil, label: "local devnet", close: func() {
			c.Close()
			os.RemoveAll(dir)
			config.CoinbaseMaturity = old
		}}
	}

	// --- shared OBX testnet leg ----------------------------------------------
	// A node may run the prototype PoW backend on a live chain, so
	// honor OBX_ALLOW_PROTOTYPE_POW exactly like cmd/obscura-node's guard.
	guardPrototypePoW()
	datadir := fs.obxDatadir
	if datadir == "" {
		// Stable, reusable path so the synced chain persists across runs.
		datadir = filepath.Join(os.TempDir(), "obscura-swap-obx")
	}
	if err := os.MkdirAll(datadir, 0700); err != nil {
		log.Fatalf("obx datadir: %v", err)
	}
	c, err := chain.New(datadir)
	if err != nil {
		log.Fatalf("obx chain: %v", err)
	}
	log.Printf("OBX testnet leg: chain at %s (height %d), dialing seed %s", datadir, c.Height(), fs.obxSeed)

	// P2P node + mempool, mirroring cmd/obscura-node's wiring. Bind P2P to an
	// ephemeral port so multiple executors / a co-located node never clash.
	mp := mempool.New(c)
	node := p2p.NewNode("0.0.0.0:0", c, mp, filepath.Join(datadir, "peers.json"))
	if err := node.Start([]string{fs.obxSeed}); err != nil {
		log.Fatalf("obx p2p: %v", err)
	}
	log.Printf("OBX P2P listening on %s; syncing from %s…", node.Addr(), fs.obxSeed)

	// Wait for the chain to catch up to peers before mining, so our blocks build
	// on the network tip (node.syncLoop drives the download once a peer connects).
	syncToNetwork(c, node)

	// Paced miner to `us`, reusing the node's mineLoop pacing+seed+broadcast path.
	stopMiner := make(chan struct{})
	go netMineLoop(c, mp, node, us.Address(), stopMiner)

	// #5: mine until `us` holds enough spendable (mature) coinbase to fund the swap
	// (>= obxAmt+fee), so the joint address is only advertised once OBX is fundable.
	log.Printf("mining OBX coinbase to swap wallet until it can fund the swap (>= %s OBX)…", config.FormatAmount(required))
	for {
		scanAll(c, us)
		if us.Balance() >= required {
			break
		}
		time.Sleep(2 * time.Second)
	}
	log.Printf("OBX swap wallet funded: %s OBX spendable (need >= %s) (height %d)", config.FormatAmount(us.Balance()), config.FormatAmount(required), c.Height())

	return &obxLeg{c: c, node: node, label: "testnet " + fs.obxSeed, close: func() {
		close(stopMiner)
		node.Stop()
		c.Close()
		// NB: persistent datadir is intentionally NOT removed.
	}}
}

// syncToNetwork blocks until the chain stops advancing from peer sync (height
// stable across two polls while at least one peer is connected), or a short grace
// period elapses with no peers. It is a best-effort barrier before we start mining.
func syncToNetwork(c *chain.Chain, node *p2p.Node) {
	deadline := time.Now().Add(90 * time.Second)
	last := c.Height()
	stable := 0
	for time.Now().Before(deadline) {
		time.Sleep(3 * time.Second)
		h := c.Height()
		if node.PeerCount() == 0 {
			continue // wait for the seed handshake before judging sync
		}
		if h == last {
			if stable++; stable >= 2 {
				break // height held steady with peers connected ⇒ synced to tip
			}
		} else {
			stable = 0
		}
		last = h
	}
	log.Printf("OBX synced to height %d (peers=%d)", c.Height(), node.PeerCount())
}

// netMineLoop is the executor's miner for the shared testnet leg. It is a trimmed
// copy of cmd/obscura-node's mineLoop: SAME target-block-time pacing (so two miners
// — the droplet + this executor — converge instead of one bursting), SAME per-epoch
// PoWSeed via MineSeed, SAME AddBlock + async BroadcastBlock. It only adds a stop
// channel and mines empty coinbase blocks (the swap txs are mined inline by mineWith,
// which also broadcasts) so the wallet matures funds and stays on the network tip.
func netMineLoop(c *chain.Chain, mp *mempool.Mempool, node *p2p.Node, dest commit.StealthAddress, stop <-chan struct{}) {
	target := time.Duration(config.TargetBlockTime) * time.Second
	var prevStart time.Time
	for {
		select {
		case <-stop:
			return
		default:
		}
		// Pace to the target interval, exactly like the node, so directly-connected
		// miners converge (see cmd/obscura-node mineLoop's rationale).
		if pace := target - time.Since(prevStart); !prevStart.IsZero() && pace > 0 {
			time.Sleep(pace)
		}
		prevStart = time.Now()

		txs := mp.Select(1000)
		fees := chain.CollectedFees(txs)
		minted := c.ExpectedCoinbaseMinted(fees, nil)
		cb, err := wallet.BuildCoinbaseTo(dest, c.Height()+1, minted, nil)
		if err != nil {
			log.Printf("coinbase: %v", err)
			time.Sleep(time.Second)
			continue
		}
		all := append([]*tx.Transaction{cb}, txs...)
		tmpl, err := c.BlockTemplate(all)
		if err != nil {
			log.Printf("template: %v", err)
			time.Sleep(time.Second)
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		// re-template if a block arrives mid-mining (same as the node).
		mstop := make(chan struct{})
		go func() {
			startH := tmpl.Header.Height
			t := time.NewTicker(500 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-mstop:
					return
				case <-t.C:
					if c.Height() >= startH {
						cancel()
						return
					}
				}
			}
		}()
		found := miner.MineSeed(ctx, tmpl, c.PoWSeed(tmpl.Header.Height), 0)
		close(mstop)
		cancel()
		if !found {
			continue
		}
		if err := c.AddBlock(tmpl); err != nil {
			continue
		}
		mp.Remove(tmpl.Txs)
		go node.BroadcastBlock(tmpl)
	}
}

// guardPrototypePoW mirrors cmd/obscura-node: refuse to run on the insecure
// prototype PoW backend unless OBX_ALLOW_PROTOTYPE_POW=1 (dev networks set it).
func guardPrototypePoW() {
	const prototypeBackend = "vm-randomx-style"
	if pow.BackendName != prototypeBackend {
		return
	}
	if os.Getenv("OBX_ALLOW_PROTOTYPE_POW") != "1" {
		log.Fatalf("refusing to join the OBX network on prototype PoW backend %q; set OBX_ALLOW_PROTOTYPE_POW=1 to override (non-production only)", pow.BackendName)
	}
	log.Printf("OBX_ALLOW_PROTOTYPE_POW=1 set — joining the network on prototype PoW backend %q", pow.BackendName)
}

// waitForReceivable polls for the user's XNO send to `account` and returns its block
// hash ONLY once it is (a) at least `minRaw` (if set) and (b) CEMENTED. Issues #1 + #3:
//   - #1: the previous code returned on first sighting and the OBX secret sA was then
//     revealed against a not-yet-cemented (replaceable) Nano send — if it never cemented
//     the OBX leg was lost. We now block on nano.Confirmed before settling.
//   - #3: the previous code ignored the agreed amount entirely; a 1-raw send would still
//     trigger the full OBX settlement. We now refuse to settle below the agreed minimum.
func waitForReceivable(nano *swapd.NanoRPC, account string, minRaw *big.Int, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		hash, amt, ok := nano.Receivable(account)
		if !ok {
			time.Sleep(6 * time.Second)
			continue
		}
		amtInt, okAmt := new(big.Int).SetString(amt, 10)
		if !okAmt {
			log.Printf("  receivable %s has an unparseable amount %q; ignoring", hash, amt)
			time.Sleep(6 * time.Second)
			continue
		}
		if minRaw != nil && amtInt.Cmp(minRaw) < 0 {
			log.Printf("  receivable %s is %s raw, BELOW the agreed %s raw — NOT settling; waiting for the correct amount", hash, amt, minRaw)
			time.Sleep(6 * time.Second)
			continue
		}
		log.Printf("  receivable seen: %s raw (block %s) — waiting for cementation before settling…", amt, hash)
		// #1: do NOT reveal the OBX adaptor secret against an un-cemented send.
		confDeadline := time.Now().Add(5 * time.Minute)
		for time.Now().Before(confDeadline) {
			if nano.Confirmed(hash) {
				log.Printf("  XNO lock %s cemented ✓", hash)
				return hash
			}
			time.Sleep(4 * time.Second)
		}
		log.Printf("  receivable %s did not cement within 5m; NOT settling (your XNO stays in the joint account, recoverable via sA+sB)", hash)
		return ""
	}
	return ""
}

type liveFlags struct {
	rpc, xnoDest, xnoAmountRaw, work, obxSeed, obxDatadir, obxAmount string
}

// obxAtomic parses an --obx-amount value (whole/decimal OBX) to atomic units,
// defaulting to 3 OBX when empty. Demo-side knob (#4); real amount/rate binding to a
// matched offer is WS3.
func obxAtomic(s string) uint64 {
	if s == "" {
		return 3 * config.AtomicPerCoin
	}
	f, ok := new(big.Float).SetString(s)
	if !ok {
		log.Fatalf("--obx-amount must be a number of OBX")
	}
	v, _ := new(big.Float).Mul(f, new(big.Float).SetUint64(config.AtomicPerCoin)).Uint64()
	if v == 0 {
		log.Fatalf("--obx-amount must be > 0")
	}
	return v
}

func flagSet(args []string) liveFlags {
	var f liveFlags
	for i := 0; i < len(args); i++ {
		get := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch args[i] {
		case "--nano-rpc":
			f.rpc = get()
		case "--xno-dest":
			f.xnoDest = get()
		case "--xno-amount-raw":
			f.xnoAmountRaw = get()
		case "--nano-work-url":
			f.work = get()
		case "--obx-seed":
			f.obxSeed = get()
		case "--obx-datadir":
			f.obxDatadir = get()
		case "--obx-amount":
			f.obxAmount = get()
		}
	}
	return f
}
