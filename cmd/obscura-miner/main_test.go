package main

import (
	"encoding/hex"
	"net/http/httptest"
	"testing"

	"obscura/pkg/mempool"
	"obscura/pkg/rpc"
	"obscura/tests/critical/harness"
)

func TestMineOnce(t *testing.T) {
	c := harness.NewChain(t)
	srv := rpc.NewServer(c, mempool.New(c), nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	cl, err := rpc.NewClient(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	addr := hex.EncodeToString(harness.NewWallet("miner-bin").AddressBytes())

	start := c.Height()
	for i := 0; i < 2; i++ {
		mined, h, err := mineOnce(cl, addr)
		if err != nil {
			t.Fatalf("mineOnce: %v", err)
		}
		if !mined {
			t.Fatal("mineOnce reported no block on a quiet chain")
		}
		if h != start+uint64(i)+1 {
			t.Fatalf("mined height %d, want %d", h, start+uint64(i)+1)
		}
	}
	if c.Height() != start+2 {
		t.Fatalf("chain height %d, want %d", c.Height(), start+2)
	}
}
