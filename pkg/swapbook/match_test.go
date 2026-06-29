package swapbook

import (
	"testing"
	"time"

	"filippo.io/edwards25519"
)

// makeOffer builds and signs a maker offer (maker gives `give` of giveAsset for
// `get` of getAsset) under secret, expiring ttl from now.
func makeOffer(t *testing.T, secret *edwards25519.Scalar, giveAsset, getAsset string, give, get uint64, ttl time.Duration) *Offer {
	t.Helper()
	o := &Offer{
		GiveAsset:  giveAsset,
		GetAsset:   getAsset,
		GiveAmount: give,
		GetAmount:  get,
		Expiry:     time.Now().Add(ttl).Unix(),
	}
	o.Sign(secret)
	return o
}

// addOK adds an offer that must be admitted.
func addOK(t *testing.T, b *Book, o *Offer) {
	t.Helper()
	ok, err := b.Add(o)
	if err != nil || !ok {
		t.Fatalf("Add: ok=%v err=%v", ok, err)
	}
}

// TestQuoteFullFill: a taker giving XNO for OBX walks several offers and fills
// completely; verify VWAP and totals are exact.
func TestQuoteFullFill(t *testing.T) {
	b := NewBook()
	// Taker gives XNO, wants OBX. Makers give OBX, want XNO.
	// Offer A: 100 OBX for 50 XNO  -> rate 2.0 OBX/XNO (best)
	// Offer B: 100 OBX for 100 XNO -> rate 1.0 OBX/XNO
	addOK(t, b, makeOffer(t, randScalar(t), "OBX", "XNO", 100, 50, time.Hour))
	addOK(t, b, makeOffer(t, randScalar(t), "OBX", "XNO", 100, 100, time.Hour))

	// Taker wants to spend 150 XNO. Best-first: consume A (50 XNO -> 100 OBX),
	// then 100 XNO of B -> 100 OBX. Total: filled 150 XNO, get 200 OBX.
	filled, getOut, vwap, used, full := b.Quote("XNO", "OBX", 150)
	if !full {
		t.Fatalf("expected full fill, got partial (filled=%d)", filled)
	}
	if filled != 150 || getOut != 200 || used != 2 {
		t.Fatalf("filled=%d getOut=%d used=%d, want 150/200/2", filled, getOut, used)
	}
	if wantVWAP := 200.0 / 150.0; vwap != wantVWAP {
		t.Fatalf("vwap=%v want %v", vwap, wantVWAP)
	}
}

// TestQuotePartialFill: the book is too thin; Quote returns what is fillable and
// flags full=false.
func TestQuotePartialFill(t *testing.T) {
	b := NewBook()
	// One offer: 100 OBX for 50 XNO. Taker wants to spend 200 XNO.
	addOK(t, b, makeOffer(t, randScalar(t), "OBX", "XNO", 100, 50, time.Hour))
	filled, getOut, vwap, used, full := b.Quote("XNO", "OBX", 200)
	if full {
		t.Fatal("expected partial fill")
	}
	if filled != 50 || getOut != 100 || used != 1 {
		t.Fatalf("filled=%d getOut=%d used=%d, want 50/100/1", filled, getOut, used)
	}
	if vwap != 2.0 {
		t.Fatalf("vwap=%v want 2.0", vwap)
	}
}

// TestQuoteBestRateFirst: ensures offers are consumed best-taker-rate-first, not
// in insertion or map order, by interleaving a worse offer that must be used last.
func TestQuoteBestRateFirst(t *testing.T) {
	b := NewBook()
	// worse: 10 OBX for 100 XNO (rate 0.1); best: 100 OBX for 10 XNO (rate 10)
	addOK(t, b, makeOffer(t, randScalar(t), "OBX", "XNO", 10, 100, time.Hour))
	addOK(t, b, makeOffer(t, randScalar(t), "OBX", "XNO", 100, 10, time.Hour))
	// Spend only 10 XNO: must take the best (rate 10) offer -> 100 OBX, 1 offer.
	filled, getOut, _, used, full := b.Quote("XNO", "OBX", 10)
	if !full || filled != 10 || getOut != 100 || used != 1 {
		t.Fatalf("filled=%d getOut=%d used=%d full=%v, want 10/100/1/true", filled, getOut, used, full)
	}
}

// TestQuoteNoMatch: pair with no offers yields a zero quote.
func TestQuoteNoMatch(t *testing.T) {
	b := NewBook()
	addOK(t, b, makeOffer(t, randScalar(t), "OBX", "XNO", 100, 50, time.Hour))
	filled, getOut, vwap, used, full := b.Quote("BTC", "OBX", 100)
	if filled != 0 || getOut != 0 || vwap != 0 || used != 0 || full {
		t.Fatalf("expected empty quote, got %d/%d/%v/%d/%v", filled, getOut, vwap, used, full)
	}
}

