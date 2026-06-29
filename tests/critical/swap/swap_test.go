package swap

import (
	"testing"

	"filippo.io/edwards25519"

	"obscura/pkg/commit"
	"obscura/pkg/config"
	"obscura/pkg/swap"
)

// TestEndToEndAtomicSwap simulates a full trustless XMR↔Obscura swap on the
// Obscura side: Bob locks OBX in a swap output (claim key = 2-of-2 A+B), the
// taker Alice claims it with an adapted signature, and the published signature
// reveals Alice's swap secret s_a to Bob — who then has s_a + s_b and can spend
// the Monero (modeled by checking (s_a+s_b)·G equals the XMR lock key).
func TestEndToEndAtomicSwap(t *testing.T) {
	defer setMargin(10)() // claim at h=50 with unlock 100: 50+10 <= 100 ok
	// swap secrets (ed25519 scalars; SAME curve as Monero → no cross-curve DLEQ)
	sA := commit.RandomScalar() // Alice's secret (the adaptor secret)
	sB := commit.RandomScalar() // Bob's secret
	T := new(edwards25519.Point).ScalarBaseMult(sA) // adaptor point S_a
	// the Monero output is locked to spend key s_a + s_b:
	xmrKey := new(edwards25519.Point).ScalarBaseMult(new(edwards25519.Scalar).Add(sA, sB))

	// 2-of-2 keys controlling the OBX swap-output claim path
	a, b := commit.RandomScalar(), commit.RandomScalar()
	A := new(edwards25519.Point).ScalarBaseMult(a)
	B := new(edwards25519.Point).ScalarBaseMult(b)
	K := swap.AggregateKey(A, B)

	claimMsg := []byte("alice claims the OBX swap output")

	// Both parties co-sign an aggregate adaptor pre-signature bound to T=s_a·G.
	// The nonces (R=Ra+Rb) and adaptor point T are committed into the swap output
	// at fund time so the claim path can enforce sig.R == R+T (atomicity binding).
	ra, rb := commit.RandomScalar(), commit.RandomScalar()
	pre, gotK := swap.CoSignClaim(a, b, ra, rb, claimMsg, T)
	if gotK.Equal(K) != 1 {
		t.Fatal("aggregate key mismatch")
	}
	if !commit.PreVerify(K.Bytes(), claimMsg, T, pre) {
		t.Fatal("aggregate adaptor pre-signature failed to verify")
	}
	out := swap.SwapOutput{ClaimKey: K.Bytes(), RefundKey: B.Bytes(), UnlockHeight: 100, ClaimR: pre.R, ClaimT: T.Bytes()}

	// Alice adapts with her secret s_a → a valid full signature → claims OBX.
	full, err := commit.Adapt(pre, sA, T)
	if err != nil {
		t.Fatal(err)
	}
	if !out.VerifyClaim(50, claimMsg, full) { // height 50 < unlock 100
		t.Fatal("OBX claim rejected before unlock height")
	}

	// ATOMICITY: Bob sees the published claim signature and extracts s_a.
	recovered, err := commit.Extract(pre, full)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Equal(sA) != 1 {
		t.Fatal("Bob failed to extract s_a from the on-chain claim")
	}
	// Bob now knows s_a + s_b and can spend the Monero output.
	bobXmrKey := new(edwards25519.Point).ScalarBaseMult(new(edwards25519.Scalar).Add(recovered, sB))
	if bobXmrKey.Equal(xmrKey) != 1 {
		t.Fatal("Bob cannot reconstruct the XMR spend key (swap not atomic)")
	}
}

// TestSwapRefundPath: if the claim never happens, the funder refunds after the
// timelock; the claim path is closed after unlock and the refund path before it.
func TestSwapRefundPath(t *testing.T) {
	b := commit.RandomScalar()
	B := new(edwards25519.Point).ScalarBaseMult(b)
	a := commit.RandomScalar()
	K := swap.AggregateKey(new(edwards25519.Point).ScalarBaseMult(a), B)
	out := swap.SwapOutput{ClaimKey: K.Bytes(), RefundKey: B.Bytes(), UnlockHeight: 100}

	msg := []byte("bob refunds")
	// Bob signs a normal Schnorr sig under RefundKey B (full sig with R=r·G).
	r := commit.RandomScalar()
	R := new(edwards25519.Point).ScalarBaseMult(r)
	e := commit.AdaptorChallenge(R, B, msg) // same challenge form, T=0 ⇒ plain Schnorr
	s := new(edwards25519.Scalar).Add(r, new(edwards25519.Scalar).Multiply(e, b))
	sig := &commit.FullSig{R: R.Bytes(), S: s.Bytes()}

	if out.VerifyRefund(50, msg, sig) {
		t.Fatal("refund accepted before unlock height")
	}
	if !out.VerifyRefund(100, msg, sig) {
		t.Fatal("valid refund rejected at unlock height")
	}
	// claim path must be closed after the unlock height
	if out.VerifyClaim(100, msg, sig) {
		t.Fatal("claim accepted at/after unlock height")
	}
}

