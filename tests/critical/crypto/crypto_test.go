package crypto

// Critical crypto-workflow tests for Obscura. These exercise the primitives
// that consensus and value-soundness depend on: groups of unknown order (RSA
// and class group), the BBF accumulator + proofs (PoE/PoKE2/MultiPoKE/ZK),
// hash-to-prime, and the edwards25519 commitment/range/spend/stealth layer.
//
// Each test asserts an end-to-end behavior, not just "no panic". Negative
// cases (tamper / wrong secret / inflated value) MUST fail verification.

import (
	"bytes"
	"math/big"
	"testing"

	"filippo.io/edwards25519"

	"obscura/pkg/accumulator"
	"obscura/pkg/commit"
	"obscura/pkg/group"
)

// smallRSA builds a small RSA group for fast tests. N = 61*53 = 3233 with a
// generator. Factorization is "known" here but the group axioms and
// marshalling behavior are identical to the production modulus.
func smallRSA(t *testing.T) *group.RSAGroup {
	t.Helper()
	// Use a larger product so canonicalization and degenerate edges are
	// meaningful but operations stay instant.
	p := big.NewInt(1000003)
	q := big.NewInt(999983)
	N := new(big.Int).Mul(p, q)
	return group.NewRSAGroup(N, big.NewInt(3), "rsa-test")
}

// smallClassGroup derives a quick 256-bit discriminant class group.
func smallClassGroup(t *testing.T) *group.ClassGroup {
	t.Helper()
	D := group.DeriveDiscriminant([]byte("t"), 256)
	cg, err := group.NewClassGroup(D, "cg-test")
	if err != nil {
		t.Fatalf("NewClassGroup: %v", err)
	}
	return cg
}

// ---------------------------------------------------------------------------
// 1-3 + 7. RSA group
// ---------------------------------------------------------------------------

// 1. Identity neutral, inverse cancels, associativity holds.
func TestRSAGroupAxioms(t *testing.T) {
	G := smallRSA(t)
	g := G.Generator()
	id := G.Identity()

	// identity: g ∘ id == g
	if !G.Equal(G.Op(g, id), g) {
		t.Fatal("identity is not neutral")
	}
	// inverse: g ∘ g^-1 == id
	if !G.Equal(G.Op(g, G.Inverse(g)), id) {
		t.Fatal("inverse does not cancel")
	}
	// associativity: (a∘b)∘c == a∘(b∘c)
	a := G.Exp(g, big.NewInt(7))
	b := G.Exp(g, big.NewInt(11))
	c := G.Exp(g, big.NewInt(13))
	lhs := G.Op(G.Op(a, b), c)
	rhs := G.Op(a, G.Op(b, c))
	if !G.Equal(lhs, rhs) {
		t.Fatal("Op is not associative")
	}
}

// 2. Exponent homomorphism: g^a · g^b == g^(a+b).
func TestRSAExponentHomomorphism(t *testing.T) {
	G := smallRSA(t)
	g := G.Generator()
	a := big.NewInt(123456)
	b := big.NewInt(987654)
	lhs := G.Op(G.Exp(g, a), G.Exp(g, b))
	rhs := G.Exp(g, new(big.Int).Add(a, b))
	if !G.Equal(lhs, rhs) {
		t.Fatal("g^a·g^b != g^(a+b)")
	}
	// also (g^a)^b == g^(ab)
	tower := G.Exp(G.Exp(g, a), b)
	flat := G.Exp(g, new(big.Int).Mul(a, b))
	if !G.Equal(tower, flat) {
		t.Fatal("(g^a)^b != g^(ab)")
	}
}

