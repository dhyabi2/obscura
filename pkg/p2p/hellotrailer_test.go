package p2p

import (
	"encoding/binary"
	"testing"
)

// buildTrailer encodes the obsLen(2) observed verLen(2) version trailer exactly as
// helloPayload does, so the test pins the on-wire encode/decode round-trip.
func buildTrailer(observed, version string) []byte {
	var b []byte
	var ol [2]byte
	binary.BigEndian.PutUint16(ol[:], uint16(len(observed)))
	b = append(b, ol[:]...)
	b = append(b, observed...)
	var vl [2]byte
	binary.BigEndian.PutUint16(vl[:], uint16(len(version)))
	b = append(b, vl[:]...)
	b = append(b, version...)
	return b
}

func TestParseHelloTrailerRoundTrip(t *testing.T) {
	cases := []struct{ obs, ver string }{
		{"1.2.3.4:18080", "1.0.0"},
		{"", "1.0.0"},
		{"host:1", ""},
		{"", ""},
		{"203.0.113.255:65535", "9.9.9-rc1"},
	}
	for _, tc := range cases {
		obs, ver := parseHelloTrailer(buildTrailer(tc.obs, tc.ver))
		if obs != tc.obs || ver != tc.ver {
			t.Fatalf("roundtrip (obs=%q ver=%q) -> got (obs=%q ver=%q)", tc.obs, tc.ver, obs, ver)
		}
	}
}

// An un-upgraded peer sends `observed` as the raw remainder with no length trailer.
// parseHelloTrailer must fall back to treating the whole remainder as observed and
// report an empty version — never a handshake-breaking misparse.
func TestParseHelloTrailerOldFormatFallback(t *testing.T) {
	old := "203.0.113.7:18080"
	obs, ver := parseHelloTrailer([]byte(old))
	if ver != "" {
		t.Fatalf("old format should yield empty version, got %q", ver)
	}
	if obs != old {
		t.Fatalf("old format observed mismatch: got %q want %q", obs, old)
	}
	// empty remainder (peer advertised nothing extra) must not panic
	if o, v := parseHelloTrailer(nil); o != "" || v != "" {
		t.Fatalf("nil remainder -> (%q,%q)", o, v)
	}
}
