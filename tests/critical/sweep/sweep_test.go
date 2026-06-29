// Package sweep tests CreateSweepTransaction (Block 33): spend the entire
// spendable balance to one destination with no change.
package sweep

import (
	"testing"

	"obscura/pkg/tx"
	"obscura/tests/critical/harness"
)

const fee = 100_000_000

func TestSweepSpendsEverythingNoChange(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("sw-alice")
	harness.Funded(t, c, alice, 4) // several coinbase outputs
	bal := alice.Balance()
	if bal == 0 {
		t.Fatal("no balance")
	}
	bob := harness.NewWallet("sw-bob")

	txn, err := alice.CreateSweepTransaction(c, bob.Address(), fee)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if err := c.ValidateStandaloneTx(txn); err != nil {
		t.Fatalf("sweep tx invalid: %v", err)
	}
	// exactly one output (no change) paying total − fee
	if len(txn.Outputs) != 1 {
		t.Fatalf("sweep produced %d outputs, want 1 (no change)", len(txn.Outputs))
	}
	if txn.Fee != fee {
		t.Fatalf("fee %d, want %d", txn.Fee, fee)
	}

	// mine it in a block with a throwaway coinbase so alice gets no new funds
	harness.MineEmptyBlock(t, c, []*tx.Transaction{txn})
	harness.ScanAll(c, alice)
	harness.ScanAll(c, bob)
	if alice.Balance() != 0 {
		t.Fatalf("alice balance after sweep = %d, want 0", alice.Balance())
	}
	if bob.Balance() != bal-fee {
		t.Fatalf("bob balance %d, want %d", bob.Balance(), bal-fee)
	}
}
