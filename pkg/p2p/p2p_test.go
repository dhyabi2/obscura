package p2p_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"obscura/pkg/chain"
	"obscura/pkg/config"
	"obscura/pkg/mempool"
	"obscura/pkg/miner"
	"obscura/pkg/p2p"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
)

func mineN(t *testing.T, c *chain.Chain, w *wallet.Wallet, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		minted := c.ExpectedCoinbaseMinted(0, nil)
		cb, err := w.BuildCoinbase(c.Height()+1, minted, nil)
		if err != nil {
			t.Fatalf("coinbase: %v", err)
		}
		tmpl, err := c.BlockTemplate([]*tx.Transaction{cb})
		if err != nil {
			t.Fatalf("template: %v", err)
		}
		if !miner.Mine(context.Background(), tmpl, 0) {
			t.Fatal("mine failed")
		}
		if err := c.AddBlock(tmpl); err != nil {
			t.Fatalf("addblock: %v", err)
		}
	}
}

// TestTwoNodeSync verifies the hardened P2P (magic+version handshake, framing,
// sync) lets a fresh node download a peer's chain — and guards against the
// non-deterministic-genesis regression (both nodes must agree on genesis).
func TestTwoNodeSync(t *testing.T) {
	chainA, err := chain.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer chainA.Close()
	chainB, err := chain.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer chainB.Close()

	// genesis must be identical across independently-created chains.
	gA, _ := chainA.HeaderByHeight(0)
	gB, _ := chainB.HeaderByHeight(0)
	if gA.ID() != gB.ID() {
		t.Fatalf("genesis not deterministic: %x != %x", gA.ID(), gB.ID())
	}

	miner := wallet.FromSeed([]byte("p2p-miner-seed-000000000000000000"))
	mineN(t, chainA, miner, 4)

	addrA := "127.0.0.1:19581"
	addrB := "127.0.0.1:19582"
	nodeA := p2p.NewNode(addrA, chainA, mempool.New(chainA), "")
	nodeB := p2p.NewNode(addrB, chainB, mempool.New(chainB), "")
	defer nodeA.Stop()
	defer nodeB.Stop()
	if err := nodeA.Start(nil); err != nil {
		t.Fatalf("start A: %v", err)
	}
	if err := nodeB.Start([]string{addrA}); err != nil {
		t.Fatalf("start B: %v", err)
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if chainB.Height() == chainA.Height() {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if chainB.Height() != chainA.Height() {
		t.Fatalf("B did not sync: A=%d B=%d", chainA.Height(), chainB.Height())
	}
	if chainB.Height() != 4 {
		t.Fatalf("unexpected synced height %d", chainB.Height())
	}
	_ = fmt.Sprint(config.Ticker)
}
