// Package harness provides shared helpers for the critical-workflow test suite
// under tests/critical/*. It is built on the public APIs of the Obscura
// packages so each test package can spin up chains, mine, fund wallets, and
// build transactions without duplicating boilerplate.
package harness

import (
	"context"
	"testing"

	"obscura/pkg/block"
	"obscura/pkg/chain"
	"obscura/pkg/config"
	"obscura/pkg/miner"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
)

// SmallMaturity sets coinbase maturity to 1 so tests can spend coinbase quickly.
// Returns a restore function; call it via t.Cleanup or defer.
func SmallMaturity() func() {
	old := config.CoinbaseMaturity
	config.CoinbaseMaturity = 1
	return func() { config.CoinbaseMaturity = old }
}

// NewChain creates a fresh persistent chain in a temp dir (auto-closed).
func NewChain(t *testing.T) *chain.Chain {
	t.Helper()
	c, err := chain.New(t.TempDir())
	if err != nil {
		t.Fatalf("new chain: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// NewWallet derives a deterministic wallet from a seed string.
func NewWallet(seed string) *wallet.Wallet {
	return wallet.FromSeed([]byte(seed + "-padpadpadpadpadpadpadpadpadpadpad"))
}

// MineBlock mines one block (coinbase to w plus the given txs) and applies it.
// Returns the mined block. Fails the test on any error.
func MineBlock(t *testing.T, c *chain.Chain, w *wallet.Wallet, txs []*tx.Transaction) *block.Block {
	t.Helper()
	fees := chain.CollectedFees(txs)
	minted := c.ExpectedCoinbaseMinted(fees, nil)
	cb, err := w.BuildCoinbase(c.Height()+1, minted, nil)
	if err != nil {
		t.Fatalf("coinbase: %v", err)
	}
	all := append([]*tx.Transaction{cb}, txs...)
	tmpl, err := c.BlockTemplate(all)
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	// mine under the per-epoch PoW seed (epoch 0 = constant, so short test chains
	// are unaffected; deep chains that cross an epoch boundary rotate correctly).
	if !miner.MineSeed(context.Background(), tmpl, c.PoWSeed(tmpl.Header.Height), 0) {
		t.Fatal("mine failed")
	}
	if err := c.AddBlock(tmpl); err != nil {
		t.Fatalf("addblock h=%d: %v", tmpl.Header.Height, err)
	}
	return tmpl
}

// MineEmptyBlock mines a block containing the given txs with the coinbase paid
// to a throwaway wallet (useful when you don't care about the reward).
func MineEmptyBlock(t *testing.T, c *chain.Chain, txs []*tx.Transaction) *block.Block {
	return MineBlock(t, c, NewWallet("coinbase-sink"), txs)
}

// MineN mines n coinbase-only blocks to w.
func MineN(t *testing.T, c *chain.Chain, w *wallet.Wallet, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		MineBlock(t, c, w, nil)
	}
}

// ScanAll scans the entire chain into the wallet.
func ScanAll(c *chain.Chain, w *wallet.Wallet) {
	for h := uint64(0); h <= c.Height(); h++ {
		if b, ok := c.BlockByHeight(h); ok {
			w.ScanBlock(b)
		}
	}
}

// Funded mines `blocks` coinbase blocks to w then scans, giving w a spendable
// (mature) balance. Requires SmallMaturity() to have been called.
func Funded(t *testing.T, c *chain.Chain, w *wallet.Wallet, blocks int) {
	t.Helper()
	MineN(t, c, w, blocks)
	ScanAll(c, w)
}

// BuildTemplate assembles (without mining) a block with coinbase to w + txs.
func BuildTemplate(t *testing.T, c *chain.Chain, w *wallet.Wallet, txs []*tx.Transaction) *block.Block {
	t.Helper()
	fees := chain.CollectedFees(txs)
	minted := c.ExpectedCoinbaseMinted(fees, nil)
	cb, err := w.BuildCoinbase(c.Height()+1, minted, nil)
	if err != nil {
		t.Fatalf("coinbase: %v", err)
	}
	tmpl, err := c.BlockTemplate(append([]*tx.Transaction{cb}, txs...))
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	return tmpl
}

// MineHeader grinds PoW for an already-assembled template.
func MineHeader(t *testing.T, b *block.Block) {
	t.Helper()
	if !miner.Mine(context.Background(), b, 0) {
		t.Fatal("mine failed")
	}
}
