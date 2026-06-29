// Package address_test covers Base58 encoding and the human-facing checksummed
// address format (Block 28): round-trips, leading-zero handling, and that typos /
// wrong-version / bad-checksum addresses are rejected.
package address_test

import (
	"bytes"
	"crypto/rand"
	"testing"

	"golang.org/x/crypto/blake2b"

	"obscura/pkg/base58"
	"obscura/pkg/commit"
)

func blake2bSum4(p []byte) []byte {
	s := blake2b.Sum256(p)
	return s[:4]
}

func TestBase58RoundTrip(t *testing.T) {
	for i := 0; i < 200; i++ {
		n := 1 + i%70
		b := make([]byte, n)
		rand.Read(b)
		got, err := base58.Decode(base58.Encode(b))
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !bytes.Equal(got, b) {
			t.Fatalf("round-trip mismatch:\n in =%x\n out=%x", b, got)
		}
	}
}

func TestBase58LeadingZeros(t *testing.T) {
	b := []byte{0, 0, 0, 1, 2, 3}
	got, err := base58.Decode(base58.Encode(b))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, b) {
		t.Fatalf("leading zeros lost: %x", got)
	}
}

func TestBase58RejectsInvalidChar(t *testing.T) {
	if _, err := base58.Decode("abc0OIl"); err == nil { // 0,O,I,l not in alphabet
		t.Fatal("invalid characters accepted")
	}
}

func TestAddressRoundTrip(t *testing.T) {
	for i := 0; i < 50; i++ {
		k := commit.NewStealthKeys()
		s := k.Addr.String()
		got, err := commit.ParseHumanAddress(s)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if got.A.Equal(k.Addr.A) != 1 || got.B.Equal(k.Addr.B) != 1 {
			t.Fatal("parsed address keys differ from original")
		}
	}
}

func TestAddressChecksumDetectsTypo(t *testing.T) {
	k := commit.NewStealthKeys()
	s := []byte(k.Addr.String())
	// flip a character to a different valid Base58 char near the middle
	pos := len(s) / 2
	orig := s[pos]
	for _, c := range []byte("123456789abcdefghij") {
		if c != orig {
			s[pos] = c
			break
		}
	}
	if _, err := commit.ParseHumanAddress(string(s)); err == nil {
		t.Fatal("a single-character typo passed the checksum")
	}
}

func TestAddressWrongVersionRejected(t *testing.T) {
	k := commit.NewStealthKeys()
	// build a blob with a wrong version byte but a valid checksum over it
	payload := append([]byte{commit.AddressVersion ^ 0xff}, k.Addr.Encode()...)
	// recompute a matching checksum so only the version is "wrong"
	// (ParseHumanAddress must reject on the version, not just the checksum)
	full := appendChecksum(payload)
	if _, err := commit.ParseHumanAddress(base58.Encode(full)); err == nil {
		t.Fatal("wrong version accepted")
	}
}

func TestAddressGarbageRejected(t *testing.T) {
	if _, err := commit.ParseHumanAddress("not-a-real-address"); err == nil {
		t.Fatal("garbage accepted as address")
	}
	if _, err := commit.ParseHumanAddress(""); err == nil {
		t.Fatal("empty string accepted as address")
	}
}

// appendChecksum mirrors the address checksum (blake2b first 4 bytes) so the test
// can isolate the version check. It re-implements the scheme deliberately.
func appendChecksum(payload []byte) []byte {
	sum := blake2bSum4(payload)
	return append(append([]byte(nil), payload...), sum...)
}
