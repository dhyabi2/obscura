// Package feeest tests dynamic fee estimation (Block 20): the manipulation-
// resistant estimator and the node /feerate RPC.
package feeest

import (
	"net/http/httptest"
	"testing"

	"obscura/pkg/config"
	"obscura/pkg/fee"
	"obscura/pkg/mempool"
	"obscura/pkg/rpc"
	"obscura/tests/critical/harness"
)

const floor = config.MinFeePerByte

// quiet/empty chains must suggest exactly the floor (graceful degradation).
func TestEstimateEmptyAndQuiet(t *testing.T) {
	if got := fee.Estimate(nil, 2, floor); got != floor {
		t.Fatalf("no blocks: got %d want floor %d", got, floor)
	}
	// underfull blocks, even with some high-fee txs, mean the floor sufficed
	quiet := []fee.BlockFees{
		{Rates: []uint64{floor, floor * 50}, Fullness: 0.10},
		{Rates: []uint64{floor}, Fullness: 0.02},
		{Rates: nil, Fullness: 0.0},
	}
	if got := fee.Estimate(quiet, 2, floor); got != floor {
		t.Fatalf("quiet chain: got %d want floor %d", got, floor)
	}
}

// when blocks are congested, the suggestion rises above the floor toward the
// marginal included fee-rate.
func TestEstimateCongested(t *testing.T) {
	congested := make([]fee.BlockFees, 0, 10)
	for i := 0; i < 10; i++ {
		congested = append(congested, fee.BlockFees{
			Rates:    []uint64{floor * 2, floor * 4, floor * 8, floor * 16},
			Fullness: 0.99,
		})
	}
	got := fee.Estimate(congested, 2, floor)
	if got <= floor {
		t.Fatalf("congested chain should exceed floor: got %d floor %d", got, floor)
	}
	// urgent target (1) must be >= patient target (6)
	urgent := fee.Estimate(congested, 1, floor)
	patient := fee.Estimate(congested, 6, floor)
	if urgent < patient {
		t.Fatalf("urgent fee %d < patient fee %d (should be >=)", urgent, patient)
	}
}

// a single miner stuffing ONE block with sky-high self-paying fees must not move
// the median-based suggestion: manipulation resistance without identity/stake.
func TestEstimateManipulationResistance(t *testing.T) {
	honest := make([]fee.BlockFees, 0, 11)
	for i := 0; i < 11; i++ {
		honest = append(honest, fee.BlockFees{
			Rates:    []uint64{floor * 2, floor * 3},
			Fullness: 0.99,
		})
	}
	base := fee.Estimate(honest, 1, floor)

	// attacker replaces one block with absurd fee-rates
	attacked := append([]fee.BlockFees(nil), honest...)
	attacked[5] = fee.BlockFees{
		Rates:    []uint64{floor * 100000, floor * 100000, floor * 100000},
		Fullness: 1.0,
	}
	after := fee.Estimate(attacked, 1, floor)
	if after != base {
		t.Fatalf("one stuffed block moved the median: base %d after %d", base, after)
	}
}

// the suggestion is bounded to MaxFeeMultiplier * floor no matter how extreme.
func TestEstimateBounded(t *testing.T) {
	insane := make([]fee.BlockFees, 0, 5)
	for i := 0; i < 5; i++ {
		insane = append(insane, fee.BlockFees{
			Rates:    []uint64{floor * 1_000_000},
			Fullness: 1.0,
		})
	}
	got := fee.Estimate(insane, 1, floor)
	if got > floor*fee.MaxFeeMultiplier {
		t.Fatalf("estimate %d exceeds cap %d", got, floor*fee.MaxFeeMultiplier)
	}
}

// end-to-end: a fresh chain has no congestion, so /feerate returns the floor.
func TestFeeRateRPC(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("fee-alice")
	harness.MineN(t, c, alice, 3)

	srv := rpc.NewServer(c, mempool.New(c), nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	cl, err := rpc.NewClient(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	fr, err := cl.FeeRate(2)
	if err != nil {
		t.Fatalf("feerate rpc: %v", err)
	}
	if fr.FloorPerByte != config.MinFeePerByte {
		t.Fatalf("floor %d != %d", fr.FloorPerByte, config.MinFeePerByte)
	}
	if fr.FeePerByte != config.MinFeePerByte {
		t.Fatalf("quiet chain should suggest the floor, got %d", fr.FeePerByte)
	}
	if fr.Target != 2 {
		t.Fatalf("target echoed wrong: %d", fr.Target)
	}
}
