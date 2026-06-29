package stark

import (
	"math/rand"
	"testing"
)

// TestRootOfUnityOrder checks the 2^logN-th root has exactly that order: ω^(2^logN)=1
// but ω^(2^(logN-1))≠1.
func TestRootOfUnityOrder(t *testing.T) {
	for logN := uint(1); logN <= 20; logN++ {
		w := RootOfUnity(logN)
		if w.Exp(1<<logN) != 1 {
			t.Fatalf("ω^(2^%d) ≠ 1", logN)
		}
		if w.Exp(1<<(logN-1)) == 1 {
			t.Fatalf("ω has order < 2^%d (not primitive)", logN)
		}
	}
}

// TestNTTRoundTrip verifies INTT(NTT(c)) == c.
func TestNTTRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for logN := uint(1); logN <= 12; logN++ {
		n := 1 << logN
		c := make([]Felt, n)
		for i := range c {
			c[i] = randField(r)
		}
		got := INTT(NTT(c))
		for i := range c {
			if got[i] != c[i] {
				t.Fatalf("round-trip mismatch n=%d at %d: %d≠%d", n, i, got[i], c[i])
			}
		}
	}
}

// TestNTTMatchesNaiveEval checks NTT(c)[i] == P(ω^i) computed by Horner — the
// transform really is multi-point evaluation over the roots of unity.
func TestNTTMatchesNaiveEval(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	logN := uint(8)
	n := 1 << logN
	c := make([]Felt, n)
	for i := range c {
		c[i] = randField(r)
	}
	evals := NTT(c)
	w := RootOfUnity(logN)
	x := Felt(1)
	for i := 0; i < n; i++ {
		if EvalPoly(c, x) != evals[i] {
			t.Fatalf("NTT[%d] ≠ P(ω^%d)", i, i)
		}
		x = x.Mul(w)
	}
}
