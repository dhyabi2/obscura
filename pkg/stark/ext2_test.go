package stark

import (
	"math/rand"
	"testing"
)

// randFelt returns a uniform base-field element.
func rndFelt(r *rand.Rand) Felt {
	return NewFelt(r.Uint64())
}

// randFelt2 returns a uniform extension-field element.
func randFelt2(r *rand.Rand) Felt2 {
	return NewFelt2(rndFelt(r), rndFelt(r))
}

// flattenExt appends the base coordinates of each extension element to dst — used by
// zero-knowledge leakage tests to scan both A and B coordinates of revealed OOD values.
func flattenExt(dst []Felt, xs ...Felt2) []Felt {
	for _, x := range xs {
		dst = append(dst, x.A, x.B)
	}
	return dst
}

// TestExt2NonResidue verifies the foundational assumption: 7 is a quadratic
// NON-RESIDUE of Goldilocks, so u^2 − 7 is irreducible and F_p[u]/(u^2−7) is a field.
// Euler's criterion: a non-residue r satisfies r^((p−1)/2) = −1 = p−1.
func TestExt2NonResidue(t *testing.T) {
	nr := Felt(ext2NonResidue)
	leg := nr.Exp((P - 1) / 2)
	if uint64(leg) != P-1 {
		t.Fatalf("ext2NonResidue %d is NOT a quadratic non-residue: legendre symbol = %d (want %d)", nr, leg, P-1)
	}
	// Sanity: a known residue (4 = 2^2) must give +1.
	if uint64(Felt(4).Exp((P-1)/2)) != 1 {
		t.Fatalf("Euler criterion broken: 4 should be a residue")
	}
}

// TestExt2EmbedAndConsts checks the embedding and identities.
func TestExt2EmbedAndConsts(t *testing.T) {
	if !Zero2().IsZero() {
		t.Fatal("Zero2 not zero")
	}
	if One2().IsZero() {
		t.Fatal("One2 is zero")
	}
	x := Felt2From(NewFelt(12345))
	if !x.IsBase() || x.A != NewFelt(12345) || x.B != 0 {
		t.Fatal("Felt2From wrong")
	}
	if !Felt2From(Felt(1)).Equal(One2()) {
		t.Fatal("embed of 1 != One2")
	}
}

// TestExt2FieldAxioms checks commutativity, associativity, distributivity, and the
// additive/multiplicative identities over random elements.
func TestExt2FieldAxioms(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 5000; i++ {
		a, b, c := randFelt2(r), randFelt2(r), randFelt2(r)

		// Additive group.
		if !a.Add(b).Equal(b.Add(a)) {
			t.Fatal("add not commutative")
		}
		if !a.Add(b).Add(c).Equal(a.Add(b.Add(c))) {
			t.Fatal("add not associative")
		}
		if !a.Add(Zero2()).Equal(a) {
			t.Fatal("add identity")
		}
		if !a.Add(a.Neg()).IsZero() {
			t.Fatal("add inverse")
		}
		if !a.Sub(b).Equal(a.Add(b.Neg())) {
			t.Fatal("sub != add neg")
		}

		// Multiplicative monoid.
		if !a.Mul(b).Equal(b.Mul(a)) {
			t.Fatal("mul not commutative")
		}
		if !a.Mul(b).Mul(c).Equal(a.Mul(b.Mul(c))) {
			t.Fatal("mul not associative")
		}
		if !a.Mul(One2()).Equal(a) {
			t.Fatal("mul identity")
		}

		// Distributivity.
		if !a.Mul(b.Add(c)).Equal(a.Mul(b).Add(a.Mul(c))) {
			t.Fatal("distributivity")
		}

		// Square == self mul.
		if !a.Square().Equal(a.Mul(a)) {
			t.Fatal("square != a*a")
		}

		// MulBase consistency.
		s := rndFelt(r)
		if !a.MulBase(s).Equal(a.Mul(Felt2From(s))) {
			t.Fatal("MulBase != Mul(embed)")
		}
	}
}

// TestExt2Inverse checks x·x^(−1) = 1 for all nonzero x and Inv(0)=0.
func TestExt2Inverse(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	if !Zero2().Inv().IsZero() {
		t.Fatal("Inv(0) must be 0 by convention")
	}
	for i := 0; i < 5000; i++ {
		x := randFelt2(r)
		if x.IsZero() {
			continue
		}
		if !x.Mul(x.Inv()).Equal(One2()) {
			t.Fatalf("x·x^-1 != 1 for x=%v", x)
		}
	}
	// Inverse of a pure base element matches the base-field inverse embedded.
	for i := 0; i < 200; i++ {
		s := rndFelt(r)
		if s == 0 {
			continue
		}
		if !Felt2From(s).Inv().Equal(Felt2From(s.Inv())) {
			t.Fatal("Inv of embedded base != embedded base Inv")
		}
	}
}

