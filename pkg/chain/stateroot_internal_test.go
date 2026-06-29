package chain

import (
	"context"
	"testing"

	"obscura/pkg/config"
	"obscura/pkg/miner"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
)

// TestStateRootConsensus proves the header StateRoot is enforced by validation, that a tampered
// StateRoot is rejected, and that the underlying state commitment survives a restart unchanged
// (cross-restart determinism — the property that keeps nodes in consensus).
func TestStateRootConsensus(t *testing.T) {
	old := config.CoinbaseMaturity
	config.CoinbaseMaturity = 1
	defer func() { config.CoinbaseMaturity = old }()

	dir := t.TempDir()
	c, err := New(dir)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	w := wallet.FromSeed([]byte("stateroot-seed-00000000000000000000"))

	// (1) honest blocks validate (StateRoot set by template, checked by validate).
	mineCoinbaseBlocks(t, c, w, 4)
	rootAfter4 := c.stateRootLocked()

	// (2) a tampered StateRoot is rejected. Corrupt BEFORE mining so PoW is valid for the
	// corrupted header — isolating the state-root check from the PoW check.
	minted := c.ExpectedCoinbaseMinted(0, nil)
	cb, err := w.BuildCoinbase(c.Height()+1, minted, nil)
	if err != nil {
		t.Fatalf("coinbase: %v", err)
	}
	tmpl, err := c.BlockTemplate([]*tx.Transaction{cb})
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	tmpl.Header.StateRoot[0] ^= 0xff
	if !miner.Mine(context.Background(), tmpl, 0) {
		t.Fatal("mine failed")
	}
	if err := c.AddBlock(tmpl); err == nil {
		t.Fatal("block with tampered StateRoot must be rejected")
	}

	// the honest chain is unchanged and still extends correctly.
	mineCoinbaseBlocks(t, c, w, 1)
	rootAfter5 := c.stateRootLocked()
	if rootAfter5 == rootAfter4 {
		t.Fatal("state root must advance as the chain grows")
	}
	heightBefore := c.Height()
	c.Close()

	// (3) restart: reopen the same dir and confirm the state commitment is identical —
	// the disk-set commits must restore exactly (via the per-count commit bucket).
	c2, err := New(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer c2.Close()
	if c2.Height() != heightBefore {
		t.Fatalf("restart height %d != %d", c2.Height(), heightBefore)
	}
	if c2.stateRootLocked() != rootAfter5 {
		t.Fatal("state root must be identical after restart (commit restoration broken)")
	}
}
