// Package pqchain_test is the end-to-end integration test for the post-quantum
// (Version-2) transaction path in the real pkg/chain engine: a PQ output that
// exists in the anonymity set, detected with an ML-KEM view key, spent with a
// hybrid (Schnorr ⊕ WOTS+) signature verified by the chain, membership-proven
// against a committed anchor, value-conserved over public amounts, fee-paid, and
// protected from double-spend — all through genuine mined blocks. See
// docs/POST_QUANTUM_ROADMAP.md.
//
// ORIGIN OF PQ OUTPUTS. Coinbase PQ minting was REMOVED from consensus: it had no
// aggregate cap and let a miner mint unlimited PQ value (audit CRITICAL: uncapped
// coinbase PQ minting). A capped PQ emission / transparent-to-PQ wrap is still a
// documented TODO. Until it lands, a PQ output cannot be created inside a mined
// block at all (validate.go forbids coinbase PQ legs, and a PQ tx must SPEND an
// existing PQ output). These tests therefore originate PQ outputs via the
// explicit, capped, consensus-fixed bootstrap (chain.SeedPQOutput) — the analogue
// of pqtx.Ledger.AddOutput's "genesis funding" role — and then exercise the real
// PQ SPEND path through genuine validation and mined blocks. The coinbase-minting
// route is asserted to be REJECTED (TestCoinbasePQMintRejected).
package pqchain_test

import (
	"context"
	"testing"

	"obscura/pkg/chain"
	"obscura/pkg/miner"
	"obscura/pkg/pqsign"
	"obscura/pkg/pqwallet"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
	"obscura/tests/critical/harness"
)

const (
	unit = uint64(1_000_000_000_000) // 1 OBX in atomic units
	fee  = uint64(1_000_000_000)     // 0.001 OBX — comfortably above min fee
)

// seedPQ originates a PQ output in the chain's anonymity set via the capped
// bootstrap (coinbase PQ minting is disabled — see the package doc). This stands
// in for the future capped PQ emission; the SPEND of the seeded output below is
// the genuine consensus PQ path under test.
func seedPQ(t *testing.T, c *chain.Chain, out tx.PQOutput) {
	t.Helper()
	if err := c.SeedPQOutput(out); err != nil {
		t.Fatalf("seed pq output: %v", err)
	}
}

// minePQ mines a block carrying PQ spend transactions. Unlike harness.MineBlock
// it credits ZERO fees to the coinbase: PQ-tx fees are BURNED in the PQ value
// space (consensus excludes them from the classical coinbase — crediting them
// would create the value twice). The classical coinbase mints the base reward
// only.
func minePQ(t *testing.T, c *chain.Chain, w *wallet.Wallet, pqTxs []*tx.Transaction) {
	t.Helper()
	minted := c.ExpectedCoinbaseMinted(0, nil) // PQ fees burned → no fee credit
	cb, err := w.BuildCoinbase(c.Height()+1, minted, nil)
	if err != nil {
		t.Fatalf("coinbase: %v", err)
	}
	all := append([]*tx.Transaction{cb}, pqTxs...)
	tmpl, err := c.BlockTemplate(all)
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	if !miner.MineSeed(context.Background(), tmpl, c.PoWSeed(tmpl.Header.Height), 0) {
		t.Fatal("mine failed")
	}
	if err := c.AddBlock(tmpl); err != nil {
		t.Fatalf("addblock h=%d: %v", tmpl.Header.Height, err)
	}
}

func fundAlice(t *testing.T, c *chain.Chain, amount uint64) (*pqwallet.Account, *pqwallet.Detected) {
	t.Helper()
	alice, err := pqwallet.NewAccount()
	if err != nil {
		t.Fatal(err)
	}
	recv, err := alice.NewReceiveKey()
	if err != nil {
		t.Fatal(err)
	}
	out, err := pqwallet.BuildOutput(alice.StealthPub(), recv, amount)
	if err != nil {
		t.Fatal(err)
	}
	seedPQ(t, c, out)
	det, ok := alice.Scan(&out)
	if !ok {
		t.Fatal("Alice failed to detect her seeded PQ output")
	}
	if det.Amount != amount {
		t.Fatalf("amount %d, want %d", det.Amount, amount)
	}
	return alice, det
}