// TestQuoteFloorNeverOvercredits: a partial sip of a high-priced offer must
// floor the getAsset (never round up in the taker's favor).
func TestQuoteFloorRounding(t *testing.T) {
	b := NewBook()
	// 3 OBX for 2 XNO (rate 1.5). Taker spends 1 XNO -> floor(1*3/2)=1 OBX.
	addOK(t, b, makeOffer(t, randScalar(t), "OBX", "XNO", 3, 2, time.Hour))
	filled, getOut, _, _, full := b.Quote("XNO", "OBX", 1)
	if !full || filled != 1 || getOut != 1 {
		t.Fatalf("filled=%d getOut=%d full=%v, want 1/1/true", filled, getOut, full)
	}
}

// TestDepthLadder: cumulative ladder is best-rate-first and cumulative sums are
// correct.
func TestDepthLadder(t *testing.T) {
	b := NewBook()
	addOK(t, b, makeOffer(t, randScalar(t), "OBX", "XNO", 100, 100, time.Hour)) // rate 1.0
	addOK(t, b, makeOffer(t, randScalar(t), "OBX", "XNO", 100, 50, time.Hour))  // rate 2.0 (best)
	addOK(t, b, makeOffer(t, randScalar(t), "OBX", "XNO", 30, 60, time.Hour))   // rate 0.5
	d := b.Depth("XNO", "OBX")
	if len(d) != 3 {
		t.Fatalf("depth len=%d want 3", len(d))
	}
	// Best-first by rate: 2.0, 1.0, 0.5
	if d[0].Rate != 2.0 || d[1].Rate != 1.0 || d[2].Rate != 0.5 {
		t.Fatalf("rate order wrong: %v %v %v", d[0].Rate, d[1].Rate, d[2].Rate)
	}
	// CumGive = sum of GetAmounts (XNO capacity): 50, 50+100=150, 150+60=210
	if d[0].CumGive != 50 || d[1].CumGive != 150 || d[2].CumGive != 210 {
		t.Fatalf("cumGive wrong: %d %d %d", d[0].CumGive, d[1].CumGive, d[2].CumGive)
	}
	// CumGet = sum of GiveAmounts (OBX): 100, 200, 230
	if d[0].CumGet != 100 || d[1].CumGet != 200 || d[2].CumGet != 230 {
		t.Fatalf("cumGet wrong: %d %d %d", d[0].CumGet, d[1].CumGet, d[2].CumGet)
	}
}

// TestExpiryExclusion: an offer whose Expiry passes is excluded from Quote/Depth/
// Best/List and counted by PruneExpired.
func TestExpiryExclusion(t *testing.T) {
	b := NewBook()
	live := makeOffer(t, randScalar(t), "OBX", "XNO", 100, 50, time.Hour)
	addOK(t, b, live)
	// Add an offer, then force it expired by mutating the stored copy's Expiry
	// to the past (cannot Add an already-expired offer since Verify rejects it).
	exp := makeOffer(t, randScalar(t), "OBX", "XNO", 100, 50, time.Hour)
	addOK(t, b, exp)
	b.mu.Lock()
	b.offers[exp.ID()].Expiry = time.Now().Add(-time.Minute).Unix()
	b.mu.Unlock()

	// PruneExpired removes exactly the one expired offer.
	if n := b.PruneExpired(time.Now()); n != 1 {
		t.Fatalf("PruneExpired removed %d, want 1", n)
	}
	if b.Size() != 1 {
		t.Fatalf("size=%d want 1", b.Size())
	}
	// Quote sees only the live offer.
	filled, _, _, used, full := b.Quote("XNO", "OBX", 50)
	if !full || filled != 50 || used != 1 {
		t.Fatalf("quote after prune: filled=%d used=%d full=%v", filled, used, full)
	}
}

// TestExpiryExcludedBeforePrune: even without calling PruneExpired, the read
// paths (List/Quote) must not surface an expired offer.
func TestExpiryExcludedFromQuote(t *testing.T) {
	b := NewBook()
	good := makeOffer(t, randScalar(t), "OBX", "XNO", 100, 50, time.Hour)
	addOK(t, b, good)
	stale := makeOffer(t, randScalar(t), "OBX", "XNO", 999, 1, time.Hour) // would be "best"
	addOK(t, b, stale)
	b.mu.Lock()
	b.offers[stale.ID()].Expiry = time.Now().Add(-time.Second).Unix()
	b.mu.Unlock()
	// The stale offer has a far better rate; if it leaked, getOut would be huge.
	filled, getOut, _, used, _ := b.Quote("XNO", "OBX", 50)
	if filled != 50 || getOut != 100 || used != 1 {
		t.Fatalf("expired offer leaked into quote: filled=%d getOut=%d used=%d", filled, getOut, used)
	}
}

// TestCancelValid: a maker-signed cancel removes the offer.
func TestCancelValid(t *testing.T) {
	b := NewBook()
	sec := randScalar(t)
	o := makeOffer(t, sec, "OBX", "XNO", 100, 50, time.Hour)
	addOK(t, b, o)
	id := o.ID()
	sig := SignCancel(sec, id)
	if err := b.Cancel(id, sig); err != nil {
		t.Fatalf("valid cancel rejected: %v", err)
	}
	if b.Size() != 0 {
		t.Fatalf("offer not removed, size=%d", b.Size())
	}
}

