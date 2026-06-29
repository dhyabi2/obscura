package pqstealth

import (
	"crypto/rand"
	"testing"
)

func TestStealthRoundTrip(t *testing.T) {
	v, err := GenerateViewKey()
	if err != nil {
		t.Fatal(err)
	}
	ann, ssSend, err := Send(v.PublicKey(), 1234567)
	if err != nil {
		t.Fatal(err)
	}
	amt, ssRecv, ok := v.Scan(ann)
	if !ok {
		t.Fatal("recipient failed to detect own payment")
	}
	if amt != 1234567 {
		t.Fatalf("amount = %d want 1234567", amt)
	}
	if string(ssSend) != string(ssRecv) {
		t.Fatal("shared secret mismatch sender vs recipient")
	}
}

func TestStealthNotMine(t *testing.T) {
	alice, _ := GenerateViewKey()
	bob, _ := GenerateViewKey()
	ann, _, _ := Send(alice.PublicKey(), 99)
	if _, _, ok := bob.Scan(ann); ok {
		t.Fatal("bob detected a payment that was for alice")
	}
}

func TestStealthTamperedAmount(t *testing.T) {
	v, _ := GenerateViewKey()
	ann, _, _ := Send(v.PublicKey(), 500)
	ann.EncAmount[0] ^= 0xff // flip amount, leave MAC
	if _, _, ok := v.Scan(ann); ok {
		t.Fatal("accepted tampered amount (MAC bypass!)")
	}
}

func TestViewKeyFromSeedDeterministic(t *testing.T) {
	seed := make([]byte, 64)
	rand.Read(seed)
	v1, err := ViewKeyFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := ViewKeyFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	if string(v1.PublicKey()) != string(v2.PublicKey()) {
		t.Fatal("seed-derived view key not deterministic")
	}
	// a payment to v1 must be detectable by v2 (same key)
	ann, _, _ := Send(v1.PublicKey(), 7)
	if amt, _, ok := v2.Scan(ann); !ok || amt != 7 {
		t.Fatal("seed-derived twin failed to detect payment")
	}
}

func TestStealthBadInputs(t *testing.T) {
	v, _ := GenerateViewKey()
	if _, _, err := Send([]byte("too short"), 1); err == nil {
		t.Fatal("Send accepted bad recipient key")
	}
	if _, _, ok := v.Scan(nil); ok {
		t.Fatal("Scan accepted nil")
	}
	if _, _, ok := v.Scan(&Announcement{}); ok {
		t.Fatal("Scan accepted empty announcement")
	}
}
