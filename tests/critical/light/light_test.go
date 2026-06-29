// Package light tests SPV light-client verification (Block 8): header-chain
// validation without full blocks, and Merkle transaction-inclusion proofs.
package light

import (
	"net/http/httptest"
	"testing"

	"obscura/pkg/block"
	"obscura/pkg/chain"
	"obscura/pkg/light"
	"obscura/pkg/rpc"
	"obscura/pkg/tx"
	"obscura/tests/critical/harness"
)

func headersOf(c *chain.Chain) []block.Header {
	var hs []block.Header
	for h := uint64(0); h <= c.Height(); h++ {
		hdr, ok := c.HeaderByHeight(h)
		if !ok {
			break
		}
		hs = append(hs, hdr)
	}
	return hs
}

// TestVerifyHeaderChain: a light client validates the header chain (PoW,
// linkage, difficulty, timestamps) and agrees with the full node's tip.
func TestVerifyHeaderChain(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	miner := harness.NewWallet("light-miner")
	harness.MineN(t, c, miner, 6)

	hs := headersOf(c)
	genesis := hs[0].ID()
	tip, height, work, err := light.VerifyHeaderChain(hs, genesis)
	if err != nil {
		t.Fatalf("verify header chain: %v", err)
	}
	wantTip, _ := c.HeaderByHeight(c.Height())
	if tip != wantTip.ID() || height != c.Height() {
		t.Fatalf("light tip/height mismatch: got %x/%d want %x/%d", tip, height, wantTip.ID(), c.Height())
	}
	if work.Sign() <= 0 {
		t.Fatal("cumulative work should be positive")
	}
}

// TestRejectsTamperedHeader: flipping a header's nonce breaks its PoW.
func TestRejectsTamperedHeader(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	harness.MineN(t, c, harness.NewWallet("lm2"), 5)
	hs := headersOf(c)
	genesis := hs[0].ID()
	hs[3].Nonce ^= 0xDEADBEEF // invalidate PoW of header 3
	if _, _, _, err := light.VerifyHeaderChain(hs, genesis); err == nil {
		t.Fatal("tampered header accepted")
	}
}

// TestRejectsWrongGenesis: a chain not rooted at the trusted genesis is rejected.
func TestRejectsWrongGenesis(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	harness.MineN(t, c, harness.NewWallet("lm3"), 3)
	hs := headersOf(c)
	var wrong [32]byte
	wrong[0] = 0xAA
	if _, _, _, err := light.VerifyHeaderChain(hs, wrong); err == nil {
		t.Fatal("wrong genesis accepted")
	}
}

// TestMerkleInclusion: a light client confirms a tx is in a block via a Merkle
// branch against the verified header's root, and rejects non-membership.
func TestMerkleInclusion(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("light-alice")
	harness.Funded(t, c, alice, 3)
	bob := harness.NewWallet("light-bob")

	spend, err := alice.CreateTransaction(c, bob.Address(), alice.Balance()/4, 1_000_000_000)
	if err != nil {
		t.Fatalf("create tx: %v", err)
	}
	b := harness.MineBlock(t, c, alice, []*tx.Transaction{spend})
	if len(b.Txs) < 2 {
		t.Fatalf("expected coinbase + spend, got %d txs", len(b.Txs))
	}

	hdr := b.Header
	for i, tr := range b.Txs {
		steps, ok := block.MerkleProof(b.Txs, i)
		if !ok {
			t.Fatalf("merkle proof gen failed for tx %d", i)
		}
		if !light.VerifyInclusion(hdr, tr.Hash(), steps) {
			t.Fatalf("inclusion proof failed for tx %d", i)
		}
		// a wrong txid must NOT verify with this branch
		var fake [32]byte
		fake[0] = 0x99
		if light.VerifyInclusion(hdr, fake, steps) {
			t.Fatalf("non-member verified for tx %d", i)
		}
	}
}

// TestRemoteLightSync: a light client fetches only HEADERS over RPC and verifies
// the chain independently (no full blocks), agreeing with the node's tip.
func TestRemoteLightSync(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	harness.MineN(t, c, harness.NewWallet("rls-miner"), 5)

	srv := httptest.NewServer(rpc.NewServer(c, nil, nil).Handler())
	defer srv.Close()
	cl, err := rpc.NewClient(srv.URL)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	hs, err := cl.Headers(0, 1000)
	if err != nil {
		t.Fatalf("fetch headers: %v", err)
	}
	if uint64(len(hs)) != c.Height()+1 {
		t.Fatalf("got %d headers, want %d", len(hs), c.Height()+1)
	}
	tip, height, _, err := light.VerifyHeaderChain(hs, hs[0].ID())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	want, _ := c.HeaderByHeight(c.Height())
	if tip != want.ID() || height != c.Height() {
		t.Fatal("remote light sync tip mismatch")
	}
}
