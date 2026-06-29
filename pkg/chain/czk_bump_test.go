package chain_test

import (
	"bytes"
	"testing"

	"obscura/pkg/chain"
	"obscura/pkg/config"
	"obscura/pkg/mempool"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
)

// TestBumpZKSpendRBF proves replace-by-fee works for CONFIDENTIAL and ANONYMOUS ZK
// spends — the gap that previously left only transparent-input txs bumpable. The key
// invariant: the nullifier nf = H(nsk, rho) depends only on the spent coin, so a
// rebuild at a higher fee carries the IDENTICAL nullifier and therefore REPLACES the
// stuck tx in the mempool rather than creating a second, independent spend.
func TestBumpZKSpendRBF(t *testing.T) {
	old := config.CoinbaseMaturity
	config.CoinbaseMaturity = 1
	defer func() { config.CoinbaseMaturity = old }()

	c, err := chain.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	alice := wallet.FromSeed([]byte("alice-bump-seed-0000000000000000"))
	bob := wallet.FromSeed([]byte("bob-bump-seed-00000000000000000000"))

	for i := 0; i < 4; i++ {
		cb, err := alice.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(0, nil), nil)
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

	// Mint a ZK coin to Alice, mine it, and scan so the coin lands in her zkCoins
	// (exercises the by-nullifier resolution the CLI bump relies on).
	mintFee := uint64(5_000_000_000)
	mintAmount := alice.Balance() / 4
	mintTx, _, err := alice.CreateZKMint(c, mintAmount, mintFee)
	if err != nil {
		t.Fatalf("create mint: %v", err)
	}
	cb, _ := alice.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(mintTx.Fee, nil), nil)
	mineBlock(t, c, cb, []*tx.Transaction{mintTx})
	for h := uint64(0); h <= c.Height(); h++ {
		b, _ := c.BlockByHeight(h)
		alice.ScanBlock(b)
	}
	coins := alice.ZKCoins()
	if len(coins) == 0 {
		t.Fatal("alice did not record her minted ZK coin")
	}
	coin := coins[0]

	anchor, path, ok := c.ZKWitnessFor(coin.Leaf)
	if !ok {
		t.Fatal("minted leaf not found")
	}
	depth := c.ZKDepth()

	// Original (stuck) CONFIDENTIAL spend at a low fee.
	lowFee := uint64(5_000_000_000)
	prev, err := alice.CreateCZKSpend(coin, anchor, path, depth, bob.Address(), lowFee)
	if err != nil {
		t.Fatalf("create confidential spend: %v", err)
	}

	// A real mempool accepts it.
	mp := mempool.New(c)
	if err := mp.Add(prev); err != nil {
		t.Fatalf("mempool rejected original: %v", err)
	}
	if mp.Size() != 1 {
		t.Fatalf("mempool size = %d, want 1 after original", mp.Size())
	}

	// Bump to a higher fee. nullifier must be unchanged (the RBF conflict key).
	highFee := uint64(40_000_000_000)
	bump, err := alice.BumpZKSpend(prev, coin, anchor, path, depth, bob.Address(), highFee)
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	if !bytes.Equal(bump.CZKSpends[0].Nullifier, prev.CZKSpends[0].Nullifier) {
		t.Fatal("bumped spend has a DIFFERENT nullifier — would be a second spend, not a replacement")
	}
	if bump.Fee != highFee || bump.CZKSpends[0].Fee != highFee {
		t.Fatalf("bump fee = (tx %d, czk %d), want %d", bump.Fee, bump.CZKSpends[0].Fee, highFee)
	}
	// The two are distinct transactions (fresh output note) sharing only the nullifier.
	if bump.HashHex() == prev.HashHex() {
		t.Fatal("bump produced an identical tx (expected a fresh output)")
	}

	// The bump must REPLACE the original in the mempool, not be rejected or duplicated.
	if err := mp.Add(bump); err != nil {
		t.Fatalf("mempool rejected the bump (RBF should accept): %v", err)
	}
	if mp.Size() != 1 {
		t.Fatalf("mempool size = %d, want 1 after RBF replacement", mp.Size())
	}
	sel := mp.Select(10)
	if len(sel) != 1 || sel[0].HashHex() != bump.HashHex() {
		t.Fatalf("mempool did not hold the bump after replacement: got %d tx(s)", len(sel))
	}
	if sel[0].HashHex() == prev.HashHex() {
		t.Fatal("original still present after bump")
	}

	// The bumped tx must be independently valid (real proof over the new fee/amount).
	if err := c.ValidateStandaloneTx(bump); err != nil {
		t.Fatalf("bumped confidential spend failed standalone validation: %v", err)
	}

	// By-nullifier coin resolution (used by the CLI bump) finds the right coin.
	if got := alice.ZKCoinForNullifier(prev.CZKSpends[0].Nullifier); got == nil {
		t.Fatal("ZKCoinForNullifier did not resolve the spent coin")
	}

	// Guard rails: fee must increase, and must clear the RBF floor.
	if _, err := alice.BumpZKSpend(prev, coin, anchor, path, depth, bob.Address(), lowFee); err == nil {
		t.Fatal("bump accepted a non-increasing fee")
	}
	if _, err := alice.BumpZKSpend(prev, coin, anchor, path, depth, bob.Address(), lowFee+1); err == nil {
		t.Fatal("bump accepted a fee below the RBF floor")
	}
}

