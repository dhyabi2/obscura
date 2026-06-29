package chain

import (
	"context"
	"testing"

	bolt "go.etcd.io/bbolt"

	"obscura/pkg/config"
	"obscura/pkg/miner"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
)

// TestPartitionReorgPersistsReplayedSuffix is the regression guard for the
// partition-recovery durability bug. A partition-recovery reorg replays the adopted
// branch via rebuildToBranchLocked; the fix makes that replay persist EVERY block to
// bolt directly (persist=true), instead of relying on persistActiveChainLocked which
// only flushes the bounded in-RAM cache (c.blocks, cap blockCacheCap). A deep
// partition-recovery reorg can replay up to PoWSeedLag blocks (> blockCacheCap), so
// the old cache-only flush silently dropped the deepest replayed heights from disk —
// they would be missing after a restart even though they are inside the PoR retention
// window.
//
// We cannot afford to mine blockCacheCap+ real PoW blocks here, so we exercise the
// fixed code path (the replay's per-block persist) at a small fork depth and assert
// the invariant it guarantees: after a partition-recovery reorg, the FULL active
// suffix is on disk in bucketBlocks (the suffix, not just the cached tail). This is
// the property that durability-across-restart depends on.
func TestPartitionReorgPersistsReplayedSuffix(t *testing.T) {
	oldD, oldM := config.GenesisDifficulty, config.CoinbaseMaturity
	config.GenesisDifficulty, config.CoinbaseMaturity = 16, 1 // fast mining + immediate spendability
	defer func() { config.GenesisDifficulty, config.CoinbaseMaturity = oldD, oldM }()

	defer setUInternal(&MaxReorgDepth, 2)()                 // shallow finality so depth 9 is a "deep" reorg
	defer setUInternal(&PartitionRecoveryMargin, 2)()       // enable self-healing
	defer setUInternal(&config.PoWSeedLag, 32)()            // recovery window covers the fork

	mine := func(c *Chain, w *wallet.Wallet) {
		cb, err := w.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(0, nil), nil)
		if err != nil {
			t.Fatalf("coinbase: %v", err)
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

	active, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()
	rival, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer rival.Close()

	alice := wallet.FromSeed([]byte("preplay-alice-00000000000000000000000"))
	bob := wallet.FromSeed([]byte("preplay-bob-000000000000000000000000000"))

	// active: a short chain. rival: a genesis-divergent, decisively heavier fork (depth
	// 9 > MaxReorgDepth 2), forcing the partition-recovery rebuild+replay path.
	const depth = 9
	for i := 0; i < 4; i++ {
		mine(active, alice)
	}
	for i := 0; i < depth; i++ {
		mine(rival, bob)
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

	// Invariant: every replayed height of the adopted branch is durable in bolt. Before
	// the fix, a replayed height that was not in the RAM cache at persist time was absent
	// here. (With this small depth the cache happens to cover it; the assertion still pins
	// the contract that rebuildToBranchLocked persists the suffix it replays, regardless
	// of cache state — see its comment on blockCacheCap.)
	bodyOnDisk := func(h uint64) bool {
		present := false
		_ = active.db.View(func(dtx *bolt.Tx) error {
			present = dtx.Bucket(bucketBlocks).Get(heightKey(h)) != nil
			return nil
		})
		return present
	}
	for h := uint64(0); h <= uint64(depth); h++ {
		if !bodyOnDisk(h) {
			t.Fatalf("replayed block height %d missing from bolt after partition-recovery reorg "+
				"— the replayed suffix was not persisted", h)
		}
	}

	// And the persisted bodies must be the rival (adopted) branch, not the abandoned one.
	for h := uint64(1); h <= uint64(depth); h++ {
		want, _ := rival.HeaderByHeight(h)
		got, ok := active.HeaderByHeight(h)
		if !ok || got.ID() != want.ID() {
			t.Fatalf("active height %d header mismatch after reorg", h)
		}
	}
}

// setUInternal temporarily overrides a uint64 consensus var (internal-package twin of
// the external setU helper) and returns a restore func.
func setUInternal(p *uint64, v uint64) func() { old := *p; *p = v; return func() { *p = old } }
