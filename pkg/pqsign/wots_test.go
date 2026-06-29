package pqsign

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func seed(t *testing.T) []byte {
	t.Helper()
	s := make([]byte, SeedSize)
	if _, err := rand.Read(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSignVerifyRoundTrip(t *testing.T) {
	s := seed(t)
	pub, err := KeyGen(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(pub) != PubKeySize {
		t.Fatalf("pub size = %d want %d", len(pub), PubKeySize)
	}
	msg := []byte("spend authorization for output #42")
	sig, err := Sign(s, msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(sig) != SigSize {
		t.Fatalf("sig size = %d want %d", len(sig), SigSize)
	}
	if !Verify(pub, msg, sig) {
		t.Fatal("valid signature rejected")
	}
}

func TestDeterministic(t *testing.T) {
	s := seed(t)
	p1, _ := KeyGen(s)
	p2, _ := KeyGen(s)
	if !bytes.Equal(p1, p2) {
		t.Fatal("KeyGen not deterministic")
	}
	msg := []byte("hello")
	a, _ := Sign(s, msg)
	b, _ := Sign(s, msg)
	if !bytes.Equal(a, b) {
		t.Fatal("Sign not deterministic")
	}
}

func TestWrongMessageRejected(t *testing.T) {
	s := seed(t)
	pub, _ := KeyGen(s)
	sig, _ := Sign(s, []byte("pay alice"))
	if Verify(pub, []byte("pay mallory"), sig) {
		t.Fatal("signature verified against wrong message (forgery!)")
	}
}

func TestWrongKeyRejected(t *testing.T) {
	s1, s2 := seed(t), seed(t)
	pub2, _ := KeyGen(s2)
	msg := []byte("x")
	sig1, _ := Sign(s1, msg)
	if Verify(pub2, msg, sig1) {
		t.Fatal("signature verified against wrong public key")
	}
}

// TestForgeByFlippingDigits is the core WOTS+ security property: an attacker who
// sees one signature cannot produce a valid signature for a different message.
// Because the checksum moves opposite to the message digits, advancing some
// chains forces others to go backwards — which is one-way-hard.
func TestTamperedSignatureRejected(t *testing.T) {
	s := seed(t)
	pub, _ := KeyGen(s)
	msg := []byte("amount: 100")
	sig, _ := Sign(s, msg)

	for i := 0; i < len(sig); i += 257 { // sample-tamper across the signature
		bad := append([]byte(nil), sig...)
		bad[i] ^= 0x01
		if Verify(pub, msg, bad) {
			t.Fatalf("tampered signature (byte %d) accepted", i)
		}
	}
}

func TestBadLengths(t *testing.T) {
	if _, err := KeyGen(make([]byte, 31)); err == nil {
		t.Fatal("KeyGen accepted short seed")
	}
	if _, err := Sign(make([]byte, 16), nil); err == nil {
		t.Fatal("Sign accepted short seed")
	}
	s := seed(t)
	pub, _ := KeyGen(s)
	if Verify(pub, []byte("m"), make([]byte, SigSize-1)) {
		t.Fatal("Verify accepted short signature")
	}
	if Verify(make([]byte, PubKeySize-1), []byte("m"), make([]byte, SigSize)) {
		t.Fatal("Verify accepted short pubkey")
	}
}

func BenchmarkSign(b *testing.B) {
	s := make([]byte, SeedSize)
	rand.Read(s)
	msg := []byte("benchmark message")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Sign(s, msg)
	}
}

func BenchmarkVerify(b *testing.B) {
	s := make([]byte, SeedSize)
	rand.Read(s)
	pub, _ := KeyGen(s)
	msg := []byte("benchmark message")
	sig, _ := Sign(s, msg)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Verify(pub, msg, sig)
	}
}