// 3. Marshal round-trip; Unmarshal rejects degenerate (1, N-1) and out-of-range.
func TestRSAMarshalRoundTripAndReject(t *testing.T) {
	G := smallRSA(t)
	g := G.Exp(G.Generator(), big.NewInt(424242))

	b := G.Marshal(g)
	got, err := G.Unmarshal(b)
	if err != nil {
		t.Fatalf("round-trip Unmarshal: %v", err)
	}
	if !G.Equal(got, g) {
		t.Fatal("marshal round-trip changed element")
	}

	mk := func(x *big.Int) []byte {
		buf := make([]byte, G.MarshalSize())
		x.FillBytes(buf)
		return buf
	}
	// reject 1
	if _, err := G.Unmarshal(mk(big.NewInt(1))); err == nil {
		t.Fatal("Unmarshal accepted degenerate element 1")
	}
	// reject N-1
	if _, err := G.Unmarshal(mk(new(big.Int).Sub(G.N, big.NewInt(1)))); err == nil {
		t.Fatal("Unmarshal accepted degenerate element N-1")
	}
	// reject out of range: 0 and N
	if _, err := G.Unmarshal(mk(big.NewInt(0))); err == nil {
		t.Fatal("Unmarshal accepted 0 (out of range)")
	}
	// N: need a buffer wide enough to hold N (MarshalSize is bytes of N).
	nbuf := make([]byte, (G.N.BitLen()+7)/8)
	G.N.FillBytes(nbuf)
	if _, err := G.Unmarshal(nbuf); err == nil {
		t.Fatal("Unmarshal accepted N (out of range)")
	}
}

// 7. Canonicalization: x and N-x map to the same element.
func TestRSACanonicalization(t *testing.T) {
	G := smallRSA(t)
	// pick an arbitrary in-range residue and its negation mod N
	x := new(big.Int).Mod(big.NewInt(7777777), G.N)
	negx := new(big.Int).Sub(G.N, x)

	bx := make([]byte, G.MarshalSize())
	x.FillBytes(bx)
	bnx := make([]byte, G.MarshalSize())
	negx.FillBytes(bnx)

	ex, err := G.Unmarshal(bx)
	if err != nil {
		t.Fatalf("unmarshal x: %v", err)
	}
	enx, err := G.Unmarshal(bnx)
	if err != nil {
		t.Fatalf("unmarshal N-x: %v", err)
	}
	if !G.Equal(ex, enx) {
		t.Fatal("x and N-x are not canonicalized to the same element")
	}

	// Op/Exp results are canonical too: g^e and its sign-flip compare equal
	// after going through Op. Build h and -h and confirm Op gives canonical.
	g := G.Generator()
	h1 := G.Exp(g, big.NewInt(31))
	// multiply by identity should keep canonical form stable & equal
	if !G.Equal(G.Op(h1, G.Identity()), h1) {
		t.Fatal("Op result not canonical/stable")
	}
}

// ---------------------------------------------------------------------------
// 4-6. Class group
// ---------------------------------------------------------------------------

// 4. Class group axioms.
func TestClassGroupAxioms(t *testing.T) {
	G := smallClassGroup(t)
	g := G.Generator()
	id := G.Identity()

	if !G.Equal(G.Op(g, id), g) {
		t.Fatal("class group identity not neutral")
	}
	if !G.Equal(G.Op(g, G.Inverse(g)), id) {
		t.Fatal("class group inverse does not cancel")
	}
	a := G.Exp(g, big.NewInt(5))
	b := G.Exp(g, big.NewInt(9))
	c := G.Exp(g, big.NewInt(17))
	if !G.Equal(G.Op(G.Op(a, b), c), G.Op(a, G.Op(b, c))) {
		t.Fatal("class group Op not associative")
	}
	// commutativity
	if !G.Equal(G.Op(a, b), G.Op(b, a)) {
		t.Fatal("class group Op not commutative")
	}
}

// 5. Power tower: (g^m)^n == g^(mn).
func TestClassGroupPowerTower(t *testing.T) {
	G := smallClassGroup(t)
	g := G.Generator()
	m := big.NewInt(37)
	n := big.NewInt(41)
	tower := G.Exp(G.Exp(g, m), n)
	flat := G.Exp(g, new(big.Int).Mul(m, n))
	if !G.Equal(tower, flat) {
		t.Fatal("(g^m)^n != g^(mn) in class group")
	}
	// exponent homomorphism in class group too
	lhs := G.Op(G.Exp(g, m), G.Exp(g, n))
	rhs := G.Exp(g, new(big.Int).Add(m, n))
	if !G.Equal(lhs, rhs) {
		t.Fatal("g^m·g^n != g^(m+n) in class group")
	}
}

