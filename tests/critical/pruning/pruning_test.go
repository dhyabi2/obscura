// Package pruning_test covers the in-design pruning foundations: the header
// NullRoot commitment (the spent key-image set is committed in every header, so
// a pruned/light node can trustlessly verify a spent-set snapshot) and the
// bounded anchor set. See docs/PRUNING_DESIGN.md.
package pruning_test

import (
	"bytes"
	"testing"

	"obscura/pkg/tx"
	"obscura/tests/critical/harness"
)

const fee = 100_000_000

// TestNullRootCommitsSpends: an honest block with a spend commits a non-zero
// nullifier root; a block whose NullRoot is tampered is rejected.
func TestNullRootCommitsSpends(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("prune-alice")
	harness.Funded(t, c, alice, 3)
	bob := harness.NewWallet("prune-bob")

	// before any spend, the tip's nullifier root is the empty (all-zero) root
	var zero [32]byte
	if c.Tip().NullRoot != zero {
		t.Fatalf("expected zero NullRoot before any spend, got %x", c.Tip().NullRoot)
	}

	spend, err := alice.CreateTransaction(c, bob.Address(), 1_000_000_000, fee)
	if err != nil {
		t.Fatalf("create spend: %v", err)
	}
	blk := harness.MineBlock(t, c, harness.NewWallet("prune-sink"), []*tx.Transaction{spend})

	// the block that includes a spend must commit a NON-zero nullifier root
	if blk.Header.NullRoot == zero {
		t.Fatal("block with a spend committed a zero NullRoot (spent set not committed)")
	}
	if c.Tip().NullRoot != blk.Header.NullRoot {
		t.Fatal("tip NullRoot != mined block NullRoot")
	}

	// an empty block afterwards must NOT change the nullifier root (no new spends)
	empty := harness.MineBlock(t, c, harness.NewWallet("prune-sink2"), nil)
	if empty.Header.NullRoot != blk.Header.NullRoot {
		t.Fatal("empty block changed the nullifier root")
	}
}

// TestNullRootTamperRejected: a block whose NullRoot does not match the spent
// key-images it contains is rejected by consensus.
func TestNullRootTamperRejected(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("prune-alice2")
	harness.Funded(t, c, alice, 3)
	bob := harness.NewWallet("prune-bob2")

	spend, err := alice.CreateTransaction(c, bob.Address(), 1_000_000_000, fee)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := harness.BuildTemplate(t, c, harness.NewWallet("prune-sink3"), []*tx.Transaction{spend})
	// corrupt the committed nullifier root, then mine valid PoW over the lie
	tmpl.Header.NullRoot[0] ^= 0xff
	harness.MineHeader(t, tmpl)
	if err := c.AddBlock(tmpl); err == nil {
		t.Fatal("accepted a block with a tampered NullRoot")
	} else if !bytes.Contains([]byte(err.Error()), []byte("nullifier root")) {
		t.Fatalf("expected nullifier-root mismatch, got: %v", err)
	}
}
