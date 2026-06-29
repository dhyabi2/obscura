package swapd

import (
	"math/big"
	"strings"
	"testing"
)

// TestMockNanoBigAmountRoundTrip validates the fund-critical *big.Int migration of
// the NanoClient amount: a 0.00001-XNO lock (1e25 raw) must round-trip through
// Lock→LockInfo EXACTLY. Under the old uint64 interface this value SATURATED
// (1e25 > max uint64 ~1.8e19), so amount-equality checks only passed by coincident
// truncation and a real on-ledger amount could never be verified against the agreed
// amount. With *big.Int the agreed XNO survives the interface intact.
func TestMockNanoBigAmountRoundTrip(t *testing.T) {
	m := NewMockNano()

	// 0.00001 XNO = 1e25 raw — built from a decimal string so there is no chance of
	// float/int64 truncation in the literal itself.
	amount, ok := new(big.Int).SetString("10000000000000000000000000", 10)
	if !ok {
		t.Fatal("could not parse 1e25 amount")
	}
	// sanity: this amount genuinely overflows uint64 (the old interface would have lost it).
	if amount.IsUint64() {
		t.Fatalf("1e25 unexpectedly fits in uint64 — test would not exercise the saturation bug")
	}

	// a valid 32-byte account pubkey (any point will do for the mock lock).
	_, pub, _, err := MinerXNOAccount([]byte("nano-bigamount-test-seed-00000000"))
	if err != nil {
		t.Fatalf("MinerXNOAccount: %v", err)
	}

	id, err := m.Lock(amount, pub)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}

	gotAmt, gotPub, err := m.LockInfo(id)
	if err != nil {
		t.Fatalf("LockInfo: %v", err)
	}
	if gotAmt.Cmp(amount) != 0 {
		t.Fatalf("LockInfo amount = %s, want %s (1e25 did NOT round-trip exactly)", gotAmt, amount)
	}
	if string(gotPub) != string(pub) {
		t.Fatal("LockInfo returned the wrong account pubkey")
	}

	// LockInfo must return a defensive copy, not an alias into the ledger.
	gotAmt.SetInt64(0)
	again, _, _ := m.LockInfo(id)
	if again.Cmp(amount) != 0 {
		t.Fatal("LockInfo amount aliased the internal ledger state (mutation leaked)")
	}
}

// TestXNOOfferUnitsToRaw validates the offer-units→raw conversion the in-node
// settlement leg applies before locking: offer units are 1e12 per XNO, raw is 1e30
// per XNO, so raw = offerUnits × 10^18. 5 offer-XNO (5e12 offer units) must become
// 5e30 raw. (Previously the 1e12-unit value was fed straight into Lock, under-paying
// the maker by 10^18×.)
func TestXNOOfferUnitsToRaw(t *testing.T) {
	// 5 XNO expressed in offer units = 5 × 10^12.
	offerUnits := big.NewInt(5_000_000_000_000)
	want, _ := new(big.Int).SetString("5000000000000000000000000000000", 10) // 5e30 raw

	got := XNOOfferUnitsToRaw(offerUnits)
	if got.Cmp(want) != 0 {
		t.Fatalf("XNOOfferUnitsToRaw(5e12) = %s, want %s (5 offer-XNO must be 5e30 raw)", got, want)
	}

	// the multiplier is exactly 10^18.
	if scaled := new(big.Int).Quo(got, offerUnits); scaled.Cmp(new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)) != 0 {
		t.Fatalf("offer→raw scale = %s, want 10^18", scaled)
	}

	// nil → 0 (defensive).
	if z := XNOOfferUnitsToRaw(nil); z.Sign() != 0 {
		t.Fatalf("XNOOfferUnitsToRaw(nil) = %s, want 0", z)
	}
}

// TestXNORawToOfferUnits validates the inverse conversion the MAKER side uses to
// size an order-book reservation for an inbound Init (whose XNOAmount is raw):
// offerUnits = raw / 10^18 (floor). It must round-trip OfferUnitsToRaw and floor
// sub-10^18 dust to 0.
func TestXNORawToOfferUnits(t *testing.T) {
	// 5 XNO raw (5e30) → 5e12 offer units.
	raw, _ := new(big.Int).SetString("5000000000000000000000000000000", 10)
	if got := XNORawToOfferUnits(raw); got != 5_000_000_000_000 {
		t.Fatalf("XNORawToOfferUnits(5e30) = %d, want 5e12", got)
	}
	// round-trip: offer units -> raw -> offer units.
	offerUnits := big.NewInt(7_500_000_000_000)
	if got := XNORawToOfferUnits(XNOOfferUnitsToRaw(offerUnits)); got != offerUnits.Uint64() {
		t.Fatalf("round-trip XNORawToOfferUnits = %d, want %d", got, offerUnits.Uint64())
	}
	// sub-10^18 dust floors to 0.
	if got := XNORawToOfferUnits(big.NewInt(1_000)); got != 0 {
		t.Fatalf("XNORawToOfferUnits(dust) = %d, want 0", got)
	}
	// nil / non-positive → 0.
	if got := XNORawToOfferUnits(nil); got != 0 {
		t.Fatalf("XNORawToOfferUnits(nil) = %d, want 0", got)
	}
}

// TestMinerXNOAccountAddress confirms the seed-derived XNO proceeds account yields a
// canonical nano_ address that DecodeNanoAddress accepts and that round-trips back to
// the same public key — i.e. the swept-XNO destination is a real, recoverable account.
func TestMinerXNOAccountAddress(t *testing.T) {
	sec, pub, addr, err := MinerXNOAccount([]byte("miner-xno-proceeds-seed-00000000"))
	if err != nil {
		t.Fatalf("MinerXNOAccount: %v", err)
	}
	if sec == nil || len(pub) != 32 {
		t.Fatalf("MinerXNOAccount returned bad secret/pub (pub len %d)", len(pub))
	}
	if !strings.HasPrefix(addr, "nano_") {
		t.Fatalf("address %q does not start with nano_", addr)
	}
	decoded, err := DecodeNanoAddress(addr)
	if err != nil {
		t.Fatalf("DecodeNanoAddress(%q): %v", addr, err)
	}
	if string(decoded) != string(pub) {
		t.Fatal("DecodeNanoAddress did not round-trip back to the derived pubkey")
	}

	// derivation is deterministic from the seed (recoverable).
	_, _, addr2, _ := MinerXNOAccount([]byte("miner-xno-proceeds-seed-00000000"))
	if addr2 != addr {
		t.Fatal("MinerXNOAccount is not deterministic for the same seed")
	}
}
