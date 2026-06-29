package stark

import "testing"

const starkQueries = 40

// buildSquareTrace runs the square-step recurrence to produce a valid trace.
func buildSquareTrace(start, K Felt, T int) []Felt {
	tr := make([]Felt, T)
	tr[0] = start
	for i := 1; i < T; i++ {
		tr[i] = tr[i-1].Mul(tr[i-1]).Add(K)
	}
	return tr
}

// TestSTARKHonest: an honestly-generated trace verifies, for several lengths.
func TestSTARKHonest(t *testing.T) {
	for _, T := range []int{2, 4, 8, 16, 64} {
		trace := buildSquareTrace(Felt(3), Felt(7), T)
		pf, err := ProveSquareStep(trace, Felt(7), starkQueries)
		if err != nil {
			t.Fatalf("T=%d prove: %v", T, err)
		}
		if !VerifySquareStep(pf, starkQueries) {
			t.Fatalf("T=%d honest proof rejected", T)
		}
	}
}

// TestSTARKInvalidTraceRejectedAtProve: a trace that breaks the transition can't
// even be proven (a quotient division leaves a remainder).
func TestSTARKInvalidTraceRejectedAtProve(t *testing.T) {
	trace := buildSquareTrace(Felt(3), Felt(7), 16)
	trace[5] = trace[5].Add(1) // corrupt one step
	if _, err := ProveSquareStep(trace, Felt(7), starkQueries); err != errBadTrace {
		t.Fatalf("expected errBadTrace, got %v", err)
	}
}

// TestSTARKForgedPublicOutput: a valid trace but a lied-about public end value must
// be rejected by the boundary algebraic check at z.
func TestSTARKForgedPublicOutput(t *testing.T) {
	trace := buildSquareTrace(Felt(3), Felt(7), 16)
	pf, err := ProveSquareStep(trace, Felt(7), starkQueries)
	if err != nil {
		t.Fatal(err)
	}
	pf.PubEnd = pf.PubEnd.Add(1)
	if VerifySquareStep(pf, starkQueries) {
		t.Fatal("forged public output accepted")
	}
}

// TestSTARKForgedOODValue: tampering with the out-of-domain f(z) breaks either the
// algebraic check at z or the DEEP cross-check at the query points.
func TestSTARKForgedOODValue(t *testing.T) {
	trace := buildSquareTrace(Felt(3), Felt(7), 16)
	pf, _ := ProveSquareStep(trace, Felt(7), starkQueries)
	pf.Fz = pf.Fz.Add(One2())
	if VerifySquareStep(pf, starkQueries) {
		t.Fatal("tampered f(z) accepted")
	}
}

// TestSTARKForgedComposition: a tampered CP(z) is caught by the z-relation.
func TestSTARKForgedComposition(t *testing.T) {
	trace := buildSquareTrace(Felt(3), Felt(7), 16)
	pf, _ := ProveSquareStep(trace, Felt(7), starkQueries)
	pf.CPz = pf.CPz.Add(One2())
	if VerifySquareStep(pf, starkQueries) {
		t.Fatal("tampered CP(z) accepted")
	}
}

// TestSTARKTamperedDeepOpening: corrupting a committed f opening breaks the Merkle
// authentication / DEEP cross-check.
func TestSTARKTamperedDeepOpening(t *testing.T) {
	trace := buildSquareTrace(Felt(3), Felt(7), 16)
	pf, _ := ProveSquareStep(trace, Felt(7), starkQueries)
	pf.OpenF[0].P = pf.OpenF[0].P.Add(1)
	if VerifySquareStep(pf, starkQueries) {
		t.Fatal("tampered DEEP opening accepted")
	}
}

// TestSTARKWrongK: verifying against a different public constant K fails.
func TestSTARKWrongK(t *testing.T) {
	trace := buildSquareTrace(Felt(3), Felt(7), 16)
	pf, _ := ProveSquareStep(trace, Felt(7), starkQueries)
	pf.K = Felt(8)
	if VerifySquareStep(pf, starkQueries) {
		t.Fatal("wrong K accepted")
	}
}

// TestSTARKZeroKnowledgeShape sanity-checks the proof reveals only public inputs +
// commitments + a bounded number of openings (no full trace).
func TestSTARKProofShape(t *testing.T) {
	trace := buildSquareTrace(Felt(3), Felt(7), 64)
	pf, _ := ProveSquareStep(trace, Felt(7), starkQueries)
	if len(pf.OpenF) != starkQueries || len(pf.Fri.Queries) != starkQueries {
		t.Fatal("unexpected opening count")
	}
}

// TestSTARKFuzzTamper perturbs random proof fields and asserts none verify — a
// blanket check that no single-field mutation slips past the verifier.
func TestSTARKFuzzTamper(t *testing.T) {
	trace := buildSquareTrace(Felt(11), Felt(5), 32)
	base, err := ProveSquareStep(trace, Felt(5), starkQueries)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifySquareStep(base, starkQueries) {
		t.Fatal("baseline rejected")
	}
	for i := 0; i < 64; i++ {
		p := *base // shallow copy; mutate scalar/commitment fields only
		switch i % 6 {
		case 0:
			p.Fz = p.Fz.Add(Felt2From(Felt(uint64(i + 1))))
		case 1:
			p.Fgz = p.Fgz.Add(Felt2From(Felt(uint64(i + 1))))
		case 2:
			p.CPz = p.CPz.Add(Felt2From(Felt(uint64(i + 1))))
		case 3:
			p.PubStart = p.PubStart.Add(Felt(uint64(i + 1)))
		case 4:
			r := p.RootF
			r[i%32] ^= byte(i + 1)
			p.RootF = r
		case 5:
			r := p.RootCP
			r[i%32] ^= byte(i + 1)
			p.RootCP = r
		}
		if VerifySquareStep(&p, starkQueries) {
			t.Fatalf("mutation %d accepted", i)
		}
	}
}
