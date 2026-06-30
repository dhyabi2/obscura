package chain

import (
	"testing"

	bolt "go.etcd.io/bbolt"
)

// TestTxIndexBuildOnOpen verifies both maintenance paths of the additive txid->height
// query index: (1) the apply hook indexes a block's txs as it is added (here, genesis),
// and (2) buildTxIndexIfAbsent rebuilds the index from persisted block bodies when an
// existing database lacks it (the upgrade path for a node whose DB predates the index).
func TestTxIndexBuildOnOpen(t *testing.T) {
	dir := t.TempDir()

	c, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	gb, ok := c.BlockByHeight(0)
	if !ok {
		t.Fatal("no genesis block")
	}
	gtid := gb.Txs[0].HashHex()

	// (1) apply-hook indexing: genesis tx must resolve to height 0.
	if h, ok := c.TxHeight(gtid); !ok || h != 0 {
		t.Fatalf("apply-hook index: got h=%d ok=%v, want 0 true", h, ok)
	}
	if h, ok := c.TxHeight("00" + gtid[2:]); ok {
		t.Fatalf("unknown txid resolved: h=%d", h)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Simulate a database created before this index existed: drop the txindex bucket.
	db, err := bolt.Open(dir+"/obscura.db", 0600, nil)
	if err != nil {
		t.Fatalf("reopen bolt: %v", err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		if tx.Bucket(bucketTxIndex) != nil {
			return tx.DeleteBucket(bucketTxIndex)
		}
		return nil
	}); err != nil {
		t.Fatalf("drop txindex bucket: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close bolt: %v", err)
	}

	// (2) build-on-open: reopening must repopulate the index from block bodies.
	c2, err := New(dir)
	if err != nil {
		t.Fatalf("reopen New: %v", err)
	}
	defer c2.Close()
	if h, ok := c2.TxHeight(gtid); !ok || h != 0 {
		t.Fatalf("build-on-open index: got h=%d ok=%v, want 0 true", h, ok)
	}
}
