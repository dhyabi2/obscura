package chain_test

import (
	"testing"

	"obscura/pkg/chain"
	"obscura/pkg/config"
	"obscura/pkg/stark"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
)

// TestZKEpochRolloverOnChain proves the scale answer end-to-end: with a tiny epoch
// capacity, minting more coins than one epoch holds rolls into new epochs (unlimited
// total coins), and a coin minted in an EARLY epoch is still spendable against its own
// (finalized) epoch root — at constant per-proof depth.
func TestZKEpochRolloverOnChain(t *testing.T) {
	oldMat, oldDepth := config.CoinbaseMaturity, stark.ZKDepth
	config.CoinbaseMaturity = 1
	stark.ZKDepth = 1 // epoch capacity = 2 coins → rolls fast
	defer func() { config.CoinbaseMaturity, stark.ZKDepth = oldMat, oldDepth }()

	c, err := chain.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	alice := wallet.FromSeed([]byte("alice-epoch-seed-0000000000000000"))
	bob := wallet.FromSeed([]byte("bob-epoch-seed-000000000000000000"))

	for i := 0; i < 8; i++ {
		minted := c.ExpectedCoinbaseMinted(0, nil)
		cb, _ := alice.BuildCoinbase(c.Height()+1, minted, nil)
		mineBlock(t, c, cb, nil)
	}
	for h := uint64(0); h <= c.Height(); h++ {
		b, _ := c.BlockByHeight(h)
		alice.ScanBlock(b)
	}

	fee := uint64(5_000_000_000)
	amt := uint64(2_000_000_000_000)

	// mint our coin FIRST (epoch 0), then mint enough more to roll past several epochs.
	mintTx, myCoin, err := alice.CreateZKMint(c, amt, fee)
	if err != nil {
		t.Fatal(err)
	}
	cb, _ := alice.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(fee, nil), nil)
	mineBlock(t, c, cb, []*tx.Transaction{mintTx})
	nb, _ := c.BlockByHeight(c.Height())
	alice.ScanBlock(nb)

	// mint 9 more coins (one per block) → forces several epoch rollovers (cap 4).
	for i := 0; i < 4; i++ {
		mt, _, err := alice.CreateZKMint(c, amt, fee)
		if err != nil {
			t.Fatalf("extra mint %d: %v", i, err)
		}
		cb, _ := alice.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(fee, nil), nil)
		mineBlock(t, c, cb, []*tx.Transaction{mt})
		b2, _ := c.BlockByHeight(c.Height())
		alice.ScanBlock(b2)
	}

	// SPEND our epoch-0 coin (now several epochs old) against its own epoch root.
	anchor, path, ok := c.ZKWitnessFor(myCoin.Leaf)
	if !ok {
		t.Fatal("old-epoch coin witness not found")
	}
	if len(path.Siblings) != stark.ZKDepth {
		t.Fatalf("path depth %d != fixed %d (proof cost must be constant)", len(path.Siblings), stark.ZKDepth)
	}
	spendTx, err := alice.CreateZKSpend(myCoin, anchor, path, c.ZKDepth(), bob.Address(), fee)
	if err != nil {
		t.Fatalf("spend old-epoch coin: %v", err)
	}
	cb, _ = alice.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(fee, nil), nil)
	mineBlock(t, c, cb, []*tx.Transaction{spendTx})

	for h := uint64(0); h <= c.Height(); h++ {
		b, _ := c.BlockByHeight(h)
		bob.ScanBlock(b)
	}
	if bob.Balance() != amt-fee {
		t.Fatalf("bob balance %d, want %d", bob.Balance(), amt-fee)
	}
	t.Logf("epoch rollover OK: minted 5 coins into cap-2 epochs, spent an old-epoch coin at fixed depth %d", stark.ZKDepth)
}

// TestZKEpochMidBlockRollover is the regression for the anchor bug found in review:
// when several coins fill+roll an epoch WITHIN ONE BLOCK, the finalized epoch's
// terminal root must still be a valid spend anchor (else those coins are stuck).
func TestZKEpochMidBlockRollover(t *testing.T) {
	oldMat, oldDepth := config.CoinbaseMaturity, stark.ZKDepth
	config.CoinbaseMaturity = 1
	stark.ZKDepth = 1 // cap 2 coins/epoch
	defer func() { config.CoinbaseMaturity, stark.ZKDepth = oldMat, oldDepth }()

	c, err := chain.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	alice := wallet.FromSeed([]byte("alice-midblk-seed-00000000000000"))
	bob := wallet.FromSeed([]byte("bob-midblk-seed-0000000000000000"))
	for i := 0; i < 8; i++ {
		minted := c.ExpectedCoinbaseMinted(0, nil)
		cb, _ := alice.BuildCoinbase(c.Height()+1, minted, nil)
		mineBlock(t, c, cb, nil)
	}
	for h := uint64(0); h <= c.Height(); h++ {
		b, _ := c.BlockByHeight(h)
		alice.ScanBlock(b)
	}

	fee := uint64(5_000_000_000)
	amt := uint64(2_000_000_000_000)
	// THREE mints, all in ONE block: coins 0,1 fill epoch 0; coin 2 rolls to epoch 1.
	var batch []*tx.Transaction
	var coin0 *wallet.ZKCoin
	for i := 0; i < 3; i++ {
		mt, coin, err := alice.CreateZKMint(c, amt, fee)
		if err != nil {
			t.Fatalf("mint %d: %v", i, err)
		}
		if i == 0 {
			coin0 = coin
		}
		batch = append(batch, mt)
	}
	cb, _ := alice.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(3*fee, nil), nil)
	mineBlock(t, c, cb, batch)

	// spend coin 0 — it's in the now-FINALIZED epoch 0; its terminal root must be a
	// valid anchor (the bug: it wasn't recorded mid-block).
	anchor, path, ok := c.ZKWitnessFor(coin0.Leaf)
	if !ok {
		t.Fatal("coin0 witness not found")
	}
	sp, err := alice.CreateZKSpend(coin0, anchor, path, c.ZKDepth(), bob.Address(), fee)
	if err != nil {
		t.Fatal(err)
	}
	cb, _ = alice.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(fee, nil), nil)
	if err := mineMayFail(t, c, cb, []*tx.Transaction{sp}); err != nil {
		t.Fatalf("spend of finalized-epoch coin rejected (anchor bug): %v", err)
	}
}
