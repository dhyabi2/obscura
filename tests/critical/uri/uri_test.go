// Package uri_test covers BIP21-style payment URIs (Block 35).
package uri_test

import (
	"strings"
	"testing"

	"obscura/pkg/commit"
	"obscura/pkg/uri"
)

func TestFormatParseRoundTrip(t *testing.T) {
	addr := commit.NewStealthKeys().Addr.String()
	cases := []struct{ amount, label string }{
		{"", ""},
		{"1.5", ""},
		{"", "Coffee"},
		{"0.001", "Order #42 — café"},
	}
	for _, tc := range cases {
		s := uri.Format(addr, tc.amount, tc.label)
		if !strings.HasPrefix(s, "obscura:") {
			t.Fatalf("URI missing scheme: %q", s)
		}
		gotAddr, gotAmt, gotLabel, err := uri.Parse(s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		if gotAddr != addr || gotAmt != tc.amount || gotLabel != tc.label {
			t.Fatalf("round-trip mismatch for %q:\n got addr=%q amount=%q label=%q\nwant addr=%q amount=%q label=%q",
				s, gotAddr, gotAmt, gotLabel, addr, tc.amount, tc.label)
		}
	}
}

func TestParseRejectsNonURI(t *testing.T) {
	if !uri.IsURI("obscura:abc") {
		t.Fatal("IsURI false for an obscura: string")
	}
	if uri.IsURI("bitcoin:abc") {
		t.Fatal("IsURI true for a non-obscura scheme")
	}
	if _, _, _, err := uri.Parse("bitcoin:abc"); err == nil {
		t.Fatal("parsed a non-obscura URI")
	}
	if _, _, _, err := uri.Parse("obscura:"); err == nil {
		t.Fatal("parsed a URI with no address")
	}
}

func TestParsedAddressIsValid(t *testing.T) {
	k := commit.NewStealthKeys()
	s := uri.Format(k.Addr.String(), "2.5", "tip")
	gotAddr, amount, _, err := uri.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	if amount != "2.5" {
		t.Fatalf("amount %q, want 2.5", amount)
	}
	parsed, err := commit.ParseHumanAddress(gotAddr)
	if err != nil {
		t.Fatalf("address from URI failed to parse: %v", err)
	}
	if parsed.A.Equal(k.Addr.A) != 1 || parsed.B.Equal(k.Addr.B) != 1 {
		t.Fatal("address from URI differs from original")
	}
}
