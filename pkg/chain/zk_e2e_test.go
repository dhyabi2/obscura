package chain_test

import (
	"context"
	"testing"

	"obscura/pkg/chain"
	"obscura/pkg/config"
	"obscura/pkg/miner"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
)

// mineMayFail builds+mines a block and returns AddBlock's error (for adversarial
// cases that must be rejected).
func mineMayFail(t *testing.T, c *chain.Chain, cb *tx.Transaction, txs []*tx.Transaction) error {
	t.Helper()
	all := append([]*tx.Transaction{cb}, txs...)
	tmpl, err := c.BlockTemplate(all)
	if err != nil {
		return err
	}
	if !miner.Mine(context.Background(), tmpl, 0) {
		t.Fatal("mining failed")
	}
	return c.AddBlock(tmpl)
}

// TestZKMintAndSpendEndToEnd drives the full anonymous-spend lifecycle through real
// blocks: mine → mint a ZK coin → spend it anonymously → recipient receives. Then
// it checks the two soundness guards (double-spend, inflation).
func TestZKMintAndSpendEndToEnd(t *testing.T) {
	old := config.CoinbaseMaturity
	config.CoinbaseMaturity = 1
	defer func() { config.CoinbaseMaturity = old }()

	c, err := chain.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	alice := wallet.FromSeed([]byte("alice-zk-seed-000000000000000000"))
	bob := wallet.FromSeed([]byte("bob-zk-seed-0000000000000000000000"))

	// Alice mines to get spendable balance.
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

	// --- MINT: shield value into a ZK coin ---
	fee := uint64(5_000_000_000)
	mintAmount := alice.Balance() / 4
	mintTx, coin, err := alice.CreateZKMint(c, mintAmount, fee)
	if err != nil {
		t.Fatalf("create mint: %v", err)
	}
	minted := c.ExpectedCoinbaseMinted(mintTx.Fee, nil)
	cb, err := alice.BuildCoinbase(c.Height()+1, minted, nil)
	if err != nil {
		t.Fatal(err)
	}
	mineBlock(t, c, cb, []*tx.Transaction{mintTx})
	t.Logf("minted ZK coin worth %s OBX", config.FormatAmount(mintAmount))

	// locate the coin in the commitment tree + take an anchor.
	anchor, path, ok := c.ZKWitnessFor(coin.Leaf)
	if !ok {
		t.Fatal("minted leaf not found in commitment tree")
	}

	// --- SPEND: anonymously to Bob ---
	spendTx, err := alice.CreateZKSpend(coin, anchor, path, c.ZKDepth(), bob.Address(), fee)
	if err != nil {
		t.Fatalf("create zk spend: %v", err)
	}
	minted = c.ExpectedCoinbaseMinted(spendTx.Fee, nil)
	cb, err = alice.BuildCoinbase(c.Height()+1, minted, nil)
	if err != nil {
		t.Fatal(err)
	}
	mineBlock(t, c, cb, []*tx.Transaction{spendTx})

	// Bob scans and should now hold (mintAmount − fee).
	for h := uint64(0); h <= c.Height(); h++ {
		b, _ := c.BlockByHeight(h)
		bob.ScanBlock(b)
	}
	if bob.Balance() != mintAmount-fee {
		t.Fatalf("bob balance = %d, want %d", bob.Balance(), mintAmount-fee)
	}
	t.Logf("bob received %s OBX via anonymous spend", config.FormatAmount(bob.Balance()))

	// --- ADVERSARIAL: double-spend the same coin (reuse serial) must be rejected ---
	dsTx, err := alice.CreateZKSpend(coin, anchor, path, c.ZKDepth(), bob.Address(), fee)
	if err != nil {
		t.Fatalf("create double-spend: %v", err)
	}
	minted = c.ExpectedCoinbaseMinted(dsTx.Fee, nil)
	cb, _ = alice.BuildCoinbase(c.Height()+1, minted, nil)
	if err := mineMayFail(t, c, cb, []*tx.Transaction{dsTx}); err == nil {
		t.Fatal("double-spend (reused nullifier) was accepted")
	}
}

