// Package swap tests the adaptor-signature cornerstone of trustless XMR<->Obscura
// atomic swaps (Block 12): a pre-signature verifies without the secret, can be
// adapted into a valid signature with the secret, and publishing the signature
// reveals the secret (atomicity). See docs/INVENTION_SWAPS.md.
package swap

import (
	"testing"

	"filippo.io/edwards25519"

	"obscura/pkg/commit"
)

// TestAdaptorRejectsIdentityPoint is the regression for the audit finding that an
// identity adaptor point T collapses the pre-signature into a plain signature
// (so a plain Sign would PreVerify), defeating swap atomicity. PreSign must
// refuse, and PreVerify must reject T = identity.
func TestAdaptorRejectsIdentityPoint(t *testing.T) {
	x := commit.RandomScalar()
	P := new(edwards25519.Point).ScalarBaseMult(x).Bytes()
	msg := []byte("swap")
	id := edwards25519.NewIdentityPoint()

	if commit.PreSign(x, msg, id) != nil {
		t.Fatal("PreSign accepted an identity adaptor point")
	}
	plain := commit.Sign(x, msg) // a plain signature
	if commit.PreVerify(P, msg, id, &commit.AdaptorSig{R: plain.R, S: plain.S}) {
		t.Fatal("PreVerify accepted a plain signature under identity T (atomicity broken)")
	}
}

func TestAdaptorSwapFlow(t *testing.T) {
	x := commit.RandomScalar() // signing key (e.g. controls the swap output)
	P := new(edwards25519.Point).ScalarBaseMult(x).Bytes()
	tt := commit.RandomScalar() // the swap secret
	T := new(edwards25519.Point).ScalarBaseMult(tt)
	msg := []byte("spend the swap output")

	// 1) maker pre-signs, bound to T
	pre := commit.PreSign(x, msg, T)
	if !commit.PreVerify(P, msg, T, pre) {
		t.Fatal("pre-signature failed to verify")
	}
	// a pre-signature is NOT a valid full signature on its own
	if commit.VerifyFull(P, msg, &commit.FullSig{R: pre.R, S: pre.S}) {
		t.Fatal("pre-signature wrongly accepted as a full signature")
	}

	// 2) holder of t adapts it into a valid signature (this is the on-chain spend)
	full, err := commit.Adapt(pre, tt, T)
	if err != nil {
		t.Fatal(err)
	}
	if !commit.VerifyFull(P, msg, full) {
		t.Fatal("adapted signature did not verify")
	}

	// 3) ATOMICITY: from the pre-sig + the published full sig, anyone extracts t
	got, err := commit.Extract(pre, full)
	if err != nil {
		t.Fatal(err)
	}
	if got.Equal(tt) != 1 {
		t.Fatal("extracted secret != t (swap atomicity broken)")
	}
	// the extracted secret really is the discrete log of T
	if new(edwards25519.Point).ScalarBaseMult(got).Equal(T) != 1 {
		t.Fatal("extracted t·G != T")
	}
}

func TestAdaptorWrongSecretFails(t *testing.T) {
	x := commit.RandomScalar()
	P := new(edwards25519.Point).ScalarBaseMult(x).Bytes()
	tt := commit.RandomScalar()
	T := new(edwards25519.Point).ScalarBaseMult(tt)
	msg := []byte("m")
	pre := commit.PreSign(x, msg, T)

	// adapting with the WRONG secret yields an invalid signature
	wrong := commit.RandomScalar()
	bad, _ := commit.Adapt(pre, wrong, T)
	if commit.VerifyFull(P, msg, bad) {
		t.Fatal("adapting with wrong secret produced a valid signature")
	}
}

func TestAdaptorPreSigBoundToT(t *testing.T) {
	x := commit.RandomScalar()
	P := new(edwards25519.Point).ScalarBaseMult(x).Bytes()
	T := new(edwards25519.Point).ScalarBaseMult(commit.RandomScalar())
	otherT := new(edwards25519.Point).ScalarBaseMult(commit.RandomScalar())
	msg := []byte("m")
	pre := commit.PreSign(x, msg, T)
	// verifying against a different adaptor point must fail (binding)
	if commit.PreVerify(P, msg, otherT, pre) {
		t.Fatal("pre-signature not bound to its adaptor point T")
	}
}
