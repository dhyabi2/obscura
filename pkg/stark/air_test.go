package stark

import "testing"

const airQueries = 40

// fibCircuit is a 2-column AIR: next_a = b, next_b = a + b, with boundaries
// a[0]=1, b[0]=1, b[T-1]=result. It validates the general multi-column engine on a
// genuine coupled recurrence (no periodic columns).
type fibCircuit struct {
	T      int
	result Felt
}

func fibConstraints[T any](e cenv[T], cur, next, _ []T) []T {
	c0 := e.Sub(next[0], cur[1])                // next_a − b
	c1 := e.Sub(e.Sub(next[1], cur[0]), cur[1]) // next_b − a − b
	return []T{c0, c1}
}

func (fibCircuit) Name() string           { return "fib" }
func (fibCircuit) Cols() int              { return 2 }
func (fibCircuit) Periodic() int          { return 0 }
func (c fibCircuit) TraceLen() int        { return c.T }
func (fibCircuit) PeriodicCol(int) []Felt { return nil }
func (c fibCircuit) Boundaries() []Boundary {
	return []Boundary{{Col: 0, Row: 0, Val: 1}, {Col: 1, Row: 0, Val: 1}, {Col: 1, Row: c.T - 1, Val: c.result}}
}
func (fibCircuit) ConstraintsFelt(cur, next, per []Felt) []Felt {
	return fibConstraints[Felt](feltEnv{}, cur, next, per)
}
func (fibCircuit) ConstraintsPoly(cur, next, per []Poly) []Poly {
	return fibConstraints[Poly](polyEnv{}, cur, next, per)
}
func (fibCircuit) ConstraintsExt(cur, next, per []Felt2) []Felt2 {
	return fibConstraints[Felt2](felt2Env{}, cur, next, per)
}

func buildFibTrace(T int) (cols [][]Felt, result Felt) {
	a := make([]Felt, T)
	b := make([]Felt, T)
	a[0], b[0] = Felt(1), Felt(1)
	for i := 1; i < T; i++ {
		a[i] = b[i-1]
		b[i] = a[i-1].Add(b[i-1])
	}
	return [][]Felt{a, b}, b[T-1]
}

func TestAIRFibHonest(t *testing.T) {
	for _, T := range []int{4, 8, 16, 64} {
		cols, result := buildFibTrace(T)
		c := fibCircuit{T: T, result: result}
		pf, err := ProveAIR(c, cols, airQueries)
		if err != nil {
			t.Fatalf("T=%d prove: %v", T, err)
		}
		if !VerifyAIR(c, pf, airQueries) {
			t.Fatalf("T=%d honest rejected", T)
		}
	}
}

func TestAIRFibBadTraceRejectedAtProve(t *testing.T) {
	cols, result := buildFibTrace(16)
	cols[1][7] = cols[1][7].Add(1) // break the recurrence
	c := fibCircuit{T: 16, result: result}
	if _, err := ProveAIR(c, cols, airQueries); err != errAIRBadTrace {
		t.Fatalf("expected errAIRBadTrace, got %v", err)
	}
}

func TestAIRFibForgedResult(t *testing.T) {
	cols, result := buildFibTrace(16)
	c := fibCircuit{T: 16, result: result}
	pf, _ := ProveAIR(c, cols, airQueries)
	// Verify against a circuit claiming a different public result.
	bad := fibCircuit{T: 16, result: result.Add(1)}
	if VerifyAIR(bad, pf, airQueries) {
		t.Fatal("forged result accepted")
	}
}

func TestAIRFibTamperedProof(t *testing.T) {
	cols, result := buildFibTrace(16)
	c := fibCircuit{T: 16, result: result}
	pf, _ := ProveAIR(c, cols, airQueries)
	pf.Fz[0] = pf.Fz[0].Add(One2())
	if VerifyAIR(c, pf, airQueries) {
		t.Fatal("tampered f(z) accepted")
	}
	pf2, _ := ProveAIR(c, cols, airQueries)
	pf2.OpenP[0].Cols[1] = pf2.OpenP[0].Cols[1].Add(1)
	if VerifyAIR(c, pf2, airQueries) {
		t.Fatal("tampered column opening accepted")
	}
	pf3, _ := ProveAIR(c, cols, airQueries)
	pf3.OpenP[0].CP = pf3.OpenP[0].CP.Add(1)
	if VerifyAIR(c, pf3, airQueries) {
		t.Fatal("tampered CP opening accepted")
	}
}