// TestExt2NormMultiplicative checks N(x·y) = N(x)·N(y) and N(x) = x·Conj(x), and that
// the norm is zero only for x=0 (confirms NR is a non-residue: no nonzero zero-divisor).
func TestExt2Norm(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	for i := 0; i < 5000; i++ {
		x, y := randFelt2(r), randFelt2(r)

		// N(x) = x · Conj(x) lands in the base field.
		prod := x.Mul(x.Conj())
		if prod.B != 0 {
			t.Fatal("x·Conj(x) must be a base element")
		}
		if prod.A != x.Norm() {
			t.Fatal("Norm != x·Conj(x).A")
		}

		// Multiplicativity.
		if x.Mul(y).Norm() != x.Norm().Mul(y.Norm()) {
			t.Fatal("norm not multiplicative")
		}

		// Non-zero norm for non-zero x (field, no zero divisors).
		if !x.IsZero() && x.Norm() == 0 {
			t.Fatalf("nonzero x has zero norm — NR is a RESIDUE, field broken: x=%v", x)
		}
	}
}

// TestExt2Frobenius checks the Frobenius map x ↦ x^p: it is the nontrivial Galois
// automorphism (conjugation), fixes the base field, has order 2, and gives the norm
// as x·Frob(x).
func TestExt2Frobenius(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	for i := 0; i < 3000; i++ {
		x := randFelt2(r)

		// Frobenius == explicit p-th power.
		if !x.Frobenius().Equal(x.Exp(P)) {
			// Exp only takes uint64; p fits in uint64. Compute x^p directly.
			t.Fatalf("Frobenius != x^p for x=%v: got %v want %v", x, x.Frobenius(), x.Exp(P))
		}
		// Conjugation matches Frobenius.
		if !x.Frobenius().Equal(x.Conj()) {
			t.Fatal("Frobenius != Conj")
		}
		// Order 2: Frob(Frob(x)) = x.
		if !x.Frobenius().Frobenius().Equal(x) {
			t.Fatal("Frobenius not order 2")
		}
		// Fixes the base field exactly: Frob(x)=x  <=>  x is base.
		if x.Frobenius().Equal(x) != x.IsBase() {
			t.Fatal("Frobenius fixed-field != base field")
		}
		// Homomorphism: Frob(x·y) = Frob(x)·Frob(y).
		y := randFelt2(r)
		if !x.Mul(y).Frobenius().Equal(x.Frobenius().Mul(y.Frobenius())) {
			t.Fatal("Frobenius not multiplicative")
		}
		if !x.Add(y).Frobenius().Equal(x.Frobenius().Add(y.Frobenius())) {
			t.Fatal("Frobenius not additive")
		}
		// Norm via Frobenius.
		nf := x.Mul(x.Frobenius())
		if nf.B != 0 || nf.A != x.Norm() {
			t.Fatal("x·Frob(x) != Norm")
		}
	}
}

// TestExt2ExpConsistency cross-checks Exp against repeated multiplication and Fermat's
// little theorem in F_{p^2}: x^(p^2−1) = 1 for nonzero x.  We verify a cheaper corollary
// here: x^p · x = x^(p+1) = Norm(x) (a base element) — exercising Exp over a full p-sized
// exponent path implicitly via Frobenius.
func TestExt2ExpConsistency(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	for i := 0; i < 1000; i++ {
		x := randFelt2(r)
		// x^(p+1) = x · x^p = x · Frob(x) = Norm(x).
		got := x.Exp(P - 1).Mul(x).Mul(x) // x^(p-1) · x · x = x^(p+1)
		want := Felt2From(x.Norm())
		if !got.Equal(want) {
			t.Fatalf("x^(p+1) != Norm(x): x=%v got=%v want=%v", x, got, want)
		}
		// Small exponent vs naive product.
		acc := One2()
		for k := 0; k < 13; k++ {
			if !acc.Equal(x.Exp(uint64(k))) {
				t.Fatalf("Exp(%d) mismatch", k)
			}
			acc = acc.Mul(x)
		}
	}
}
