package pqsign

import (
	"bytes"
	"testing"
)

func TestHybridRoundTrip(t *testing.T) {
	priv, pub, err := GenerateHybrid()
	if err != nil {
		t.Fatal(err)
	}
	if len(pub.Key) != 32 {
		t.Fatalf("key size %d", len(pub.Key))
	}
	msg := []byte("hybrid spend of output #7")
	sig, err := HybridSign(priv, pub, msg)
	if err != nil {
		t.Fatal(err)
	}
	if !HybridVerify(pub.Key, pub.P, pub.R, msg, sig) {
		t.Fatal("valid hybrid signature rejected")
	}
}

func TestHybridWrongMessage(t *testing.T) {
	priv, pub, _ := GenerateHybrid()
	sig, _ := HybridSign(priv, pub, []byte("pay alice 5"))
	if HybridVerify(pub.Key, pub.P, pub.R, []byte("pay alice 500"), sig) {
		t.Fatal("forged message accepted")
	}
}

// Breaking only the classical half (forging Schnorr) must NOT yield a valid
// spend, because the WOTS+ half still has to verify. We simulate a "classical
// break" by swapping in a fresh, fully-valid Schnorr from another key while
// keeping the original committed key — verification must fail because R no
// longer matches and/or the WOTS+ half is absent.
func TestHybridRequiresBothHalves(t *testing.T) {
	priv, pub, _ := GenerateHybrid()
	msg := []byte("m")
	sig, _ := HybridSign(priv, pub, msg)

	// Tamper the WOTS+ half only — Schnorr stays valid.
	badW := append([]byte(nil), sig.Wots...)
	badW[100] ^= 0xff
	if HybridVerify(pub.Key, pub.P, pub.R, msg, &HybridSig{Schnorr: sig.Schnorr, Wots: badW}) {
		t.Fatal("accepted with broken WOTS+ half (PQ protection bypassed!)")
	}

	// Tamper the Schnorr half only — WOTS+ stays valid.
	badS := append([]byte(nil), sig.Schnorr...)
	badS[10] ^= 0xff
	if HybridVerify(pub.Key, pub.P, pub.R, msg, &HybridSig{Schnorr: badS, Wots: sig.Wots}) {
		t.Fatal("accepted with broken Schnorr half")
	}
}

// The committed key binds P and R: substituting a different R (e.g. an
// attacker's WOTS+ key) must fail the key-commitment check.
func TestHybridKeyBinding(t *testing.T) {
	priv, pub, _ := GenerateHybrid()
	_, pub2, _ := GenerateHybrid()
	msg := []byte("m")
	sig, _ := HybridSign(priv, pub, msg)
	if HybridVerify(pub.Key, pub.P, pub2.R, msg, sig) {
		t.Fatal("accepted substituted R against committed key")
	}
	if !bytes.Equal(HybridKeyOf(pub.P, pub.R), pub.Key) {
		t.Fatal("HybridKeyOf mismatch")
	}
}