// 6. Unmarshal rejects wrong discriminant and non-divisible C.
func TestClassGroupUnmarshalRejects(t *testing.T) {
	G := smallClassGroup(t)

	// valid round-trip first
	g := G.Exp(G.Generator(), big.NewInt(123))
	good := G.Marshal(g)
	if _, err := G.Unmarshal(good); err != nil {
		t.Fatalf("valid class-group element rejected: %v", err)
	}

	// wrong discriminant: marshal an element from a DIFFERENT class group and
	// try to unmarshal it under G. Its (A,B) won't satisfy 4A | B²−D_G (or the
	// discriminant check), so it must be rejected.
	D2 := group.DeriveDiscriminant([]byte("other-seed"), 256)
	G2, err := group.NewClassGroup(D2, "cg-test-2")
	if err != nil {
		t.Fatalf("second class group: %v", err)
	}
	other := G2.Exp(G2.Generator(), big.NewInt(99))
	bad := G2.Marshal(other)
	if _, err := G.Unmarshal(bad); err == nil {
		t.Fatal("Unmarshal accepted a form from a different discriminant")
	}

	// craft bytes with non-divisible C: A=3, B=2. Then B²−D may not be
	// divisible by 4A. We encode (A,B) in the same wire format and expect the
	// 4A ∤ B²−D rejection (or a downstream discriminant/reduce rejection).
	craft := func(A *big.Int, B *big.Int) []byte {
		ab := A.Bytes()
		bb := B.Bytes()
		out := make([]byte, 0, len(ab)+len(bb)+5)
		out = append(out, byte(len(ab)>>8), byte(len(ab)))
		out = append(out, ab...)
		out = append(out, 0) // B sign positive
		out = append(out, byte(len(bb)>>8), byte(len(bb)))
		out = append(out, bb...)
		return out
	}
	// A=3,B=2 -> B²−D = 4 − D. D is odd (negative prime), so 4−D is odd,
	// not divisible by 12 -> rejected.
	if _, err := G.Unmarshal(craft(big.NewInt(3), big.NewInt(2))); err == nil {
		t.Fatal("Unmarshal accepted a crafted non-divisible-C form")
	}
}

// ---------------------------------------------------------------------------
// 8-10. Accumulator add/remove/membership/non-membership
// ---------------------------------------------------------------------------

// primeFor maps a label to its accumulator prime.
func primeFor(label string) *big.Int {
	p, _ := accumulator.HashToPrime([]byte(label))
	return p
}

// 8. Add + membership witness verifies.
func TestAccumulatorAddMembership(t *testing.T) {
	G := smallRSA(t)
	acc := accumulator.New(G)
	p1 := primeFor("out-1")
	p2 := primeFor("out-2")
	if err := acc.Add(p1); err != nil {
		t.Fatalf("Add p1: %v", err)
	}
	if err := acc.Add(p2); err != nil {
		t.Fatalf("Add p2: %v", err)
	}
	w, err := acc.MembershipWitness(p1)
	if err != nil {
		t.Fatalf("MembershipWitness: %v", err)
	}
	if !accumulator.VerifyMembership(G, acc.Value(), w, p1) {
		t.Fatal("valid membership witness failed to verify")
	}
	// wrong prime against same witness must fail
	if accumulator.VerifyMembership(G, acc.Value(), w, p2) {
		t.Fatal("membership verified for wrong prime")
	}
}

// 9. Remove then non-membership.
func TestAccumulatorRemoveNonMembership(t *testing.T) {
	G := smallRSA(t)
	acc := accumulator.New(G)
	p1 := primeFor("rm-1")
	p2 := primeFor("rm-2")
	_ = acc.Add(p1)
	_ = acc.Add(p2)
	if err := acc.Remove(p1); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if acc.Contains(p1) {
		t.Fatal("removed element still present")
	}
	// p1 is now absent: a non-membership proof must exist and verify.
	nm, err := acc.ProveNonMembership(p1)
	if err != nil {
		t.Fatalf("ProveNonMembership for removed element: %v", err)
	}
	if !accumulator.VerifyNonMembership(G, acc.Value(), p1, nm) {
		t.Fatal("non-membership proof for removed element failed")
	}
	// p2 is still a member: its membership witness still works.
	w, err := acc.MembershipWitness(p2)
	if err != nil {
		t.Fatalf("witness p2: %v", err)
	}
	if !accumulator.VerifyMembership(G, acc.Value(), w, p2) {
		t.Fatal("surviving member lost its witness")
	}
}