func TestPQChainEndToEnd(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	w := harness.NewWallet("pq-miner")

	alice, det := fundAlice(t, c, unit)

	// Alice spends to Bob (minus fee) + change to herself.
	bob, _ := pqwallet.NewAccount()
	bobRecv, _ := bob.NewReceiveKey()
	change, _ := alice.NewReceiveKey()
	membership, err := c.PQProve(det.Out.OneTimeKey)
	if err != nil {
		t.Fatalf("membership: %v", err)
	}
	spend, err := pqwallet.BuildSpendTx(det, []pqwallet.Payment{
		{StealthPub: bob.StealthPub(), Hybrid: bobRecv, Amount: 6 * unit / 10},
		{StealthPub: alice.StealthPub(), Hybrid: change, Amount: unit - 6*unit/10 - fee},
	}, fee, c.PQRoot(), membership)
	if err != nil {
		t.Fatal(err)
	}

	if err := c.ValidateStandaloneTx(spend); err != nil {
		t.Fatalf("valid PQ spend rejected by chain: %v", err)
	}
	minePQ(t, c, w, []*tx.Transaction{spend})

	// Bob detects his payment.
	sb, _ := c.BlockByHeight(c.Height())
	bobOk := false
	for _, tr := range sb.Txs {
		for i := range tr.PQOutputs {
			if d, ok := bob.Scan(&tr.PQOutputs[i]); ok && d.Amount == 6*unit/10 {
				bobOk = true
			}
		}
	}
	if !bobOk {
		t.Fatal("Bob did not detect his PQ payment")
	}

	// Double-spend rejected.
	if err := c.ValidateStandaloneTx(spend); err == nil {
		t.Fatal("double-spend of PQ output was accepted")
	}
}

// TestCoinbasePQMintRejected pins the security fix: a coinbase may carry ONLY
// transparent outputs. A miner cannot mint PQ value (nor any other alternative
// value leg) in the coinbase — that route had no aggregate cap and was an
// inflation hole (audit CRITICAL: uncapped coinbase PQ minting). The block must
// be REJECTED by consensus.
func TestCoinbasePQMintRejected(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	w := harness.NewWallet("pq-cb-mint")

	alice, _ := pqwallet.NewAccount()
	recv, _ := alice.NewReceiveKey()
	out, err := pqwallet.BuildOutput(alice.StealthPub(), recv, unit)
	if err != nil {
		t.Fatal(err)
	}

	// Hand-build a block whose coinbase carries a PQ output (the old, removed
	// minting route) and try to add it.
	minted := c.ExpectedCoinbaseMinted(0, nil)
	cb, err := w.BuildCoinbase(c.Height()+1, minted, nil)
	if err != nil {
		t.Fatalf("coinbase: %v", err)
	}
	cb.PQOutputs = []tx.PQOutput{out}
	tmpl, err := c.BlockTemplate([]*tx.Transaction{cb})
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	if !miner.MineSeed(context.Background(), tmpl, c.PoWSeed(tmpl.Header.Height), 0) {
		t.Fatal("mine failed")
	}
	if err := c.AddBlock(tmpl); err == nil {
		t.Fatal("coinbase PQ minting was accepted — the inflation hole is back")
	}
	// And no PQ output leaked into the set.
	if _, err := c.PQProve(out.OneTimeKey); err == nil {
		t.Fatal("rejected coinbase still seeded a PQ output into the set")
	}
}

