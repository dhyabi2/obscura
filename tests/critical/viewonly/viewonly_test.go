// Package viewonly tests watch-only wallets (Block 32): a view key lets a wallet
// detect incoming payments and report balances but never spend.
package viewonly

import (
	"testing"

	"obscura/pkg/commit"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
	"obscura/tests/critical/harness"
)

const fee = 100_000_000

func TestViewOnlyScansButCannotSpend(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	funder := harness.NewWallet("vo-funder")
	harness.Funded(t, c, funder, 4)

	full := harness.NewWallet("vo-alice")
	// build a watch-only wallet from the full wallet's view key
	watch, err := wallet.FromViewKey(full.ViewKey())
	if err != nil {
		t.Fatalf("from view key: %v", err)
	}
	if !watch.IsViewOnly() {
		t.Fatal("watch wallet not marked view-only")
	}
	if full.IsViewOnly() {
		t.Fatal("full wallet wrongly marked view-only")
	}
	// same address
	if watch.Address().A.Equal(full.Address().A) != 1 || watch.Address().B.Equal(full.Address().B) != 1 {
		t.Fatal("watch wallet address differs from the full wallet")
	}

	// pay the address; both wallets should see the same balance
	const amt = 3_000_000_000
	txn, err := funder.CreateTransaction(c, full.Address(), amt, fee)
	if err != nil {
		t.Fatalf("pay: %v", err)
	}
	harness.MineBlock(t, c, funder, []*tx.Transaction{txn})
	harness.ScanAll(c, full)
	harness.ScanAll(c, watch)
	if watch.Balance() != full.Balance() || watch.Balance() != amt {
		t.Fatalf("watch balance %d, full %d, want %d", watch.Balance(), full.Balance(), amt)
	}

	// the watch-only wallet must REFUSE to spend
	bob := harness.NewWallet("vo-bob")
	if _, err := watch.CreateTransaction(c, bob.Address(), 1_000_000_000, fee); err == nil {
		t.Fatal("view-only wallet was allowed to spend")
	}
	// the full wallet can still spend the same funds
	if _, err := full.CreateTransaction(c, bob.Address(), 1_000_000_000, fee); err != nil {
		t.Fatalf("full wallet spend failed: %v", err)
	}
}

func TestViewKeyRoundTrip(t *testing.T) {
	full := harness.NewWallet("vo2")
	vk := full.ViewKey()
	if len(vk) != 96 {
		t.Fatalf("view key length %d, want 96 (a||B||NfPk)", len(vk))
	}
	k, err := commit.StealthKeysFromViewKey(vk)
	if err != nil {
		t.Fatalf("parse view key: %v", err)
	}
	if k.Addr.A.Equal(full.Address().A) != 1 || k.Addr.B.Equal(full.Address().B) != 1 {
		t.Fatal("view-key address mismatch")
	}
	// view-only keys cannot derive a one-time spend secret
	out := commit.CreateOutputDeterministic(full.Address(), commit.RandomScalar())
	if _, err := k.OneTimeSecret(out); err == nil {
		t.Fatal("view-only keys derived a spend secret")
	}
}

func TestBadViewKeyRejected(t *testing.T) {
	if _, err := wallet.FromViewKey(make([]byte, 10)); err == nil {
		t.Fatal("short view key accepted")
	}
}
