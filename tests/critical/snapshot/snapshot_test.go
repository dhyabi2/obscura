// Package snapshot_test verifies state-snapshot save + restart: a node can restore
// full consensus state from a snapshot (verified against header-committed roots)
// and replay only newer blocks — including when the pre-snapshot block bodies are
// gone (the property block-body pruning relies on). See docs/SCALING_100M.md.
package snapshot_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	bolt "go.etcd.io/bbolt"

	"obscura/pkg/chain"
	"obscura/pkg/config"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
	"obscura/tests/critical/harness"
)

const fee = 100_000_000

func mineOn(t *testing.T, c *chain.Chain, w *wallet.Wallet, txs []*tx.Transaction) {
	t.Helper()
	harness.MineBlock(t, c, w, txs)
}

// TestSnapshotRestartIdentical: mine, snapshot, mine more, restart → identical tip,
// AccValue/AccSize, and a pre-snapshot double-spend is still rejected.
func TestSnapshotRestartIdentical(t *testing.T) {
	defer harness.SmallMaturity()()
	dir := t.TempDir()
	c, err := chain.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	alice := harness.NewWallet("snap-alice")
	bob := harness.NewWallet("snap-bob")
	for i := 0; i < 4; i++ {
		mineOn(t, c, alice, nil)
	}
	harness.ScanAll(c, alice)

	spend, err := alice.CreateTransaction(c, bob.Address(), 1_000_000_000, fee)
	if err != nil {
		t.Fatal(err)
	}
	mineOn(t, c, harness.NewWallet("snap-sink"), []*tx.Transaction{spend})

	// take the snapshot here, then mine MORE blocks on top
	if err := c.SaveSnapshot(); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	snapHeight := c.Height()
	for i := 0; i < 3; i++ {
		mineOn(t, c, harness.NewWallet("snap-sink2"), nil)
	}
	tipBefore := c.Tip()
	hBefore := tipBefore.Height
	idBefore := tipBefore.ID()
	accBefore := append([]byte(nil), c.AccValue()...)
	accSize := c.AccSize()
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	// --- restart: must restore from snapshot + replay only post-snapshot blocks ---
	c2, err := chain.New(dir)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	defer c2.Close()
	t2 := c2.Tip()
	if t2.Height != hBefore || t2.ID() != idBefore {
		t.Fatalf("restart tip mismatch: got height %d want %d", t2.Height, hBefore)
	}
	if !bytes.Equal(c2.AccValue(), accBefore) {
		t.Fatal("restart AccValue mismatch")
	}
	if c2.AccSize() != accSize {
		t.Fatalf("restart AccSize mismatch: %d vs %d", c2.AccSize(), accSize)
	}
	_ = snapHeight

	// double-spend protection survived the restart: replaying the old spend is rejected
	tmpl := harness.BuildTemplate(t, c2, harness.NewWallet("snap-sink3"), []*tx.Transaction{spend})
	harness.MineHeader(t, tmpl)
	if err := c2.AddBlock(tmpl); err == nil {
		t.Fatal("accepted a double-spend after restart (state not restored)")
	}

	// can still mine a fresh valid block on the restored chain
	mineOn(t, c2, harness.NewWallet("snap-sink4"), nil)
	if c2.Height() != hBefore+1 {
		t.Fatal("could not extend the restored chain")
	}
}

// TestSnapshotRestartWithPrunedBodies proves a snapshot makes the pre-snapshot
// block bodies unnecessary: we DELETE them from bolt, then restart must still
// reconstruct the exact tip (this is what block-body pruning will rely on).
//
// PoR caveat: bodies in the PoR-challengeable / protocol-retained window
// [tip-PoRWindow, tip] MUST stay on disk or the restored node cannot mine
// (pkg/block/por.go + pkg/chain/por.go). So we shrink PoRWindow and delete only
// the bodies the protocol would itself prune — those strictly below the PoR floor
// — mirroring real pruning rather than wiping the whole pre-snapshot history.
func TestSnapshotRestartWithPrunedBodies(t *testing.T) {
	defer harness.SmallMaturity()()
	// PoRWindow=3: after restart the tip is 8 and mining proceeds at height 9, so the
	// PoR floor is 9-3=6. Heights [0,5] are then below the floor (never challengeable,
	// legitimately prunable); the retained window [6,8] is left intact.
	oldW := config.PoRWindow
	config.PoRWindow = 3
	defer func() { config.PoRWindow = oldW }()

	dir := t.TempDir()
	c, err := chain.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	w := harness.NewWallet("snap2-w")
	for i := 0; i < 5; i++ {
		mineOn(t, c, w, nil)
	}
	if err := c.SaveSnapshot(); err != nil {
		t.Fatal(err)
	}
	snapHeight := c.Height()
	for i := 0; i < 3; i++ {
		mineOn(t, c, harness.NewWallet("snap2-sink"), nil)
	}
	tipBefore := c.Tip()
	hBefore := tipBefore.Height
	idBefore := tipBefore.ID()
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	// Delete pre-snapshot bodies that the protocol would itself prune: those strictly
	// below the PoR floor for the next mined block (height hBefore+1). Bodies inside
	// the retained PoR window [hBefore+1-PoRWindow, tip] are KEPT — PoR needs them.
	porFloor := uint64(0)
	if next := hBefore + 1; next > config.PoRWindow {
		porFloor = next - config.PoRWindow
	}
	delMax := snapHeight
	if porFloor > 0 && porFloor-1 < delMax {
		delMax = porFloor - 1 // never delete a body the protocol retains for PoR
	}
	db, err := bolt.Open(dir+"/obscura.db", 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Update(func(dtx *bolt.Tx) error {
		b := dtx.Bucket([]byte("blocks"))
		for h := uint64(0); h <= delMax; h++ {
			var k [8]byte
			binary.BigEndian.PutUint64(k[:], h)
			if err := b.Delete(k[:]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	// restart with pre-snapshot bodies GONE: must restore from snapshot + replay
	// only the retained newer bodies, reaching the exact same tip.
	c2, err := chain.New(dir)
	if err != nil {
		t.Fatalf("restart with pruned bodies failed: %v", err)
	}
	defer c2.Close()
	t2 := c2.Tip()
	if t2.Height != hBefore || t2.ID() != idBefore {
		t.Fatalf("pruned restart tip mismatch: got %d want %d", t2.Height, hBefore)
	}
	// and it still extends correctly
	mineOn(t, c2, harness.NewWallet("snap2-sink2"), nil)
	if c2.Height() != hBefore+1 {
		t.Fatal("could not extend chain after pruned restart")
	}
}