// 10. Duplicate add rejected.
func TestAccumulatorDuplicateAddRejected(t *testing.T) {
	G := smallRSA(t)
	acc := accumulator.New(G)
	p := primeFor("dup")
	if err := acc.Add(p); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	if err := acc.Add(p); err == nil {
		t.Fatal("duplicate Add was not rejected")
	}
}

// ---------------------------------------------------------------------------
// 11-16. PoE / PoKE2 / MultiPoKE
// ---------------------------------------------------------------------------

// 11. PoE prove/verify.
func TestPoEProveVerify(t *testing.T) {
	G := smallRSA(t)
	base := G.Generator()
	exp := big.NewInt(1234567)
	target := G.Exp(base, exp)
	pf := accumulator.ProvePoE(G, base, target, exp)
	if !accumulator.VerifyPoE(G, base, target, exp, pf) {
		t.Fatal("valid PoE failed to verify")
	}
}

// 12. PoE verify fails for wrong exponent.
func TestPoEWrongExponentFails(t *testing.T) {
	G := smallRSA(t)
	base := G.Generator()
	exp := big.NewInt(1234567)
	target := G.Exp(base, exp)
	pf := accumulator.ProvePoE(G, base, target, exp)
	wrong := new(big.Int).Add(exp, big.NewInt(1))
	if accumulator.VerifyPoE(G, base, target, wrong, pf) {
		t.Fatal("PoE verified for wrong exponent")
	}
}

// 13. PoKE2 prove/verify.
func TestPoKE2ProveVerify(t *testing.T) {
	G := smallRSA(t)
	base := G.Exp(G.Generator(), big.NewInt(7))
	x := big.NewInt(98765432)
	target := G.Exp(base, x)
	pf := accumulator.ProvePoKE2(G, base, target, x)
	if !accumulator.VerifyPoKE2(G, base, target, pf) {
		t.Fatal("valid PoKE2 failed to verify")
	}
}

// 14. PoKE2 fails for wrong target.
func TestPoKE2WrongTargetFails(t *testing.T) {
	G := smallRSA(t)
	base := G.Exp(G.Generator(), big.NewInt(7))
	x := big.NewInt(98765432)
	target := G.Exp(base, x)
	pf := accumulator.ProvePoKE2(G, base, target, x)
	badTarget := G.Op(target, base) // target * base != base^x
	if accumulator.VerifyPoKE2(G, base, badTarget, pf) {
		t.Fatal("PoKE2 verified against wrong target")
	}
}

// 15. MultiPoKE prove/verify: B1^x1 · B2^x2 = T.
func TestMultiPoKEProveVerify(t *testing.T) {
	G := smallRSA(t)
	B1 := G.Exp(G.Generator(), big.NewInt(3))
	B2 := G.Exp(G.Generator(), big.NewInt(5))
	x1 := big.NewInt(111111)
	x2 := big.NewInt(222222)
	T := G.Op(G.Exp(B1, x1), G.Exp(B2, x2))
	pf := accumulator.ProveMultiPoKE(G, B1, B2, T, x1, x2)
	if !accumulator.VerifyMultiPoKE(G, B1, B2, T, pf) {
		t.Fatal("valid MultiPoKE failed to verify")
	}
}

// 16. MultiPoKE fails on wrong target.
func TestMultiPoKEWrongTargetFails(t *testing.T) {
	G := smallRSA(t)
	B1 := G.Exp(G.Generator(), big.NewInt(3))
	B2 := G.Exp(G.Generator(), big.NewInt(5))
	x1 := big.NewInt(111111)
	x2 := big.NewInt(222222)
	T := G.Op(G.Exp(B1, x1), G.Exp(B2, x2))
	pf := accumulator.ProveMultiPoKE(G, B1, B2, T, x1, x2)
	badT := G.Op(T, B1)
	if accumulator.VerifyMultiPoKE(G, B1, B2, badT, pf) {
		t.Fatal("MultiPoKE verified against wrong target")
	}
}

// ---------------------------------------------------------------------------
// 17-18. ZK membership / non-membership end-to-end
// ---------------------------------------------------------------------------

