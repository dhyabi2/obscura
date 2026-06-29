package swapbook

import (
	"testing"
	"time"
)

// TestBuildSignedOfferAdmitted verifies the server-side auto-liquidity offer
// constructor produces an offer that the order book ADMITS — i.e. it carries a
// valid maker signature, the anti-spam PoW, and a sane expiry, exactly like a
// wallet-posted offer. This is the core guarantee the node's auto-liquidity loop
// relies on.
func TestBuildSignedOfferAdmitted(t *testing.T) {
	secret := randScalar(t)
	o := BuildSignedOffer("OBX", "XNO", 500_000_000 /*5 OBX @ 1e8*/, 5_000_000_000, time.Hour, secret)

	if !o.Verify(time.Now()) {
		t.Fatal("auto-built offer should verify (PoW + signature + expiry)")
	}
	b := NewBook()
	added, err := b.Add(o)
	if err != nil || !added {
		t.Fatalf("auto-built offer should be admitted: added=%v err=%v", added, err)
	}
	if b.Size() != 1 {
		t.Fatalf("book size = %d, want 1", b.Size())
	}

	// The maker pubkey set by Sign must be findable via MakerOffers (how the loop
	// counts its own outstanding offers).
	mine := b.MakerOffers(o.Maker)
	if len(mine) != 1 || mine[0].ID() != o.ID() {
		t.Fatalf("MakerOffers should return our one offer, got %d", len(mine))
	}

	// A different maker has no offers.
	other := randScalar(t)
	o2 := BuildSignedOffer("OBX", "XNO", 1, 1, time.Hour, other)
	if got := b.MakerOffers(o2.Maker); len(got) != 0 {
		t.Fatalf("MakerOffers(other) = %d, want 0", len(got))
	}
}

// TestBuildSignedOfferTTLClamp checks that an out-of-range TTL is clamped to the
// allowed window rather than producing an offer the book rejects.
func TestBuildSignedOfferTTLClamp(t *testing.T) {
	secret := randScalar(t)
	o := BuildSignedOffer("OBX", "XNO", 100, 200, 999*time.Hour /*> MaxOfferTTL*/, secret)
	if !o.Verify(time.Now()) {
		t.Fatal("offer with clamped TTL should verify")
	}
	if o.Expiry > time.Now().Add(MaxOfferTTL).Unix()+2 {
		t.Fatal("TTL not clamped to MaxOfferTTL")
	}
}
