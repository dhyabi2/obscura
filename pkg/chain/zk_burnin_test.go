package chain_test

import (
	"testing"
	"time"

	"obscura/pkg/chain"
	"obscura/pkg/config"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
)

// TestZKBurnIn mints many ZK coins across blocks and spends a batch, measuring
// proof size, prove/verify time, throughput, and — critically — that the node's
// commitment-tree state stays CONSTANT-SIZE regardless of coin count. Skipped under
// -short (each proof is ~1s).
func TestZKBurnIn(t *testing.T) {
	if testing.Short() {
		t.Skip("burn-in is slow; run without -short")
	}
	old := config.CoinbaseMaturity
	config.CoinbaseMaturity = 1
	defer func() { config.CoinbaseMaturity = old }()

	c, err := chain.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	alice := wallet.FromSeed([]byte("alice-burn-seed-0000000000000000"))
	bob := wallet.FromSeed([]byte("bob-burn-seed-00000000000000000000"))

	// Build up a balance.
	for i := 0; i < 8; i++ {
		minted := c.ExpectedCoinbaseMinted(0, nil)
		cb, _ := alice.BuildCoinbase(c.Height()+1, minted, nil)
		mineBlock(t, c, cb, nil)
	}
	for h := uint64(0); h <= c.Height(); h++ {
		b, _ := c.BlockByHeight(h)
		alice.ScanBlock(b)
	}

	const nCoins = 12
	fee := uint64(5_000_000_000)
	mintAmt := uint64(2_000_000_000_000)

	// --- mint nCoins ZK coins, a few per block ---
	stateSize1 := -1
	var coins []*wallet.ZKCoin
	var mintProveTotal time.Duration
	minted := 0
	for minted < nCoins {
		var batch []*tx.Transaction
		for k := 0; k < 3 && minted < nCoins; k++ {
			t0 := time.Now()
			mt, coin, err := alice.CreateZKMint(c, mintAmt, fee)
			mintProveTotal += time.Since(t0)
			if err != nil {
				t.Fatalf("mint %d: %v", minted, err)
			}
			batch = append(batch, mt)
			coins = append(coins, coin)
			minted++
		}
		cb, _ := alice.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(uint64(len(batch))*fee, nil), nil)
		mineBlock(t, c, cb, batch)
		nb, _ := c.BlockByHeight(c.Height())
		alice.ScanBlock(nb) // pick up change so it can fund the next mints
		if stateSize1 < 0 {
			stateSize1 = c.ZKStateSize()
		}
	}
	stateSizeN := c.ZKStateSize()

	// --- spend a batch, measuring prove/verify + proof size ---
	const nSpend = 6
	var proveTotal, verifyTotal time.Duration
	var proofBytes int
	spent := 0
	for i := 0; i < nSpend; i++ {
		coin := coins[i]
		anchor, path, ok := c.ZKWitnessFor(coin.Leaf)
		if !ok {
			t.Fatalf("coin %d missing", i)
		}
		t0 := time.Now()
		sp, err := alice.CreateZKSpend(coin, anchor, path, c.ZKDepth(), bob.Address(), fee)
		proveTotal += time.Since(t0)
		if err != nil {
			t.Fatalf("spend %d: %v", i, err)
		}
		proofBytes += len(sp.ZKInputs[0].Proof)
		t1 := time.Now()
		cb, _ := alice.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(fee, nil), nil)
		mineBlock(t, c, cb, []*tx.Transaction{sp})
		verifyTotal += time.Since(t1)
		spent++
	}

	for h := uint64(0); h <= c.Height(); h++ {
		b, _ := c.BlockByHeight(h)
		bob.ScanBlock(b)
	}

	t.Logf("BURN-IN: minted %d coins, spent %d to bob (height %d)", nCoins, spent, c.Height())
	t.Logf("  node commitment-tree state: %d B at 1 coin, %d B at %d coins (constant-size: %v)",
		stateSize1, stateSizeN, nCoins, stateSize1 == stateSizeN)
	t.Logf("  avg mint prove:  %v", mintProveTotal/time.Duration(nCoins))
	t.Logf("  avg spend prove: %v", proveTotal/time.Duration(nSpend))
	t.Logf("  avg block(incl verify): %v", verifyTotal/time.Duration(nSpend))
	t.Logf("  avg spend proof size: %d B", proofBytes/nSpend)
	t.Logf("  bob balance: %s OBX", config.FormatAmount(bob.Balance()))

	// HARD assertion: node state is constant-size regardless of coin count.
	if stateSize1 != stateSizeN {
		t.Fatalf("commitment-tree node state grew with coins (%d → %d) — not constant-size", stateSize1, stateSizeN)
	}
	if bob.Balance() != uint64(nSpend)*(mintAmt-fee) {
		t.Fatalf("bob balance %d, want %d", bob.Balance(), uint64(nSpend)*(mintAmt-fee))
	}
}