// TestPQChainReplay proves PQ state (accumulator, anchors, UTXO, nullifiers) is
// reconstructed deterministically on restart: a PQ output spent in a mined block
// stays spent after the chain is closed and reopened from disk.
func TestPQChainReplay(t *testing.T) {
	defer harness.SmallMaturity()()
	dir := t.TempDir()
	w := harness.NewWallet("pq-miner3")

	alice, err := pqwallet.NewAccount()
	if err != nil {
		t.Fatal(err)
	}
	aliceRecv, _ := alice.NewReceiveKey()
	pqOut, err := pqwallet.BuildOutput(alice.StealthPub(), aliceRecv, unit)
	if err != nil {
		t.Fatal(err)
	}

	var spend *tx.Transaction
	var height uint64
	{
		c, err := chain.New(dir)
		if err != nil {
			t.Fatal(err)
		}
		seedPQ(t, c, pqOut)
		det, _ := alice.Scan(&pqOut)
		bob, _ := pqwallet.NewAccount()
		bobRecv, _ := bob.NewReceiveKey()
		membership, _ := c.PQProve(pqOut.OneTimeKey)
		spend, err = pqwallet.BuildSpendTx(det, []pqwallet.Payment{
			{StealthPub: bob.StealthPub(), Hybrid: bobRecv, Amount: unit - fee},
		}, fee, c.PQRoot(), membership)
		if err != nil {
			t.Fatal(err)
		}
		minePQ(t, c, w, []*tx.Transaction{spend})
		height = c.Height()
		// Snapshot the live PQ state at the tip so the restart restores the seeded
		// output + its spend nullifier (the seed never lived in a block, so a
		// from-genesis replay would not reconstruct it — exactly what a real capped
		// PQ emission, once it lands, would persist for).
		if err := c.SaveSnapshot(); err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		_ = c.Close()
	}

	c2, err := chain.New(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer c2.Close()
	if c2.Height() != height {
		t.Fatalf("height after replay = %d, want %d", c2.Height(), height)
	}
	if err := c2.ValidateStandaloneTx(spend); err == nil {
		t.Fatal("replayed chain accepted an already-spent PQ output (state lost)")
	}
}

// TestPQReorgRebuildsState exercises the CRITICAL reorg fix on the PQ value
// space: when the chain reorganizes to a heavier branch, the PQ
// accumulator/UTXO/anchor/nullifier sets must be RESET and rebuilt to the adopted
// branch — the abandoned branch's PQ state must NOT leak through. main carries a
// PQ output (A) in its in-memory anonymity set; a heavier alt branch (which never
// saw A) is fed in and adopted. After the reorg A must be gone and the PQ set
// must be rebuilt to the adopted branch's state (here: empty), proving the reset
// path runs and no stale PQ state survives. Without the snapshot/reset fix the PQ
// root would diverge (or A would linger).
func TestPQReorgRebuildsState(t *testing.T) {
	defer harness.SmallMaturity()()
	cMain := harness.NewChain(t)
	cAlt := harness.NewChain(t)
	wM := harness.NewWallet("pq-reorg-main")
	wA := harness.NewWallet("pq-reorg-alt")

	// The PQ root of a fresh chain (no PQ outputs) — the state the adopted alt
	// branch, which never carried any PQ output, must rebuild to.
	emptyRoot := append([]byte(nil), harness.NewChain(t).PQRoot()...)

	// main: seed PQ output A into its anonymity set, then a block to abandon.
	am, _ := pqwallet.NewAccount()
	aRecv, _ := am.NewReceiveKey()
	outA, err := pqwallet.BuildOutput(am.StealthPub(), aRecv, unit)
	if err != nil {
		t.Fatal(err)
	}
	seedPQ(t, cMain, outA)
	harness.MineBlock(t, cMain, wM, nil) // main h1 (the branch that will be abandoned)
	if _, err := cMain.PQProve(outA.OneTimeKey); err != nil {
		t.Fatalf("seeded PQ output A not present pre-reorg: %v", err)
	}
	if string(cMain.PQRoot()) == string(emptyRoot) {
		t.Fatal("seeding A did not change the PQ root")
	}

	// alt: a heavier (2-block) branch that never carried any PQ output.
	harness.MineBlock(t, cAlt, wA, nil) // alt h1
	harness.MineBlock(t, cAlt, wA, nil) // alt h2 (heavier)

	// Feed the alt branch into main → triggers a reorg that resets PQ state.
	for h := uint64(1); h <= cAlt.Height(); h++ {
		b, _ := cAlt.BlockByHeight(h)
		if err := cMain.AddBlock(b); err != nil && !chain.IsOrphanErr(err) {
			t.Fatalf("add alt h=%d: %v", h, err)
		}
	}
	if cMain.Height() != cAlt.Height() {
		t.Fatalf("reorg failed: main height %d, alt %d", cMain.Height(), cAlt.Height())
	}

	// PQ state rebuilt to the adopted (PQ-empty) branch; the abandoned A is gone.
	if got := cMain.PQRoot(); string(got) != string(emptyRoot) {
		t.Fatal("PQ root not reset to adopted branch after reorg (stale PQ state)")
	}
	if _, err := cMain.PQProve(outA.OneTimeKey); err == nil {
		t.Fatal("abandoned-branch PQ output A still present after reorg (PQ state not reset)")
	}
}

func TestPQChainRejections(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)

	_, det := fundAlice(t, c, unit)
	bob, _ := pqwallet.NewAccount()
	bobRecv, _ := bob.NewReceiveKey()
	membership, _ := c.PQProve(det.Out.OneTimeKey)
	anchor := c.PQRoot()
	good, err := pqwallet.BuildSpendTx(det, []pqwallet.Payment{
		{StealthPub: bob.StealthPub(), Hybrid: bobRecv, Amount: unit - fee},
	}, fee, anchor, membership)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ValidateStandaloneTx(good); err != nil {
		t.Fatalf("honest spend rejected: %v", err)
	}

	// 1) tampered hybrid signature
	bad1 := *good
	bad1.PQInputs = []tx.PQInput{good.PQInputs[0]}
	bad1.PQInputs[0].HybridSig = append([]byte(nil), good.PQInputs[0].HybridSig...)
	bad1.PQInputs[0].HybridSig[len(bad1.PQInputs[0].HybridSig)-1] ^= 0xff
	if err := c.ValidateStandaloneTx(&bad1); err == nil {
		t.Fatal("accepted tampered hybrid signature")
	}

	// 2) unknown anchor
	bad2 := *good
	bad2.PQInputs = []tx.PQInput{good.PQInputs[0]}
	bad2.PQInputs[0].Anchor = make([]byte, 32)
	bad2.PQInputs[0].Anchor[0] = 0x99
	if err := c.ValidateStandaloneTx(&bad2); err == nil {
		t.Fatal("accepted unknown PQ anchor")
	}

	// 3) value inflation: outputs sum to MORE than the input but correctly signed
	infOut, _ := pqwallet.BuildOutput(bob.StealthPub(), bobRecv, 5*unit) // 5x the input
	inf := &tx.Transaction{
		Version: 2,
		Fee:     fee,
		PQInputs: []tx.PQInput{{
			OutputRef:  append([]byte(nil), det.Out.OneTimeKey...),
			P:          append([]byte(nil), det.Pub.P...),
			WotsRoot:   append([]byte(nil), det.Pub.R...),
			Nullifier:  pqwallet.NullifierOf(det.Out.OneTimeKey),
			Anchor:     anchor,
			Membership: membership,
		}},
		PQOutputs: []tx.PQOutput{infOut},
	}
	ctx := inf.CoreHash()
	s, _ := pqsign.HybridSign(det.Priv, det.Pub, ctx[:])
	inf.PQInputs[0].HybridSig = marshalSig(s)
	if err := c.ValidateStandaloneTx(inf); err == nil {
		t.Fatal("accepted PQ value inflation")
	}

	// 4) nonexistent output
	bad4 := *good
	bad4.PQInputs = []tx.PQInput{good.PQInputs[0]}
	bogus := make([]byte, 32)
	bogus[0] = 0xaa
	bad4.PQInputs[0].OutputRef = bogus
	if err := c.ValidateStandaloneTx(&bad4); err == nil {
		t.Fatal("accepted spend of a nonexistent PQ output")
	}
}

func marshalSig(s *pqsign.HybridSig) []byte {
	out := make([]byte, 0, 8+len(s.Schnorr)+len(s.Wots))
	put := func(b []byte) {
		var l [4]byte
		l[0] = byte(len(b) >> 24)
		l[1] = byte(len(b) >> 16)
		l[2] = byte(len(b) >> 8)
		l[3] = byte(len(b))
		out = append(out, l[:]...)
		out = append(out, b...)
	}
	put(s.Schnorr)
	put(s.Wots)
	return out
}