// TestBumpZKSpendPublicLeg covers the public-amount anonymous spend (CreateZKSpend):
// the bump must likewise preserve the nullifier and remain valid.
func TestBumpZKSpendPublicLeg(t *testing.T) {
	old := config.CoinbaseMaturity
	config.CoinbaseMaturity = 1
	defer func() { config.CoinbaseMaturity = old }()

	c, err := chain.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	alice := wallet.FromSeed([]byte("alice-bump2-seed-000000000000000"))
	bob := wallet.FromSeed([]byte("bob-bump2-seed-0000000000000000000"))

	for i := 0; i < 4; i++ {
		cb, _ := alice.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(0, nil), nil)
		mineBlock(t, c, cb, nil)
	}
	for h := uint64(0); h <= c.Height(); h++ {
		b, _ := c.BlockByHeight(h)
		alice.ScanBlock(b)
	}

	mintFee := uint64(5_000_000_000)
	mintAmount := alice.Balance() / 4
	mintTx, coin, err := alice.CreateZKMint(c, mintAmount, mintFee)
	if err != nil {
		t.Fatalf("create mint: %v", err)
	}
	cb, _ := alice.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(mintTx.Fee, nil), nil)
	mineBlock(t, c, cb, []*tx.Transaction{mintTx})

	anchor, path, ok := c.ZKWitnessFor(coin.Leaf)
	if !ok {
		t.Fatal("minted leaf not found")
	}
	depth := c.ZKDepth()

	lowFee := uint64(5_000_000_000)
	prev, err := alice.CreateZKSpend(coin, anchor, path, depth, bob.Address(), lowFee)
	if err != nil {
		t.Fatalf("create anonymous spend: %v", err)
	}

	highFee := uint64(40_000_000_000)
	bump, err := alice.BumpZKSpend(prev, coin, anchor, path, depth, bob.Address(), highFee)
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	if !bytes.Equal(bump.ZKInputs[0].Nullifier, prev.ZKInputs[0].Nullifier) {
		t.Fatal("bumped anonymous spend changed the nullifier")
	}
	if bump.Fee != highFee {
		t.Fatalf("bump fee = %d, want %d", bump.Fee, highFee)
	}
	if err := c.ValidateStandaloneTx(bump); err != nil {
		t.Fatalf("bumped anonymous spend failed standalone validation: %v", err)
	}
}
