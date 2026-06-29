package accumulator

import (
	"math/big"
	"testing"

	"obscura/pkg/group"
)

func testGroups(t *testing.T) []group.Group {
	t.Helper()
	p, _ := new(big.Int).SetString("170141183460469231731687303715884105727", 10)
	q, _ := new(big.Int).SetString("162259276829213363391578010288127", 10)
	N := new(big.Int).Mul(p, q)
	rsa := group.NewRSAGroup(N, big.NewInt(2), "rsa-test")

	D := group.DeriveDiscriminant([]byte("acc-test"), 256)
	cg, err := group.NewClassGroup(D, "cg-test")
	if err != nil {
		t.Fatal(err)
	}
	return []group.Group{rsa, cg}
}

func TestHashToPrime(t *testing.T) {
	p, nonce := HashToPrime([]byte("output-pubkey-1"))
	if !p.ProbablyPrime(20) {
		t.Fatal("not prime")
	}
	if !VerifyHashToPrime([]byte("output-pubkey-1"), nonce, p) {
		t.Fatal("verify failed")
	}
	// distinct inputs -> distinct primes
	p2, _ := HashToPrime([]byte("output-pubkey-2"))
	if p.Cmp(p2) == 0 {
		t.Fatal("collision")
	}
	if VerifyHashToPrime([]byte("wrong"), nonce, p) {
		t.Fatal("verify should fail for wrong data")
	}
}

func TestAccumulatorMembership(t *testing.T) {
	for _, G := range testGroups(t) {
		acc := New(G)
		primes := make([]*big.Int, 8)
		for i := range primes {
			primes[i], _ = HashToPrime([]byte{byte('a' + i)})
			if err := acc.Add(primes[i]); err != nil {
				t.Fatalf("[%s] add: %v", G.Name(), err)
			}
		}
		// every member has a valid witness
		for i, p := range primes {
			w, err := acc.MembershipWitness(p)
			if err != nil {
				t.Fatalf("[%s] witness: %v", G.Name(), err)
			}
			if !VerifyMembership(G, acc.Value(), w, p) {
				t.Fatalf("[%s] membership %d failed", G.Name(), i)
			}
		}
		// double-add rejected
		if err := acc.Add(primes[0]); err == nil {
			t.Fatalf("[%s] double-add should fail", G.Name())
		}
	}
}

func TestAccumulatorRemove(t *testing.T) {
	for _, G := range testGroups(t) {
		acc := New(G)
		var ps []*big.Int
		for i := 0; i < 6; i++ {
			p, _ := HashToPrime([]byte{byte('m'), byte(i)})
			ps = append(ps, p)
			acc.Add(p)
		}
		// witness before removal
		victim := ps[2]
		acc.Remove(victim)
		// removed element no longer has a valid witness against new acc
		if acc.Contains(victim) {
			t.Fatalf("[%s] still contains removed", G.Name())
		}
		// remaining members still verify
		for i, p := range ps {
			if i == 2 {
				continue
			}
			w, _ := acc.MembershipWitness(p)
			if !VerifyMembership(G, acc.Value(), w, p) {
				t.Fatalf("[%s] post-remove membership %d failed", G.Name(), i)
			}
		}
	}
}

func TestPoE(t *testing.T) {
	for _, G := range testGroups(t) {
		base := G.Generator()
		exp, _ := new(big.Int).SetString("12345678901234567890123456789", 10)
		acc := G.Exp(base, exp)
		pf := ProvePoE(G, base, acc, exp)
		if !VerifyPoE(G, base, acc, exp, pf) {
			t.Fatalf("[%s] PoE verify failed", G.Name())
		}
		// wrong exponent must fail
		if VerifyPoE(G, base, acc, new(big.Int).Add(exp, big.NewInt(1)), pf) {
			t.Fatalf("[%s] PoE accepted wrong exp", G.Name())
		}
	}
}

func TestPoKE2(t *testing.T) {
	for _, G := range testGroups(t) {
		// prove knowledge of the prime exponent linking witness -> acc
		acc := New(G)
		var ps []*big.Int
		for i := 0; i < 5; i++ {
			p, _ := HashToPrime([]byte{byte('k'), byte(i)})
			ps = append(ps, p)
			acc.Add(p)
		}
		secret := ps[3]
		w, _ := acc.MembershipWitness(secret)
		// base^secret = acc where base = witness
		pf := ProvePoKE2(G, w, acc.Value(), secret)
		if !VerifyPoKE2(G, w, acc.Value(), pf) {
			t.Fatalf("[%s] PoKE2 verify failed", G.Name())
		}
		// proof for wrong target fails
		bad := G.Exp(acc.Value(), big.NewInt(2))
		if VerifyPoKE2(G, w, bad, pf) {
			t.Fatalf("[%s] PoKE2 accepted wrong target", G.Name())
		}
	}
}

func TestNonMembership(t *testing.T) {
	for _, G := range testGroups(t) {
		acc := New(G)
		var ps []*big.Int
		for i := 0; i < 5; i++ {
			p, _ := HashToPrime([]byte{byte('n'), byte(i)})
			ps = append(ps, p)
			acc.Add(p)
		}
		// an element never added
		outsider, _ := HashToPrime([]byte("outsider"))
		pf, err := acc.ProveNonMembership(outsider)
		if err != nil {
			t.Fatalf("[%s] prove non-membership: %v", G.Name(), err)
		}
		if !VerifyNonMembership(G, acc.Value(), outsider, pf) {
			t.Fatalf("[%s] non-membership verify failed", G.Name())
		}
		// a real member must NOT have a non-membership proof
		if _, err := acc.ProveNonMembership(ps[0]); err == nil {
			t.Fatalf("[%s] non-membership proof produced for member", G.Name())
		}
	}
}
