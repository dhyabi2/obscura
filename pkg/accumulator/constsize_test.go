package accumulator

import (
	"testing"

	"obscura/pkg/group"
)

// TestConstantProofSize demonstrates Obscura's core advantage over ring
// signatures: the size of a spend's membership proof is CONSTANT regardless of
// how large the anonymity set (UTXO set) is. With ring signatures, proof size
// grows with the ring; here it does not.
func TestConstantProofSize(t *testing.T) {
	p, _ := newRSATest()
	G := p

	sizes := []int{8, 64, 512}
	var proofSizes []int
	for _, n := range sizes {
		acc := New(G)
		var primes []interface{ Bytes() []byte }
		_ = primes
		var first []byte
		for i := 0; i < n; i++ {
			pr, _ := HashToPrime([]byte{byte(i), byte(i >> 8), 0xAB})
			acc.Add(pr)
			if i == 0 {
				first = pr.Bytes()
			}
		}
		// build a witness-hiding membership proof for the first element
		prime0, _ := HashToPrime([]byte{0, 0, 0xAB})
		_ = first
		w, err := acc.MembershipWitness(prime0)
		if err != nil {
			t.Fatal(err)
		}
		m := ProveZKMembership(G, acc.Value(), prime0, w)
		size := len(m.Serialize(G))
		proofSizes = append(proofSizes, size)
		t.Logf("anonymity set = %5d outputs -> membership proof = %d bytes", n, size)
	}
	// all proof sizes must be identical
	for i := 1; i < len(proofSizes); i++ {
		if proofSizes[i] != proofSizes[0] {
			t.Fatalf("proof size not constant: %v", proofSizes)
		}
	}
	t.Logf("CONSTANT proof size across all anonymity-set sizes: %d bytes", proofSizes[0])
}

func newRSATest() (*group.RSAGroup, error) {
	return group.NewRSA2048Group(), nil
}