// 17. ZK membership prove/verify against the accumulator value.
func TestZKMembership(t *testing.T) {
	G := smallRSA(t)
	acc := accumulator.New(G)
	p := primeFor("zk-1")
	other := primeFor("zk-2")
	_ = acc.Add(p)
	_ = acc.Add(other)
	w, err := acc.MembershipWitness(p)
	if err != nil {
		t.Fatalf("witness: %v", err)
	}
	zk := accumulator.ProveZKMembership(G, acc.Value(), p, w)
	if !accumulator.VerifyZKMembership(G, acc.Value(), zk) {
		t.Fatal("valid ZK membership proof failed to verify")
	}
	// against a different accumulator value it must fail
	acc2 := accumulator.New(G)
	_ = acc2.Add(other)
	if accumulator.VerifyZKMembership(G, acc2.Value(), zk) {
		t.Fatal("ZK membership verified against the wrong accumulator")
	}
}

// 18. Member has no non-membership proof; an outsider verifies.
func TestNonMembershipSemantics(t *testing.T) {
	G := smallRSA(t)
	acc := accumulator.New(G)
	member := primeFor("nm-member")
	_ = acc.Add(member)

	// real member cannot produce a non-membership proof (gcd != 1).
	if _, err := acc.ProveNonMembership(member); err == nil {
		t.Fatal("ProveNonMembership succeeded for an actual member")
	}

	// an outsider prime can prove and verify non-membership.
	outsider := primeFor("nm-outsider")
	nm, err := acc.ProveNonMembership(outsider)
	if err != nil {
		t.Fatalf("ProveNonMembership outsider: %v", err)
	}
	if !accumulator.VerifyNonMembership(G, acc.Value(), outsider, nm) {
		t.Fatal("outsider non-membership proof failed to verify")
	}
}

// ---------------------------------------------------------------------------
// 19. hash-to-prime
// ---------------------------------------------------------------------------

func TestHashToPrime(t *testing.T) {
	data := []byte("output-key-abc")
	p, nonce := accumulator.HashToPrime(data)
	if !p.ProbablyPrime(20) {
		t.Fatal("HashToPrime returned a composite")
	}
	// determinism
	p2, nonce2 := accumulator.HashToPrime(data)
	if p.Cmp(p2) != 0 || nonce != nonce2 {
		t.Fatal("HashToPrime is not deterministic")
	}
	// verifiable
	if !accumulator.VerifyHashToPrime(data, nonce, p) {
		t.Fatal("VerifyHashToPrime rejected the genuine prime")
	}
	// distinct inputs -> distinct primes
	q, _ := accumulator.HashToPrime([]byte("output-key-xyz"))
	if p.Cmp(q) == 0 {
		t.Fatal("distinct inputs produced the same prime")
	}
	// wrong nonce fails
	if accumulator.VerifyHashToPrime(data, nonce+1, p) {
		t.Fatal("VerifyHashToPrime accepted a wrong nonce")
	}
	// verifiable data helper round-trips to the same prime bytes
	b, ok := accumulator.HashToPrimeVerifyableData(data, nonce)
	if !ok || !bytes.Equal(b, p.Bytes()) {
		t.Fatal("HashToPrimeVerifyableData mismatch")
	}
}

// ---------------------------------------------------------------------------
// 20. Pedersen homomorphism
// ---------------------------------------------------------------------------

func TestPedersenHomomorphism(t *testing.T) {
	a := uint64(40000)
	b := uint64(2000000)
	ra := commit.RandomScalar()
	rb := commit.RandomScalar()
	Ca := commit.Commit(a, ra)
	Cb := commit.Commit(b, rb)

	// C(a)+C(b) == C(a+b, ra+rb)
	sum := new(edwards25519.Point).Add(Ca, Cb)
	rsum := new(edwards25519.Scalar).Add(ra, rb)
	expect := commit.Commit(a+b, rsum)
	if sum.Equal(expect) != 1 {
		t.Fatal("Pedersen commitment is not additively homomorphic")
	}
}

// ---------------------------------------------------------------------------
// 21-22. Range proofs
// ---------------------------------------------------------------------------

// 21. Range proof valid for several boundary values + bytes round-trip.
func TestRangeProofValid(t *testing.T) {
	for _, v := range []uint64{0, 1, 1 << 32, ^uint64(0)} {
		C, _, proof, err := commit.ProveRange(v)
		if err != nil {
			t.Fatalf("ProveRange(%d): %v", v, err)
		}
		if !commit.VerifyRange(C, proof) {
			t.Fatalf("VerifyRange failed for v=%d", v)
		}
		// VerifyRangeBytes round-trip
		if !commit.VerifyRangeBytes(C.Bytes(), proof.Serialize()) {
			t.Fatalf("VerifyRangeBytes failed for v=%d", v)
		}
	}
}

