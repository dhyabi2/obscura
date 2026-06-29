package stark

import "testing"

func TestRangeHonest(t *testing.T) {
	for _, v := range []uint64{0, 1, 2, 255, 1000000, (1 << 32) - 1} {
		pf, err := ProveRange(Felt(v), 32, airQueries)
		if err != nil {
			t.Fatalf("v=%d prove: %v", v, err)
		}
		if !VerifyRange(Felt(v), 32, pf, airQueries) {
			t.Fatalf("v=%d honest range rejected", v)
		}
	}
}

// TestRangeOutOfRange: a value ≥ 2^bits cannot be proven (the peeled remainder ≠ 0,
// so the val_n=0 boundary division has a remainder).
func TestRangeOutOfRange(t *testing.T) {
	// value = 2^32 needs 33 bits; proving it in 32 bits must fail.
	if _, err := ProveRange(Felt(1<<32), 32, airQueries); err != errAIRBadTrace {
		t.Fatalf("expected errAIRBadTrace for out-of-range, got %v", err)
	}
	// a near-field-modulus value (the wraparound/negative case) must fail too.
	if _, err := ProveRange(Felt(PModulus-1), 32, airQueries); err != errAIRBadTrace {
		t.Fatalf("expected errAIRBadTrace for huge value, got %v", err)
	}
}

// TestRangeForgedBits: tampering the proof breaks verification.
func TestRangeForgedValue(t *testing.T) {
	pf, _ := ProveRange(Felt(12345), 32, airQueries)
	if VerifyRange(Felt(54321), 32, pf, airQueries) {
		t.Fatal("range proof accepted for a different value")
	}
}
