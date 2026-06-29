// Package keystore_test covers passphrase encryption of the wallet seed at rest
// (Block 24): round-trip, wrong-passphrase rejection, tamper detection, legacy
// detection, and that the wallet derived after decryption matches the original.
package keystore_test

import (
	"bytes"
	"testing"
	"time"

	"obscura/pkg/keystore"
	"obscura/pkg/wallet"
)

var seed = []byte("0123456789abcdef0123456789abcdef") // 32-byte seed
var pass = []byte("correct horse battery staple")

// Tests use a cheap KDF cost so the suite stays fast; production defaults remain
// memory-hard. The params are stored per-blob, so Decrypt cost matches Encrypt.
func init() {
	keystore.DefaultTime = 1
	keystore.DefaultMemKiB = 1024 // 1 MiB
	keystore.DefaultThreads = 1
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	blob, err := keystore.Encrypt(seed, pass)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !keystore.IsEncrypted(blob) {
		t.Fatal("blob not recognized as encrypted")
	}
	if bytes.Contains(blob, seed) {
		t.Fatal("plaintext seed appears in the ciphertext")
	}
	got, err := keystore.Decrypt(blob, pass)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, seed) {
		t.Fatal("decrypted seed does not match")
	}
	// the recovered seed must derive the same wallet
	if !bytes.Equal(wallet.FromSeed(got).AddressBytes(), wallet.FromSeed(seed).AddressBytes()) {
		t.Fatal("wallet derived from decrypted seed differs")
	}
}

func TestWrongPassphraseFails(t *testing.T) {
	blob, err := keystore.Encrypt(seed, pass)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := keystore.Decrypt(blob, []byte("wrong passphrase")); err == nil {
		t.Fatal("decrypt succeeded with the wrong passphrase")
	}
}

func TestTamperDetected(t *testing.T) {
	blob, err := keystore.Encrypt(seed, pass)
	if err != nil {
		t.Fatal(err)
	}
	// flip a bit in the ciphertext body (last byte = within the tag/ct) — AEAD
	// must reject it rather than return garbage.
	bad := append([]byte(nil), blob...)
	bad[len(bad)-1] ^= 0x01
	if _, err := keystore.Decrypt(bad, pass); err == nil {
		t.Fatal("decrypt accepted tampered ciphertext")
	}

	// tamper the salt (part of the authenticated header, and changes the derived
	// key) — must be rejected by the AEAD.
	bad2 := append([]byte(nil), blob...)
	bad2[5+1] ^= 0x01 // first salt byte
	if _, err := keystore.Decrypt(bad2, pass); err == nil {
		t.Fatal("decrypt accepted tampered salt")
	}

	// tamper the `time` parameter to an astronomical value — Decrypt must reject
	// it on the params bound BEFORE running a multi-hour KDF (DoS protection), and
	// it must return quickly.
	bad3 := append([]byte(nil), blob...)
	bad3[5+1+16] = 0xff // high byte of the BE uint32 `time`
	done := make(chan error, 1)
	go func() { _, e := keystore.Decrypt(bad3, pass); done <- e }()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("decrypt accepted astronomical KDF time parameter")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("decrypt with tampered time param did not reject promptly (DoS)")
	}
}

func TestUniqueCiphertexts(t *testing.T) {
	// random salt + nonce → two encryptions of the same seed differ.
	a, _ := keystore.Encrypt(seed, pass)
	b, _ := keystore.Encrypt(seed, pass)
	if bytes.Equal(a, b) {
		t.Fatal("two encryptions produced identical blobs (salt/nonce not random)")
	}
}

func TestLegacyPlaintextNotMisdetected(t *testing.T) {
	// a legacy plaintext-hex seed file must NOT look like an encrypted blob.
	legacy := []byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if keystore.IsEncrypted(legacy) {
		t.Fatal("plaintext hex misdetected as encrypted")
	}
	if _, err := keystore.Decrypt(legacy, pass); err == nil {
		t.Fatal("Decrypt accepted a non-keystore input")
	}
}

func TestEmptyPassphraseRejected(t *testing.T) {
	if _, err := keystore.Encrypt(seed, nil); err == nil {
		t.Fatal("Encrypt accepted an empty passphrase")
	}
}
