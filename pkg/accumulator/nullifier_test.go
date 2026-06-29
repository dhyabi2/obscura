package accumulator

import (
	"math/big"
	"testing"

	"obscura/pkg/group"
)

// single-element accumulator acc = g^p with witness w = g (w^p = acc).
func accForPrime(G group.Group, p *big.Int) (acc, w group.Element) {
	g := G.Generator()
	return G.Exp(g, p), g
}

func TestMembershipNullifier_HonestVerifies(t *testing.T) {
	G := group.NewRSA2048Group()
	p := nextPrimeForTest(big.NewInt(1_000_003))
	acc, w := accForPrime(G, p)

	mn := ProveMembershipNullifier(G, acc, p, w)
	if !VerifyMembershipNullifier(G, acc, mn) {
		t.Fatal("honest membership-nullifier proof did not verify")
	}
}

func TestMembershipNullifier_DeterministicPerCoin(t *testing.T) {
	G := group.NewRSA2048Group()
	p := nextPrimeForTest(big.NewInt(2_000_011))
	acc, w := accForPrime(G, p)

	a := ProveMembershipNullifier(G, acc, p, w)
	b := ProveMembershipNullifier(G, acc, p, w)
	if !G.Equal(a.N, b.N) {
		t.Fatal("same coin produced different nullifiers (double-spend would slip)")
	}
	// a different coin must produce a different nullifier
	p2 := nextPrimeForTest(big.NewInt(3_000_017))
	acc2, w2 := accForPrime(G, p2)
	c := ProveMembershipNullifier(G, acc2, p2, w2)
	if G.Equal(a.N, c.N) {
		t.Fatal("different coins collided on one nullifier")
	}
}

func TestMembershipNullifier_TamperedNullifierRejected(t *testing.T) {
	G := group.NewRSA2048Group()
	p := nextPrimeForTest(big.NewInt(4_000_037))
	acc, w := accForPrime(G, p)
	mn := ProveMembershipNullifier(G, acc, p, w)

	// swap in a DIFFERENT coin's nullifier U^p2 while keeping the membership of p —
	// the binding must reject it (this is exactly the double-spend-with-fresh-N attack).
	p2 := nextPrimeForTest(big.NewInt(5_000_011))
	mn.N = G.Exp(uGenerator(G), p2)
	if VerifyMembershipNullifier(G, acc, mn) {
		t.Fatal("accepted a nullifier not bound to the accumulated element")
	}
}

func TestMembershipNullifier_TamperedBindingRejected(t *testing.T) {
	G := group.NewRSA2048Group()
	p := nextPrimeForTest(big.NewInt(6_000_011))
	acc, w := accForPrime(G, p)
	mn := ProveMembershipNullifier(G, acc, p, w)

	mn.Bind.R = new(big.Int).Add(mn.Bind.R, big.NewInt(1))
	if VerifyMembershipNullifier(G, acc, mn) {
		t.Fatal("accepted a tampered binding proof")
	}
}

func TestEqualExp_RejectsWrongTarget(t *testing.T) {
	G := group.NewRSA2048Group()
	g := G.Generator()
	U := uGenerator(G)
	x := nextPrimeForTest(big.NewInt(7_000_003))
	t1, t2 := G.Exp(g, x), G.Exp(U, x)
	pf := ProveEqualExp(G, g, U, t1, t2, x)
	if !VerifyEqualExp(G, g, U, t1, t2, pf) {
		t.Fatal("honest equal-exp did not verify")
	}
	// wrong second target (different exponent) must fail
	bad := G.Exp(U, new(big.Int).Add(x, big.NewInt(2)))
	if VerifyEqualExp(G, g, U, t1, bad, pf) {
		t.Fatal("equal-exp accepted mismatched exponents")
	}
}
