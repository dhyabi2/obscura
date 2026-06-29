package stark

import "testing"

// TestGrainConstants: the Grain generator yields the right count of valid,
// non-trivial, distinct field elements (a basic sanity check on the LFSR — NOT a
// substitute for cross-checking against the reference sage known-answer output).
func TestGrainConstants(t *testing.T) {
	rc := grainRoundConstants(poseidonT, poseidonRF, poseidonRP)
	if len(rc) != poseidonRounds {
		t.Fatalf("got %d rounds, want %d", len(rc), poseidonRounds)
	}
	seen := make(map[uint64]int)
	zero := 0
	for r := range rc {
		if len(rc[r]) != poseidonT {
			t.Fatalf("round %d width %d", r, len(rc[r]))
		}
		for _, f := range rc[r] {
			if uint64(f) >= P {
				t.Fatal("constant not reduced mod P")
			}
			if f == 0 {
				zero++
			}
			seen[uint64(f)]++
		}
	}
	if zero > 1 {
		t.Fatalf("%d zero constants — LFSR likely broken", zero)
	}
	if len(seen) < poseidonRounds*poseidonT-1 {
		t.Fatalf("constants not distinct (%d unique of %d)", len(seen), poseidonRounds*poseidonT)
	}
}

// TestGrainDeterministic: generation is reproducible.
func TestGrainDeterministic(t *testing.T) {
	a := grainRoundConstants(poseidonT, poseidonRF, poseidonRP)
	b := grainRoundConstants(poseidonT, poseidonRF, poseidonRP)
	for r := range a {
		for i := range a[r] {
			if a[r][i] != b[r][i] {
				t.Fatal("Grain not deterministic")
			}
		}
	}
}
