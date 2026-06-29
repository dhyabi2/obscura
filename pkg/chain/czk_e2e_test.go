package chain_test

import (
	"testing"

	"obscura/pkg/chain"
	"obscura/pkg/config"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
)

// TestConfidentialSpendEndToEnd drives the CONFIDENTIAL ZK→ZK spend through real blocks:
// mint a ZK coin → confidentially spend it to Bob with the amount HIDDEN → Bob recovers
// the hidden amount and re-spends it confidentially. Then the soundness guards:
// double-spend, out-of-range fee (FINDING 5), and tampered proof must all be rejected.
func TestConfidentialSpendEndToEnd(t *testing.T) {
	old := config.CoinbaseMaturity
	config.CoinbaseMaturity = 1
	defer func() { config.CoinbaseMaturity = old }()

	c, err := chain.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	alice := wallet.FromSeed([]byte("alice-czk-seed-00000000000000000"))
	bob := wallet.FromSeed([]byte("bob-czk-seed-0000000000000000000000"))
	carol := wallet.FromSeed([]byte("carol-czk-seed-000000000000000000"))

	for i := 0; i < 4; i++ {
		minted := c.ExpectedCoinbaseMinted(0, nil)
		cb, err := alice.BuildCoinbase(c.Height()+1, minted, nil)
		if err != nil {
			t.Fatal(err)
		}
		mineBlock(t, c, cb, nil)
	}
	for h := uint64(0); h <= c.Height(); h++ {
		b, _ := c.BlockByHeight(h)
		alice.ScanBlock(b)
	}
	if alice.Balance() == 0 {
		t.Fatal("alice has no balance")
	}

	// MINT a ZK coin to Alice (in confidential range).
	fee := uint64(5_000_000_000)
	mintAmount := alice.Balance() / 4
	mintTx, coin, err := alice.CreateZKMint(c, mintAmount, fee)
	if err != nil {
		t.Fatalf("create mint: %v", err)
	}
	cb, _ := alice.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(mintTx.Fee, nil), nil)
	mineBlock(t, c, cb, []*tx.Transaction{mintTx})

	anchor, path, ok := c.ZKWitnessFor(coin.Leaf)
	if !ok {
		t.Fatal("minted leaf not found")
	}

	// CONFIDENTIAL SPEND to Bob — amount hidden, only fee public.
	czkTx, err := alice.CreateCZKSpend(coin, anchor, path, c.ZKDepth(), bob.Address(), fee)
	if err != nil {
		t.Fatalf("create confidential spend: %v", err)
	}
	// the tx must carry NO public amount for the confidential leg.
	if len(czkTx.CZKSpends) != 1 || czkTx.CZKSpends[0].Fee != fee {
		t.Fatal("malformed confidential spend tx")
	}
	cb, _ = alice.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(czkTx.Fee, nil), nil)
	mineBlock(t, c, cb, []*tx.Transaction{czkTx})

	// Bob scans, recovers the HIDDEN amount, and must hold (mintAmount − fee).
	var bobCoin *wallet.ZKCoin
	for h := uint64(0); h <= c.Height(); h++ {
		b, _ := c.BlockByHeight(h)
		for _, zc := range bob.ScanZKCoins(b) {
			bobCoin = zc
		}
	}
	if bobCoin == nil {
		t.Fatal("bob did not recover the confidential coin")
	}
	if bobCoin.Amount != mintAmount-fee {
		t.Fatalf("bob recovered amount %d, want %d", bobCoin.Amount, mintAmount-fee)
	}
	t.Logf("bob recovered HIDDEN amount %s OBX", config.FormatAmount(bobCoin.Amount))

	// Bob re-spends his confidential coin to Carol (proves the recovered coin is usable).
	banchor, bpath, ok := c.ZKWitnessFor(bobCoin.Leaf)
	if !ok {
		t.Fatal("bob's coin leaf not found")
	}
	bobTx, err := bob.CreateCZKSpend(bobCoin, banchor, bpath, c.ZKDepth(), carol.Address(), fee)
	if err != nil {
		t.Fatalf("bob confidential spend: %v", err)
	}
	cb, _ = alice.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(bobTx.Fee, nil), nil)
	mineBlock(t, c, cb, []*tx.Transaction{bobTx})
	for h := uint64(0); h <= c.Height(); h++ {
		b, _ := c.BlockByHeight(h)
		for _, zc := range carol.ScanZKCoins(b) {
			if zc.Amount != bobCoin.Amount-fee {
				t.Fatalf("carol amount %d, want %d", zc.Amount, bobCoin.Amount-fee)
			}
		}
	}

	// ADVERSARIAL 1: double-spend Alice's already-spent coin → rejected (serial reused).
	dsTx, err := alice.CreateCZKSpend(coin, anchor, path, c.ZKDepth(), bob.Address(), fee)
	if err != nil {
		t.Fatalf("create double-spend: %v", err)
	}
	cb, _ = alice.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(dsTx.Fee, nil), nil)
	if err := mineMayFail(t, c, cb, []*tx.Transaction{dsTx}); err == nil {
		t.Fatal("confidential double-spend accepted")
	}
}

