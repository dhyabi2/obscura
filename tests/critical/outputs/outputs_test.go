// Package outputs tests SpendableOutputs (Block 40): the coin-control view used
// by the `outputs` CLI, including coinbase-maturity gating.
package outputs

import (
	"testing"

	"obscura/pkg/config"
	"obscura/tests/critical/harness"
)

func TestSpendableOutputsMaturityGating(t *testing.T) {
	// use the real (non-small) maturity so coinbase is initially immature
	c := harness.NewChain(t)
	alice := harness.NewWallet("out-alice")

	harness.MineN(t, c, alice, 2)
	harness.ScanAll(c, alice)

	if len(alice.Outputs) < 2 {
		t.Fatalf("expected coinbase outputs, got %d", len(alice.Outputs))
	}
	// at the current tip, coinbase outputs are NOT yet mature (CoinbaseMaturity > 2)
	if got := alice.SpendableOutputs(c.Height() + 1); len(got) != 0 {
		t.Fatalf("coinbase should be immature: %d spendable", len(got))
	}
	// far in the future they ARE mature
	future := c.Height() + config.CoinbaseMaturity + 1
	if got := alice.SpendableOutputs(future); len(got) != len(alice.Outputs) {
		t.Fatalf("matured spendable %d, want %d", len(got), len(alice.Outputs))
	}
}

func TestSpendableOutputsAfterSmallMaturity(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("out-alice2")
	harness.Funded(t, c, alice, 3)
	sp := alice.SpendableOutputs(c.Height() + 1)
	if len(sp) == 0 {
		t.Fatal("no spendable outputs after maturity")
	}
	var total uint64
	for _, o := range sp {
		total += o.Amount
	}
	if total != alice.Balance() {
		t.Fatalf("spendable total %d != balance %d", total, alice.Balance())
	}
}
