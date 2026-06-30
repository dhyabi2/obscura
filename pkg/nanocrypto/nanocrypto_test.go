package nanocrypto

import (
	"bytes"
	"crypto/rand"
	"math/big"
	"testing"

	"filippo.io/edwards25519"
)

// zeroAccount is the canonical nano_ address of the all-zero public key (the
// well-known Nano "burn"/zero account). It is a fixed ground-truth vector that
// pins the base32 codec independent of any in-repo implementation.
const zeroAccount = "nano_1111111111111111111111111111111111111111111111111111hifc8npp"

func TestEncodeAddressZeroKAT(t *testing.T) {
	got, err := EncodeAddress(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	if got != zeroAccount {
		t.Fatalf("zero-account encode mismatch:\n got %q\nwant %q", got, zeroAccount)
	}
	back, err := DecodeAddress(zeroAccount)
	if err != nil {
		t.Fatalf("decode zero account: %v", err)
	}
	if !bytes.Equal(back, make([]byte, 32)) {
		t.Fatal("zero account did not decode back to 32 zero bytes")
	}
}

func TestAddressRoundTrip(t *testing.T) {
	for i := 0; i < 100; i++ {
		pub := make([]byte, 32)
		if _, err := rand.Read(pub); err != nil {
			t.Fatal(err)
		}
		// a random 32-byte value may not be a valid curve point, but the address
		// codec is a pure bit-encoding over the 32 bytes, so it must round-trip.
		addr, err := EncodeAddress(pub)
		if err != nil {
			t.Fatal(err)
		}
		back, err := DecodeAddress(addr)
		if err != nil {
			t.Fatalf("decode %q: %v", addr, err)
		}
		if !bytes.Equal(back, pub) {
			t.Fatalf("round-trip mismatch for %x via %q", pub, addr)
		}
	}
}

func TestDecodeRejectsBadChecksum(t *testing.T) {
	bad := []byte(zeroAccount)
	bad[10] ^= 1 // flip a char in the account body so the checksum no longer matches
	// keep it a valid alphabet char
	if _, ok := decodeRune(bad[10]); !ok {
		bad[10] = '3'
	}
	if _, err := DecodeAddress(string(bad)); err == nil {
		t.Fatal("expected checksum mismatch to be rejected")
	}
}

func TestSignVerifyRoundTrip(t *testing.T) {
	x, err := new(edwards25519.Scalar).SetUniformBytes(randUniform(t))
	if err != nil {
		t.Fatal(err)
	}
	pub := PubFromSecret(x)
	msg := []byte("the 32-byte block hash goes here")
	sig := Sign(x, msg[:32])
	if !Verify(pub, msg[:32], sig) {
		t.Fatal("valid signature rejected")
	}
	// tampered message must fail
	bad := append([]byte(nil), msg[:32]...)
	bad[0] ^= 0xff
	if Verify(pub, bad, sig) {
		t.Fatal("verify accepted a tampered message")
	}
	// tampered signature must fail
	badSig := append([]byte(nil), sig...)
	badSig[40] ^= 0xff
	if Verify(pub, msg[:32], badSig) {
		t.Fatal("verify accepted a tampered signature")
	}
}

func TestStateHashDeterministicAndSensitive(t *testing.T) {
	acct := bytes.Repeat([]byte{0x01}, 32)
	prev := bytes.Repeat([]byte{0x02}, 32)
	rep := bytes.Repeat([]byte{0x03}, 32)
	link := bytes.Repeat([]byte{0x04}, 32)
	h1 := StateHash(acct, prev, rep, big.NewInt(1000), link)
	h2 := StateHash(acct, prev, rep, big.NewInt(1000), link)
	if !bytes.Equal(h1, h2) {
		t.Fatal("StateHash is not deterministic")
	}
	if bytes.Equal(h1, StateHash(acct, prev, rep, big.NewInt(1001), link)) {
		t.Fatal("StateHash did not change with the balance")
	}
	if len(h1) != 32 {
		t.Fatalf("StateHash len = %d, want 32", len(h1))
	}
}

func randUniform(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 64)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}
