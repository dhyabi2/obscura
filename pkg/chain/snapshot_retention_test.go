package chain

import (
	"obscura/pkg/config"
	"testing"
)

// TestSnapshotRetentionCoversDeepReorg locks the audit-2026-06-28 fix: the retained
// snapshots must reach at least PoWSeedLag below the tip so a deep partition-recovery
// reorg can always restore a snapshot at-or-below its fork point.
func TestSnapshotRetentionCoversDeepReorg(t *testing.T) {
	oldSI := SnapshotInterval
	defer func() { SnapshotInterval = oldSI }()
	for _, si := range []uint64{200, 100, 25, 1} {
		SnapshotInterval = si
		keep := snapshotsToKeep()
		if keep < 2 {
			t.Fatalf("SI=%d: keep=%d < 2", si, keep)
		}
		// the oldest of `keep` snapshots sits (keep-1)*SI below the newest; it must be
		// at least PoWSeedLag below the tip.
		if cover := uint64(keep-1) * si; cover < config.PoWSeedLag {
			t.Fatalf("SI=%d keep=%d: coverage %d < PoWSeedLag %d (deep reorg cannot heal)", si, keep, cover, config.PoWSeedLag)
		}
	}
}
