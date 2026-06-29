// Package mempoolstats tests the mempool snapshot used by the /mempool RPC
// (Block 39): counts, bytes, total fees, and the fee-rate min/median/max.
package mempoolstats

import (
	"net/http/httptest"
	"testing"

	"obscura/pkg/mempool"
	"obscura/pkg/rpc"
	"obscura/pkg/tx"
	"obscura/tests/critical/harness"
)

func TestMempoolStatsAndRPC(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	mp := mempool.New(c)

	// empty pool
	if mp.Stats().Count != 0 {
		t.Fatal("empty mempool reports nonzero count")
	}

	// fund several wallets so their txs use disjoint inputs (no conflicts) and
	// add them with distinct fees
	fees := []uint64{30_000_000, 90_000_000, 60_000_000}
	seeds := []string{"mps-a", "mps-b", "mps-c"}
	var total uint64
	for i, s := range seeds {
		w := harness.NewWallet(s)
		harness.Funded(t, c, w, 2)
		sink := harness.NewWallet("mps-sink")
		txn, err := w.CreateTransaction(c, sink.Address(), 1_000_000_000, fees[i])
		if err != nil {
			t.Fatalf("tx %s: %v", s, err)
		}
		if err := mp.Add(txn); err != nil {
			t.Fatalf("add %s: %v", s, err)
		}
		total += fees[i]
	}

	st := mp.Stats()
	if st.Count != len(seeds) {
		t.Fatalf("count %d, want %d", st.Count, len(seeds))
	}
	if st.TotalFees != total {
		t.Fatalf("total fees %d, want %d", st.TotalFees, total)
	}
	if st.Bytes <= 0 {
		t.Fatal("bytes not positive")
	}
	if !(st.MinFeeRate <= st.MedFeeRate && st.MedFeeRate <= st.MaxFeeRate) {
		t.Fatalf("fee-rate ordering wrong: min %d med %d max %d", st.MinFeeRate, st.MedFeeRate, st.MaxFeeRate)
	}
	if st.MinFeeRate == 0 {
		t.Fatal("min fee-rate is zero")
	}

	// over RPC
	srv := rpc.NewServer(c, mp, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	cl, _ := rpc.NewClient(ts.URL)
	got, err := cl.Mempool()
	if err != nil {
		t.Fatalf("rpc mempool: %v", err)
	}
	if got.Count != st.Count || got.TotalFees != st.TotalFees {
		t.Fatalf("RPC stats differ: %+v vs %+v", got, st)
	}
	_ = tx.MaxInputs
}
