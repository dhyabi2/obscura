// Package txproof_test covers payment (receipt) proofs and the underlying DLEQ
// (Block 27): a valid proof reveals the amount, a wrong address / forged / tampered
// proof fails, and the wallet can prove a scanned output.
package txproof_test

import (
	"testing"

	"filippo.io/edwards25519"

	"obscura/pkg/commit"
	"obscura/pkg/wallet"
	"obscura/tests/critical/harness"
)

const amount = 1234567

func mustPoint(t *testing.T, b []byte) *edwards25519.Point {
	t.Helper()
	p, err := new(edwards25519.Point).SetBytes(b)
	if err != nil {
		t.Fatalf("point: %v", err)
	}
	return p
}

// makeOutput builds a stealth output paying `amount` to addr, returning the
// output and its on-chain encrypted-amount field.
func makeOutput(addr commit.StealthAddress) (*commit.StealthOutput, []byte) {
	r := commit.RandomScalar()
	out := commit.CreateOutputDeterministic(addr, r)
	shared := commit.SharedSecretSender(addr, r)
	return out, commit.EncryptAmount(shared, amount)
}

func TestReceiptProofValidRevealsAmount(t *testing.T) {
	k := commit.NewStealthKeys()
	out, enc := makeOutput(k.Addr)

	pp := k.ProveReceipt(out, enc)
	got, ok := commit.VerifyPayment(k.Addr, out, enc, pp)
	if !ok {
		t.Fatal("valid receipt proof rejected")
	}
	if got != amount {
		t.Fatalf("revealed amount %d, want %d", got, amount)
	}
}

func TestProofDoesNotVerifyForWrongAddress(t *testing.T) {
	k := commit.NewStealthKeys()
	other := commit.NewStealthKeys()
	out, enc := makeOutput(k.Addr)
	pp := k.ProveReceipt(out, enc)
	if _, ok := commit.VerifyPayment(other.Addr, out, enc, pp); ok {
		t.Fatal("proof verified against the wrong address")
	}
}

func TestNonRecipientCannotForge(t *testing.T) {
	k := commit.NewStealthKeys()        // real recipient
	attacker := commit.NewStealthKeys() // does not own the output
	out, enc := makeOutput(k.Addr)

	// attacker tries to prove THEY received it (claiming their own address)
	forged := attacker.ProveReceipt(out, enc)
	if _, ok := commit.VerifyPayment(attacker.Addr, out, enc, forged); ok {
		t.Fatal("attacker forged a receipt for an output they don't own")
	}
	// attacker cannot make a valid proof for the real address either (no view key)
	if _, ok := commit.VerifyPayment(k.Addr, out, enc, forged); ok {
		t.Fatal("attacker's proof verified for the real address")
	}
}

func TestTamperedProofFails(t *testing.T) {
	k := commit.NewStealthKeys()
	out, enc := makeOutput(k.Addr)
	pp := k.ProveReceipt(out, enc)

	// tamper the shared point D
	bad := pp.Serialize()
	bad[0] ^= 0x01
	if parsed, err := commit.ParsePaymentProof(bad); err == nil {
		if _, ok := commit.VerifyPayment(k.Addr, out, enc, parsed); ok {
			t.Fatal("tampered D accepted")
		}
	}
	// tamper the response scalar S
	bad2 := pp.Serialize()
	bad2[95] ^= 0x01
	if parsed, err := commit.ParsePaymentProof(bad2); err == nil {
		if _, ok := commit.VerifyPayment(k.Addr, out, enc, parsed); ok {
			t.Fatal("tampered S accepted")
		}
	}
}

// TestForgedAmountRejected is the regression for the audit finding that the
// receipt amount was unauthenticated: re-encrypting a DIFFERENT amount under the
// (public) shared point D must NO LONGER verify, because encAmount is now bound
// into the proof's challenge.
func TestForgedAmountRejected(t *testing.T) {
	k := commit.NewStealthKeys()
	out, enc := makeOutput(k.Addr)
	pp := k.ProveReceipt(out, enc)

	// sanity: honest amount verifies
	if got, ok := commit.VerifyPayment(k.Addr, out, enc, pp); !ok || got != amount {
		t.Fatalf("honest verify failed: ok=%v got=%d", ok, got)
	}
	// forge: D is public (in pp). Re-encrypt a different amount under D.
	fakeEnc := commit.EncryptAmount(pp.D.Bytes(), amount*7)
	if _, ok := commit.VerifyPayment(k.Addr, out, fakeEnc, pp); ok {
		t.Fatal("forged (re-encrypted) amount was accepted — receipt amount is unauthenticated")
	}
}

func TestSerializeRoundTrip(t *testing.T) {
	k := commit.NewStealthKeys()
	out, enc := makeOutput(k.Addr)
	pp := k.ProveReceipt(out, enc)
	b := pp.Serialize()
	if len(b) != 96 {
		t.Fatalf("serialized length %d, want 96", len(b))
	}
	parsed, err := commit.ParsePaymentProof(b)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, ok := commit.VerifyPayment(k.Addr, out, enc, parsed); !ok {
		t.Fatal("round-tripped proof failed to verify")
	}
}

