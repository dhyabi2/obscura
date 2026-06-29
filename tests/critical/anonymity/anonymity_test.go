// Package anonymity tests the Triptych-style linkable one-out-of-many proof
// (Block 2 — sender anonymity). See docs/INVENTION_ANONYMITY.md.
package anonymity

import (
	"testing"

	"filippo.io/edwards25519"

	"obscura/pkg/commit"
)

// ring builds N coin keys C_i = x_i·G and returns the keys + secrets.
func ring(n int) ([]*edwards25519.Point, []*edwards25519.Scalar) {
	keys := make([]*edwards25519.Point, n)
	secs := make([]*edwards25519.Scalar, n)
	for i := 0; i < n; i++ {
		secs[i] = commit.RandomScalar()
		keys[i] = new(edwards25519.Point).ScalarBaseMult(secs[i])
	}
	return keys, secs
}

// TestCompleteness: an honest spend of any index verifies.
func TestCompleteness(t *testing.T) {
	for _, n := range []int{2, 4, 8, 16} {
		keys, secs := ring(n)
		for l := 0; l < n; l++ {
			pf, tag, err := commit.ProveOneOfMany(keys, l, secs[l], []byte("tx"))
			if err != nil {
				t.Fatalf("n=%d l=%d prove: %v", n, l, err)
			}
			if !commit.VerifyOneOfMany(keys, tag, pf, []byte("tx")) {
				t.Fatalf("n=%d l=%d honest proof rejected", n, l)
			}
		}
	}
}

// TestSoundnessNoSecret: you cannot prove membership for a key you don't own.
func TestSoundnessNoSecret(t *testing.T) {
	keys, _ := ring(8)
	wrong := commit.RandomScalar() // not the opening of any key
	pf, tag, err := commit.ProveOneOfMany(keys, 3, wrong, []byte("tx"))
	if err != nil {
		t.Fatalf("prove: %v", err)
	}
	if commit.VerifyOneOfMany(keys, tag, pf, []byte("tx")) {
		t.Fatal("SOUNDNESS BROKEN: proof with wrong secret accepted")
	}
}

// TestForeignRingRejected: a valid proof does not verify against a different ring.
func TestForeignRingRejected(t *testing.T) {
	keys, secs := ring(8)
	pf, tag, _ := commit.ProveOneOfMany(keys, 2, secs[2], []byte("tx"))
	other, _ := ring(8)
	if commit.VerifyOneOfMany(other, tag, pf, []byte("tx")) {
		t.Fatal("proof verified against a foreign ring")
	}
}

// TestContextBinding: proof is bound to its tx context.
func TestContextBinding(t *testing.T) {
	keys, secs := ring(8)
	pf, tag, _ := commit.ProveOneOfMany(keys, 5, secs[5], []byte("tx-A"))
	if commit.VerifyOneOfMany(keys, tag, pf, []byte("tx-B")) {
		t.Fatal("proof not bound to context")
	}
}

// TestTagUniquenessAndLinkability: the tag is deterministic per coin (same key →
// same tag, enabling double-spend detection) and differs across coins.
func TestTagUniquenessAndLinkability(t *testing.T) {
	keys, secs := ring(8)
	_, tag1, _ := commit.ProveOneOfMany(keys, 4, secs[4], []byte("tx1"))
	_, tag2, _ := commit.ProveOneOfMany(keys, 4, secs[4], []byte("tx2"))
	if tag1.Equal(tag2) != 1 {
		t.Fatal("same coin produced different tags (double-spend would be undetectable)")
	}
	_, tag3, _ := commit.ProveOneOfMany(keys, 5, secs[5], []byte("tx1"))
	if tag1.Equal(tag3) == 1 {
		t.Fatal("different coins produced the same tag (false double-spend)")
	}
}

// TestForgedTagRejected: substituting a different tag fails the binding check.
func TestForgedTagRejected(t *testing.T) {
	keys, secs := ring(8)
	pf, _, _ := commit.ProveOneOfMany(keys, 1, secs[1], []byte("tx"))
	forged := new(edwards25519.Point).ScalarMult(commit.RandomScalar(), commit.U())
	if commit.VerifyOneOfMany(keys, forged, pf, []byte("tx")) {
		t.Fatal("forged tag accepted (key-image binding broken)")
	}
}

// TestIndexHiding: proofs for different spent indices are structurally
// indistinguishable (same sizes); a verifier cannot tell which index was spent.
func TestIndexHiding(t *testing.T) {
	keys, secs := ring(8)
	pfA, _, _ := commit.ProveOneOfMany(keys, 0, secs[0], []byte("tx"))
	pfB, _, _ := commit.ProveOneOfMany(keys, 7, secs[7], []byte("tx"))
	// Same number of elements regardless of index (log-size, index-independent).
	if len(pfA.Cl) != len(pfB.Cl) || len(pfA.Cd) != len(pfB.Cd) || len(pfA.F) != len(pfB.F) {
		t.Fatal("proof shape leaks the index")
	}
}

// TestLogSize: proof element count grows logarithmically with the ring size.
func TestLogSize(t *testing.T) {
	k8, s8 := ring(8)
	k16, s16 := ring(16)
	p8, _, _ := commit.ProveOneOfMany(k8, 1, s8[1], []byte("tx"))
	p16, _, _ := commit.ProveOneOfMany(k16, 1, s16[1], []byte("tx"))
	if len(p8.Cl) != 3 || len(p16.Cl) != 4 { // log2(8)=3, log2(16)=4
		t.Fatalf("not log-size: m8=%d m16=%d", len(p8.Cl), len(p16.Cl))
	}
}

// TestNonPowerOfTwoRejected: ring sizes must be powers of two.
func TestNonPowerOfTwoRejected(t *testing.T) {
	keys, secs := ring(8)
	if _, _, err := commit.ProveOneOfMany(keys[:6], 1, secs[1], []byte("tx")); err == nil {
		t.Fatal("expected error for non-power-of-two ring")
	}
}
