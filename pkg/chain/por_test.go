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

// TestPoRMineAndValidate: every block past genesis carries valid PoR (built from bodies,
// verified against headers); tampering it is rejected.
func TestPoRMineAndValidate(t *testing.T) {
	oldD, oldM := config.GenesisDifficulty, config.CoinbaseMaturity
	config.GenesisDifficulty, config.CoinbaseMaturity = 16, 1
	defer func() { config.GenesisDifficulty, config.CoinbaseMaturity = oldD, oldM }()
	c, err := chain.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	w := wallet.FromSeed([]byte("por-wallet-seed-00000000000000000000"))

	for i := 0; i < 8; i++ { // each mineBlock builds PoR (template) + validates it (AddBlock)
		cb, err := w.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(0, nil), nil)
		if err != nil {
			t.Fatal(err)
		}
		mineBlock(t, c, cb, nil)
	}
	top, _ := c.BlockByHeight(c.Height())
	if len(top.PoR) != config.PoRChallenges {
		t.Fatalf("block has %d PoR entries, want %d", len(top.PoR), config.PoRChallenges)
	}
	if top.Header.NumTxs != uint32(len(top.Txs)) {
		t.Fatalf("NumTxs %d != %d", top.Header.NumTxs, len(top.Txs))
	}

	// fresh template at the tip (carries PoR); mine it but DON'T add — tamper + revalidate.
	cb, _ := w.BuildCoinbase(c.Height()+1, c.ExpectedCoinbaseMinted(0, nil), nil)
	tmpl, err := c.BlockTemplate([]*tx.Transaction{cb})
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	if !miner.Mine(context.Background(), tmpl, 0) {
		t.Fatal("mine")
	}
	if err := c.ValidateBlock(tmpl); err != nil {
		t.Fatalf("honest PoR block rejected: %v", err)
	}
	// tamper a PoR entry → PoRRoot mismatch → rejected.
	if len(tmpl.PoR) > 0 {
		saved := append([]byte(nil), tmpl.PoR[0].TxBytes...)
		tmpl.PoR[0].TxBytes = append(tmpl.PoR[0].TxBytes, 0xFF)
		if err := c.ValidateBlock(tmpl); err == nil {
			t.Fatal("tampered PoR entry accepted")
		}
		tmpl.PoR[0].TxBytes = saved
	}
	// drop a PoR entry → count mismatch → rejected.
	cut := tmpl.PoR[:len(tmpl.PoR)-1]
	full := tmpl.PoR
	tmpl.PoR = cut
	if err := c.ValidateBlock(tmpl); err == nil {
		t.Fatal("missing PoR entry accepted")
	}
	tmpl.PoR = full
	// lie about NumTxs → rejected.
	tmpl.Header.NumTxs++
	if err := c.ValidateBlock(tmpl); err == nil {
		t.Fatal("wrong NumTxs accepted")
	}
	tmpl.Header.NumTxs--
	if err := c.ValidateBlock(tmpl); err != nil {
		t.Fatalf("restored block rejected: %v", err)
	}
}
