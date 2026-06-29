package swapbook

import (
	"testing"
	"time"

	"filippo.io/edwards25519"
)

// signedOfferAmt builds + signs an OBX/XNO offer with explicit amounts and maker.
func signedOfferAmt(t *testing.T, give, get string, giveAmt, getAmt uint64, maker *edwards25519.Scalar) *Offer {
	t.Helper()
	o := &Offer{
		GiveAsset:  give,
		GetAsset:   get,
		GiveAmount: giveAmt,
		GetAmount:  getAmt,
		Expiry:     time.Now().Add(time.Hour).Unix(),
	}
	o.Sign(maker)
	return o
}

// TestLiquidityAggregatesBook seeds a book with three live OBX/XNO offers from two
// distinct makers and asserts Liquidity() aggregates Σ give, Σ get, the offer +
// maker counts, and the best (highest get/give) rate per pair.
func TestLiquidityAggregatesBook(t *testing.T) {
	b := NewBook()
	makerA := randScalar(t)
	makerB := randScalar(t)

	// OBX/XNO offers: rates get/give = 200/100=2, 600/200=3 (best), 50/100=0.5.
	offers := []*Offer{
		signedOfferAmt(t, "OBX", "XNO", 100, 200, makerA),
		signedOfferAmt(t, "OBX", "XNO", 200, 600, makerA), // best rate 3
		signedOfferAmt(t, "OBX", "XNO", 100, 50, makerB),  // worst rate 0.5
	}
	for _, o := range offers {
		ok, err := b.Add(o)
		if err != nil || !ok {
			t.Fatalf("seed offer: ok=%v err=%v", ok, err)
		}
	}

	pairs, totalOffers, totalMakers := b.Liquidity()
	if totalOffers != 3 {
		t.Fatalf("total offers = %d, want 3", totalOffers)
	}
	if totalMakers != 2 {
		t.Fatalf("total makers = %d, want 2", totalMakers)
	}
	if len(pairs) != 1 {
		t.Fatalf("pairs = %d, want 1 (only OBX/XNO)", len(pairs))
	}
	p := pairs[0]
	if p.Pair != "OBX/XNO" || p.GiveAsset != "OBX" || p.GetAsset != "XNO" {
		t.Fatalf("pair mismatch: %+v", p)
	}
	if p.TotalGive != 400 { // 100+200+100
		t.Fatalf("total give = %d, want 400", p.TotalGive)
	}
	if p.TotalGet != 850 { // 200+600+50
		t.Fatalf("total get = %d, want 850", p.TotalGet)
	}
	if p.Offers != 3 {
		t.Fatalf("pair offers = %d, want 3", p.Offers)
	}
	if p.Makers != 2 {
		t.Fatalf("pair makers = %d, want 2", p.Makers)
	}
	// best rate = 600/200 = 3 (highest get/give).
	if p.BestRate != "3" {
		t.Fatalf("best rate = %q, want \"3\"", p.BestRate)
	}
}

// TestLiquidityEmptyBook: an empty book yields no pairs and zero counts.
func TestLiquidityEmptyBook(t *testing.T) {
	b := NewBook()
	pairs, totalOffers, totalMakers := b.Liquidity()
	if len(pairs) != 0 || totalOffers != 0 || totalMakers != 0 {
		t.Fatalf("empty book: pairs=%d offers=%d makers=%d, want 0/0/0", len(pairs), totalOffers, totalMakers)
	}
}

// TestRatioStringFractional checks the fractional rate rendering + trimming.
func TestRatioStringFractional(t *testing.T) {
	cases := []struct {
		num, den uint64
		want     string
	}{
		{3, 1, "3"},
		{1, 2, "0.5"},
		{1, 4, "0.25"},
		{5, 2, "2.5"},
		{0, 7, "0"},
	}
	for _, c := range cases {
		if got := ratioString(c.num, c.den); got != c.want {
			t.Fatalf("ratioString(%d,%d) = %q, want %q", c.num, c.den, got, c.want)
		}
	}
}
