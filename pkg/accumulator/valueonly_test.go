package accumulator

import (
	"math/big"
	"testing"

	"obscura/pkg/group"
)

// A value-only accumulator must produce the SAME value and size as a member-
// retaining one for every prefix — otherwise switching the chain to value-only
// would change the header-committed AccValue/AccSize (a consensus break).
func TestValueOnlyMatchesMemberMode(t *testing.T) {
	G := group.NewRSA2048Group()
	mem := New(G)
	vo := NewValueOnly(G)
	// distinct primes only (the chain guarantees no duplicate output-prime ever
	// reaches Add — uniqueness is enforced in validation).
	p := big.NewInt(3)
	for i := 0; i < 200; i++ {
		_ = mem.Add(p)
		_ = vo.Add(p)
		if !G.Equal(mem.Value(), vo.Value()) {
			t.Fatalf("value mismatch at i=%d", i)
		}
		if mem.Size() != vo.Size() {
			t.Fatalf("size mismatch at i=%d: %d vs %d", i, mem.Size(), vo.Size())
		}
		p = nextPrimeForTest(new(big.Int).Add(p, big.NewInt(1)))
	}
	// value-only cannot witness / contain
	if _, err := vo.MembershipWitness(big.NewInt(5)); err == nil {
		t.Fatal("value-only MembershipWitness should error")
	}
	if vo.Contains(big.NewInt(5)) {
		t.Fatal("value-only Contains should be false")
	}
	// snapshot round-trip preserves value + size
	data := vo.MarshalState()
	r, err := RestoreState(G, data)
	if err != nil {
		t.Fatal(err)
	}
	if !G.Equal(r.Value(), vo.Value()) || r.Size() != vo.Size() {
		t.Fatal("value-only snapshot round-trip mismatch")
	}
}

func nextPrimeForTest(n *big.Int) *big.Int {
	p := new(big.Int).Set(n)
	if p.Bit(0) == 0 {
		p.Add(p, big.NewInt(1))
	}
	for !p.ProbablyPrime(20) {
		p.Add(p, big.NewInt(2))
	}
	return p
}
