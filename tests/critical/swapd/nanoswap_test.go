// Package swapd_test, XNO leg: an end-to-end trustless Nano↔Obscura atomic swap.
// Nano shares Obscura's ed25519 curve, so this is the SAME scriptless construction
// as the Monero swap; the refund is anchored on the OBX leg's timelock (Nano has
// none of its own). See docs/INVENTION_CROSSCHAIN_SWAPS.md.
package swapd_test

import (
	"math/big"
	"testing"

	"filippo.io/edwards25519"

	"obscura/pkg/commit"
	"obscura/pkg/config"
	"obscura/pkg/swap"
	"obscura/pkg/swapd"
	"obscura/pkg/tx"
	"obscura/tests/critical/harness"
)

// TestNanoSwapHappyPath: Alice has XNO + wants OBX; Bob has OBX + wants XNO. Both
// settle atomically via the adaptor secret reveal — identical to the XMR flow.
func TestNanoSwapHappyPath(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	bob := harness.NewWallet("xno-bob")     // funds OBX, receives XNO
	alice := harness.NewWallet("xno-alice") // locks XNO, receives OBX
	harness.MineN(t, c, bob, 4)
	harness.ScanAll(c, bob)

	mxno := swapd.NewMockNano()
	// raw XNO is a 128-bit *big.Int now (NanoClient carries raw, not uint64).
	xnoAmount := big.NewInt(7_000_000)
	obxAmount := uint64(5 * config.AtomicPerCoin)
	fee := uint64(1_000_000_000)

	// swap secrets (ed25519; XNO locked to s_a + s_b)
	sA := commit.RandomScalar()
	sB := commit.RandomScalar()
	T := pt(sA)
	xnoPub, err := swapd.NanoAccountPub(pt(sA).Bytes(), pt(sB).Bytes())
	if err != nil {
		t.Fatal(err)
	}

	a, b := commit.RandomScalar(), commit.RandomScalar()
	K := swap.AggregateKey(pt(a), pt(b))

	// 1) Alice locks her XNO to the joint account S_a + S_b.
	lockID, err := mxno.Lock(xnoAmount, xnoPub)
	if err != nil {
		t.Fatal(err)
	}

	// 2) Bob waits for confirmation, funds the OBX swap output.
	if !mxno.Confirmed(lockID) {
		t.Fatal("xno lock not confirmed")
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

	// 3) Alice claims OBX (2-of-2 adapted with s_a), revealing s_a on-chain.
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

	// 4) Bob extracts s_a, forms s_a + s_b, sweeps the XNO account.
	fullSig, _ := commit.ParseFullSig(claim.SwapInputs[0].Sig)
	recovered, err := commit.Extract(pre, fullSig)
	if err != nil {
		t.Fatal(err)
	}
	accountSecret := new(edwards25519.Scalar).Add(recovered, sB)
	if err := mxno.Sweep(lockID, accountSecret, "bob-xno"); err != nil {
		t.Fatalf("bob could not sweep XNO: %v", err)
	}

	if mxno.Balance("bob-xno").Cmp(xnoAmount) != 0 {
		t.Fatalf("bob XNO = %s, want %s", mxno.Balance("bob-xno"), xnoAmount)
	}
	t.Logf("XNO SWAP COMPLETE: Alice +%s OBX, Bob +%s XNO (atomic)", config.FormatAmount(alice.Balance()), mxno.Balance("bob-xno"))
}

// TestNanoSweepNeedsBothSecrets: the XNO account cannot be swept without the full
// key s_a + s_b — a single share is insufficient (what keeps the lock safe before
// the swap reveals the other share).
func TestNanoSweepNeedsBothSecrets(t *testing.T) {
	mxno := swapd.NewMockNano()
	sA := commit.RandomScalar()
	sB := commit.RandomScalar()
	xnoPub, _ := swapd.NanoAccountPub(pt(sA).Bytes(), pt(sB).Bytes())
	id, _ := mxno.Lock(big.NewInt(1000), xnoPub)

	if mxno.Sweep(id, sA, "x") == nil {
		t.Fatal("swept XNO with only s_a")
	}
	if mxno.Sweep(id, sB, "x") == nil {
		t.Fatal("swept XNO with only s_b")
	}
	full := new(edwards25519.Scalar).Add(sA, sB)
	if err := mxno.Sweep(id, full, "x"); err != nil {
		t.Fatalf("legit sweep failed: %v", err)
	}
	if mxno.Balance("x").Cmp(big.NewInt(1000)) != 0 {
		t.Fatal("balance not credited")
	}
}