// TestZKMintInflationRejected: a mint whose leaf commits more than the declared
// amount cannot be formed/accepted (the mint proof binds value to the leaf).
func TestZKMintInflationRejected(t *testing.T) {
	old := config.CoinbaseMaturity
	config.CoinbaseMaturity = 1
	defer func() { config.CoinbaseMaturity = old }()

	c, err := chain.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	alice := wallet.FromSeed([]byte("alice-inf-seed-00000000000000000"))
	for i := 0; i < 3; i++ {
		minted := c.ExpectedCoinbaseMinted(0, nil)
		cb, _ := alice.BuildCoinbase(c.Height()+1, minted, nil)
		mineBlock(t, c, cb, nil)
	}
	for h := uint64(0); h <= c.Height(); h++ {
		b, _ := c.BlockByHeight(h)
		alice.ScanBlock(b)
	}
	fee := uint64(5_000_000_000)
	mintTx, _, err := alice.CreateZKMint(c, alice.Balance()/4, fee)
	if err != nil {
		t.Fatal(err)
	}
	// Forge: inflate the declared amount without re-proving (mint proof now binds the
	// ORIGINAL amount, so verification must fail).
	mintTx.ZKOutputs[0].Amount += 1_000_000_000_000
	minted := c.ExpectedCoinbaseMinted(mintTx.Fee, nil)
	cb, _ := alice.BuildCoinbase(c.Height()+1, minted, nil)
	if err := mineMayFail(t, c, cb, []*tx.Transaction{mintTx}); err == nil {
		t.Fatal("inflated mint was accepted")
	}
}

// TestZKSpendUnknownAnchorAndTamper: a spend against a non-whitelisted anchor, and
// a spend with a tampered proof, must both be rejected by consensus.
func TestZKSpendUnknownAnchorAndTamper(t *testing.T) {
	old := config.CoinbaseMaturity
	config.CoinbaseMaturity = 1
	defer func() { config.CoinbaseMaturity = old }()

	c, err := chain.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	alice := wallet.FromSeed([]byte("alice-adv-seed-00000000000000000"))
	bob := wallet.FromSeed([]byte("bob-adv-seed-000000000000000000000"))
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
	minted := c.ExpectedCoinbaseMinted(mintTx.Fee, nil)
	cb, _ := alice.BuildCoinbase(c.Height()+1, minted, nil)
	mineBlock(t, c, cb, []*tx.Transaction{mintTx})

	anchor, path, _ := c.ZKWitnessFor(coin.Leaf)

	// (1) tampered proof bytes.
	badProofTx, _ := alice.CreateZKSpend(coin, anchor, path, c.ZKDepth(), bob.Address(), fee)
	if len(badProofTx.ZKInputs[0].Proof) > 100 {
		badProofTx.ZKInputs[0].Proof[50] ^= 0xFF
	}
	cb, _ = alice.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(fee, nil), nil)
	if err := mineMayFail(t, c, cb, []*tx.Transaction{badProofTx}); err == nil {
		t.Fatal("tampered zk proof accepted")
	}

	// (2) unknown anchor: a valid spend whose Anchor field is swapped to a
	// non-whitelisted root must be rejected by the consensus anchor check.
	badAnchorTx, err := alice.CreateZKSpend(coin, anchor, path, c.ZKDepth(), bob.Address(), fee)
	if err != nil {
		t.Fatal(err)
	}
	badAnchorTx.ZKInputs[0].Anchor = make([]byte, 8)
	for i := range badAnchorTx.ZKInputs[0].Anchor {
		badAnchorTx.ZKInputs[0].Anchor[i] = byte(i + 1)
	}
	cb, _ = alice.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(fee, nil), nil)
	if err := mineMayFail(t, c, cb, []*tx.Transaction{badAnchorTx}); err == nil {
		t.Fatal("spend against unknown anchor accepted")
	}

	// also confirm that a spender CANNOT even produce a proof for a fake anchor.
	if _, err := alice.CreateZKSpend(coin, []byte{1, 2, 3, 4, 5, 6, 7, 8}, path, c.ZKDepth(), bob.Address(), fee); err == nil {
		t.Fatal("expected proof-build failure for a fabricated anchor")
	}
}

