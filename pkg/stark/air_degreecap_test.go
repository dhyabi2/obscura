package stark

import "testing"

// TestAIRDegreeCapPreventsPanic locks in the consensus-DoS fix (audit 2026-06-28 HIGH):
// the prover-chosen proof Degree is capped so the LDE domain friBlowup*d always fits the
// Goldilocks NTT, and RootOfUnity therefore never panics on a crafted proof. The cap must
// be both SAFE (no panic at the cap) and TIGHT (one binary step above would overflow the
// 2-adicity, which is exactly the attack the cap blocks).
func TestAIRDegreeCapPreventsPanic(t *testing.T) {
	// 1) friBlowup * maxAIRDegree must fit the NTT domain (<= 2^twoAdicity).
	if got := log2(friBlowup * maxAIRDegree); got > twoAdicity {
		t.Fatalf("cap too large: log2(friBlowup*maxAIRDegree)=%d > twoAdicity=%d", got, twoAdicity)
	}
	// 2) RootOfUnity must not panic at the cap (this is what a malicious proof tried to break).
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("RootOfUnity panicked at the degree cap: %v", r)
			}
		}()
		_ = RootOfUnity(log2(friBlowup * maxAIRDegree))
	}()
	// 3) The cap is tight: a degree one binary step past it WOULD exceed the 2-adicity, i.e.
	//    the cap is exactly the boundary an attacker would have crossed to force a panic.
	if log2(friBlowup*(maxAIRDegree*2)) <= twoAdicity {
		t.Fatal("cap not tight: 2*maxAIRDegree still fits, attacker has headroom to panic the node")
	}
}
