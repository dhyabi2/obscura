//go:build pq

package pqtx

import (
	"testing"

	"obscura/pkg/pqsign"
)

func newHybridPub(t *testing.T) (*pqsign.HybridPub, *pqsign.HybridPriv, error) {
	t.Helper()
	priv, pub, err := pqsign.GenerateHybrid()
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv, nil
}

// TestEndToEnd exercises the full PQ output+spend lifecycle through the ledger:
// fund Alice → Alice detects/decrypts (PQ stealth) → Alice spends to Bob with
// change, authorized by HybridVerify → ledger conserves value and records the
// nullifier → Bob detects his output → double-spend is rejected.
func TestEndToEnd(t *testing.T) {
	led := NewLedger()
	alice, err := NewAccount()
	if err != nil {
		t.Fatal(err)
	}
	bob, err := NewAccount()
	if err != nil {
		t.Fatal(err)
	}

	// --- fund Alice with a genesis output of 1000 ---
	aliceRecv, err := alice.NewReceiveKey()
	if err != nil {
		t.Fatal(err)
	}
	genesis, _, err := BuildOutput(alice.StealthPub(), aliceRecv, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if err := led.AddOutput(genesis); err != nil {
		t.Fatal(err)
	}

	// --- Alice detects and values her output (post-quantum stealth) ---
	det, ok := alice.Scan(genesis)
	if !ok {
		t.Fatal("Alice failed to detect her own funding output")
	}
	if det.Amount != 1000 {
		t.Fatalf("detected amount %d, want 1000", det.Amount)
	}
	if _, ok := bob.Scan(genesis); ok {
		t.Fatal("Bob detected an output that was Alice's")
	}

	// --- Alice spends: 600 to Bob, 350 change to herself, 50 fee ---
	bobRecv, _ := bob.NewReceiveKey()
	aliceChange, _ := alice.NewReceiveKey()
	spend, err := BuildSpend(det, []Payment{
		{StealthPub: bob.StealthPub(), Hybrid: bobRecv, Amount: 600},
		{StealthPub: alice.StealthPub(), Hybrid: aliceChange, Amount: 350},
	}, 50)
	if err != nil {
		t.Fatal(err)
	}

	root := led.Root()
	member, err := led.Prove(det.Out.OneTimeKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := led.ValidateSpend(spend, root, member); err != nil {
		t.Fatalf("valid spend rejected: %v", err)
	}
	if err := led.ApplySpend(spend, root, member); err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	// --- Bob detects his 600 output ---
	var bobOut *PQOutput
	for i := range spend.Outputs {
		if d, ok := bob.Scan(&spend.Outputs[i]); ok {
			if d.Amount != 600 {
				t.Fatalf("Bob's amount %d, want 600", d.Amount)
			}
			bobOut = &spend.Outputs[i]
		}
	}
	if bobOut == nil {
		t.Fatal("Bob did not detect his payment")
	}

	// --- double-spend rejected ---
	if err := led.ApplySpend(spend, root, member); err == nil {
		t.Fatal("double-spend was accepted")
	}
}

func TestTamperedSignatureRejected(t *testing.T) {
	led, alice, det := fundedAlice(t)
	bob, _ := NewAccount()
	bobRecv, _ := bob.NewReceiveKey()
	change, _ := alice.NewReceiveKey()
	spend, err := BuildSpend(det, []Payment{
		{StealthPub: bob.StealthPub(), Hybrid: bobRecv, Amount: 900},
		{StealthPub: alice.StealthPub(), Hybrid: change, Amount: 90},
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	root := led.Root()
	member, _ := led.Prove(det.Out.OneTimeKey)

	// flip a byte in the WOTS+ half of the hybrid signature
	bad := *spend
	bad.HybridSig = append([]byte(nil), spend.HybridSig...)
	bad.HybridSig[len(bad.HybridSig)-1] ^= 0xff
	if err := led.ValidateSpend(&bad, root, member); err == nil {
		t.Fatal("accepted a tampered hybrid signature")
	}
}

func TestWrongClassicalKeyRejected(t *testing.T) {
	led, alice, det := fundedAlice(t)
	bob, _ := NewAccount()
	bobRecv, _ := bob.NewReceiveKey()
	change, _ := alice.NewReceiveKey()
	spend, _ := BuildSpend(det, []Payment{
		{StealthPub: bob.StealthPub(), Hybrid: bobRecv, Amount: 990},
		{StealthPub: alice.StealthPub(), Hybrid: change, Amount: 9},
	}, 1)
	root := led.Root()
	member, _ := led.Prove(det.Out.OneTimeKey)

	// substitute a different classical point P — the hybrid key won't reconstruct
	other, _, _ := newHybridPub(t)
	bad := *spend
	bad.P = other.P
	if err := led.ValidateSpend(&bad, root, member); err == nil {
		t.Fatal("accepted a spend with a substituted classical key")
	}
}

func TestConservationViolationRejected(t *testing.T) {
	led, alice, det := fundedAlice(t)
	bob, _ := NewAccount()
	bobRecv, _ := bob.NewReceiveKey()
	change, _ := alice.NewReceiveKey()
	spend, _ := BuildSpend(det, []Payment{
		{StealthPub: bob.StealthPub(), Hybrid: bobRecv, Amount: 500},
		{StealthPub: alice.StealthPub(), Hybrid: change, Amount: 499},
	}, 1)
	root := led.Root()
	member, _ := led.Prove(det.Out.OneTimeKey)

	// corrupt the aggregate blinding witness → conservation must fail
	bad := *spend
	bad.BlindDiff = append([]int32(nil), spend.BlindDiff...)
	bad.BlindDiff[0] += 1
	if err := led.ValidateSpend(&bad, root, member); err == nil {
		t.Fatal("accepted a spend that fails value conservation")
	}
}

func TestForgedMembershipRejected(t *testing.T) {
	led, alice, det := fundedAlice(t)
	bob, _ := NewAccount()
	bobRecv, _ := bob.NewReceiveKey()
	_ = alice
	spend, _ := BuildSpend(det, []Payment{
		{StealthPub: bob.StealthPub(), Hybrid: bobRecv, Amount: 1000},
	}, 0)
	root := led.Root()
	member, _ := led.Prove(det.Out.OneTimeKey)
	// verify the honest path works
	if err := led.ValidateSpend(spend, root, member); err != nil {
		t.Fatalf("honest spend rejected: %v", err)
	}
	// a wrong root must fail membership
	badRoot := append([]byte(nil), root...)
	badRoot[0] ^= 0xff
	if err := led.ValidateSpend(spend, badRoot, member); err == nil {
		t.Fatal("accepted membership against a wrong root")
	}
}

// --- helpers ---

func fundedAlice(t *testing.T) (*Ledger, *Account, *Detected) {
	t.Helper()
	led := NewLedger()
	alice, err := NewAccount()
	if err != nil {
		t.Fatal(err)
	}
	recv, err := alice.NewReceiveKey()
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := BuildOutput(alice.StealthPub(), recv, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if err := led.AddOutput(out); err != nil {
		t.Fatal(err)
	}
	det, ok := alice.Scan(out)
	if !ok {
		t.Fatal("scan failed")
	}
	return led, alice, det
}
