// Adaptor-nonce uniqueness (audit #14): CONSENSUS must reject any swap output
// whose pre-signature nonce ClaimR was already funded on-chain. Reusing R across
// two claims under the same aggregate key leaks the secret share, so the chain
// keys a persistent uniqueness set on ClaimR (the safe superset of the precise
// (ClaimKey,ClaimR) leak condition) and rejects reuse outright as defence-in-depth.
//
// These tests keep block counts LOW (class-group mining is slow): run with
//   OBX_ALLOW_PROTOTYPE_POW=1 go test ./tests/critical/swapchain/ -run NonceUniq
package swapchain

import (
	"context"
	"testing"

	"filippo.io/edwards25519"

	"obscura/pkg/chain"
	"obscura/pkg/commit"
	"obscura/pkg/config"
	"obscura/pkg/miner"
	"obscura/pkg/swap"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
	"obscura/tests/critical/harness"
)

// fundSwapWithR builds a valid swap-fund tx for `bob` reusing the supplied aggregate
// pre-signature nonce R. All other claim-binding fields (K=A+B with PoPs, non-identity
// T) are freshly derived from the seed so the only thing under test is ClaimR reuse.
func fundSwapWithR(t *testing.T, c *chain.Chain, bob *wallet.Wallet, swapKey []byte, R *edwards25519.Point) *tx.Transaction {
	t.Helper()
	a := commit.RandomScalar()
	b := commit.RandomScalar()
	A := new(edwards25519.Point).ScalarBaseMult(a)
	B := new(edwards25519.Point).ScalarBaseMult(b)
	K := swap.AggregateKey(A, B)
	T := new(edwards25519.Point).ScalarBaseMult(commit.RandomScalar())
	amount := uint64(3 * config.AtomicPerCoin)
	fee := uint64(1_000_000_000)
	fund, err := bob.FundSwap(c, swapKey, amount, K.Bytes(), B.Bytes(),
		A.Bytes(), B.Bytes(), swap.ProvePossession(a), swap.ProvePossession(b), R.Bytes(), T.Bytes(), 1000, fee)
	if err != nil {
		t.Fatalf("fund swap: %v", err)
	}
	return fund
}

func randR() *edwards25519.Point {
	return new(edwards25519.Point).Add(
		new(edwards25519.Point).ScalarBaseMult(commit.RandomScalar()),
		new(edwards25519.Point).ScalarBaseMult(commit.RandomScalar()),
	)
}

// TestNonceUniqFreshValidatesReuseRejected: a swap with a fresh ClaimR validates and
// is funded; a SECOND swap (distinct SwapKey) reusing that ClaimR is rejected by
// consensus once the first is on-chain.
func TestNonceUniqFreshValidatesReuseRejected(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	bob := harness.NewWallet("nonce-uniq-bob")
	harness.MineN(t, c, bob, 4)
	harness.ScanAll(c, bob)

	R := randR()

	// fresh ClaimR -> valid
	f1 := fundSwapWithR(t, c, bob, key32("nonce-uniq-1"), R)
	if err := c.ValidateStandaloneTx(f1); err != nil {
		t.Fatalf("fresh-nonce swap rejected: %v", err)
	}
	harness.MineBlock(t, c, bob, []*tx.Transaction{f1})
	if _, ok := c.Swap(key32("nonce-uniq-1")); !ok {
		t.Fatal("first swap not registered on-chain")
	}

	harness.ScanAll(c, bob)

	// reuse the SAME ClaimR on a DISTINCT swap key -> rejected by consensus
	f2 := fundSwapWithR(t, c, bob, key32("nonce-uniq-2"), R)
	if err := c.ValidateStandaloneTx(f2); err == nil {
		t.Fatal("consensus accepted a swap reusing a funded ClaimR — adaptor-nonce reuse not rejected")
	}

	// and a swap with a DIFFERENT fresh ClaimR still validates (no false positive)
	f3 := fundSwapWithR(t, c, bob, key32("nonce-uniq-3"), randR())
	if err := c.ValidateStandaloneTx(f3); err != nil {
		t.Fatalf("distinct-nonce swap wrongly rejected: %v", err)
	}
}

// TestNonceUniqReuseInSameBlockRejected: two swap outputs sharing a ClaimR cannot
// both land in ONE block (block-wide "swapnonce:" dedup).
func TestNonceUniqReuseInSameBlockRejected(t *testing.T) {
	oldD := config.GenesisDifficulty
	config.GenesisDifficulty = 16
	defer func() { config.GenesisDifficulty = oldD }()
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	bob := harness.NewWallet("nonce-uniq-block-bob")
	harness.MineN(t, c, bob, 6)
	harness.ScanAll(c, bob)

	R := randR()
	f1 := fundSwapWithR(t, c, bob, key32("blk-nonce-1"), R)
	if err := c.ValidateStandaloneTx(f1); err != nil {
		t.Fatalf("f1 rejected standalone: %v", err)
	}
	f2 := fundSwapWithR(t, c, bob, key32("blk-nonce-2"), R)
	if err := c.ValidateStandaloneTx(f2); err != nil {
		t.Fatalf("f2 rejected standalone (should be fine in isolation): %v", err)
	}
	// A block containing BOTH must be rejected on validation (the second swap output
	// reuses the nonce within the block). BlockTemplate only assembles, so mine the
	// candidate and feed it to AddBlock, which validates.
	cb, err := bob.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(chain.CollectedFees([]*tx.Transaction{f1, f2}), nil), nil)
	if err != nil {
		t.Fatalf("coinbase: %v", err)
	}
	tmpl, err := c.BlockTemplate([]*tx.Transaction{cb, f1, f2})
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	if !miner.Mine(context.Background(), tmpl, 0) {
		t.Fatal("mine")
	}
	if err := c.AddBlock(tmpl); err == nil {
		t.Fatal("consensus accepted a block with two swap outputs sharing a ClaimR — in-block nonce dedup missing")
	}
}

