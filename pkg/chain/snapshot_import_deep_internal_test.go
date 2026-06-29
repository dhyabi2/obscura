package chain

import (
	"obscura/pkg/config"
	"obscura/pkg/wallet"
	"testing"
)

// TestSnapshotImportDeepTrackTip reproduces the DROPLET scenario that the shallow
// TestSnapshotImportPositive (8 blocks, applies only H+1) does not exercise:
//   - a deep chain (well past the snapshot interval, many saved snapshots),
//   - import the latest transfer snapshot into a FRESH node, then
//   - apply EVERY origin block from H+1..tip (the node must track the live tip, not
//     just accept one block).
//
// The post-import "state-root mismatch" seen on the testnet means the committed state
// after import (stateRootLocked) diverges from the residual root the import verified
// (stateRootOf(rs)). If that bug exists, the FIRST post-import origin block fails here.
func TestSnapshotImportDeepTrackTip(t *testing.T) {
	oldI, oldM := SnapshotInterval, config.CoinbaseMaturity
	SnapshotInterval, config.CoinbaseMaturity = 5, 1 // multiple saved snapshots; mines well under timeout
	t.Cleanup(func() { SnapshotInterval, config.CoinbaseMaturity = oldI, oldM })

	src, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new src: %v", err)
	}
	defer src.Close()
	w := wallet.FromSeed([]byte("snap-deep-seed-00000000000000000"))
	mineCoinbaseBlocks(t, src, w, 18) // snapshots saved at 5,10,15; import at H=15; tail-track 15->18 (3 blocks)

	src.mu.Lock()
	data, h, err := src.encodeTransferSnapshotLocked()
	src.mu.Unlock()
	if err != nil {
		t.Fatalf("encodeTransferSnapshot: %v", err)
	}
	if h == 0 {
		t.Fatalf("transfer height = 0")
	}
	t.Logf("exported transfer snapshot at H=%d (origin tip=%d)", h, src.Height())

	fresh, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new fresh: %v", err)
	}
	defer fresh.Close()

	gotH, err := fresh.VerifyAndImportSnapshot(data)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if gotH != h {
		t.Fatalf("import height = %d, want %d", gotH, h)
	}

	// Direct faithfulness check: the imported state's pre-state root must equal what the
	// origin committed (i.e. block H+1's StateRoot). This is exactly the live validation.
	originHdrH1, ok := src.BlockByHeight(h + 1)
	if !ok {
		t.Fatalf("origin missing block %d", h+1)
	}
	fresh.mu.Lock()
	importedRoot := fresh.stateRootLocked()
	fresh.mu.Unlock()
	if importedRoot != originHdrH1.Header.StateRoot {
		t.Fatalf("FAITHFULNESS BUG: imported stateRootLocked()=%x != origin block[%d].StateRoot=%x",
			importedRoot[:8], h+1, originHdrH1.Header.StateRoot[:8])
	}

	// Now track the tip: apply every origin block H+1..tip. The first failure is the bug.
	tip := src.Height()
	for ht := h + 1; ht <= tip; ht++ {
		nb, ok := src.BlockByHeight(ht)
		if !ok {
			t.Fatalf("origin missing block %d", ht)
		}
		if err := fresh.AddBlock(nb); err != nil {
			t.Fatalf("apply origin block %d to imported node: %v", ht, err)
		}
	}
	if fresh.Height() != tip {
		t.Fatalf("imported node tracked to %d, want tip %d", fresh.Height(), tip)
	}
	t.Logf("imported node tracked origin tip %d->%d (%d blocks) with no mismatch", h, tip, tip-h)
}
