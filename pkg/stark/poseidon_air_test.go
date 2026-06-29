package stark

import "testing"

// TestPoseidonPreimageHonest: a real preimage proof verifies, and y matches the
// in-the-clear hash.
func TestPoseidonPreimageHonest(t *testing.T) {
	s := Felt(0xDEADBEEF)
	y, pf, err := ProvePoseidonPreimage(s, airQueries)
	if err != nil {
		t.Fatalf("prove: %v", err)
	}
	if y != PoseidonHash1(s) {
		t.Fatal("proof output y disagrees with PoseidonHash1")
	}
	if !VerifyPoseidonPreimage(y, pf, airQueries) {
		t.Fatal("honest preimage proof rejected")
	}
}

// TestPoseidonPreimageWrongOutput: verifying against the wrong y must fail.
func TestPoseidonPreimageWrongOutput(t *testing.T) {
	y, pf, _ := ProvePoseidonPreimage(Felt(12345), airQueries)
	if VerifyPoseidonPreimage(y.Add(1), pf, airQueries) {
		t.Fatal("wrong output accepted")
	}
}

// TestPoseidonPreimageForgedTraceRejected: a trace whose output is patched to a
// chosen y without a real preimage cannot be proven (round constraint breaks) — and
// even if forced through, verification fails.
func TestPoseidonPreimageForgedTraceRejected(t *testing.T) {
	s := Felt(777)
	trace := poseidonPreimageTrace(s)
	// Patch the claimed output to a chosen value without recomputing the rounds.
	forgedY := Felt(999999)
	trace[0][poseidonRounds] = forgedY
	c := poseidonPreimageCircuit{y: forgedY}
	if _, err := ProveAIR(c, trace, airQueries); err != errAIRBadTrace {
		t.Fatalf("expected errAIRBadTrace for forged output, got %v", err)
	}
}

// TestPoseidonPreimageTampered: mutating the proof breaks verification.
func TestPoseidonPreimageTampered(t *testing.T) {
	y, pf, _ := ProvePoseidonPreimage(Felt(42), airQueries)
	pf.Fz[0] = pf.Fz[0].Add(One2())
	if VerifyPoseidonPreimage(y, pf, airQueries) {
		t.Fatal("tampered proof accepted")
	}
}

// TestPoseidonPreimageZeroKnowledge: the proof does not contain the preimage s in
// any opened/OOD scalar (sanity — s is the row-0 col-0 trace value, never a
// boundary or a directly-revealed field).
func TestPoseidonPreimageHidesPreimage(t *testing.T) {
	s := Felt(0x1234567890ABCDEF % P)
	y, pf, _ := ProvePoseidonPreimage(s, airQueries)
	for _, v := range flattenExt(flattenExt(nil, pf.Fz...), pf.Fgz...) {
		if v == s {
			t.Fatal("preimage leaked into an out-of-domain evaluation")
		}
	}
	_ = y
}