func TestDLEQSoundness(t *testing.T) {
	// honest proof verifies
	x := commit.RandomScalar()
	g2 := new(edwards25519.Point).ScalarBaseMult(commit.RandomScalar()) // arbitrary second base
	p1, p2, proof := commit.ProveDLEQ(x, commit.BasePoint, g2, "test")
	if !commit.VerifyDLEQ(commit.BasePoint, p1, g2, p2, proof, "test") {
		t.Fatal("honest DLEQ rejected")
	}
	// a P2 with a DIFFERENT discrete log must fail
	p2bad := new(edwards25519.Point).ScalarMult(commit.RandomScalar(), g2)
	if commit.VerifyDLEQ(commit.BasePoint, p1, g2, p2bad, proof, "test") {
		t.Fatal("DLEQ accepted mismatched discrete logs")
	}
	// wrong domain must fail
	if commit.VerifyDLEQ(commit.BasePoint, p1, g2, p2, proof, "other") {
		t.Fatal("DLEQ accepted under the wrong domain")
	}
}

func TestSenderOutProof(t *testing.T) {
	// the SENDER, who knows r, proves they paid `addr` — verified by the same
	// VerifyPayment used for recipient proofs.
	recipient := commit.NewStealthKeys()
	r := commit.RandomScalar()
	out := commit.CreateOutputDeterministic(recipient.Addr, r)
	enc := commit.EncryptAmount(commit.SharedSecretSender(recipient.Addr, r), amount)

	pp := commit.ProveSpend(r, recipient.Addr, enc)
	got, ok := commit.VerifyPayment(recipient.Addr, out, enc, pp)
	if !ok {
		t.Fatal("sender out-proof rejected")
	}
	if got != amount {
		t.Fatalf("out-proof amount %d, want %d", got, amount)
	}
	// wrong address must fail
	if _, ok := commit.VerifyPayment(commit.NewStealthKeys().Addr, out, enc, pp); ok {
		t.Fatal("out-proof verified for the wrong address")
	}
}

func TestWalletSenderProofEndToEnd(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("tp-send-alice")
	harness.Funded(t, c, alice, 3)
	bob := harness.NewWallet("tp-send-bob")

	txn, err := alice.CreateTransaction(c, bob.Address(), 2_000_000_000, 100_000_000)
	if err != nil {
		t.Fatalf("tx: %v", err)
	}
	alice.RecordSent(txn, bob.Address(), 2_000_000_000)
	s := alice.FindSent(txn.HashHex())
	if s == nil || len(s.DestR) != 32 {
		t.Fatal("sender did not retain the tx secret r")
	}
	bundle, err := alice.ProveSpendBundle(s)
	if err != nil {
		t.Fatalf("prove spend: %v", err)
	}
	amt, ok := commit.VerifyBundle(bob.Address(), bundle)
	if !ok {
		t.Fatal("sender proof failed to verify against the recipient address")
	}
	if amt != 2_000_000_000 {
		t.Fatalf("proven amount %d, want 2000000000", amt)
	}
	// and it must NOT verify for an unrelated address
	if _, ok := commit.VerifyBundle(alice.Address(), bundle); ok {
		t.Fatal("sender proof verified for a non-recipient address")
	}
}

func TestReceiptBundleRoundTrip(t *testing.T) {
	k := commit.NewStealthKeys()
	out, enc := makeOutput(k.Addr)
	bundle := commit.ReceiptBundle{Out: out, EncAmount: enc, Proof: k.ProveReceipt(out, enc)}

	b := bundle.Serialize()
	if len(b) != 168 {
		t.Fatalf("bundle length %d, want 168", len(b))
	}
	parsed, err := commit.ParseReceiptBundle(b)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, ok := commit.VerifyBundle(k.Addr, parsed)
	if !ok || got != amount {
		t.Fatalf("bundle verify ok=%v amount=%d (want %d)", ok, got, amount)
	}
	// wrong address must fail
	if _, ok := commit.VerifyBundle(commit.NewStealthKeys().Addr, parsed); ok {
		t.Fatal("bundle verified against the wrong address")
	}
}

// TestWalletProvesScannedOutput: end-to-end via the wallet on a real chain.
func TestWalletProvesScannedOutput(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("tp-alice")
	harness.Funded(t, c, alice, 2)

	var owned *wallet.OwnedOutput
	for _, o := range alice.Outputs {
		if !o.Spent {
			owned = o
			break
		}
	}
	if owned == nil {
		t.Fatal("no owned output")
	}
	pp, err := alice.ProvePayment(owned)
	if err != nil {
		t.Fatalf("prove: %v", err)
	}
	so := &commit.StealthOutput{P: mustPoint(t, owned.Out.OneTimeKey), R: mustPoint(t, owned.Out.TxPubKey)}
	amt, ok := commit.VerifyPayment(alice.Address(), so, owned.Out.EncAmount, pp)
	if !ok {
		t.Fatal("wallet proof failed to verify")
	}
	if amt != owned.Amount {
		t.Fatalf("proof amount %d != owned amount %d", amt, owned.Amount)
	}
}
