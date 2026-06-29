// Package swapchain is the end-to-end ON-CHAIN test of the atomic-swap output
// (Block 14): OBX is locked in a swap contract, claimed via a 2-of-2 adapted
// signature (whose publication reveals the swap secret, enabling the Monero
// leg), and the refund path is enforced after the timelock.
package swapchain

import (
	"crypto/sha256"
	"testing"

	"filippo.io/edwards25519"

	"obscura/pkg/commit"
	"obscura/pkg/config"
	"obscura/pkg/swap"
	"obscura/pkg/tx"
	"obscura/tests/critical/harness"
)

func key32(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }

func TestOnChainSwapClaimRevealsSecret(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	bob := harness.NewWallet("swap-bob")   // funds OBX, wants XMR
	alice := harness.NewWallet("swap-alice") // claims OBX

	harness.MineN(t, c, bob, 4)
	harness.ScanAll(c, bob)

	// 2-of-2 claim key K = A + B ; refund key = B (Bob)
	a := commit.RandomScalar()
	b := commit.RandomScalar()
	A := new(edwards25519.Point).ScalarBaseMult(a)
	B := new(edwards25519.Point).ScalarBaseMult(b)
	K := swap.AggregateKey(A, B)

	// swap secret on ed25519 (same curve as Monero) → Monero locked to s_a + s_b
	sA := commit.RandomScalar()
	sB := commit.RandomScalar()
	T := new(edwards25519.Point).ScalarBaseMult(sA)
	xmrKey := new(edwards25519.Point).ScalarBaseMult(new(edwards25519.Scalar).Add(sA, sB))

	swapKey := key32("swap-1")
	amount := uint64(5 * config.AtomicPerCoin)
	fee := uint64(1_000_000_000)

	// Agree the aggregate pre-signature nonce R = Ra+Rb and proofs-of-possession at
	// FUND time (consensus binds the claim signature to R+T and requires K=A+B with
	// a PoP for each share). The SAME nonces are reused for the claim pre-sig.
	ra, rb := commit.RandomScalar(), commit.RandomScalar()
	R := new(edwards25519.Point).Add(
		new(edwards25519.Point).ScalarBaseMult(ra),
		new(edwards25519.Point).ScalarBaseMult(rb),
	)
	popA := swap.ProvePossession(a)
	popB := swap.ProvePossession(b)

	// Bob funds the swap output (claim before height 1000; refund after).
	fund, err := bob.FundSwap(c, swapKey, amount, K.Bytes(), B.Bytes(),
		A.Bytes(), B.Bytes(), popA, popB, R.Bytes(), T.Bytes(), 1000, fee)
	if err != nil {
		t.Fatalf("fund swap: %v", err)
	}
	if err := c.ValidateStandaloneTx(fund); err != nil {
		t.Fatalf("fund rejected: %v", err)
	}
	harness.MineBlock(t, c, bob, []*tx.Transaction{fund})
	if _, ok := c.Swap(swapKey); !ok {
		t.Fatal("swap contract not registered on-chain")
	}

	// Alice claims: she + Bob co-sign an adaptor pre-sig over the claim's
	// CoreHash, Alice adapts with s_a → valid sig under K. Capture pre-sig so Bob
	// can extract s_a afterwards.
	var pre *commit.AdaptorSig
	claim, err := alice.BuildSwapSpend(swapKey, amount, false, fee, func(coreHash []byte) []byte {
		p, _ := swap.CoSignClaim(a, b, ra, rb, coreHash, T)
		pre = p
		full, _ := commit.Adapt(p, sA, T)
		return full.Serialize()
	})
	if err != nil {
		t.Fatalf("build claim: %v", err)
	}
	if err := c.ValidateStandaloneTx(claim); err != nil {
		t.Fatalf("claim rejected by consensus: %v", err)
	}
	harness.MineBlock(t, c, bob, []*tx.Transaction{claim})
	if _, ok := c.Swap(swapKey); ok {
		t.Fatal("swap should be closed after claim")
	}

	// Alice received amount−fee privately.
	harness.ScanAll(c, alice)
	if alice.Balance() != amount-fee {
		t.Fatalf("alice balance = %d, want %d", alice.Balance(), amount-fee)
	}

	// ATOMICITY: Bob extracts s_a from the on-chain claim signature → gets the
	// Monero spend key s_a + s_b.
	fullSig, _ := commit.ParseFullSig(claim.SwapInputs[0].Sig)
	got, err := commit.Extract(pre, fullSig)
	if err != nil {
		t.Fatal(err)
	}
	if got.Equal(sA) != 1 {
		t.Fatal("Bob failed to extract the swap secret from the on-chain claim")
	}
	bobXmr := new(edwards25519.Point).ScalarBaseMult(new(edwards25519.Scalar).Add(got, sB))
	if bobXmr.Equal(xmrKey) != 1 {
		t.Fatal("Bob cannot reconstruct the XMR spend key — swap not atomic")
	}
}