// TestZKToZKTransfer: Alice mints a ZK coin payable to Bob (stealth), Bob discovers
// it by scanning, then Bob spends it anonymously to Carol.
func TestZKToZKTransfer(t *testing.T) {
	old := config.CoinbaseMaturity
	config.CoinbaseMaturity = 1
	defer func() { config.CoinbaseMaturity = old }()

	c, err := chain.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	alice := wallet.FromSeed([]byte("alice-z2z-seed-00000000000000000"))
	bob := wallet.FromSeed([]byte("bob-z2z-seed-000000000000000000000"))
	carol := wallet.FromSeed([]byte("carol-z2z-seed-00000000000000000"))

	for i := 0; i < 4; i++ {
		minted := c.ExpectedCoinbaseMinted(0, nil)
		cb, _ := alice.BuildCoinbase(c.Height()+1, minted, nil)
		mineBlock(t, c, cb, nil)
	}
	for h := uint64(0); h <= c.Height(); h++ {
		b, _ := c.BlockByHeight(h)
		alice.ScanBlock(b)
	}

	// Alice mints a ZK coin payable to BOB (private transfer).
	fee := uint64(5_000_000_000)
	amount := alice.Balance() / 4
	mintTx, _, err := alice.CreateZKMintTo(c, bob.Address(), amount, fee)
	if err != nil {
		t.Fatal(err)
	}
	minted := c.ExpectedCoinbaseMinted(mintTx.Fee, nil)
	cb, _ := alice.BuildCoinbase(c.Height()+1, minted, nil)
	mineBlock(t, c, cb, []*tx.Transaction{mintTx})

	// Bob discovers the coin by scanning the mint block.
	mintBlock, _ := c.BlockByHeight(c.Height())
	coins := bob.ScanZKCoins(mintBlock)
	if len(coins) != 1 {
		t.Fatalf("bob discovered %d ZK coins, want 1", len(coins))
	}
	bobCoin := coins[0]
	if bobCoin.Amount != amount {
		t.Fatalf("discovered amount %d, want %d", bobCoin.Amount, amount)
	}
	// Alice (not the recipient) must NOT discover it as hers... actually Alice is the
	// sender and CAN reconstruct it (known caveat); Carol (unrelated) must not.
	if got := carol.ScanZKCoins(mintBlock); len(got) != 0 {
		t.Fatal("unrelated party discovered the ZK coin")
	}

	// Bob spends his discovered coin anonymously to Carol.
	bAnchor, bPath, ok := c.ZKWitnessFor(bobCoin.Leaf)
	if !ok {
		t.Fatal("bob coin not in tree")
	}
	spendTx, err := bob.CreateZKSpend(bobCoin, bAnchor, bPath, c.ZKDepth(), carol.Address(), fee)
	if err != nil {
		t.Fatal(err)
	}
	minted = c.ExpectedCoinbaseMinted(spendTx.Fee, nil)
	cb, _ = alice.BuildCoinbase(c.Height()+1, minted, nil)
	mineBlock(t, c, cb, []*tx.Transaction{spendTx})

	for h := uint64(0); h <= c.Height(); h++ {
		b, _ := c.BlockByHeight(h)
		carol.ScanBlock(b)
	}
	if carol.Balance() != amount-fee {
		t.Fatalf("carol balance = %d, want %d", carol.Balance(), amount-fee)
	}
	t.Logf("ZK→ZK: alice→(shielded)→bob→(anon spend)→carol received %s OBX", config.FormatAmount(carol.Balance()))
}
