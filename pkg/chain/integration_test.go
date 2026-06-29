package chain_test

import (
	"context"
	"testing"

	"obscura/pkg/block"
	"obscura/pkg/chain"
	"obscura/pkg/commit"
	"obscura/pkg/config"
	"obscura/pkg/miner"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
)

// mineBlock builds a template with the given coinbase + txs, grinds PoW, and
// adds the block to the chain.
func mineBlock(t *testing.T, c *chain.Chain, cb *tx.Transaction, txs []*tx.Transaction) {
	t.Helper()
	all := append([]*tx.Transaction{cb}, txs...)
	tmpl, err := c.BlockTemplate(all)
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	if !miner.Mine(context.Background(), tmpl, 0) {
		t.Fatal("mining failed")
	}
	if err := c.AddBlock(tmpl); err != nil {
		t.Fatalf("add block height %d: %v", tmpl.Header.Height, err)
	}
}

// TestEndToEnd exercises the full flow: genesis -> mine rewards -> private
// transfer -> recipient receives, all proofs verified by consensus.
func TestEndToEnd(t *testing.T) {
	old := config.CoinbaseMaturity
	config.CoinbaseMaturity = 1
	defer func() { config.CoinbaseMaturity = old }()

	dir := t.TempDir()
	c, err := chain.New(dir)
	if err != nil {
		t.Fatalf("new chain: %v", err)
	}
	defer c.Close()

	alice := wallet.FromSeed([]byte("alice-seed-0000000000000000000000"))
	bob := wallet.FromSeed([]byte("bob-seed-00000000000000000000000000"))

	// Alice mines several blocks.
	const mined = 3
	for i := 0; i < mined; i++ {
		fees := uint64(0)
		minted := c.ExpectedCoinbaseMinted(fees, nil)
		cb, err := alice.BuildCoinbase(c.Height()+1, minted, nil)
		if err != nil {
			t.Fatalf("coinbase: %v", err)
		}
		mineBlock(t, c, cb, nil)
	}

	// Alice scans all blocks to discover her coinbase outputs.
	for h := uint64(0); h <= c.Height(); h++ {
		b, _ := c.BlockByHeight(h)
		alice.ScanBlock(b)
	}
	if alice.Balance() == 0 {
		t.Fatal("alice has no balance after mining")
	}
	t.Logf("alice balance after %d blocks: %s OBX", mined, config.FormatAmount(alice.Balance()))

	// Alice sends a private payment to Bob.
	sendAmount := alice.Balance() / 4
	fee := uint64(1_000_000_000) // 0.001 OBX, above the per-byte minimum
	spendTx, err := alice.CreateTransaction(c, bob.Address(), sendAmount, fee)
	if err != nil {
		t.Fatalf("create tx: %v", err)
	}

	// Validate the transaction in isolation (as the mempool/consensus would).
	if err := c.ValidateBlock(mustTemplateWithTx(t, c, alice, spendTx)); err != nil {
		t.Fatalf("block with spend failed validation: %v", err)
	}

	// Mine the spend into a block.
	minted := c.ExpectedCoinbaseMinted(spendTx.Fee, nil)
	cb, err := alice.BuildCoinbase(c.Height()+1, minted, nil)
	if err != nil {
		t.Fatalf("coinbase: %v", err)
	}
	mineBlock(t, c, cb, []*tx.Transaction{spendTx})

	// Bob scans and should see the payment.
	for h := uint64(0); h <= c.Height(); h++ {
		b, _ := c.BlockByHeight(h)
		bob.ScanBlock(b)
	}
	if bob.Balance() != sendAmount {
		t.Fatalf("bob balance = %d, want %d", bob.Balance(), sendAmount)
	}
	t.Logf("bob received: %s OBX", config.FormatAmount(bob.Balance()))

	// Double-spend attempt: re-submitting the same tx must fail (nullifier seen).
	dsTmpl := mustTemplateWithTx(t, c, alice, spendTx)
	if err := c.ValidateBlock(dsTmpl); err == nil {
		t.Fatal("double-spend was accepted")
	}
}

// mustTemplateWithTx builds (but does not add) a mined block containing tx, for
// validation testing.
func mustTemplateWithTx(t *testing.T, c *chain.Chain, minerW *wallet.Wallet, txn *tx.Transaction) *block.Block {
	t.Helper()
	minted := c.ExpectedCoinbaseMinted(txn.Fee, nil)
	cb, err := minerW.BuildCoinbase(c.Height()+1, minted, nil)
	if err != nil {
		t.Fatalf("coinbase: %v", err)
	}
	tmpl, err := c.BlockTemplate([]*tx.Transaction{cb, txn})
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	if !miner.Mine(context.Background(), tmpl, 0) {
		t.Fatal("mining failed")
	}
	return tmpl
}

func TestEmissionCurve(t *testing.T) {
	// reward decreases then hits the tail floor.
	r0 := config.BlockReward(0)
	rMid := config.BlockReward(config.MoneySupplyCap / 2)
	if rMid >= r0 {
		t.Fatal("reward should decrease with emission")
	}
	rTail := config.BlockReward(config.MoneySupplyCap)
	if rTail != config.TailEmissionAtomic {
		t.Fatalf("tail emission = %d, want %d", rTail, config.TailEmissionAtomic)
	}
	_ = commit.RandomScalar
}
