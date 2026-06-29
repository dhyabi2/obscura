// Package mining tests the external-miner RPC (Block 37): fetch a block template,
// grind the PoW, submit it, and confirm the chain advanced.
package mining

import (
	"context"
	"encoding/hex"
	"net/http/httptest"
	"testing"

	"obscura/pkg/mempool"
	"obscura/pkg/miner"
	"obscura/pkg/rpc"
	"obscura/tests/critical/harness"
)

func TestExternalMineViaRPC(t *testing.T) {
	c := harness.NewChain(t)
	mp := mempool.New(c)
	srv := rpc.NewServer(c, mp, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	cl, err := rpc.NewClient(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	minerWallet := harness.NewWallet("ext-miner")
	addr := hex.EncodeToString(minerWallet.AddressBytes())

	startH := c.Height()
	for i := 0; i < 3; i++ {
		tmpl, seed, diff, err := cl.BlockTemplate(addr)
		if err != nil {
			t.Fatalf("template: %v", err)
		}
		if tmpl.Header.Height != c.Height()+1 {
			t.Fatalf("template height %d, want %d", tmpl.Header.Height, c.Height()+1)
		}
		if diff == 0 {
			t.Fatal("template difficulty is zero")
		}
		if !miner.MineSeed(context.Background(), tmpl, seed, 0) {
			t.Fatal("mining failed")
		}
		h, err := cl.SubmitBlock(tmpl)
		if err != nil {
			t.Fatalf("submit: %v", err)
		}
		if h != startH+uint64(i)+1 {
			t.Fatalf("submitted height %d, want %d", h, startH+uint64(i)+1)
		}
	}
	if c.Height() != startH+3 {
		t.Fatalf("chain height %d, want %d", c.Height(), startH+3)
	}

	// the externally-mined coinbase must be spendable by the miner wallet after
	// maturity — confirms the coinbase really paid the supplied address.
	defer harness.SmallMaturity()()
	harness.MineN(t, c, minerWallet, 2) // age past maturity
	harness.ScanAll(c, minerWallet)
	if minerWallet.Balance() == 0 {
		t.Fatal("miner wallet received nothing from external mining")
	}
}

func TestSubmitBadBlockRejected(t *testing.T) {
	c := harness.NewChain(t)
	srv := rpc.NewServer(c, mempool.New(c), nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	cl, err := rpc.NewClient(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	miner := harness.NewWallet("ext-miner2")
	tmpl, _, _, err := cl.BlockTemplate(hex.EncodeToString(miner.AddressBytes()))
	if err != nil {
		t.Fatal(err)
	}
	// submit WITHOUT mining (PoW not satisfied) → must be rejected
	if _, err := cl.SubmitBlock(tmpl); err == nil {
		t.Fatal("node accepted a block with insufficient proof-of-work")
	}
}
