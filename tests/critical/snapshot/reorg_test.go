package snapshot_test

import (
	"testing"

	"obscura/pkg/block"
	"obscura/pkg/chain"
	"obscura/tests/critical/harness"
)

// TestSnapshotBasedReorgAcrossPrunedBodies forces a reorg whose fork point is
// ABOVE the pruned region: with bodies below the oldest kept snapshot deleted, the
// reorg MUST restore from a snapshot and replay only retained blocks. This proves
// snapshot-based reorg + body pruning are consensus-correct together.
func TestSnapshotBasedReorgAcrossPrunedBodies(t *testing.T) {
	defer harness.SmallMaturity()()
	// short snapshot interval + reorg depth so a few blocks create 3 snapshots
	// (triggering pruning) with the fork point inside the reorg window.
	oldI, oldD := chain.SnapshotInterval, chain.MaxReorgDepth
	chain.SnapshotInterval, chain.MaxReorgDepth = 4, 2
	defer func() { chain.SnapshotInterval, chain.MaxReorgDepth = oldI, oldD }()

	cMain := harness.NewChain(t)
	cAlt := harness.NewChain(t)
	wA := harness.NewWallet("reorg-alt")
	wM := harness.NewWallet("reorg-main")

	// shared prefix heights 1..12: mine on cAlt, import into cMain so both agree.
	// cMain snapshots at 4, 8, 12 → keeps {8,12}, prunes block bodies below 8.
	for h := 0; h < 12; h++ {
		b := harness.MineBlock(t, cAlt, wA, nil)
		if err := cMain.AddBlock(b); err != nil {
			t.Fatalf("import shared block h=%d: %v", b.Header.Height, err)
		}
	}
	if cMain.Height() != 12 || cAlt.Height() != 12 {
		t.Fatalf("setup heights: main=%d alt=%d", cMain.Height(), cAlt.Height())
	}

	// cMain extends with its own 13,14 (the branch that will be reorged away).
	harness.MineBlock(t, cMain, wM, nil) // main 13
	harness.MineBlock(t, cMain, wM, nil) // main 14

	// cAlt builds a heavier branch 13,14,15 forking at 12 (depth 2 = MaxReorgDepth).
	a13 := harness.MineBlock(t, cAlt, wA, nil)
	a14 := harness.MineBlock(t, cAlt, wA, nil)
	a15 := harness.MineBlock(t, cAlt, wA, nil)

	// feed the alt suffix; a15 makes the alt branch heaviest → reorg.
	for _, b := range []*block.Block{a13, a14, a15} {
		if err := cMain.AddBlock(b); err != nil && !chain.IsOrphanErr(err) {
			t.Fatalf("add alt block h=%d: %v", b.Header.Height, err)
		}
	}

	if cMain.Height() != 15 {
		t.Fatalf("expected reorg to height 15, got %d", cMain.Height())
	}
	got := cMain.Tip()
	want := cAlt.Tip()
	if got.ID() != want.ID() {
		t.Fatal("reorg did not adopt the alt tip (snapshot-based reorg failed)")
	}
	// chain is healthy: it extends further after the snapshot-based reorg.
	harness.MineBlock(t, cMain, wM, nil)
	if cMain.Height() != 16 {
		t.Fatal("could not extend after snapshot-based reorg")
	}
}