// TestNonceUniqReorgSafe is the reorg-safety test. A swap is funded on the active
// chain (its ClaimR enters swapNonces). A genesis-divergent, heavier rival fork is
// adopted (the funding is rolled back), and:
//   (a) the SAME legitimate swap, re-included on the new branch, must still validate
//       (the set was correctly restored to the fork point, so its R was dropped);
//   (b) a DIFFERENT swap reusing that ClaimR after the re-include is still rejected.
func TestNonceUniqReorgSafe(t *testing.T) {
	// fast mining + immediate spendability + shallow finality so a small rival fork
	// triggers a real reorg (pattern from deep_reorg_persist_internal_test.go).
	oldD, oldM := config.GenesisDifficulty, config.CoinbaseMaturity
	config.GenesisDifficulty, config.CoinbaseMaturity = 16, 1
	defer func() { config.GenesisDifficulty, config.CoinbaseMaturity = oldD, oldM }()

	mine := func(c *chain.Chain, w *wallet.Wallet, txs []*tx.Transaction) {
		cb, err := w.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(chain.CollectedFees(txs), nil), nil)
		if err != nil {
			t.Fatalf("coinbase: %v", err)
		}
		all := append([]*tx.Transaction{cb}, txs...)
		tmpl, err := c.BlockTemplate(all)
		if err != nil {
			t.Fatalf("template: %v", err)
		}
		if !miner.Mine(context.Background(), tmpl, 0) {
			t.Fatal("mine")
		}
		if err := c.AddBlock(tmpl); err != nil {
			t.Fatalf("add h=%d: %v", tmpl.Header.Height, err)
		}
	}
	scan := func(c *chain.Chain, w *wallet.Wallet) {
		for h := uint64(0); h <= c.Height(); h++ {
			if b, ok := c.BlockByHeight(h); ok {
				w.ScanBlock(b)
			}
		}
	}

	active := harness.NewChain(t)
	rival := harness.NewChain(t)
	bob := wallet.FromSeed([]byte("reorg-bob-000000000000000000000000000000"))
	rbob := wallet.FromSeed([]byte("reorg-rbob-00000000000000000000000000000"))

	// active: short chain; fund a swap with nonce R on it.
	for i := 0; i < 4; i++ {
		mine(active, bob, nil)
	}
	scan(active, bob)

	R := randR()
	fund := fundSwapWithR(t, active, bob, key32("reorg-swap"), R)
	mine(active, bob, []*tx.Transaction{fund})
	if _, ok := active.Swap(key32("reorg-swap")); !ok {
		t.Fatal("swap not funded on active chain")
	}

	// rival: a genesis-divergent, decisively heavier fork (no swaps on it) — adopting
	// it rolls back the funding so R leaves the active set via snapshot-restore+replay.
	const depth = 8
	for i := 0; i < depth; i++ {
		mine(rival, rbob, nil)
	}
	for h := uint64(1); h <= uint64(depth); h++ {
		b, _ := rival.BlockByHeight(h)
		if err := active.AddBlock(b); err != nil {
			t.Fatalf("feeding rival block %d: %v", h, err)
		}
	}
	if active.Height() != uint64(depth) {
		t.Fatalf("active did not adopt heavier rival fork: height %d, want %d", active.Height(), depth)
	}
	// The rolled-back swap must be GONE.
	if _, ok := active.Swap(key32("reorg-swap")); ok {
		t.Fatal("rolled-back swap still live after reorg")
	}

	// (a) re-include the SAME legitimate swap on the NEW branch: must validate again,
	// proving its ClaimR was correctly removed from swapNonces by the restore.
	scan(active, rbob)
	refund := fundSwapWithR(t, active, rbob, key32("reorg-swap-again"), R)
	if err := active.ValidateStandaloneTx(refund); err != nil {
		t.Fatalf("re-included swap with the same (rolled-back) ClaimR was rejected — set not restored on reorg: %v", err)
	}
	mine(active, rbob, []*tx.Transaction{refund})
	if _, ok := active.Swap(key32("reorg-swap-again")); !ok {
		t.Fatal("re-included swap not funded on the new branch")
	}

	// (b) now that R is funded again on the new branch, a DIFFERENT swap reusing it is
	// still rejected (the set is live and effective post-reorg).
	scan(active, rbob)
	dup := fundSwapWithR(t, active, rbob, key32("reorg-swap-dup"), R)
	if err := active.ValidateStandaloneTx(dup); err == nil {
		t.Fatal("consensus accepted ClaimR reuse on the post-reorg branch — set not effective after restore")
	}
}
