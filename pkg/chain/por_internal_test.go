package chain

import (
	"context"
	"testing"

	bolt "go.etcd.io/bbolt"

	"obscura/pkg/block"
	"obscura/pkg/config"
	"obscura/pkg/miner"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
)

// TestPoRPrunedMinerCannotMine: the defining property — a node missing a CHALLENGED block
// body cannot produce a valid block template (so a pruned node cannot mine).
func TestPoRPrunedMinerCannotMine(t *testing.T) {
	oldD, oldM := config.GenesisDifficulty, config.CoinbaseMaturity
	config.GenesisDifficulty, config.CoinbaseMaturity = 16, 1
	defer func() { config.GenesisDifficulty, config.CoinbaseMaturity = oldD, oldM }()
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	w := wallet.FromSeed([]byte("por-prune-seed-000000000000000000000"))
	mine := func() {
		cb, err := w.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(0, nil), nil)
		if err != nil {
			t.Fatal(err)
		}
		tmpl, err := c.BlockTemplate([]*tx.Transaction{cb})
		if err != nil {
			t.Fatalf("template: %v", err)
		}
		if !miner.Mine(context.Background(), tmpl, 0) {
			t.Fatal("mine")
		}
		if err := c.AddBlock(tmpl); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	for i := 0; i < 10; i++ {
		mine()
	}

	// Determine which body height the NEXT block will challenge, then evict it.
	c.mu.RLock()
	tip := c.headers[len(c.headers)-1]
	prevHash := tip.ID()
	nextH := tip.Height + 1
	victim := block.PoRChallengeHeight(prevHash, 0, nextH)
	c.mu.RUnlock()

	c.mu.Lock()
	delete(c.blocks, victim)
	if c.db != nil {
		_ = c.db.Update(func(dtx *bolt.Tx) error { return dtx.Bucket(bucketBlocks).Delete(heightKey(victim)) })
	}
	c.mu.Unlock()
	if _, ok := c.BlockByHeight(victim); ok {
		t.Fatalf("body %d still present after eviction", victim)
	}

	// A pruned miner cannot build the template (cannot answer the challenge for `victim`).
	cb, _ := w.BuildCoinbase(nextH, c.ExpectedCoinbaseMinted(0, nil), nil)
	if _, err := c.BlockTemplate([]*tx.Transaction{cb}); err == nil {
		t.Fatal("pruned node produced a block template — PoR did not prevent mining")
	}
}

// TestPoRWindowPruneInvariant verifies the protocol design: body pruning is intrinsic
// (every node prunes), challenges are bounded to [H-PoRWindow, H), and the two coincide
// — so a still-mining node always holds every challengeable body. With a small PoRWindow
// and SnapshotInterval, after mining past the window: (a) every body in [tip-PoRWindow,
// tip] is retained on disk, (b) bodies below that floor are pruned, and (c) no PoR
// challenge ever targets a pruned (below-floor) height, so the node keeps mining.
func TestPoRWindowPruneInvariant(t *testing.T) {
	oldD, oldM, oldSI, oldW := config.GenesisDifficulty, config.CoinbaseMaturity, SnapshotInterval, config.PoRWindow
	config.GenesisDifficulty, config.CoinbaseMaturity, SnapshotInterval, config.PoRWindow = 16, 1, 2, 4
	defer func() {
		config.GenesisDifficulty, config.CoinbaseMaturity, SnapshotInterval, config.PoRWindow = oldD, oldM, oldSI, oldW
	}()

	bodyOnDisk := func(c *Chain, h uint64) bool {
		present := false
		_ = c.db.View(func(dtx *bolt.Tx) error {
			present = dtx.Bucket(bucketBlocks).Get(heightKey(h)) != nil
			return nil
		})
		return present
	}

	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	w := wallet.FromSeed([]byte("por-window-seed-00000000000000000000"))
	// Mine well past PoRWindow so the prune floor (tip-PoRWindow) is above genesis and
	// bodies below it must be pruned. Each AddBlock first builds PoR from bodies (so if a
	// challengeable body were pruned, this would fail) — that is property (c).
	for i := 0; i < 16; i++ {
		cb, err := w.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(0, nil), nil)
		if err != nil {
			t.Fatal(err)
		}
		tmpl, err := c.BlockTemplate([]*tx.Transaction{cb})
		if err != nil {
			t.Fatalf("template (height %d): %v", c.Height()+1, err)
		}
		if !miner.Mine(context.Background(), tmpl, 0) {
			t.Fatal("mine")
		}
		if err := c.AddBlock(tmpl); err != nil {
			t.Fatalf("add: %v", err)
		}
	}

	tip := c.Height()
	floor := tip - config.PoRWindow // lowest body the protocol guarantees retained
	// (a) every body in [floor, tip] is retained.
	for h := floor; h <= tip; h++ {
		if !bodyOnDisk(c, h) {
			t.Fatalf("body %d in PoR window [%d,%d] was pruned", h, floor, tip)
		}
	}
	// (b) at least one body below the floor was actually pruned (the snapshot floor is
	// even lower here, so the PoR floor governs; with floor>1 some low body must be gone).
	if floor < 2 {
		t.Fatalf("test misconfigured: floor %d too low to observe pruning", floor)
	}
	if bodyOnDisk(c, 1) {
		t.Fatalf("body 1 (below PoR floor %d) was not pruned — pruning is not intrinsic", floor)
	}
	// (c) no PoR challenge for the NEXT block targets a below-floor (pruned) height.
	c.mu.RLock()
	prevHash := c.headers[len(c.headers)-1].ID()
	nextH := tip + 1
	c.mu.RUnlock()
	nextFloor := nextH - config.PoRWindow
	for slot := 0; slot < config.PoRChallenges; slot++ {
		hc := block.PoRChallengeHeight(prevHash, slot, nextH)
		if hc < nextFloor {
			t.Fatalf("slot %d challenges height %d below retained floor %d", slot, hc, nextFloor)
		}
		if hc >= nextH {
			t.Fatalf("slot %d challenges height %d >= block height %d", slot, hc, nextH)
		}
	}
}

// TestPoRIndexRoundTrip: the merkle-branch index derivation matches the challenged index
// for multi-tx blocks (so the proof is bound to the right leaf, not always tx[0]).
func TestPoRIndexRoundTrip(t *testing.T) {
	for _, n := range []int{1, 2, 3, 5, 8, 13} {
		txs := make([]*tx.Transaction, n)
		for i := range txs {
			txs[i] = &tx.Transaction{Version: 1, Fee: uint64(i + 1), ExtraNonce: uint64(i * 7)}
		}
		root := block.MerkleRoot(txs)
		for idx := 0; idx < n; idx++ {
			steps, ok := block.MerkleProof(txs, idx)
			if !ok {
				t.Fatalf("n=%d proof idx %d failed", n, idx)
			}
			e := &block.PoREntry{Height: 0, TxBytes: txs[idx].Serialize(), Steps: steps}
			if !block.VerifyPoREntry(e, 0, uint32(idx), root) {
				t.Fatalf("n=%d VerifyPoREntry idx %d failed", n, idx)
			}
			// wrong expected index must be rejected (binds proof to the leaf).
			if n > 1 && block.VerifyPoREntry(e, 0, uint32((idx+1)%n), root) {
				t.Fatalf("n=%d proof accepted for wrong index", n)
			}
		}
	}
}
