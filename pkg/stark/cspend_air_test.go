package stark

import (
	"math/rand"
	"testing"
)

func buildCSpendTree(depth, idx int, serial, amount, blind Felt, seed int64) *PoseidonIMT256 {
	r := rand.New(rand.NewSource(seed))
	imt := NewPoseidonIMT256(depth)
	for i := 0; i < (1 << depth); i++ {
		if i == idx {
			imt.Append(SpendLeaf256(serial, amount, blind))
		} else {
			imt.Append(randNode256(r))
		}
	}
	return imt
}

// TestCSpendInputHonest: a coin with a hidden, in-range amount spends + verifies,
// revealing only serial + root (NOT the amount).
func TestCSpendInputHonest(t *testing.T) {
	for _, depth := range []int{2, 3} {
		serial, amount, blind := Felt(0xAB), Felt(123456789), Felt(0x55)
		idx := 1
		imt := buildCSpendTree(depth, idx, serial, amount, blind, int64(depth))
		root := imt.Root()
		pf, err := ProveCSpendInput(serial, amount, blind, imt.PathFor(uint64(idx)), depth, root, nil, 32, airQueries)
		if err != nil {
			t.Fatalf("depth=%d prove: %v", depth, err)
		}
		if !VerifyCSpendInput(serial, root, nil, depth, 32, pf, airQueries) {
			t.Fatalf("depth=%d honest confidential spend rejected", depth)
		}
		// the amount must NOT leak into revealed scalars
		for _, x := range flattenExt(flattenExt(nil, pf.Fz...), pf.Fgz...) {
			if x == amount {
				t.Fatal("hidden amount leaked into an OOD evaluation")
			}
		}
	}
}

// TestCSpendInputOutOfRange: a coin whose amount is ≥ 2^vbits cannot be spent
// confidentially at that vbits (the range proof fails).
func TestCSpendInputOutOfRange(t *testing.T) {
	serial, blind := Felt(0xAB), Felt(0x55)
	amount := Felt(1 << 20) // needs 21 bits
	imt := buildCSpendTree(3, 1, serial, amount, blind, 9)
	if _, err := ProveCSpendInput(serial, amount, blind, imt.PathFor(1), 3, imt.Root(), nil, 16, airQueries); err != errAIRBadTrace {
		t.Fatalf("expected errAIRBadTrace for out-of-range amount, got %v", err)
	}
}

// TestCSpendInputNonMember: a non-member coin can't be spent.
func TestCSpendInputNonMember(t *testing.T) {
	serial, amount, blind := Felt(0xAB), Felt(1000), Felt(0x55)
	imt := buildCSpendTree(3, 2, serial, amount, blind, 3)
	if _, err := ProveCSpendInput(Felt(0xCD), Felt(1000), Felt(0x66), imt.PathFor(2), 3, imt.Root(), nil, 16, airQueries); err != errAIRBadTrace {
		t.Fatalf("expected errAIRBadTrace for non-member, got %v", err)
	}
}

// TestCSpendInputTampered: mutating the proof breaks verification.
func TestCSpendInputTampered(t *testing.T) {
	serial, amount, blind := Felt(7), Felt(8), Felt(9)
	imt := buildCSpendTree(2, 0, serial, amount, blind, 4)
	pf, _ := ProveCSpendInput(serial, amount, blind, imt.PathFor(0), 2, imt.Root(), nil, 16, airQueries)
	pf.CPz = pf.CPz.Add(One2())
	if VerifyCSpendInput(serial, imt.Root(), nil, 2, 16, pf, airQueries) {
		t.Fatal("tampered confidential spend accepted")
	}
}
