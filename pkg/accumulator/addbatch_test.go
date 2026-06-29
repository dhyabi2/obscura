package accumulator

import (
	"crypto/rand"
	"math/big"
	"testing"

	"obscura/pkg/group"
)

// TestAddBatchEqualsSequential pins the apply-path optimization: folding n primes via one
// AddBatch(product) yields the byte-identical accumulator value (and size) as adding them
// one by one. A divergence would fork the chain (AccValue is in the header).
func TestAddBatchEqualsSequential(t *testing.T) {
	D := group.DeriveDiscriminant([]byte("obscura-mainnet-v1"), 2048)
	G, err := group.NewClassGroup(D, "addbatch")
	if err != nil {
		t.Fatal(err)
	}
	seq := NewValueOnly(G)
	bat := NewValueOnly(G)
	prod := big.NewInt(1)
	const n = 25
	for i := 0; i < n; i++ {
		p, _ := rand.Prime(rand.Reader, 256)
		_ = seq.Add(new(big.Int).Set(p))
		prod.Mul(prod, p)
	}
	bat.AddBatch(prod, n)
	if !G.Equal(seq.Value(), bat.Value()) {
		t.Fatal("AddBatch(product) != sequential Add: accumulator values differ")
	}
	if seq.Size() != bat.Size() {
		t.Fatalf("size mismatch: seq=%d bat=%d", seq.Size(), bat.Size())
	}
}
