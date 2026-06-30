package swapd

import (
	"bytes"
	"crypto/rand"
	"math/big"
	"testing"

	"filippo.io/edwards25519"

	"obscura/pkg/nanocrypto"
)

// TestNanocryptoPinnedToSwapd guards against silent divergence between
// pkg/nanocrypto (the wasm-safe extraction used by the browser) and swapd's
// in-tree Nano primitives (the proven, library-cross-checked originals). swapd is
// deliberately NOT refactored to depend on nanocrypto, so this test is the single
// point that keeps the two byte-identical. If it ever fails, the browser would
// produce blocks a real node — or this node — would reject.
func TestNanocryptoPinnedToSwapd(t *testing.T) {
	// constants must match
	if nanocrypto.DefaultRep != defaultNanoRep {
		t.Fatalf("DefaultRep mismatch: %q != %q", nanocrypto.DefaultRep, defaultNanoRep)
	}
	if nanocrypto.SendDifficulty != nanoSendDifficulty || nanocrypto.ReceiveDifficulty != nanoReceiveDifficulty {
		t.Fatal("work-difficulty constants diverge")
	}

	for i := 0; i < 50; i++ {
		pub := make([]byte, 32)
		if _, err := rand.Read(pub); err != nil {
			t.Fatal(err)
		}
		// address codec must be byte-identical
		a1, err1 := nanocrypto.EncodeAddress(pub)
		a2, err2 := EncodeNanoAddress(pub)
		if err1 != nil || err2 != nil || a1 != a2 {
			t.Fatalf("EncodeAddress diverges: %q (%v) vs %q (%v)", a1, err1, a2, err2)
		}
		d1, _ := nanocrypto.DecodeAddress(a1)
		d2, _ := DecodeNanoAddress(a2)
		if !bytes.Equal(d1, d2) {
			t.Fatal("DecodeAddress diverges")
		}
	}

	// StateHash must be byte-identical for the same block fields.
	acct := bytes.Repeat([]byte{0x11}, 32)
	prev := bytes.Repeat([]byte{0x22}, 32)
	rep := bytes.Repeat([]byte{0x33}, 32)
	link := bytes.Repeat([]byte{0x44}, 32)
	bal, _ := new(big.Int).SetString("1000000000000000000000000", 10)
	if !bytes.Equal(nanocrypto.StateHash(acct, prev, rep, bal, link), nanoStateHash(acct, prev, rep, bal, link)) {
		t.Fatal("StateHash diverges between nanocrypto and swapd")
	}

	// Signatures use a hedged-random nonce so bytes differ per call, but the
	// SCHEME must be mutually compatible: each verifier must accept the other's
	// signature over the same key+message. That pins challenge() + the sign/verify
	// equations across both implementations.
	x := commitRandomScalar(t)
	pub := new(edwards25519.Point).ScalarBaseMult(x).Bytes()
	msg := nanocrypto.StateHash(acct, prev, rep, bal, link)
	if !nanoVerify(pub, msg, nanocrypto.Sign(x, msg)) {
		t.Fatal("swapd verify rejects nanocrypto signature")
	}
	if !nanocrypto.Verify(pub, msg, nanoSign(x, msg)) {
		t.Fatal("nanocrypto verify rejects swapd signature")
	}
}

func commitRandomScalar(t *testing.T) *edwards25519.Scalar {
	t.Helper()
	b := make([]byte, 64)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	s, err := new(edwards25519.Scalar).SetUniformBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
