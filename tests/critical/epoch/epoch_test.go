// Package epoch tests RandomX-style PoW epoch seed rotation: the cache seed
// changes at epoch boundaries (derived from a lagged past block), the seed
// genuinely changes the PoW hash, and a chain mined across a boundary validates
// end-to-end.
package epoch

import (
	"bytes"
	"testing"

	"obscura/pkg/chain"
	"obscura/pkg/config"
	"obscura/pkg/pow"
	"obscura/tests/critical/harness"
)

// withSmallEpochs shrinks the epoch params (and reorg depth, to keep the
// lag >= reorg-depth invariant) so a short test chain crosses a boundary.
func withSmallEpochs(t *testing.T) {
	oe, ol, om := config.PoWEpochLen, config.PoWSeedLag, chain.MaxReorgDepth
	config.PoWEpochLen = 4
	config.PoWSeedLag = 4
	chain.MaxReorgDepth = 2
	t.Cleanup(func() {
		config.PoWEpochLen, config.PoWSeedLag, chain.MaxReorgDepth = oe, ol, om
	})
}

func TestSeedRotatesAcrossEpochs(t *testing.T) {
	withSmallEpochs(t)
	c := harness.NewChain(t)
	w := harness.NewWallet("epoch-miner")

	// mine across the first epoch boundary (heights 1..10; epoch 1 starts at h=8
	// with PoWSeedLag=PoWEpochLen=4). harness mines under c.PoWSeed, consensus
	// validates under the same derivation — if they disagreed, this would fail.
	harness.MineN(t, c, w, 10)
	if c.Height() != 10 {
		t.Fatalf("height %d, want 10 (rotation broke mining/validation)", c.Height())
	}

	epoch0 := c.PoWSeed(3) // PoWSeedHeight(3)=0 → genesis constant
	epoch1 := c.PoWSeed(9) // PoWSeedHeight(9)=4 → id of block 4
	if !bytes.Equal(epoch0, config.PoWGenesisSeed) {
		t.Fatal("epoch 0 should use the genesis seed constant")
	}
	if bytes.Equal(epoch0, epoch1) {
		t.Fatal("seed did not rotate across the epoch boundary")
	}
	// epoch-1 seed must be exactly the id of the seed-height block (height 4)
	h4, _ := c.HeaderByHeight(4)
	id4 := h4.ID()
	if !bytes.Equal(epoch1, id4[:]) {
		t.Fatal("epoch-1 seed is not the id of the seed-height block")
	}
}

func TestSeedActuallyChangesPoW(t *testing.T) {
	withSmallEpochs(t)
	c := harness.NewChain(t)
	w := harness.NewWallet("epoch-miner2")
	harness.MineN(t, c, w, 10)

	h, ok := c.HeaderByHeight(9) // an epoch-1 block
	if !ok {
		t.Fatal("missing header 9")
	}
	correct := c.PoWSeed(9)
	wrong := config.PoWGenesisSeed
	if bytes.Equal(correct, wrong) {
		t.Fatal("test setup: epoch-1 seed equals epoch-0 seed")
	}
	// the SAME header hashes differently under the two seeds (rotation matters)
	if h.PoWHashSeed(correct) == h.PoWHashSeed(wrong) {
		t.Fatal("PoW hash is independent of the cache seed (rotation is a no-op)")
	}
	// the block was mined to satisfy its CORRECT (epoch-1) seed
	if !pow.Meets(h.PoWHashSeed(correct), h.Difficulty) {
		t.Fatal("validly-mined epoch-1 block fails PoW under its correct seed")
	}
}

func TestPoWSeedHeightFormula(t *testing.T) {
	withSmallEpochs(t) // lag=4, epoch=4
	cases := map[uint64]uint64{0: 0, 3: 0, 4: 0, 7: 0, 8: 4, 11: 4, 12: 8, 16: 12}
	for h, want := range cases {
		if got := config.PoWSeedHeight(h); got != want {
			t.Fatalf("PoWSeedHeight(%d)=%d, want %d", h, got, want)
		}
	}
}
