package swapbook

import (
	"crypto/rand"
	"testing"
	"time"

	"filippo.io/edwards25519"
)

func randScalar(t *testing.T) *edwards25519.Scalar {
	t.Helper()
	var seed [64]byte
	if _, err := rand.Read(seed[:]); err != nil {
		t.Fatal(err)
	}
	s, err := edwards25519.NewScalar().SetUniformBytes(seed[:])
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newSignedOffer(t *testing.T, give, get string) *Offer {
	t.Helper()
	o := &Offer{
		GiveAsset:  give,
		GetAsset:   get,
		GiveAmount: 100,
		GetAmount:  200,
		Expiry:     time.Now().Add(time.Hour).Unix(),
	}
	o.Sign(randScalar(t))
	return o
}

func TestSignVerifyRoundTrip(t *testing.T) {
	o := newSignedOffer(t, "OBX", "XNO")
	if !o.Verify(time.Now()) {
		t.Fatal("freshly signed offer should verify")
	}
}

func TestSerializeRoundTrip(t *testing.T) {
	o := newSignedOffer(t, "OBX", "XNO")
	got, err := ParseOffer(o.Serialize())
	if err != nil {
		t.Fatal(err)
	}
	if got.ID() != o.ID() || !got.Verify(time.Now()) {
		t.Fatal("round-tripped offer mismatch")
	}
}

// TestCoreInjective is the audit regression: distinct offers must never share
// Core() bytes. With the old NUL-delimited encoding, shifting bytes across the
// asset boundary collided; length-prefixing prevents that.
func TestCoreInjective(t *testing.T) {
	maker := make([]byte, 32)
	base := func() *Offer {
		return &Offer{Maker: append([]byte(nil), maker...), GiveAmount: 1, GetAmount: 1}
	}
	a := base()
	a.GiveAsset, a.GetAsset = "AB", "C"
	b := base()
	b.GiveAsset, b.GetAsset = "A", "BC"
	if string(a.Core()) == string(b.Core()) {
		t.Fatal("Core() is ambiguous across the give/get asset boundary")
	}
	// Maker/asset boundary ambiguity.
	c := base()
	c.Maker = append(c.Maker, 'X')
	c.GiveAsset, c.GetAsset = "", "Y"
	d := base()
	d.GiveAsset, d.GetAsset = "X", "Y"
	if string(c.Core()) == string(d.Core()) {
		t.Fatal("Core() is ambiguous across the maker/asset boundary")
	}
}

func TestVerifyRejectsBadAssets(t *testing.T) {
	cases := []struct{ give, get string }{
		{"", "XMR"},
		{"XMR", ""},
		{"OB\x00X", "XMR"},          // embedded NUL
		{"OBX ", "XMR"},            // space (control-ish, not in allowlist)
		{"OBX", "OBX"},            // same asset
		{"TOOLONGASSETNAME12345", "XMR"}, // over MaxAssetLen
	}
	for _, c := range cases {
		o := &Offer{
			GiveAsset:  c.give,
			GetAsset:   c.get,
			GiveAmount: 100,
			GetAmount:  200,
			Expiry:     time.Now().Add(time.Hour).Unix(),
		}
		o.Sign(randScalar(t))
		if o.Verify(time.Now()) {
			t.Fatalf("offer with assets (%q,%q) should not verify", c.give, c.get)
		}
	}
}

// TestBTCOfferRejected is the settleability-gate regression: BTC has no real
// settleable leg (it is excluded from config.SettleableAssets), so an offer with
// BTC on either side must fail Verify and be refused by Book.Add — it cannot be
// posted, taken, or gossiped on. OBX<->XNO offers (both settleable) still admit.
func TestBTCOfferRejected(t *testing.T) {
	b := NewBook()

	// BTC on the GET side.
	getBTC := newSignedOffer(t, "OBX", "BTC")
	if getBTC.Verify(time.Now()) {
		t.Fatal("offer getting BTC should not Verify")
	}
	if ok, err := b.Add(getBTC); ok || err == nil {
		t.Fatalf("Add(OBX->BTC): ok=%v err=%v, want refused", ok, err)
	}

	// BTC on the GIVE side.
	giveBTC := newSignedOffer(t, "BTC", "XNO")
	if giveBTC.Verify(time.Now()) {
		t.Fatal("offer giving BTC should not Verify")
	}
	if ok, err := b.Add(giveBTC); ok || err == nil {
		t.Fatalf("Add(BTC->XNO): ok=%v err=%v, want refused", ok, err)
	}

	// The book stays empty — no BTC offer was admitted.
	if b.Size() != 0 {
		t.Fatalf("book size=%d after BTC rejections, want 0", b.Size())
	}

	// Sanity: a settleable OBX<->XNO offer IS admitted (the gate is selective,
	// not a blanket reject).
	ok, err := b.Add(newSignedOffer(t, "OBX", "XNO"))
	if !ok || err != nil {
		t.Fatalf("Add(OBX->XNO): ok=%v err=%v, want admitted", ok, err)
	}
	if b.Size() != 1 {
		t.Fatalf("book size=%d after admitting OBX<->XNO, want 1", b.Size())
	}
}

// TestBTCExcludedFromQuoteAndDepth: because the Add gate refuses every BTC
// offer, the book never holds a BTC pair, so the read paths (Quote/Depth) — which
// read straight from the book — can never surface one. We build the book through
// the SAME gate a real maker uses (Add) and confirm BTC offers don't get in while
// OBX<->XNO quotes work. (Quote/Depth deliberately do NOT re-gate; their BTC-free
// guarantee follows entirely from Add. If a BTC offer is force-injected past Add,
// it can match — which is exactly why Add is the single chokepoint.)
func TestBTCExcludedFromQuoteAndDepth(t *testing.T) {
	b := NewBook()
	// A real, admitted OBX<->XNO offer (taker gives XNO, wants OBX).
	addOK(t, b, makeOffer(t, randScalar(t), "OBX", "XNO", 100, 50, time.Hour))

	// Attempt to add BTC offers via the gate (the only legitimate path) — both
	// sides — and confirm they are refused, so the book stays BTC-free.
	if ok, err := b.Add(makeOffer(t, randScalar(t), "OBX", "BTC", 999, 1, time.Hour)); ok || err == nil {
		t.Fatalf("Add(OBX->BTC) admitted: ok=%v err=%v", ok, err)
	}
	if ok, err := b.Add(makeOffer(t, randScalar(t), "BTC", "OBX", 999, 1, time.Hour)); ok || err == nil {
		t.Fatalf("Add(BTC->OBX) admitted: ok=%v err=%v", ok, err)
	}

	// No BTC pair exists, so Quote/Depth on a BTC pair are empty.
	if filled, _, _, used, full := b.Quote("BTC", "OBX", 100); filled != 0 || used != 0 || full {
		t.Fatalf("BTC quote not empty: filled=%d used=%d full=%v", filled, used, full)
	}
	if d := b.Depth("BTC", "OBX"); len(d) != 0 {
		t.Fatalf("BTC depth not empty: %d levels", len(d))
	}
	// The settleable OBX<->XNO quote works as normal.
	if filled, getOut, _, used, full := b.Quote("XNO", "OBX", 50); !full || filled != 50 || getOut != 100 || used != 1 {
		t.Fatalf("OBX<->XNO quote wrong: filled=%d getOut=%d used=%d full=%v", filled, getOut, used, full)
	}
}

func TestValidAsset(t *testing.T) {
	good := []string{"OBX", "XMR", "BTC", "XNO", "a-b.c", "ABC123"}
	for _, s := range good {
		if !validAsset(s) {
			t.Errorf("validAsset(%q) = false, want true", s)
		}
	}
	bad := []string{"", "OB X", "OB\x00X", "lower/slash", "TOOLONGASSETNAME1"}
	for _, s := range bad {
		if validAsset(s) {
			t.Errorf("validAsset(%q) = true, want false", s)
		}
	}
}
