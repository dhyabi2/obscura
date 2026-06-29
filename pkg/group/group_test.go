package group

import (
	"math/big"
	"testing"
)

// testGroup runs the full abelian-group axiom suite against any backend. These
// property tests are how we *empirically validate* the class-group composition
// formula (which is too error-prone to trust by transcription alone).
func testGroup(t *testing.T, G Group) {
	t.Helper()
	g := G.Generator()
	id := G.Identity()

	// identity: g ∘ e == g
	if !G.Equal(G.Op(g, id), g) {
		t.Fatalf("[%s] identity law failed: g∘e != g", G.Name())
	}
	if !G.Equal(G.Op(id, g), g) {
		t.Fatalf("[%s] identity law failed: e∘g != g", G.Name())
	}

	// inverse: g ∘ g^-1 == e
	if !G.Equal(G.Op(g, G.Inverse(g)), id) {
		t.Fatalf("[%s] inverse law failed", G.Name())
	}

	// commutativity: (g^3) ∘ (g^5) == (g^5) ∘ (g^3)
	a := G.Exp(g, big.NewInt(3))
	b := G.Exp(g, big.NewInt(5))
	if !G.Equal(G.Op(a, b), G.Op(b, a)) {
		t.Fatalf("[%s] commutativity failed", G.Name())
	}

	// associativity: (a∘b)∘c == a∘(b∘c)
	c := G.Exp(g, big.NewInt(7))
	left := G.Op(G.Op(a, b), c)
	right := G.Op(a, G.Op(b, c))
	if !G.Equal(left, right) {
		t.Fatalf("[%s] associativity failed", G.Name())
	}

	// exponent homomorphism: g^m ∘ g^n == g^(m+n)
	for _, pair := range [][2]int64{{3, 5}, {11, 17}, {100, 250}, {1, 1}, {0, 9}} {
		m, n := big.NewInt(pair[0]), big.NewInt(pair[1])
		lhs := G.Op(G.Exp(g, m), G.Exp(g, n))
		rhs := G.Exp(g, new(big.Int).Add(m, n))
		if !G.Equal(lhs, rhs) {
			t.Fatalf("[%s] g^%d ∘ g^%d != g^(sum)", G.Name(), pair[0], pair[1])
		}
	}

	// power tower: (g^m)^n == g^(m*n)
	for _, pair := range [][2]int64{{3, 5}, {7, 13}, {251, 4}} {
		m, n := big.NewInt(pair[0]), big.NewInt(pair[1])
		lhs := G.Exp(G.Exp(g, m), n)
		rhs := G.Exp(g, new(big.Int).Mul(m, n))
		if !G.Equal(lhs, rhs) {
			t.Fatalf("[%s] (g^%d)^%d != g^(prod)", G.Name(), pair[0], pair[1])
		}
	}

	// composition of two distinct large prime exponents (exercises e>1 paths)
	p1, _ := new(big.Int).SetString("32416190071", 10) // prime
	p2, _ := new(big.Int).SetString("32416187567", 10) // prime
	lhs := G.Op(G.Exp(g, p1), G.Exp(g, p2))
	rhs := G.Exp(g, new(big.Int).Add(p1, p2))
	if !G.Equal(lhs, rhs) {
		t.Fatalf("[%s] large-prime exponent homomorphism failed", G.Name())
	}

	// marshal round-trip
	x := G.Exp(g, big.NewInt(123456))
	bs := G.Marshal(x)
	y, err := G.Unmarshal(bs)
	if err != nil {
		t.Fatalf("[%s] unmarshal error: %v", G.Name(), err)
	}
	if !G.Equal(x, y) {
		t.Fatalf("[%s] marshal round-trip mismatch", G.Name())
	}
}

func TestRSAGroup_SmallModulus(t *testing.T) {
	// Use a product of two primes; factorization "unknown" to the protocol.
	p, _ := new(big.Int).SetString("170141183460469231731687303715884105727", 10) // Mersenne prime M127
	q, _ := new(big.Int).SetString("162259276829213363391578010288127", 10)        // M107
	N := new(big.Int).Mul(p, q)
	G := NewRSAGroup(N, big.NewInt(2), "rsa-test")
	testGroup(t, G)
}

func TestRSAGroup_Challenge2048(t *testing.T) {
	G := NewRSA2048Group()
	testGroup(t, G)
}

func TestClassGroup_Small(t *testing.T) {
	// A small but valid prime discriminant D ≡ 1 (mod 8): D = -p, p ≡ 7 mod 8.
	D := DeriveDiscriminant([]byte("obscura-test-small"), 256)
	G, err := NewClassGroup(D, "classgroup-test-256")
	if err != nil {
		t.Fatalf("NewClassGroup: %v", err)
	}
	t.Logf("test discriminant bits=%d", D.BitLen())
	testGroup(t, G)
}

func TestClassGroup_Production(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 2048-bit class group test in -short mode")
	}
	D := DeriveDiscriminant([]byte("obscura-mainnet-v1"), 2048)
	G, err := NewClassGroup(D, "classgroup-d2048")
	if err != nil {
		t.Fatalf("NewClassGroup: %v", err)
	}
	testGroup(t, G)
}

func TestDiscriminantProperties(t *testing.T) {
	D := DeriveDiscriminant([]byte("x"), 256)
	if D.Sign() >= 0 {
		t.Fatal("discriminant must be negative")
	}
	negD := new(big.Int).Neg(D)
	if new(big.Int).Mod(negD, big.NewInt(8)).Cmp(big.NewInt(7)) != 0 {
		t.Fatal("-D must be ≡ 7 mod 8")
	}
	if !negD.ProbablyPrime(20) {
		t.Fatal("-D must be prime")
	}
}
