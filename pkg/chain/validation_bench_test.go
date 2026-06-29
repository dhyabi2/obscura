package chain_test

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"

	"obscura/pkg/chain"
	"obscura/pkg/config"
	"obscura/pkg/miner"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
)

// TestValidationThroughput measures the consensus layer's REAL transaction-validation
// ceiling, isolated from PoW / P2P / network (the operational confounders that capped the
// distributed load test). It builds a block of N independent transparent txs whose proofs
// were never admitted to a mempool (so they are UN-cached), then times full block
// validation. Run with OBX_SEQ_VERIFY=1 to disable the parallel pre-verify and measure the
// sequential baseline; the parallel/sequential ratio is the speedup from prewarmProofCacheLocked.
//
//	go test -run TestValidationThroughput -v ./pkg/chain/                 # parallel
//	OBX_SEQ_VERIFY=1 go test -run TestValidationThroughput -v ./pkg/chain/ # sequential
func TestValidationThroughput(t *testing.T) {
	if testing.Short() {
		t.Skip("throughput benchmark")
	}
	// fast mining for setup (does not affect the measured validation cost).
	oldD, oldM := config.GenesisDifficulty, config.CoinbaseMaturity
	config.GenesisDifficulty, config.CoinbaseMaturity = 16, 1
	defer func() { config.GenesisDifficulty, config.CoinbaseMaturity = oldD, oldM }()

	c, err := chain.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	w := wallet.FromSeed([]byte("bench-wallet-seed-00000000000000000"))
	dest := wallet.FromSeed([]byte("bench-dest-seed-0000000000000000000")).Address()

	const N = 16
	// mine N+2 coinbase blocks so the wallet holds ≥N mature, spendable outputs.
	for i := 0; i < N+2; i++ {
		cb, err := w.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(0, nil), nil)
		if err != nil {
			t.Fatal(err)
		}
		mineBlock(t, c, cb, nil)
	}
	for h := uint64(0); h <= c.Height(); h++ {
		b, _ := c.BlockByHeight(h)
		w.ScanBlock(b)
	}
	outs := w.SpendableOutputs(c.Height() + 1)
	if len(outs) < N {
		t.Fatalf("only %d spendable outputs, need %d", len(outs), N)
	}

	// build N independent txs (each spends a distinct output); NOT submitted to any
	// mempool, so their proofs are un-cached when the block is validated.
	fee := uint64(5_000_000_000)
	var txs []*tx.Transaction
	var sumFee uint64
	for i := 0; i < N; i++ {
		if outs[i].Amount <= fee {
			continue
		}
		tr, err := w.CreateTransactionFrom(c, outs[i], dest, outs[i].Amount-fee, fee)
		if err != nil {
			t.Fatalf("build tx %d: %v", i, err)
		}
		txs = append(txs, tr)
		sumFee += fee
	}
	// assemble + mine a real block carrying the N txs (do NOT add it to the chain).
	cb, err := w.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(sumFee, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := c.BlockTemplate(append([]*tx.Transaction{cb}, txs...))
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	if !miner.Mine(context.Background(), tmpl, 0) {
		t.Fatal("mine block")
	}

	// time full block validation, clearing the proof cache each rep so every rep does the
	// full per-tx proof verification (otherwise reps 2+ would hit the cache).
	const reps = 6
	var total time.Duration
	for r := 0; r < reps; r++ {
		c.ClearVerifiedProofCache()
		t0 := time.Now()
		if err := c.ValidateBlock(tmpl); err != nil {
			t.Fatalf("validate: %v", err)
		}
		total += time.Since(t0)
	}
	per := total / reps
	mode := "PARALLEL"
	if os.Getenv("OBX_SEQ_VERIFY") == "1" {
		mode = "SEQUENTIAL"
	}
	tps := float64(len(txs)) / per.Seconds()
	t.Logf("[%s] cores=%d  block=%d txs  validate=%v/block  => %.0f tx/s",
		mode, runtime.NumCPU(), len(txs), per, tps)
}