// 22. Tampered range proof rejected.
func TestRangeProofTamperRejected(t *testing.T) {
	C, _, proof, err := commit.ProveRange(123456789)
	if err != nil {
		t.Fatalf("ProveRange: %v", err)
	}
	if !commit.VerifyRange(C, proof) {
		t.Fatal("baseline range proof should verify")
	}
	// tamper a bit commitment: flip the first bit-commitment to a different
	// point. This breaks Σ C_i == C and/or the OR proof.
	ser := proof.Serialize()
	// flip a byte deep inside the serialized proof (in an OR-proof region).
	tampered := make([]byte, len(ser))
	copy(tampered, ser)
	tampered[len(tampered)/2] ^= 0xFF
	if commit.VerifyRangeBytes(C.Bytes(), tampered) {
		t.Fatal("tampered range proof verified")
	}

	// also tamper the commitment point against a genuine proof.
	if commit.VerifyRangeBytes(commit.Commit(999, commit.RandomScalar()).Bytes(), ser) {
		t.Fatal("range proof verified against an unrelated commitment")
	}
}

// ---------------------------------------------------------------------------
// 23. Ownership proof (theft prevention)
// ---------------------------------------------------------------------------

func TestOwnershipProof(t *testing.T) {
	x := commit.RandomScalar()
	P := new(edwards25519.Point).ScalarBaseMult(x)
	ctx := []byte("tx-core-hash")

	proof, err := commit.ProveOwnership(P.Bytes(), x, ctx)
	if err != nil {
		t.Fatalf("ProveOwnership: %v", err)
	}
	if !commit.VerifyOwnership(P.Bytes(), proof, ctx) {
		t.Fatal("valid ownership proof failed to verify")
	}

	// wrong secret => theft attempt must fail. Attacker proves with y != x but
	// targets the same P.
	y := commit.RandomScalar()
	theft, err := commit.ProveOwnership(P.Bytes(), y, ctx)
	if err != nil {
		t.Fatalf("ProveOwnership(theft): %v", err)
	}
	if commit.VerifyOwnership(P.Bytes(), theft, ctx) {
		t.Fatal("ownership verified with the wrong secret (theft not prevented)")
	}

	// wrong context (e.g. proof replayed onto a different tx) must fail.
	if commit.VerifyOwnership(P.Bytes(), proof, []byte("different-ctx")) {
		t.Fatal("ownership proof verified under a different context")
	}
}

// ---------------------------------------------------------------------------
// 24. Value-equality (inflation prevention)
// ---------------------------------------------------------------------------

func TestValueEquality(t *testing.T) {
	ctx := []byte("conservation-ctx")
	v := uint64(5000)

	rCommit := commit.RandomScalar()
	rPseudo := commit.RandomScalar()
	C := commit.Commit(v, rCommit)        // on-chain output commitment
	pseudo := commit.Commit(v, rPseudo)   // re-blinded pseudo commitment
	d := new(edwards25519.Scalar).Subtract(rPseudo, rCommit)

	proof, err := commit.ProveValueEquality(pseudo.Bytes(), C.Bytes(), d, ctx)
	if err != nil {
		t.Fatalf("ProveValueEquality: %v", err)
	}
	if !commit.VerifyValueEquality(pseudo.Bytes(), C.Bytes(), proof, ctx) {
		t.Fatal("equal-value proof failed to verify")
	}

	// inflation: pseudo commits to a LARGER value than C. Then pseudo - C =
	// (v2-v)·H + d·G, which is not d·G, so the Schnorr DLog proof cannot be
	// produced honestly with d and verification fails.
	pseudoBig := commit.Commit(v+1000000, rPseudo)
	badProof, err := commit.ProveValueEquality(pseudoBig.Bytes(), C.Bytes(), d, ctx)
	if err != nil {
		t.Fatalf("ProveValueEquality(inflated): %v", err)
	}
	if commit.VerifyValueEquality(pseudoBig.Bytes(), C.Bytes(), badProof, ctx) {
		t.Fatal("value-equality verified an inflated amount")
	}
}

// ---------------------------------------------------------------------------
// 25. Stealth addresses (split into focused subtests, counted as one workflow)
// ---------------------------------------------------------------------------

