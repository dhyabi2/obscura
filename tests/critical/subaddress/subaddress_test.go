// Package subaddress tests deterministic sub-accounts (Block 30): a wallet detects
// and spends payments to its subaddresses, balances combine across accounts,
// subaddresses are mutually unlinkable, and the count survives a state round-trip.
package subaddress

import (
	"testing"

	"obscura/pkg/commit"
	"obscura/pkg/tx"
	"obscura/tests/critical/harness"
)

const fee = 100_000_000

func TestSubaddressDerivationDistinct(t *testing.T) {
	k := commit.NewStealthKeys()
	if k.Subaddress(0) != k {
		t.Fatal("index 0 should return the main keypair")
	}
	a1 := k.Subaddress(1).Addr
	a2 := k.Subaddress(2).Addr
	if a1.A.Equal(a2.A) == 1 || a1.B.Equal(a2.B) == 1 {
		t.Fatal("distinct subaddresses share keys")
	}
	if a1.A.Equal(k.Addr.A) == 1 || a1.B.Equal(k.Addr.B) == 1 {
		t.Fatal("subaddress 1 collides with the main address")
	}
	// deterministic: same index → same address
	if k.Subaddress(7).Addr.A.Equal(k.Subaddress(7).Addr.A) != 1 {
		t.Fatal("subaddress derivation is not deterministic")
	}
}

func TestReceiveOnSubaddressAndCombineBalance(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	funder := harness.NewWallet("sub-funder")
	harness.Funded(t, c, funder, 4)

	alice := harness.NewWallet("sub-alice")
	_, sub1 := alice.NewSubaddress()

	const toSub = 3_000_000_000
	const toMain = 2_000_000_000

	// pay the subaddress
	txn1, err := funder.CreateTransaction(c, sub1, toSub, fee)
	if err != nil {
		t.Fatalf("pay sub: %v", err)
	}
	harness.MineBlock(t, c, funder, []*tx.Transaction{txn1})

	// pay the main address
	txn2, err := funder.CreateTransaction(c, alice.Address(), toMain, fee)
	if err != nil {
		t.Fatalf("pay main: %v", err)
	}
	harness.MineBlock(t, c, funder, []*tx.Transaction{txn2})

	harness.ScanAll(c, alice)
	if alice.Balance() != toSub+toMain {
		t.Fatalf("combined balance %d, want %d", alice.Balance(), toSub+toMain)
	}
}

func TestSpendSubaddressOutput(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	funder := harness.NewWallet("sub2-funder")
	harness.Funded(t, c, funder, 4)

	alice := harness.NewWallet("sub2-alice")
	_, sub := alice.NewSubaddress()
	txn, err := funder.CreateTransaction(c, sub, 5_000_000_000, fee)
	if err != nil {
		t.Fatalf("pay sub: %v", err)
	}
	harness.MineBlock(t, c, funder, []*tx.Transaction{txn})
	harness.ScanAll(c, alice)
	if alice.Balance() == 0 {
		t.Fatal("subaddress payment not detected")
	}

	// spend the subaddress-received funds
	bob := harness.NewWallet("sub2-bob")
	spend, err := alice.CreateTransaction(c, bob.Address(), 1_000_000_000, fee)
	if err != nil {
		t.Fatalf("spend from subaddress output: %v", err)
	}
	if err := c.ValidateStandaloneTx(spend); err != nil {
		t.Fatalf("subaddress-funded spend invalid: %v", err)
	}
}

func TestSubaddressCountSurvivesRestore(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	funder := harness.NewWallet("sub3-funder")
	harness.Funded(t, c, funder, 3)

	alice := harness.NewWallet("sub3-alice")
	alice.NewSubaddress()
	alice.NewSubaddress()
	_, sub3 := alice.NewSubaddress() // count = 3
	txn, err := funder.CreateTransaction(c, sub3, 4_000_000_000, fee)
	if err != nil {
		t.Fatalf("pay sub3: %v", err)
	}
	harness.MineBlock(t, c, funder, []*tx.Transaction{txn})
	harness.ScanAll(c, alice)
	bal := alice.Balance()
	if bal == 0 {
		t.Fatal("payment to sub3 not detected")
	}

	// round-trip the state; a restored wallet must keep the subaddress count and
	// therefore the balance received on subaddress #3.
	alice2 := harness.NewWallet("sub3-alice")
	if err := alice2.RestoreState(alice.MarshalState()); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if alice2.SubaddressCount() != 3 {
		t.Fatalf("restored subaddress count %d, want 3", alice2.SubaddressCount())
	}
	if alice2.Balance() != bal {
		t.Fatalf("restored balance %d != %d", alice2.Balance(), bal)
	}
}
