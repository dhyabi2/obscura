// Package swapd_test is the END-TO-END cross-chain swap test (Block 16): a full
// trustless XMR↔Obscura atomic swap settles both legs — the OBX leg on the real
// Obscura chain, the XMR leg on a mock Monero — with atomicity enforced by the
// adaptor secret-reveal. See docs/INVENTION_SWAPS.md.
package swapd_test

import (
	"testing"

	"filippo.io/edwards25519"

	"obscura/pkg/commit"
	"obscura/pkg/config"
	"obscura/pkg/swap"
	"obscura/pkg/swapd"
	"obscura/pkg/tx"
	"obscura/tests/critical/harness"
)

func pt(s *edwards25519.Scalar) *edwards25519.Point { return new(edwards25519.Point).ScalarBaseMult(s) }

// TestCrossChainSwapHappyPath: Alice has XMR + wants OBX; Bob has OBX + wants XMR.
// Both settle atomically.
func TestCrossChainSwapHappyPath(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	bob := harness.NewWallet("xc-bob")     // funds OBX, receives XMR
	alice := harness.NewWallet("xc-alice") // locks XMR, receives OBX
	harness.MineN(t, c, bob, 4)            // Bob earns OBX to swap
	harness.ScanAll(c, bob)

	mxmr := swapd.NewMockMonero()
	const xmrAmount = uint64(2_000_000)
	obxAmount := uint64(5 * config.AtomicPerCoin)
	fee := uint64(1_000_000_000)

	// --- setup: swap secrets (ed25519; XMR locked to s_a + s_b) ---
	sA := commit.RandomScalar() // Alice's secret (adaptor)
	sB := commit.RandomScalar() // Bob's secret
	T := pt(sA)
	xmrPub, err := swapd.XMRSpendPub(pt(sA).Bytes(), pt(sB).Bytes())
	if err != nil {
		t.Fatal(err)
	}

	// 2-of-2 OBX claim key K = A + B ; refund key = B (Bob)
	a, b := commit.RandomScalar(), commit.RandomScalar()
	K := swap.AggregateKey(pt(a), pt(b))

	// 1) Alice locks her XMR to S_a + S_b on Monero.
	lockID, err := mxmr.Lock(xmrAmount, xmrPub)
	if err != nil {
		t.Fatal(err)
	}

	// 2) Bob waits for the XMR lock to confirm, then funds the OBX swap output.
	if !mxmr.Confirmed(lockID) {
		t.Fatal("xmr lock not confirmed")
	}
	swapKey := func() []byte { h := commit.RandomScalar().Bytes(); return h[:32] }()
	// Nonces (R=Ra+Rb) and PoPs are committed at fund time; reused for the claim.
	ra, rb := commit.RandomScalar(), commit.RandomScalar()
	R := new(edwards25519.Point).Add(pt(ra), pt(rb))
	fund, err := bob.FundSwap(c, swapKey, obxAmount, K.Bytes(), pt(b).Bytes(),
		pt(a).Bytes(), pt(b).Bytes(), swap.ProvePossession(a), swap.ProvePossession(b), R.Bytes(), T.Bytes(), 10_000, fee)
	if err != nil {
		t.Fatalf("fund OBX swap: %v", err)
	}
	harness.MineBlock(t, c, bob, []*tx.Transaction{fund})

	// 3) Alice claims the OBX (2-of-2 adapted with s_a). Capture the pre-sig so
	// Bob can extract s_a from the published claim.
	var pre *commit.AdaptorSig
	claim, err := alice.BuildSwapSpend(swapKey, obxAmount, false, fee, func(coreHash []byte) []byte {
		p, _ := swap.CoSignClaim(a, b, ra, rb, coreHash, T)
		pre = p
		full, _ := commit.Adapt(p, sA, T)
		return full.Serialize()
	})
	if err != nil {
		t.Fatalf("build claim: %v", err)
	}
	harness.MineBlock(t, c, bob, []*tx.Transaction{claim})
	harness.ScanAll(c, alice)
	if alice.Balance() != obxAmount-fee {
		t.Fatalf("alice OBX = %d, want %d", alice.Balance(), obxAmount-fee)
	}

	// 4) Bob extracts s_a from the on-chain claim, forms s_a + s_b, sweeps XMR.
	fullSig, _ := commit.ParseFullSig(claim.SwapInputs[0].Sig)
	recovered, err := commit.Extract(pre, fullSig)
	if err != nil {
		t.Fatal(err)
	}
	spendSecret := new(edwards25519.Scalar).Add(recovered, sB)
	if err := mxmr.Sweep(lockID, spendSecret, "bob-xmr"); err != nil {
		t.Fatalf("bob could not sweep XMR: %v", err)
	}

	// --- both legs settled atomically ---
	if mxmr.Balance("bob-xmr") != xmrAmount {
		t.Fatalf("bob XMR = %d, want %d", mxmr.Balance("bob-xmr"), xmrAmount)
	}
	t.Logf("SWAP COMPLETE: Alice +%s OBX, Bob +%d XMR (atomic)", config.FormatAmount(alice.Balance()), mxmr.Balance("bob-xmr"))
}

// TestXMRSweepNeedsBothSecrets: the XMR cannot be swept without the full spend
// key s_a + s_b — knowing only one share fails (this is what makes the lock safe
// before the swap reveals the other share).
func TestXMRSweepNeedsBothSecrets(t *testing.T) {
	mxmr := swapd.NewMockMonero()
	sA := commit.RandomScalar()
	sB := commit.RandomScalar()
	xmrPub, _ := swapd.XMRSpendPub(pt(sA).Bytes(), pt(sB).Bytes())
	id, _ := mxmr.Lock(1000, xmrPub)

	if mxmr.Sweep(id, sA, "x") == nil {
		t.Fatal("swept XMR with only s_a")
	}
	if mxmr.Sweep(id, sB, "x") == nil {
		t.Fatal("swept XMR with only s_b")
	}
	full := new(edwards25519.Scalar).Add(sA, sB)
	if err := mxmr.Sweep(id, full, "x"); err != nil {
		t.Fatalf("legit sweep failed: %v", err)
	}
	if mxmr.Balance("x") != 1000 {
		t.Fatal("balance not credited")
	}
}
