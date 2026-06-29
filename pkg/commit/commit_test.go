package commit

import (
	"testing"

	"filippo.io/edwards25519"
)

func TestPedersenHomomorphic(t *testing.T) {
	r1, r2 := RandomScalar(), RandomScalar()
	c1 := Commit(100, r1)
	c2 := Commit(250, r2)
	sum := new(edwards25519.Point).Add(c1, c2)
	rSum := new(edwards25519.Scalar).Add(r1, r2)
	expected := Commit(350, rSum)
	if sum.Equal(expected) != 1 {
		t.Fatal("pedersen not homomorphic")
	}
}

func TestHGeneratorDistinct(t *testing.T) {
	if H().Equal(G()) == 1 {
		t.Fatal("H must differ from G")
	}
	if H().Equal(edwards25519.NewIdentityPoint()) == 1 {
		t.Fatal("H must not be identity")
	}
}

func TestRangeProofValid(t *testing.T) {
	for _, v := range []uint64{0, 1, 2, 255, 1 << 20, 1<<63 + 7, ^uint64(0)} {
		C, _, proof, err := ProveRange(v)
		if err != nil {
			t.Fatalf("prove v=%d: %v", v, err)
		}
		if !VerifyRange(C, proof) {
			t.Fatalf("verify failed for v=%d", v)
		}
	}
}

func TestRangeProofTamper(t *testing.T) {
	C, _, proof, _ := ProveRange(42)
	// tamper a bit commitment
	proof.BitComms[0] = Commit(999, RandomScalar()).Bytes()
	if VerifyRange(C, proof) {
		t.Fatal("tampered range proof accepted")
	}
}

func TestConservationProof(t *testing.T) {
	// inputs commit to 300+200, outputs to 450, fee 50 -> balances
	rin1, rin2 := RandomScalar(), RandomScalar()
	rout := RandomScalar()
	cin1 := Commit(300, rin1)
	cin2 := Commit(200, rin2)
	cout := Commit(450, rout)
	fee := uint64(50)

	// residual = cin1 + cin2 - cout - fee*H  should equal z*G with
	// z = rin1 + rin2 - rout
	feeH := new(edwards25519.Point).ScalarMult(ScalarFromUint64(fee), hPoint)
	residual := new(edwards25519.Point).Add(cin1, cin2)
	residual.Subtract(residual, cout)
	residual.Subtract(residual, feeH)

	z := new(edwards25519.Scalar).Add(rin1, rin2)
	z.Subtract(z, rout)

	// check residual == z*G
	if new(edwards25519.Point).ScalarBaseMult(z).Equal(residual) != 1 {
		t.Fatal("conservation residual mismatch")
	}
	pf := ProveDLog(residual, z, []byte("tx"))
	if !VerifyDLog(residual, pf, []byte("tx")) {
		t.Fatal("conservation proof failed")
	}
	// imbalanced tx: outputs 500 (too much) -> residual not a pure G multiple,
	// prover cannot know its dlog, and a wrong z fails verification.
	if VerifyDLog(residual, pf, []byte("different-ctx")) {
		t.Fatal("conservation proof should be context-bound")
	}
}

func TestStealthAddress(t *testing.T) {
	recipient := NewStealthKeys()
	other := NewStealthKeys()

	out := CreateOutput(recipient.Addr)
	if !recipient.Owns(out) {
		t.Fatal("recipient should own output")
	}
	if other.Owns(out) {
		t.Fatal("other should not own output")
	}
	x, err := recipient.OneTimeSecret(out)
	if err != nil {
		t.Fatalf("derive secret: %v", err)
	}
	// x·G must equal P
	if new(edwards25519.Point).ScalarBaseMult(x).Equal(out.P) != 1 {
		t.Fatal("one-time secret does not match output key")
	}
}

// TestOwnershipProofPreventsTheft: only the holder of x (with P = x·G) can
// produce a valid ownership proof — an attacker who doesn't know x cannot.
func TestOwnershipProofPreventsTheft(t *testing.T) {
	x := RandomScalar()
	P := new(edwards25519.Point).ScalarBaseMult(x).Bytes()
	ctx := []byte("tx-core-hash")

	good, err := ProveOwnership(P, x, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyOwnership(P, good, ctx) {
		t.Fatal("legit ownership proof rejected")
	}
	// attacker doesn't know x: any other scalar fails
	attacker := RandomScalar()
	forged, _ := ProveOwnership(P, attacker, ctx)
	if VerifyOwnership(P, forged, ctx) {
		t.Fatal("THEFT: ownership proof forged without the secret key")
	}
	// proof is context-bound (can't be replayed into another tx)
	if VerifyOwnership(P, good, []byte("different-tx")) {
		t.Fatal("ownership proof not bound to tx context")
	}
}

// TestValueEqualityPreventsInflation: the equality proof only verifies when the
// pseudo-commitment commits to the SAME value as the real output commitment, so
// a spender cannot inflate the spent amount.
func TestValueEqualityPreventsInflation(t *testing.T) {
	v := uint64(100)
	r := RandomScalar()
	real := Commit(v, r).Bytes()

	// honest: pseudo commits to the same value with fresh blinding
	rp := RandomScalar()
	pseudo := Commit(v, rp).Bytes()
	d := new(edwards25519.Scalar).Subtract(rp, r)
	eq, err := ProveValueEquality(pseudo, real, d, []byte("ctx"))
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyValueEquality(pseudo, real, eq, []byte("ctx")) {
		t.Fatal("honest equality proof rejected")
	}

	// attacker: pseudo commits to an INFLATED value; no d makes it verify
	rp2 := RandomScalar()
	inflated := Commit(v+1_000_000, rp2).Bytes()
	d2 := new(edwards25519.Scalar).Subtract(rp2, r)
	forged, _ := ProveValueEquality(inflated, real, d2, []byte("ctx"))
	if VerifyValueEquality(inflated, real, forged, []byte("ctx")) {
		t.Fatal("INFLATION: equality proof accepted a value mismatch")
	}
}

func TestStealthFromSeed(t *testing.T) {
	seed := []byte("0123456789abcdef0123456789abcdef")
	k1 := StealthKeysFromSeed(seed)
	k2 := StealthKeysFromSeed(seed)
	if k1.Addr.A.Equal(k2.Addr.A) != 1 || k1.Addr.B.Equal(k2.Addr.B) != 1 {
		t.Fatal("seed derivation not deterministic")
	}
}