// TestConfidentialFeeRangeAndTamper: an out-of-range fee (FINDING 5 guard) and a
// tampered proof must both be rejected by consensus.
func TestConfidentialFeeRangeAndTamper(t *testing.T) {
	old := config.CoinbaseMaturity
	config.CoinbaseMaturity = 1
	defer func() { config.CoinbaseMaturity = old }()

	c, err := chain.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	alice := wallet.FromSeed([]byte("alice-czkadv-seed-0000000000000000"))
	bob := wallet.FromSeed([]byte("bob-czkadv-seed-00000000000000000000"))
	for i := 0; i < 4; i++ {
		minted := c.ExpectedCoinbaseMinted(0, nil)
		cb, _ := alice.BuildCoinbase(c.Height()+1, minted, nil)
		mineBlock(t, c, cb, nil)
	}
	for h := uint64(0); h <= c.Height(); h++ {
		b, _ := c.BlockByHeight(h)
		alice.ScanBlock(b)
	}
	fee := uint64(5_000_000_000)
	mintTx, coin, err := alice.CreateZKMint(c, alice.Balance()/4, fee)
	if err != nil {
		t.Fatal(err)
	}
	cb, _ := alice.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(mintTx.Fee, nil), nil)
	mineBlock(t, c, cb, []*tx.Transaction{mintTx})
	anchor, path, _ := c.ZKWitnessFor(coin.Leaf)

	// FINDING 5: force the public fee out of [0, 2^ConfidentialBits). The range guard
	// must reject it (a wrapped/oversized fee would otherwise inflate).
	feeTx, err := alice.CreateCZKSpend(coin, anchor, path, c.ZKDepth(), bob.Address(), fee)
	if err != nil {
		t.Fatal(err)
	}
	feeTx.CZKSpends[0].Fee = uint64(1) << config.ConfidentialBits // out of range
	cb, _ = alice.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(feeTx.Fee, nil), nil)
	if err := mineMayFail(t, c, cb, []*tx.Transaction{feeTx}); err == nil {
		t.Fatal("out-of-range confidential fee accepted")
	}

	// Tampered proof → rejected.
	tamperTx, err := alice.CreateCZKSpend(coin, anchor, path, c.ZKDepth(), bob.Address(), fee)
	if err != nil {
		t.Fatal(err)
	}
	if len(tamperTx.CZKSpends[0].Proof) > 0 {
		tamperTx.CZKSpends[0].Proof[len(tamperTx.CZKSpends[0].Proof)/2] ^= 0xFF
	}
	cb, _ = alice.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(tamperTx.Fee, nil), nil)
	if err := mineMayFail(t, c, cb, []*tx.Transaction{tamperTx}); err == nil {
		t.Fatal("tampered confidential proof accepted")
	}
}
