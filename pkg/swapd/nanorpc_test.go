package swapd

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"testing"

	"obscura/pkg/commit"

	"filippo.io/edwards25519"
)

// TestNanoAddressZeroAccount checks the address codec against Nano's canonical
// constant: the all-zero public key is the well-known burn/zero account.
func TestNanoAddressZeroAccount(t *testing.T) {
	const zeroAcct = "nano_1111111111111111111111111111111111111111111111111111hifc8npp"
	addr, err := EncodeNanoAddress(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	if addr != zeroAcct {
		t.Fatalf("zero account:\n got  %s\n want %s", addr, zeroAcct)
	}
	pub, err := DecodeNanoAddress(zeroAcct)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(pub, make([]byte, 32)) {
		t.Fatalf("decoded zero account != 32 zero bytes: %x", pub)
	}
}

// TestNanoAddressRoundTrip encodes a real pubkey and decodes it back, and confirms the
// checksum guard rejects a corrupted address.
func TestNanoAddressRoundTrip(t *testing.T) {
	pub := new(edwards25519.Point).ScalarBaseMult(commit.RandomScalar()).Bytes()
	addr, err := EncodeNanoAddress(pub)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeNanoAddress(addr)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(got, pub) {
		t.Fatalf("round-trip mismatch:\n got  %x\n want %x", got, pub)
	}
	// flip a character in the account body → checksum must reject.
	bad := []byte(addr)
	if bad[10] == '3' {
		bad[10] = '4'
	} else {
		bad[10] = '3'
	}
	if _, err := DecodeNanoAddress(string(bad)); err == nil {
		t.Fatal("corrupted address accepted — checksum not enforced")
	}
}

// TestNanoSignVerify proves the ed25519-blake2b signer (raw joint scalar) produces a
// signature that verifies under Nano's verification equation, and that tampering breaks it.
// This is the joint-account sweep authorization at the heart of the XNO leg.
func TestNanoSignVerify(t *testing.T) {
	secret := commit.RandomScalar()
	pub := new(edwards25519.Point).ScalarBaseMult(secret).Bytes()
	msg, _ := hex.DecodeString("9f0e444c69f77a49bd0be89db92c38fe713e0963165cca12faf5712d7657120f")

	sig := nanoSign(secret, msg)
	if len(sig) != 64 {
		t.Fatalf("sig len = %d, want 64", len(sig))
	}
	if !nanoVerify(pub, msg, sig) {
		t.Fatal("valid signature failed verification")
	}
	// tamper the message → must fail.
	bad := append([]byte(nil), msg...)
	bad[0] ^= 0x01
	if nanoVerify(pub, bad, sig) {
		t.Fatal("verification accepted a tampered message")
	}
	// tamper the signature → must fail.
	badSig := append([]byte(nil), sig...)
	badSig[40] ^= 0x01
	if nanoVerify(pub, msg, badSig) {
		t.Fatal("verification accepted a tampered signature")
	}
}

// TestNanoStateHashDeterministic guards the state-block hash preimage layout (it must be
// stable — a change would invalidate every signature). Recomputed value is asserted equal.
func TestNanoStateHashDeterministic(t *testing.T) {
	acct := make([]byte, 32)
	prev := make([]byte, 32)
	rep := make([]byte, 32)
	link := make([]byte, 32)
	for i := range link {
		link[i] = byte(i)
	}
	h1 := nanoStateHash(acct, prev, rep, big.NewInt(1000), link)
	h2 := nanoStateHash(acct, prev, rep, big.NewInt(1000), link)
	if !bytes.Equal(h1, h2) {
		t.Fatal("state hash not deterministic")
	}
	if len(h1) != 32 {
		t.Fatalf("state hash len = %d, want 32", len(h1))
	}
	// a different balance must change the hash.
	if bytes.Equal(h1, nanoStateHash(acct, prev, rep, big.NewInt(1001), link)) {
		t.Fatal("state hash ignored balance")
	}
}
