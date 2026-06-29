// Package anonchain is the end-to-end on-chain test of the anonymous spend
// using frozen anonymity pools (Blocks 3 + 7): a wallet spends a coin hidden in
// its pool's canonical ring, consensus rebuilds the ring from the pool id and
// validates the joint proof + key-image, the recipient receives, and a re-spend
// is rejected by the tag set — without revealing which coin was spent.
package anonchain

import (
	"testing"

	"obscura/pkg/config"
	"obscura/pkg/tx"
	"obscura/tests/critical/harness"
)

func TestAnonymousSpendEndToEnd(t *testing.T) {
	defer harness.SmallMaturity()()
	oldPool := config.PoolSize
	config.PoolSize = 4 // small pools so a pool completes quickly in the test
	defer func() { config.PoolSize = oldPool }()

	c := harness.NewChain(t)
	alice := harness.NewWallet("anon-alice")
	bob := harness.NewWallet("anon-bob")

	// Mine enough coinbase coins to fill at least one pool, then scan.
	harness.MineN(t, c, alice, 8)
	harness.ScanAll(c, alice)
	if alice.Balance() == 0 {
		t.Fatal("alice has no funds")
	}

	// Find one of Alice's coins that sits in a complete, mature pool.
	height := c.Height() + 1
	var poolID uint64
	var poolKeys, poolCommits [][]byte
	found := false
	for _, o := range alice.Outputs {
		pid, _, ok := c.PoolOf(o.Out.OneTimeKey)
		if !ok {
			continue
		}
		keys, commits, ok := c.PoolMembers(pid, height)
		if !ok {
			continue
		}
		poolID, poolKeys, poolCommits = pid, keys, commits
		found = true
		break
	}
	if !found {
		t.Fatal("no complete mature pool containing an owned coin")
	}

	fee := uint64(1_000_000_000)
	send := alice.Balance() / 20
	anonTx, err := alice.CreateAnonTransaction(c, poolID, poolKeys, poolCommits, bob.Address(), send, fee)
	if err != nil {
		t.Fatalf("create anon tx: %v", err)
	}

	// Purely-anonymous: no transparent input, ring = a whole pool referenced by id.
	if len(anonTx.Inputs) != 0 || len(anonTx.AnonInputs) != 1 {
		t.Fatalf("expected purely-anonymous tx, got %d transparent / %d anon", len(anonTx.Inputs), len(anonTx.AnonInputs))
	}
	if anonTx.AnonInputs[0].PoolID != poolID {
		t.Fatalf("pool id mismatch")
	}

	if err := c.ValidateStandaloneTx(anonTx); err != nil {
		t.Fatalf("anon tx rejected by consensus: %v", err)
	}
	harness.MineBlock(t, c, alice, []*tx.Transaction{anonTx})

	harness.ScanAll(c, bob)
	if bob.Balance() != send {
		t.Fatalf("bob balance = %s, want %s", config.FormatAmount(bob.Balance()), config.FormatAmount(send))
	}

	// Double-spend: re-submitting the same anon tx must fail (key-image seen).
	if err := c.ValidateStandaloneTx(anonTx); err == nil {
		t.Fatal("anonymous double-spend was accepted (tag not enforced)")
	}
	if !c.TagSpent(anonTx.AnonInputs[0].Tag) {
		t.Fatal("key-image not recorded after mining")
	}
}

// TestIncompletePoolRejected: spending against a pool that is not yet full is
// rejected (the "frozen" requirement).
func TestIncompletePoolRejected(t *testing.T) {
	defer harness.SmallMaturity()()
	oldPool := config.PoolSize
	config.PoolSize = 8
	defer func() { config.PoolSize = oldPool }()

	c := harness.NewChain(t)
	alice := harness.NewWallet("inc-alice")
	harness.MineN(t, c, alice, 3) // fewer than PoolSize coins → pool 0 incomplete
	harness.ScanAll(c, alice)

	_, _, ok := c.PoolMembers(0, c.Height()+1)
	if ok {
		t.Fatal("pool should be incomplete with fewer than PoolSize coins")
	}
}