func TestStealthOwnership(t *testing.T) {
	recipient := commit.NewStealthKeys()
	other := commit.NewStealthKeys()
	out := commit.CreateOutput(recipient.Addr)

	if !recipient.Owns(out) {
		t.Fatal("recipient does not own its own output")
	}
	if other.Owns(out) {
		t.Fatal("a different keypair claims ownership of the output")
	}
}

func TestStealthOneTimeSecret(t *testing.T) {
	recipient := commit.NewStealthKeys()
	out := commit.CreateOutput(recipient.Addr)
	x, err := recipient.OneTimeSecret(out)
	if err != nil {
		t.Fatalf("OneTimeSecret: %v", err)
	}
	// x·G must equal the one-time output public key P.
	xG := new(edwards25519.Point).ScalarBaseMult(x)
	if xG.Equal(out.P) != 1 {
		t.Fatal("OneTimeSecret·G != P")
	}
	// a non-owner cannot extract the secret.
	if _, err := commit.NewStealthKeys().OneTimeSecret(out); err == nil {
		t.Fatal("non-owner extracted a one-time secret")
	}
}

func TestStealthKeysFromSeedDeterministic(t *testing.T) {
	seed := []byte("deterministic-wallet-seed-0001")
	k1 := commit.StealthKeysFromSeed(seed)
	k2 := commit.StealthKeysFromSeed(seed)
	if k1.Addr.A.Equal(k2.Addr.A) != 1 || k1.Addr.B.Equal(k2.Addr.B) != 1 {
		t.Fatal("StealthKeysFromSeed is not deterministic")
	}
	// different seed -> different keys
	k3 := commit.StealthKeysFromSeed([]byte("other-seed"))
	if k1.Addr.A.Equal(k3.Addr.A) == 1 && k1.Addr.B.Equal(k3.Addr.B) == 1 {
		t.Fatal("different seeds produced identical keys")
	}
}

func TestStealthAmountEncryptionRoundTrip(t *testing.T) {
	recipient := commit.NewStealthKeys()
	out := commit.CreateOutput(recipient.Addr)
	// recipient's shared secret a·R must equal sender's r·A — but CreateOutput
	// does not return r, so we exercise the recipient path round-trip.
	shared := recipient.SharedSecret(out)
	amt := uint64(1_234_567_890)
	enc := commit.EncryptAmount(shared, amt)
	if bytes.Equal(enc, []byte{0, 0, 0, 0, 0, 0, 0, 0}) {
		t.Fatal("encrypted amount is all zeros (no encryption applied)")
	}
	if got := commit.DecryptAmount(shared, enc); got != amt {
		t.Fatalf("amount round-trip: got %d want %d", got, amt)
	}
	// wrong shared secret decrypts to garbage (not the amount).
	wrong := commit.NewStealthKeys().SharedSecret(out)
	if got := commit.DecryptAmount(wrong, enc); got == amt {
		t.Fatal("amount decrypted correctly with the wrong shared secret")
	}
}

func TestStealthSharedSecretSenderRecipientMatch(t *testing.T) {
	recipient := commit.NewStealthKeys()
	r := commit.RandomScalar()
	out := commit.CreateOutputDeterministic(recipient.Addr, r)
	senderShared := commit.SharedSecretSender(recipient.Addr, r)
	recipShared := recipient.SharedSecret(out)
	if !bytes.Equal(senderShared, recipShared) {
		t.Fatal("sender r·A != recipient a·R (ECDH mismatch)")
	}
}

func TestStealthAddressEncodeDecode(t *testing.T) {
	k := commit.NewStealthKeys()
	enc := k.Addr.Encode()
	if len(enc) != 96 {
		t.Fatalf("encoded address length = %d, want 96 (A||B||NfPk)", len(enc))
	}
	dec, err := commit.DecodeAddress(enc)
	if err != nil {
		t.Fatalf("DecodeAddress: %v", err)
	}
	if dec.A.Equal(k.Addr.A) != 1 || dec.B.Equal(k.Addr.B) != 1 {
		t.Fatal("address encode/decode round-trip mismatch")
	}
	// malformed length rejected
	if _, err := commit.DecodeAddress(enc[:63]); err == nil {
		t.Fatal("DecodeAddress accepted a 63-byte address")
	}
}
