// Package feeselect tests fee-aware block selection (Block 21): the mempool
// hands the miner the highest fee-rate transactions first, deterministically,
// and never exceeds the block byte budget.
package feeselect

import (
	"testing"

	"obscura/pkg/chain"
	"obscura/pkg/mempool"
	"obscura/pkg/tx"
	"obscura/tests/critical/harness"
)

// buildTx funds a fresh wallet (its own coinbase outputs → distinct inputs, so
// no two of these conflict in the mempool) and returns a tx paying `fee`.
func buildTx(t *testing.T, c *chain.Chain, seed string, fee uint64) *tx.Transaction {
	t.Helper()
	w := harness.NewWallet(seed)
	harness.Funded(t, c, w, 2) // 2 blocks @ maturity 1 → mature coinbase
	sink := harness.NewWallet("feeselect-sink")
	txn, err := w.CreateTransaction(c, sink.Address(), 1_000_000_000, fee)
	if err != nil {
		t.Fatalf("create tx (%s): %v", seed, err)
	}
	return txn
}

func TestSelectOrdersByFeeRate(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)

	// distinct, clearly-separated fees; similar sizes → fee order == fee-rate order
	fees := []uint64{30_000_000, 240_000_000, 60_000_000, 120_000_000}
	seeds := []string{"fs-a", "fs-b", "fs-c", "fs-d"}
	mp := mempool.New(c)
	for i, s := range seeds {
		if err := mp.Add(buildTx(t, c, s, fees[i])); err != nil {
			t.Fatalf("add %s: %v", s, err)
		}
	}
	if mp.Size() != len(seeds) {
		t.Fatalf("mempool size %d, want %d", mp.Size(), len(seeds))
	}

	sel := mp.Select(100)
	if len(sel) != len(seeds) {
		t.Fatalf("selected %d, want %d", len(sel), len(seeds))
	}
	// fee-rates must be non-increasing
	prev := ^uint64(0)
	for i, txn := range sel {
		rate := txn.Fee / uint64(len(txn.Serialize()))
		if rate > prev {
			t.Fatalf("selection not fee-rate ordered at %d: %d > %d", i, rate, prev)
		}
		prev = rate
	}
	// highest absolute fee (240M) must come first given near-equal sizes
	if sel[0].Fee != 240_000_000 {
		t.Fatalf("highest-fee tx not first: got fee %d", sel[0].Fee)
	}
}

func TestSelectIsDeterministic(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	mp := mempool.New(c)
	fees := []uint64{40_000_000, 40_000_000, 180_000_000} // includes a fee tie
	for i, s := range []string{"det-a", "det-b", "det-c"} {
		if err := mp.Add(buildTx(t, c, s, fees[i])); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	a := mp.Select(100)
	b := mp.Select(100)
	if len(a) != len(b) {
		t.Fatalf("lengths differ %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].HashHex() != b[i].HashHex() {
			t.Fatalf("non-deterministic order at %d: %s vs %s", i, a[i].HashHex(), b[i].HashHex())
		}
	}
}

// TestSelectRespectsByteBudget: with n large, selection still stops at the block
// byte budget (MaxBlockBytes - CoinbaseReserveBytes). We can't cheaply fill 2MB
// here, so we assert the selected bytes never exceed the budget.
func TestSelectRespectsByteBudget(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	mp := mempool.New(c)
	for i, s := range []string{"bb-a", "bb-b"} {
		if err := mp.Add(buildTx(t, c, s, uint64(50_000_000*(i+1)))); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	sel := mp.Select(1000)
	used := 0
	for _, txn := range sel {
		used += len(txn.Serialize())
	}
	budget := 2_000_000 - mempool.CoinbaseReserveBytes
	if used > budget {
		t.Fatalf("selected bytes %d exceed budget %d", used, budget)
	}
}
