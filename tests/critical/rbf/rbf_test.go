// Package rbf tests replace-by-fee (Block 22): a stuck low-fee transaction can be
// superseded in the mempool by a higher-fee replacement that re-spends the same
// inputs, subject to BIP125-style anti-DoS rules.
package rbf

import (
	"testing"

	"obscura/pkg/mempool"
	"obscura/pkg/tx"
	"obscura/tests/critical/harness"
)

const (
	amount  = 1_000_000_000
	lowFee  = 25_000_000 // above the per-byte floor for a ~23.8KB tx
	highFee = 60_000_000 // covers lowFee + relay bandwidth → valid RBF
)

func shareInputs(a, b *tx.Transaction) bool {
	set := map[string]bool{}
	for _, in := range a.Inputs {
		set[string(in.OutputRef)] = true
	}
	for _, in := range b.Inputs {
		if set[string(in.OutputRef)] {
			return true
		}
	}
	return false
}

func contains(mp *mempool.Mempool, id string) bool {
	for _, t := range mp.Select(1000) {
		if t.HashHex() == id {
			return true
		}
	}
	return false
}

func TestRBFReplacesStuckTx(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("rbf-alice")
	harness.Funded(t, c, alice, 3)
	bob := harness.NewWallet("rbf-bob")

	tx1, err := alice.CreateTransaction(c, bob.Address(), amount, lowFee)
	if err != nil {
		t.Fatalf("tx1: %v", err)
	}
	mp := mempool.New(c)
	if err := mp.Add(tx1); err != nil {
		t.Fatalf("add tx1: %v", err)
	}

	tx2, err := alice.BumpFee(tx1, bob.Address(), amount, highFee)
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	if !shareInputs(tx1, tx2) {
		t.Fatal("bumped tx does not reuse the original inputs (would be a double payment, not a replacement)")
	}
	if tx2.Fee <= tx1.Fee {
		t.Fatalf("bumped fee %d not above original %d", tx2.Fee, tx1.Fee)
	}

	if err := mp.Add(tx2); err != nil {
		t.Fatalf("RBF replacement rejected: %v", err)
	}
	if mp.Size() != 1 {
		t.Fatalf("mempool size %d after replacement, want 1", mp.Size())
	}
	if contains(mp, tx1.HashHex()) {
		t.Fatal("original tx1 still present after replacement")
	}
	if !contains(mp, tx2.HashHex()) {
		t.Fatal("replacement tx2 not present")
	}
}

func TestRBFRejectsInsufficientBump(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("rbf2-alice")
	harness.Funded(t, c, alice, 3)
	bob := harness.NewWallet("rbf2-bob")

	tx1, err := alice.CreateTransaction(c, bob.Address(), amount, lowFee)
	if err != nil {
		t.Fatalf("tx1: %v", err)
	}
	mp := mempool.New(c)
	if err := mp.Add(tx1); err != nil {
		t.Fatalf("add tx1: %v", err)
	}

	// a higher fee-rate but NOT enough to cover replaced fee + relay bandwidth
	tooLow := uint64(lowFee + 5_000_000)
	tx2, err := alice.BumpFee(tx1, bob.Address(), amount, tooLow)
	if err != nil {
		t.Fatalf("bump build: %v", err)
	}
	if err := mp.Add(tx2); err == nil {
		t.Fatal("insufficient-bump replacement was accepted")
	}
	// original must survive a failed replacement
	if mp.Size() != 1 || !contains(mp, tx1.HashHex()) {
		t.Fatal("original tx1 lost after a rejected replacement")
	}
}

func TestBumpFeeMustExceedOriginal(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("rbf3-alice")
	harness.Funded(t, c, alice, 3)
	bob := harness.NewWallet("rbf3-bob")

	tx1, err := alice.CreateTransaction(c, bob.Address(), amount, lowFee)
	if err != nil {
		t.Fatalf("tx1: %v", err)
	}
	if _, err := alice.BumpFee(tx1, bob.Address(), amount, lowFee); err == nil {
		t.Fatal("BumpFee accepted a fee equal to the original")
	}
	if _, err := alice.BumpFee(tx1, bob.Address(), amount, lowFee-1); err == nil {
		t.Fatal("BumpFee accepted a fee below the original")
	}
}