// TestRogueKeyClaimRejected is the regression for the HIGH audit finding: a
// claimer who picks a rogue share A' = rogue − B so that K = A'+B = rogue (a key
// they control alone) cannot register the swap, because consensus requires a
// proof-of-possession for each contributed share (AggregateKeyVerified). Even if
// the rogue could somehow satisfy the aggregate, the claim binding (sig.R must
// equal R+T) forces the claim to be the ADAPTED pre-signature, which reveals the
// adaptor secret — defeating the silent-theft path.
func TestRogueKeyClaimRejected(t *testing.T) {
	defer setMargin(10)() // claim checked at h=50 with unlock 100
	// Honest counterparty share B (with a valid PoP).
	bScalar := commit.RandomScalar()
	B := new(edwards25519.Point).ScalarBaseMult(bScalar)
	popB := swap.ProvePossession(bScalar)

	// Attacker wants the aggregate to be a key 'rogue' they alone control:
	//   K = A' + B = rogue   ⇒   A' = rogue − B.
	rogue := commit.RandomScalar()
	rogueP := new(edwards25519.Point).ScalarBaseMult(rogue)
	Aprime := new(edwards25519.Point).Subtract(rogueP, B) // cancelling share

	// 1) The attacker CANNOT forge a proof-of-possession for A' (they do not know
	//    its discrete log), so AggregateKeyVerified rejects it.
	fakePoP := swap.ProvePossession(commit.RandomScalar()) // PoP for an unrelated key
	if swap.AggregateKeyVerified(Aprime.Bytes(), B.Bytes(), fakePoP, popB) != nil {
		t.Fatal("rogue cancelling share accepted despite invalid proof-of-possession")
	}
	// A real PoP for A' would require knowing its discrete log = rogue − b, which
	// the attacker does not have (b is the honest party's secret). If they DID know
	// it, they would equivalently know the aggregate's discrete log AND would have
	// to use it via the bound pre-signature — see the binding check below.

	// 2) Honest aggregation with valid PoPs succeeds.
	aScalar := commit.RandomScalar()
	A := new(edwards25519.Point).ScalarBaseMult(aScalar)
	K := swap.AggregateKeyVerified(A.Bytes(), B.Bytes(), swap.ProvePossession(aScalar), popB)
	if K == nil || K.Equal(swap.AggregateKey(A, B)) != 1 {
		t.Fatal("honest 2-of-2 aggregation with valid PoPs rejected")
	}

	// 3) Atomicity binding: a fresh INDEPENDENT signature under K (not adapted from
	//    the committed pre-signature) has an unbound nonce and must be rejected.
	tSecret := commit.RandomScalar()
	T := new(edwards25519.Point).ScalarBaseMult(tSecret)
	ra, rb := commit.RandomScalar(), commit.RandomScalar()
	msg := []byte("claim")
	pre, _ := swap.CoSignClaim(aScalar, bScalar, ra, rb, msg, T)
	out := swap.SwapOutput{ClaimKey: K.Bytes(), RefundKey: B.Bytes(), UnlockHeight: 100, ClaimR: pre.R, ClaimT: T.Bytes()}

	// An independent signature with a fresh nonce verifies under K but is NOT bound.
	indep := commit.Sign(new(edwards25519.Scalar).Add(aScalar, bScalar), msg) // a "rogue" full sig under K
	if out.VerifyClaim(50, msg, indep) {
		t.Fatal("unbound independent claim signature accepted — atomicity binding broken")
	}
	// The properly ADAPTED pre-signature (nonce R+T) is accepted, and extracting from
	// it recovers the adaptor secret (atomicity preserved).
	full, err := commit.Adapt(pre, tSecret, T)
	if err != nil {
		t.Fatal(err)
	}
	if !out.VerifyClaim(50, msg, full) {
		t.Fatal("bound adapted claim signature rejected")
	}
	got, _ := commit.Extract(pre, full)
	if got.Equal(tSecret) != 1 {
		t.Fatal("adapted claim did not reveal the adaptor secret")
	}
}

