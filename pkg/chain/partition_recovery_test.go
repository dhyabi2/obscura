package chain_test

import (
	"testing"

	"obscura/pkg/chain"
	"obscura/pkg/config"
	"obscura/pkg/wallet"
)

// setU temporarily overrides a uint64 consensus var and returns a restore func.
func setU(p *uint64, v uint64) func() { old := *p; *p = v; return func() { *p = old } }

// mineNTo mines n coinbase-only blocks onto chain c using wallet w.
func mineNTo(t *testing.T, c *chain.Chain, w *wallet.Wallet, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		cb, err := w.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(0, nil), nil)
		if err != nil {
			t.Fatalf("coinbase: %v", err)
		}
		mineBlock(t, c, cb, nil) // defined in integration_test.go (same package)
	}
}

func newChainT(t *testing.T) *chain.Chain {
	t.Helper()
	c, err := chain.New(t.TempDir())
	if err != nil {
		t.Fatalf("new chain: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// TestPartitionRecoveryAdoptsHeavier proves recovery HEALS a decisively heavier partition:
// the active node holds a 4-block chain; a competing 9-block fork (deep — diverges at
// genesis, depth 4 > MaxReorgDepth 2) is fed in block-by-block and must be adopted, with the
// active chain ending on the rival branch. It also proves the rejected-but-recoverable
// intermediate blocks are RETAINED (not orphaned): every feed returns without error, so the
// heavier chain can accumulate until it clears the margin.
func TestPartitionRecoveryAdoptsHeavier(t *testing.T) {
	defer setU(&config.CoinbaseMaturity, 1)()
	defer setU(&chain.MaxReorgDepth, 2)()         // shallow finality so depth 4 is "deep"
	defer setU(&chain.PartitionRecoveryMargin, 2)()
	defer setU(&config.PoWSeedLag, 16)()          // recovery cap 16 > depth 4 — seed bound not hit

	alice := wallet.FromSeed([]byte("alice-pr-seed-000000000000000000"))
	bob := wallet.FromSeed([]byte("bob-pr-seed-0000000000000000000000"))

	active := newChainT(t)
	rival := newChainT(t)

	mineNTo(t, active, alice, 4) // active tip height 4
	mineNTo(t, rival, bob, 9)    // decisively heavier fork (≈5 blocks of extra work ≫ margin 2)
	if active.Height() != 4 || rival.Height() != 9 {
		t.Fatalf("setup heights: active=%d rival=%d", active.Height(), rival.Height())
	}

	for h := uint64(1); h <= 9; h++ {
		b, _ := rival.BlockByHeight(h)
		if err := active.AddBlock(b); err != nil { // never orphaned / errored — stored then adopted
			t.Fatalf("feeding rival block %d should succeed (side branch then recovery), got: %v", h, err)
		}
	}
	if active.Height() != 9 {
		t.Fatalf("recovery should have adopted the decisively heavier rival chain (height 9); got %d", active.Height())
	}
	rh, _ := rival.HeaderByHeight(9)
	ah, _ := active.HeaderByHeight(9)
	if rh.ID() != ah.ID() {
		t.Fatal("active tip != rival tip after recovery — state not switched to the rival branch")
	}
}

// TestPartitionRecoveryMarginGates proves finality HOLDS against a deep fork that is heavier
// but NOT by the margin: active holds 4 blocks, the rival forks at genesis and reaches only 5
// (a 1-block lead) while the margin requires 5. The deep reorg must be refused — active stays
// on its own chain — yet the rival blocks are still retained (no error), not discarded.
func TestPartitionRecoveryMarginGates(t *testing.T) {
	defer setU(&config.CoinbaseMaturity, 1)()
	defer setU(&chain.MaxReorgDepth, 2)()
	defer setU(&chain.PartitionRecoveryMargin, 5)() // require a 5-block work lead
	defer setU(&config.PoWSeedLag, 16)()

	alice := wallet.FromSeed([]byte("alice-mg-seed-000000000000000000"))
	bob := wallet.FromSeed([]byte("bob-mg-seed-0000000000000000000000"))

	active := newChainT(t)
	rival := newChainT(t)

	mineNTo(t, active, alice, 4)
	mineNTo(t, rival, bob, 5) // leads by only ~1 block of work, far below the 5-block margin

	aliceTip, _ := active.HeaderByHeight(4)
	for h := uint64(1); h <= 5; h++ {
		b, _ := rival.BlockByHeight(h)
		if err := active.AddBlock(b); err != nil {
			t.Fatalf("rival block %d should be stored as a side branch (not errored): %v", h, err)
		}
	}
	if active.Height() != 4 {
		t.Fatalf("finality must hold: rival lead < margin, active should stay at 4; got %d", active.Height())
	}
	nowTip, _ := active.HeaderByHeight(4)
	if nowTip.ID() != aliceTip.ID() {
		t.Fatal("active tip changed despite the deep reorg being below the recovery margin")
	}
}

// TestPartitionRecoverySeedFinality proves the seed-finality hard bound: a reorg deeper than
// config.PoWSeedLag is rejected even when the rival is heavier by the margin, because adopting
// it would rewrite a PoW epoch-seed block. The active chain must never switch.
func TestPartitionRecoverySeedFinality(t *testing.T) {
	defer setU(&config.CoinbaseMaturity, 1)()
	defer setU(&chain.MaxReorgDepth, 2)()
	defer setU(&chain.PartitionRecoveryMargin, 2)()
	defer setU(&config.PoWSeedLag, 4)() // recovery cap 4; active depth will be 6 > 4 → seed bound

	alice := wallet.FromSeed([]byte("alice-sf-seed-000000000000000000"))
	bob := wallet.FromSeed([]byte("bob-sf-seed-0000000000000000000000"))

	active := newChainT(t)
	rival := newChainT(t)

	mineNTo(t, active, alice, 6) // active tip 6 → any genesis-fork reorg has depth 6
	mineNTo(t, rival, bob, 12)   // far heavier rival, but forked at genesis (depth 6 > PoWSeedLag 4)

	var lastErr error
	for h := uint64(1); h <= 12; h++ {
		b, ok := rival.BlockByHeight(h)
		if !ok {
			t.Fatalf("rival missing block %d", h)
		}
		lastErr = active.AddBlock(b)
	}
	if active.Height() != 6 {
		t.Fatalf("seed finality must prevent adoption beyond PoWSeedLag; active switched to %d", active.Height())
	}
	if lastErr == nil {
		t.Fatal("expected seed-finality rejection error on the too-deep rival tip")
	}
}
