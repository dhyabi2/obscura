package accumulator

import (
	"math/big"
	"testing"
)

func TestMultiPoKE(t *testing.T) {
	for _, G := range testGroups(t) {
		g := G.Generator()
		B1 := G.Exp(g, big.NewInt(7))
		B2 := G.Exp(g, big.NewInt(11))
		x1, _ := new(big.Int).SetString("999999999999999999989", 10)
		x2 := big.NewInt(-123456789)
		T := G.Op(G.Exp(B1, x1), G.Exp(B2, x2))
		pf := ProveMultiPoKE(G, B1, B2, T, x1, x2)
		if !VerifyMultiPoKE(G, B1, B2, T, pf) {
			t.Fatalf("[%s] multi-poke verify failed", G.Name())
		}
		// wrong target
		bad := G.Op(T, g)
		if VerifyMultiPoKE(G, B1, B2, bad, pf) {
			t.Fatalf("[%s] multi-poke accepted wrong target", G.Name())
		}
	}
}

func TestZKMembership(t *testing.T) {
	for _, G := range testGroups(t) {
		acc := New(G)
		var ps []*big.Int
		for i := 0; i < 6; i++ {
			p, _ := HashToPrime([]byte{byte('z'), byte(i)})
			ps = append(ps, p)
			acc.Add(p)
		}
		secret := ps[4]
		w, _ := acc.MembershipWitness(secret)

		m := ProveZKMembership(G, acc.Value(), secret, w)
		if !VerifyZKMembership(G, acc.Value(), m) {
			t.Fatalf("[%s] zk-membership verify failed", G.Name())
		}
		// proof does not verify against a different accumulator state
		acc.Add(big.NewInt(0).Add(secret, big.NewInt(2))) // perturb (add another prime-ish)
	}
}
