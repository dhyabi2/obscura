// Package forkchoice tests the invented fork-choice block (Block 1):
// most-cumulative-work selection with lowest-hash tie-break, bounded-depth
// reorg (finality), and orphan handling. See docs/INVENTION_FORKCHOICE.md.
package forkchoice

import (
	"bytes"
	"testing"

	"obscura/pkg/block"
	"obscura/pkg/chain"
	"obscura/tests/critical/harness"
)

// TestReorgHeavierBranchWins: a node on a 2-block chain adopts a competing
// 3-block (heavier) branch fed in block-by-block — i.e. it reorganizes.
func TestReorgHeavierBranchWins(t *testing.T) {
	defer harness.SmallMaturity()()
	cMain := harness.NewChain(t)
	cAlt := harness.NewChain(t)
	wM := harness.NewWallet("miner-main")
	wA := harness.NewWallet("miner-alt")

	harness.MineBlock(t, cMain, wM, nil) // main h1
	harness.MineBlock(t, cMain, wM, nil) // main h2

	a1 := harness.MineBlock(t, cAlt, wA, nil) // alt h1
	a2 := harness.MineBlock(t, cAlt, wA, nil) // alt h2
	a3 := harness.MineBlock(t, cAlt, wA, nil) // alt h3 (heavier)

	if cMain.Height() != 2 {
		t.Fatalf("main should start at height 2, got %d", cMain.Height())
	}
	for _, b := range []*block.Block{a1, a2, a3} {
		if err := cMain.AddBlock(b); err != nil && !chain.IsOrphanErr(err) {
			t.Fatalf("add alt block h=%d: %v", b.Header.Height, err)
		}
	}
	if cMain.Height() != 3 {
		t.Fatalf("expected reorg to height 3, got %d", cMain.Height())
	}
	got, _ := cMain.HeaderByHeight(3)
	want, _ := cAlt.HeaderByHeight(3)
	if got.ID() != want.ID() {
		t.Fatalf("did not adopt the alt tip after reorg")
	}
}

// TestEqualWorkLowestHashTieBreak: two competing height-1 blocks with equal
// cumulative work — the lexicographically-lowest block hash wins (eclipse- and
// selfish-mining-neutral), regardless of arrival order.
func TestEqualWorkLowestHashTieBreak(t *testing.T) {
	defer harness.SmallMaturity()()
	cM := harness.NewChain(t)
	cA := harness.NewChain(t)
	b1 := harness.MineBlock(t, cM, harness.NewWallet("tie-M"), nil)
	b2 := harness.MineBlock(t, cA, harness.NewWallet("tie-A"), nil)

	id1 := b1.Header.ID()
	id2 := b2.Header.ID()
	if id1 == id2 {
		t.Skip("blocks coincidentally identical; rerun")
	}
	expected := id1
	if bytes.Compare(id2[:], id1[:]) < 0 {
		expected = id2
	}

	c := harness.NewChain(t)
	_ = c.AddBlock(b1)
	_ = c.AddBlock(b2)
	tip, _ := c.HeaderByHeight(1)
	if c.Height() != 1 || tip.ID() != expected {
		t.Fatalf("tie-break wrong: height=%d tip=%x want=%x", c.Height(), tip.ID(), expected)
	}
}

// TestDeepReorgRejected: with the finality cap lowered, a reorg deeper than the
// cap is rejected and the node keeps its original chain.
func TestDeepReorgRejected(t *testing.T) {
	defer harness.SmallMaturity()()
	old := chain.MaxReorgDepth
	chain.MaxReorgDepth = 1
	defer func() { chain.MaxReorgDepth = old }()

	cMain := harness.NewChain(t)
	cAlt := harness.NewChain(t)
	wM := harness.NewWallet("deep-main")
	wA := harness.NewWallet("deep-alt")

	for i := 0; i < 3; i++ {
		harness.MineBlock(t, cMain, wM, nil) // main to height 3
	}
	var alt []*block.Block
	for i := 0; i < 4; i++ {
		alt = append(alt, harness.MineBlock(t, cAlt, wA, nil)) // alt to height 4
	}
	mainTip, _ := cMain.HeaderByHeight(3)

	for _, b := range alt {
		_ = cMain.AddBlock(b) // deep reorg attempt should be rejected
	}
	// Chain must NOT have switched to the alt branch (reorg depth 3 > cap 1).
	if cMain.Height() != 3 {
		t.Fatalf("expected to stay at height 3, got %d", cMain.Height())
	}
	got, _ := cMain.HeaderByHeight(3)
	if got.ID() != mainTip.ID() {
		t.Fatalf("deep reorg was wrongly accepted")
	}
}

// TestOrphanConnects: a block whose parent is unknown is buffered, then adopted
// once the parent arrives.
func TestOrphanConnects(t *testing.T) {
	defer harness.SmallMaturity()()
	cAlt := harness.NewChain(t)
	wA := harness.NewWallet("orphan-alt")
	a1 := harness.MineBlock(t, cAlt, wA, nil)
	a2 := harness.MineBlock(t, cAlt, wA, nil)

	c := harness.NewChain(t)
	if err := c.AddBlock(a2); !chain.IsOrphanErr(err) {
		t.Fatalf("expected orphan error for a2, got %v", err)
	}
	if c.Height() != 0 {
		t.Fatalf("orphan should not advance height; got %d", c.Height())
	}
	if err := c.AddBlock(a1); err != nil {
		t.Fatalf("add a1: %v", err)
	}
	// a1 applies (h1) and the buffered a2 connects (h2).
	if c.Height() != 2 {
		t.Fatalf("expected height 2 after orphan connects, got %d", c.Height())
	}
}