// TestCancelForged: a cancel signed by a different key (or over the wrong id) is
// rejected and the offer stays.
func TestCancelForged(t *testing.T) {
	b := NewBook()
	maker := randScalar(t)
	o := makeOffer(t, maker, "OBX", "XNO", 100, 50, time.Hour)
	addOK(t, b, o)
	id := o.ID()

	// Forged by a different key.
	attacker := randScalar(t)
	if err := b.Cancel(id, SignCancel(attacker, id)); err == nil {
		t.Fatal("forged cancel (wrong key) accepted")
	}
	// Maker key but signing the wrong id (replaying a cancel for another offer).
	var otherID [32]byte
	otherID[0] = 0xAB
	if err := b.Cancel(id, SignCancel(maker, otherID)); err == nil {
		t.Fatal("cancel with signature over wrong id accepted")
	}
	// Bad length.
	if err := b.Cancel(id, make([]byte, 10)); err == nil {
		t.Fatal("malformed-length cancel accepted")
	}
	// Unknown id.
	if err := b.Cancel(otherID, SignCancel(maker, otherID)); err == nil {
		t.Fatal("cancel of unknown offer accepted")
	}
	if b.Size() != 1 {
		t.Fatalf("offer wrongly removed, size=%d", b.Size())
	}
}

// TestMakerCapEnforced: one maker cannot exceed MaxOffersPerMaker live offers.
func TestMakerCapEnforced(t *testing.T) {
	b := NewBook()
	sec := randScalar(t)
	// Vary amounts so each offer has a distinct id.
	for i := 0; i < MaxOffersPerMaker; i++ {
		o := makeOffer(t, sec, "OBX", "XNO", 100, uint64(i+1), time.Hour)
		addOK(t, b, o)
	}
	// The next one from the same maker must be rejected.
	over := makeOffer(t, sec, "OBX", "XNO", 100, uint64(MaxOffersPerMaker+1), time.Hour)
	ok, err := b.Add(over)
	if ok || err == nil {
		t.Fatalf("maker cap not enforced: ok=%v err=%v", ok, err)
	}
	// A different maker is unaffected.
	other := makeOffer(t, randScalar(t), "OBX", "XNO", 100, 50, time.Hour)
	addOK(t, b, other)
}

// TestMaxBookSizeEnforced: the global cap rejects additions past MaxBookSize.
// We shrink the effective test by checking the guard directly with a small,
// hand-stuffed book rather than building 50k PoW offers (which is slow).
func TestMaxBookSizeEnforced(t *testing.T) {
	b := NewBook()
	// Stuff the map to capacity with cheap placeholder offers (bypassing Add's
	// Verify, since we only exercise the size guard). These have far-future
	// Expiry so pruneLocked won't remove them.
	exp := time.Now().Add(time.Hour).Unix()
	for i := 0; i < MaxBookSize; i++ {
		var id [32]byte
		id[0] = byte(i)
		id[1] = byte(i >> 8)
		id[2] = byte(i >> 16)
		b.offers[id] = &Offer{Expiry: exp, Maker: make([]byte, 32)}
	}
	if b.Size() != MaxBookSize {
		t.Fatalf("setup size=%d", b.Size())
	}
	// A real, valid offer must now be rejected as the book is full.
	o := makeOffer(t, randScalar(t), "OBX", "XNO", 100, 50, time.Hour)
	ok, err := b.Add(o)
	if ok || err == nil {
		t.Fatalf("MaxBookSize not enforced: ok=%v err=%v", ok, err)
	}
}

// TestDuplicateIDRejected: re-adding the same offer is a no-op (not new, no error).
func TestDuplicateIDRejected(t *testing.T) {
	b := NewBook()
	o := makeOffer(t, randScalar(t), "OBX", "XNO", 100, 50, time.Hour)
	addOK(t, b, o)
	ok, err := b.Add(o)
	if ok || err != nil {
		t.Fatalf("duplicate Add: ok=%v err=%v, want false/nil", ok, err)
	}
	if b.Size() != 1 {
		t.Fatalf("size=%d want 1", b.Size())
	}
}

// TestCancelMessageDomainSeparated: the cancel message is bound to a domain and
// the id, so an offer signature (over Core()) can never double as a cancel.
func TestCancelMessageDistinctFromOffer(t *testing.T) {
	o := makeOffer(t, randScalar(t), "OBX", "XNO", 100, 50, time.Hour)
	id := o.ID()
	cm := CancelMessage(id)
	if string(cm) == string(o.Core()) {
		t.Fatal("cancel message collides with offer Core()")
	}
	// Different ids produce different cancel messages.
	var other [32]byte
	other[0] = 1
	if string(CancelMessage(id)) == string(CancelMessage(other)) {
		t.Fatal("cancel message not bound to offer id")
	}
}
