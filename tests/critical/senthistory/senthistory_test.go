// Package senthistory tests outgoing-payment persistence (Block 23): recorded
// sends survive a state round-trip, get confirmed by scanning, and a bump both
// supersedes the old record and appends a new one. Also verifies backward
// compatibility with state files written before the history section existed.
package senthistory

import (
	"testing"

	"obscura/pkg/tx"
	"obscura/tests/critical/harness"
)

const (
	amount = 1_000_000_000
	fee    = 100_000_000
)

func TestSentRecordedAndRoundTrips(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("sh-alice")
	harness.Funded(t, c, alice, 3)
	bob := harness.NewWallet("sh-bob")

	txn, err := alice.CreateTransaction(c, bob.Address(), amount, fee)
	if err != nil {
		t.Fatalf("tx: %v", err)
	}
	alice.RecordSent(txn, bob.Address(), amount)

	// persist → restore into a fresh wallet from the same seed
	alice2 := harness.NewWallet("sh-alice")
	if err := alice2.RestoreState(alice.MarshalState()); err != nil {
		t.Fatalf("restore: %v", err)
	}
	hist := alice2.SentHistory()
	if len(hist) != 1 {
		t.Fatalf("history len %d, want 1", len(hist))
	}
	s := hist[0]
	if s.TxID != txn.HashHex() || s.Amount != amount || s.Fee != fee {
		t.Fatalf("history record wrong: %+v", s)
	}
	if s.Height != 0 || s.Replaced {
		t.Fatal("freshly recorded send should be pending and not replaced")
	}
	// stored Raw must deserialize back to the same tx
	got, err := tx.Deserialize(s.Raw)
	if err != nil || got.HashHex() != txn.HashHex() {
		t.Fatalf("stored raw tx does not round-trip: %v", err)
	}
}

func TestSentConfirmedByScan(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("sh2-alice")
	harness.Funded(t, c, alice, 3)
	bob := harness.NewWallet("sh2-bob")

	txn, err := alice.CreateTransaction(c, bob.Address(), amount, fee)
	if err != nil {
		t.Fatalf("tx: %v", err)
	}
	alice.RecordSent(txn, bob.Address(), amount)

	// mine a block that includes the payment, then scan it
	blk := harness.MineBlock(t, c, harness.NewWallet("sh2-miner"), []*tx.Transaction{txn})
	alice.ScanBlock(blk)

	s := alice.FindSent(txn.HashHex())
	if s == nil || s.Height != blk.Header.Height {
		t.Fatalf("payment not marked confirmed at its block height: %+v", s)
	}
}

func TestBumpUpdatesHistory(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("sh3-alice")
	harness.Funded(t, c, alice, 3)
	bob := harness.NewWallet("sh3-bob")

	tx1, err := alice.CreateTransaction(c, bob.Address(), amount, 25_000_000)
	if err != nil {
		t.Fatalf("tx1: %v", err)
	}
	alice.RecordSent(tx1, bob.Address(), amount)

	tx2, err := alice.BumpFee(tx1, bob.Address(), amount, 60_000_000)
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	// CLI flow: mark the old record replaced and record the new one
	alice.FindSent(tx1.HashHex()).Replaced = true
	alice.RecordSent(tx2, bob.Address(), amount)

	if len(alice.SentHistory()) != 2 {
		t.Fatalf("history len %d, want 2", len(alice.SentHistory()))
	}
	if !alice.FindSent(tx1.HashHex()).Replaced {
		t.Fatal("original not marked replaced")
	}
	if alice.FindSent(tx2.HashHex()) == nil {
		t.Fatal("bumped tx not recorded")
	}
}

// TestBackwardCompatNoHistorySection: a state file written before the history
// section existed (i.e. ending right after the outputs) must still restore, with
// an empty history.
func TestBackwardCompatNoHistorySection(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("sh4-alice")
	harness.Funded(t, c, alice, 2)

	// with no sends, the history section is exactly an 8-byte count of zero;
	// dropping it simulates an old, pre-history state file.
	full := alice.MarshalState()
	old := full[:len(full)-8]

	alice2 := harness.NewWallet("sh4-alice")
	if err := alice2.RestoreState(old); err != nil {
		t.Fatalf("restore old-format state: %v", err)
	}
	if len(alice2.SentHistory()) != 0 {
		t.Fatal("old-format restore should yield empty history")
	}
	if alice2.Balance() != alice.Balance() {
		t.Fatalf("old-format restore lost balance: %d != %d", alice2.Balance(), alice.Balance())
	}
}
