// Package swapd_test, BTC leg: an end-to-end trustless BTC↔Obscura atomic swap.
// The OBX leg settles on the real Obscura chain (adaptor-sig 2-of-2 swap); the
// BTC leg is a mock HTLC whose hashlock is SHA256(t) for the SAME secret t the
// OBX claim reveals. See docs/INVENTION_CROSSCHAIN_SWAPS.md.
package swapd_test

import (
	"bytes"
	"testing"

	"filippo.io/edwards25519"

	"obscura/pkg/commit"
	"obscura/pkg/config"
	"obscura/pkg/swap"
	"obscura/pkg/swapd"
	"obscura/pkg/tx"
	"obscura/tests/critical/harness"
)

// 33-byte compressed-pubkey stand-ins for the mock (real swaps use secp256k1).
func btcPub(tag byte) []byte { return append([]byte{0x02}, bytes.Repeat([]byte{tag}, 32)...) }

// TestBtcSwapHappyPath: Alice has BTC + wants OBX; Bob has OBX + wants BTC. Both
// settle atomically, bridged by the secret t (= OBX adaptor secret = BTC preimage).
func TestBtcSwapHappyPath(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	bob := harness.NewWallet("btc-bob")     // funds OBX, receives BTC
	alice := harness.NewWallet("btc-alice") // funds BTC, receives OBX
	harness.MineN(t, c, bob, 4)
	harness.ScanAll(c, bob)

	mbtc := swapd.NewMockBitcoin()
	const btcAmount = uint64(50_000_000) // 0.5 BTC in sats
	obxAmount := uint64(5 * config.AtomicPerCoin)
	fee := uint64(1_000_000_000)

	// shared secret t = Alice's adaptor secret; BTC hashlock = SHA256(t)
	sA := commit.RandomScalar()
	T := pt(sA)
	hash := swapd.HashPreimage(sA.Bytes())

	aliceBtc, bobBtc := btcPub(0xAA), btcPub(0xBB)

	// OBX 2-of-2 claim key; refund to Bob
	a, b := commit.RandomScalar(), commit.RandomScalar()
	K := swap.AggregateKey(pt(a), pt(b))

	// 1) Alice locks BTC into an HTLC redeemable by Bob with preimage t.
	lockID, err := mbtc.FundHTLC(btcAmount, hash, bobBtc, aliceBtc, 800_000)
	if err != nil {
		t.Fatal(err)
	}

	// 2) Bob waits for BTC confirmation, then funds the OBX swap output.
	if !mbtc.Confirmed(lockID) {
		t.Fatal("btc htlc not confirmed")
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

	// 3) Alice claims OBX (2-of-2 adapted with t), revealing t on the OBX chain.
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

	// 4) Bob extracts t from the on-chain OBX claim, redeems BTC with preimage t.
	fullSig, _ := commit.ParseFullSig(claim.SwapInputs[0].Sig)
	recovered, err := commit.Extract(pre, fullSig)
	if err != nil {
		t.Fatal(err)
	}
	preimage := recovered.Bytes()
	if err := mbtc.Redeem(lockID, preimage, bobBtc, "bob-btc"); err != nil {
		t.Fatalf("bob could not redeem BTC: %v", err)
	}

	// both legs settled atomically
	if mbtc.Balance("bob-btc") != btcAmount {
		t.Fatalf("bob BTC = %d, want %d", mbtc.Balance("bob-btc"), btcAmount)
	}
	// the preimage Bob used must hash to the agreed hashlock (the bridge holds)
	if !bytes.Equal(swapd.HashPreimage(preimage), hash) {
		t.Fatal("revealed preimage does not match the BTC hashlock")
	}
	t.Logf("BTC SWAP COMPLETE: Alice +%s OBX, Bob +%d sats (atomic)", config.FormatAmount(alice.Balance()), mbtc.Balance("bob-btc"))
}

// TestBtcSwapRefund: the swap aborts before Alice claims OBX; both parties recover
// their funds via the timelock paths, nobody is cheated.
func TestBtcSwapRefund(t *testing.T) {
	defer harness.SmallMaturity()()
	// F-1 fund-safety: consensus rejects an UnlockHeight inside the reorg margin. This
	// is a REFUND fixture with a small `height+3` unlock (claim-window length is
	// irrelevant here), so shrink the margin so the output is fundable. (BTC swapping is
	// otherwise disabled via config.SettleableAssets; the HTLC refund logic is kept.)
	oldRM := config.SwapReorgMargin
	config.SwapReorgMargin = 1
	defer func() { config.SwapReorgMargin = oldRM }()
	c := harness.NewChain(t)
	bob := harness.NewWallet("btc-bob2")
	harness.MineN(t, c, bob, 4)
	harness.ScanAll(c, bob)

	mbtc := swapd.NewMockBitcoin()
	obxAmount := uint64(3 * config.AtomicPerCoin)
	fee := uint64(1_000_000_000)
	sA := commit.RandomScalar()
	hash := swapd.HashPreimage(sA.Bytes())
	aliceBtc, bobBtc := btcPub(0xA1), btcPub(0xB1)
	a, b := commit.RandomScalar(), commit.RandomScalar()
	K := swap.AggregateKey(pt(a), pt(b))

	lockID, _ := mbtc.FundHTLC(40_000_000, hash, bobBtc, aliceBtc, 700_000)
	unlock := c.Height() + 3
	swapKey := func() []byte { h := commit.RandomScalar().Bytes(); return h[:32] }()
	ra, rb := commit.RandomScalar(), commit.RandomScalar()
	R := new(edwards25519.Point).Add(pt(ra), pt(rb))
	fund, err := bob.FundSwap(c, swapKey, obxAmount, K.Bytes(), pt(b).Bytes(),
		pt(a).Bytes(), pt(b).Bytes(), swap.ProvePossession(a), swap.ProvePossession(b), R.Bytes(), pt(sA).Bytes(), unlock, fee)
	if err != nil {
		t.Fatal(err)
	}
	harness.MineBlock(t, c, bob, []*tx.Transaction{fund})

	// --- abort: Alice never claims. Bob refunds OBX after the unlock height. ---
	for c.Height() < unlock {
		harness.MineBlock(t, c, harness.NewWallet("btc-sink"), nil)
	}
	refund, err := bob.BuildSwapSpend(swapKey, obxAmount, true, fee, func(coreHash []byte) []byte {
		return commit.Sign(b, coreHash).Serialize()
	})
	if err != nil {
		t.Fatalf("build OBX refund: %v", err)
	}
	harness.MineBlock(t, c, harness.NewWallet("btc-sink"), []*tx.Transaction{refund})
	harness.ScanAll(c, bob)

	// Alice refunds BTC after the BTC locktime.
	if err := mbtc.Refund(lockID, aliceBtc, "alice-btc"); err == nil {
		t.Fatal("BTC refund succeeded before locktime")
	}
	mbtc.SetHeight(700_000)
	if err := mbtc.Refund(lockID, aliceBtc, "alice-btc"); err != nil {
		t.Fatalf("alice BTC refund failed: %v", err)
	}
	if mbtc.Balance("alice-btc") != 40_000_000 {
		t.Fatal("alice BTC not refunded")
	}
	t.Log("BTC SWAP ABORTED SAFELY: Bob refunded OBX, Alice refunded BTC")
}

// TestBtcHTLCRules: the mock enforces the real HTLC rules (wrong preimage, wrong
// party, double-spend).
func TestBtcHTLCRules(t *testing.T) {
	mbtc := swapd.NewMockBitcoin()
	sA := commit.RandomScalar()
	hash := swapd.HashPreimage(sA.Bytes())
	redeem, refund := btcPub(0x01), btcPub(0x02)
	id, _ := mbtc.FundHTLC(1000, hash, redeem, refund, 100)

	// wrong preimage
	if mbtc.Redeem(id, commit.RandomScalar().Bytes(), redeem, "x") == nil {
		t.Fatal("redeemed with wrong preimage")
	}
	// right preimage, wrong redeemer
	if mbtc.Redeem(id, sA.Bytes(), refund, "x") == nil {
		t.Fatal("redeemed as the wrong party")
	}
	// correct redeem
	if err := mbtc.Redeem(id, sA.Bytes(), redeem, "x"); err != nil {
		t.Fatalf("legit redeem failed: %v", err)
	}
	// double-spend
	if mbtc.Redeem(id, sA.Bytes(), redeem, "x") == nil {
		t.Fatal("double-spent the HTLC")
	}
	if got, ok := mbtc.RevealedPreimage(id); !ok || !bytes.Equal(got, sA.Bytes()) {
		t.Fatal("redeem did not reveal the preimage")
	}
}

// TestBtcHTLCScript sanity-checks the real P2WSH redeem script structure.
func TestBtcHTLCScript(t *testing.T) {
	hash := swapd.HashPreimage([]byte("x"))
	redeem := append([]byte{0x02}, bytes.Repeat([]byte{0x11}, 32)...)
	refund := append([]byte{0x03}, bytes.Repeat([]byte{0x22}, 32)...)
	s, err := swapd.BtcHTLCScript(hash, redeem, refund, 750_000)
	if err != nil {
		t.Fatal(err)
	}
	// must contain OP_SHA256 (0xa8), OP_CHECKLOCKTIMEVERIFY (0xb1), OP_IF/OP_ELSE/OP_ENDIF
	for _, op := range []byte{0x63, 0xa8, 0xb1, 0x67, 0x68} {
		if !bytes.Contains(s, []byte{op}) {
			t.Fatalf("script missing opcode 0x%02x", op)
		}
	}
	if !bytes.Contains(s, hash) {
		t.Fatal("script does not commit the hashlock")
	}
	if wp := swapd.BtcWitnessProgram(s); len(wp) != 32 {
		t.Fatalf("witness program must be 32 bytes, got %d", len(wp))
	}
	var zero [32]byte
	if _, err := swapd.BtcHTLCScript(zero[:31], redeem, refund, 1); err == nil {
		t.Fatal("accepted a 31-byte hash")
	}
}