func TestOnChainSwapRefund(t *testing.T) {
	defer harness.SmallMaturity()()
	// F-1 fund-safety: consensus rejects a SwapOut funded with UnlockHeight <
	// fundHeight + SwapReorgMargin (a dead claim window). This refund fixture funds at
	// unlock = height+10, so shrink the margin to keep it fundable; the refund path
	// (the subject of this test) is unaffected by the margin.
	oldMargin := config.SwapReorgMargin
	config.SwapReorgMargin = 2
	defer func() { config.SwapReorgMargin = oldMargin }()
	c := harness.NewChain(t)
	bob := harness.NewWallet("swap-bob2")
	harness.MineN(t, c, bob, 4)
	harness.ScanAll(c, bob)

	b := commit.RandomScalar()
	B := new(edwards25519.Point).ScalarBaseMult(b)
	a := commit.RandomScalar()
	A := new(edwards25519.Point).ScalarBaseMult(a)
	K := swap.AggregateKey(A, B)

	swapKey := key32("swap-refund")
	amount := uint64(3 * config.AtomicPerCoin)
	fee := uint64(1_000_000_000)
	unlock := c.Height() + 10 // refund allowed only well in the future

	// Funding still requires valid claim-binding fields even though this swap is
	// refunded (never claimed): K=A+B with PoPs, R and a non-identity T.
	ra, rb := commit.RandomScalar(), commit.RandomScalar()
	R := new(edwards25519.Point).Add(
		new(edwards25519.Point).ScalarBaseMult(ra),
		new(edwards25519.Point).ScalarBaseMult(rb),
	)
	T := new(edwards25519.Point).ScalarBaseMult(commit.RandomScalar())
	fund, err := bob.FundSwap(c, swapKey, amount, K.Bytes(), B.Bytes(),
		A.Bytes(), B.Bytes(), swap.ProvePossession(a), swap.ProvePossession(b), R.Bytes(), T.Bytes(), unlock, fee)
	if err != nil {
		t.Fatalf("fund: %v", err)
	}
	harness.MineBlock(t, c, bob, []*tx.Transaction{fund})

	// Build a refund signed by Bob's refund key.
	refund, err := bob.BuildSwapSpend(swapKey, amount, true, fee, func(coreHash []byte) []byte {
		return commit.Sign(b, coreHash).Serialize()
	})
	if err != nil {
		t.Fatalf("build refund: %v", err)
	}

	// Before the unlock height the refund must be REJECTED.
	if err := c.ValidateStandaloneTx(refund); err == nil {
		t.Fatal("refund accepted before unlock height")
	}
	// Mine until the unlock height, then it must be accepted.
	for c.Height()+1 < unlock {
		harness.MineBlock(t, c, bob, nil)
	}
	// rebuild the refund at the new height (CoreHash unchanged; height-gated only)
	if err := c.ValidateStandaloneTx(refund); err != nil {
		t.Fatalf("valid refund rejected at unlock height: %v", err)
	}
}