// setMargin temporarily overrides config.SwapReorgMargin and returns a restore func.
func setMargin(m uint64) func() {
	old := config.SwapReorgMargin
	config.SwapReorgMargin = m
	return func() { config.SwapReorgMargin = old }
}

// TestSwapReorgMarginDeadZone is the #11 regression: the claim and refund windows
// MUST be DISJOINT with a margin-wide dead-zone between them, so a reorg of depth
// <= margin at the boundary cannot make BOTH a claim and a refund valid for the
// same swap. With UnlockHeight U and margin M the invariant is:
//
//	CLAIM  valid iff  height + M <= U   (height <= U-M)
//	REFUND valid iff  height     >= U
//
// leaving [U-M, U) as a dead-zone where NEITHER is valid.
func TestSwapReorgMarginDeadZone(t *testing.T) {
	const M = 10
	defer setMargin(M)()

	// Build a real claimable swap output (adapted pre-signature) and a real refund sig.
	sA := commit.RandomScalar()
	T := new(edwards25519.Point).ScalarBaseMult(sA)
	a, b := commit.RandomScalar(), commit.RandomScalar()
	B := new(edwards25519.Point).ScalarBaseMult(b)
	K := swap.AggregateKey(new(edwards25519.Point).ScalarBaseMult(a), B)
	msg := []byte("dead-zone")
	ra, rb := commit.RandomScalar(), commit.RandomScalar()
	pre, _ := swap.CoSignClaim(a, b, ra, rb, msg, T)
	claimSig, err := commit.Adapt(pre, sA, T)
	if err != nil {
		t.Fatal(err)
	}
	const U = 100
	out := swap.SwapOutput{ClaimKey: K.Bytes(), RefundKey: B.Bytes(), UnlockHeight: U, ClaimR: pre.R, ClaimT: T.Bytes()}

	// A valid refund signature under RefundKey B (plain Schnorr, T=0).
	r := commit.RandomScalar()
	R := new(edwards25519.Point).ScalarBaseMult(r)
	e := commit.AdaptorChallenge(R, B, msg)
	s := new(edwards25519.Scalar).Add(r, new(edwards25519.Scalar).Multiply(e, b))
	refundSig := &commit.FullSig{R: R.Bytes(), S: s.Bytes()}

	// 1) Claim valid up to and including U-M; refund invalid there.
	if !out.VerifyClaim(U-M, msg, claimSig) {
		t.Fatalf("claim should be valid at the claim deadline height %d", U-M)
	}
	if out.VerifyRefund(U-M, msg, refundSig) {
		t.Fatalf("refund must be rejected at %d (< unlock %d)", U-M, U)
	}

	// 2) DEAD-ZONE [U-M, U): a claim within the margin of unlock is rejected, AND so
	//    is a refund. For EVERY height in the dead-zone, NEITHER path is valid.
	for h := uint64(U - M + 1); h < U; h++ {
		if out.VerifyClaim(h, msg, claimSig) {
			t.Fatalf("claim accepted within reorg margin of unlock (height %d, unlock %d)", h, U)
		}
		if out.VerifyRefund(h, msg, refundSig) {
			t.Fatalf("refund accepted before unlock height (height %d, unlock %d)", h, U)
		}
	}

	// 3) At/after U: refund valid, claim invalid.
	if !out.VerifyRefund(U, msg, refundSig) {
		t.Fatalf("refund should be valid at unlock height %d", U)
	}
	if out.VerifyClaim(U, msg, claimSig) {
		t.Fatalf("claim must be rejected at unlock height %d", U)
	}

	// 4) DISJOINTNESS: no single height makes both valid (the property #11 fixes).
	for h := uint64(0); h <= U+M; h++ {
		if out.VerifyClaim(h, msg, claimSig) && out.VerifyRefund(h, msg, refundSig) {
			t.Fatalf("claim and refund BOTH valid at height %d — reorg margin broken", h)
		}
	}
}
