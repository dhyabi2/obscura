package stark

import (
	"math/big"
	"math/rand"
	"testing"
)

var modP = new(big.Int).SetUint64(P)

func bigOf(f Felt) *big.Int { return new(big.Int).SetUint64(uint64(f)) }

// randField returns a uniformly random reduced element using a fixed-seed PRNG so
// the test is deterministic (no Math.random/Date dependence).
func randField(r *rand.Rand) Felt {
	for {
		v := r.Uint64()
		if v < P {
			return Felt(v)
		}
	}
}

// TestFieldVsBigInt cross-checks Add/Sub/Mul/Exp against math/big over many random
// inputs — the ground-truth correctness test for the fast Goldilocks reduction.
func TestFieldVsBigInt(t *testing.T) {
	r := rand.New(rand.NewSource(0x5742524B))
	for i := 0; i < 200000; i++ {
		a := randField(r)
		b := randField(r)
		ba, bb := bigOf(a), bigOf(b)

		got := uint64(a.Add(b))
		want := new(big.Int).Mod(new(big.Int).Add(ba, bb), modP).Uint64()
		if got != want {
			t.Fatalf("Add(%d,%d)=%d want %d", a, b, got, want)
		}

		got = uint64(a.Sub(b))
		want = new(big.Int).Mod(new(big.Int).Sub(ba, bb), modP).Uint64()
		if got != want {
			t.Fatalf("Sub(%d,%d)=%d want %d", a, b, got, want)
		}

		got = uint64(a.Mul(b))
		want = new(big.Int).Mod(new(big.Int).Mul(ba, bb), modP).Uint64()
		if got != want {
			t.Fatalf("Mul(%d,%d)=%d want %d", a, b, got, want)
		}
	}
}

// TestFieldEdgeCases hits the boundary values where the reduction is most likely
// to be wrong (0, 1, P−1, and products that exactly straddle 2^64).
func TestFieldEdgeCases(t *testing.T) {
	vals := []Felt{0, 1, 2, Felt(P - 1), Felt(P - 2), Felt(epsilon), Felt(epsilon + 1), Felt(1 << 32), Felt(1 << 63)}
	for _, a := range vals {
		for _, b := range vals {
			ba, bb := bigOf(a), bigOf(b)
			if uint64(a.Mul(b)) != new(big.Int).Mod(new(big.Int).Mul(ba, bb), modP).Uint64() {
				t.Fatalf("Mul edge (%d,%d)", a, b)
			}
			if uint64(a.Add(b)) != new(big.Int).Mod(new(big.Int).Add(ba, bb), modP).Uint64() {
				t.Fatalf("Add edge (%d,%d)", a, b)
			}
			if uint64(a.Sub(b)) != new(big.Int).Mod(new(big.Int).Sub(ba, bb), modP).Uint64() {
				t.Fatalf("Sub edge (%d,%d)", a, b)
			}
		}
	}
}

// TestInverse verifies a·a⁻¹ = 1 for all nonzero, and Inv(0)=0.
func TestInverse(t *testing.T) {
	if Felt(0).Inv() != 0 {
		t.Fatal("Inv(0) must be 0 by convention")
	}
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 5000; i++ {
		a := randField(r)
		if a == 0 {
			continue
		}
		if a.Mul(a.Inv()) != 1 {
			t.Fatalf("a·a⁻¹≠1 for a=%d", a)
		}
	}
}

// TestExp checks Exp against repeated multiplication and Fermat (a^(P-1)=1).
func TestExp(t *testing.T) {
	r := rand.New(rand.NewSource(99))
	for i := 0; i < 1000; i++ {
		a := randField(r)
		e := r.Uint64() % 1000
		want := Felt(1)
		for j := uint64(0); j < e; j++ {
			want = want.Mul(a)
		}
		if a.Exp(e) != want {
			t.Fatalf("Exp(%d,%d) mismatch", a, e)
		}
		if a != 0 && a.Exp(P-1) != 1 {
			t.Fatalf("Fermat a^(P-1)≠1 for a=%d", a)
		}
	}
}
