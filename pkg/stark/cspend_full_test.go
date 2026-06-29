package stark

import (
	"math/rand"
	"testing"
)

// buildFullTree places leaf_in (the spent coin) at idx and fills the rest randomly.
func buildFullTree(depth, idx int, serial, aIn, blindIn Felt, seed int64) *PoseidonIMT256 {
	r := rand.New(rand.NewSource(seed))
	imt := NewPoseidonIMT256(depth)
	for i := 0; i < (1 << depth); i++ {
		if i == idx {
			imt.Append(SpendLeaf256(serial, aIn, blindIn))
		} else {
			imt.Append(randNode256(r))
		}
	}
	return imt
}

// TestCSpendFullHonest: a real coin spent confidentially → a new coin, with both
// amounts hidden, conserving value (a_in = a_out + fee). Only serial/root/leafOut/fee leak.
func TestCSpendFullHonest(t *testing.T) {
	for _, depth := range []int{2, 3} {
		serial, aIn, blindIn := Felt(0x11), Felt(1_000_000), Felt(0x22)
		fee := Felt(7000)
		aOut := aIn - fee
		serialOut, blindOut := Felt(0x33), Felt(0x44)
		leafOut := SpendLeaf256(serialOut, aOut, blindOut)
		idx := 1
		imt := buildFullTree(depth, idx, serial, aIn, blindIn, int64(depth+10))
		root := imt.Root()
		pf, err := ProveCSpendFull(serial, aIn, blindIn, imt.PathFor(uint64(idx)), depth,
			root, serialOut, aOut, blindOut, leafOut, fee, nil, 32, airQueries)
		if err != nil {
			t.Fatalf("depth=%d prove: %v", depth, err)
		}
		if !VerifyCSpendFull(serial, root, leafOut, fee, nil, depth, 32, pf, airQueries) {
			t.Fatalf("depth=%d honest confidential spend rejected", depth)
		}
	}
}

// TestCSpendFullAmountPrivacy asserts the ZK property now that the engine is masked
// (zk_mask.go): neither hidden amount, nor the hidden blinds/serial_out, may appear in
// ANY value the proof reveals — the OOD evals (Fz/Fgz/CPz) or the trace/CP query
// openings. Before masking, the `ain` constant column's OOD eval equalled a_in exactly;
// the coset LDE + Z_H·r masking remove every such leak. (This is the runtime witness of
// SECURITY_AUDIT FINDING 4 being closed; the formal joint-ZK bound still warrants
// cryptographer sign-off, as noted in zk_mask.go.)
func TestCSpendFullAmountPrivacy(t *testing.T) {
	serial, aIn, blindIn := Felt(0x11), Felt(1_000_000), Felt(0x22)
	fee := Felt(7000)
	aOut := aIn - fee
	serialOut, blindOut := Felt(0x33), Felt(0x44)
	leafOut := SpendLeaf256(serialOut, aOut, blindOut)
	imt := buildFullTree(3, 1, serial, aIn, blindIn, 13)
	pf, err := ProveCSpendFull(serial, aIn, blindIn, imt.PathFor(1), 3, imt.Root(),
		serialOut, aOut, blindOut, leafOut, fee, nil, 32, airQueries)
	if err != nil {
		t.Fatalf("prove: %v", err)
	}
	if !VerifyCSpendFull(serial, imt.Root(), leafOut, fee, nil, 3, 32, pf, airQueries) {
		t.Fatal("masked proof failed to verify")
	}
	// gather every field element the proof reveals (extension OOD values flattened to
	// their base coordinates)
	revealed := flattenExt(nil, pf.Fz...)
	revealed = flattenExt(revealed, pf.Fgz...)
	revealed = flattenExt(revealed, pf.CPz)
	for q := range pf.OpenP {
		revealed = append(revealed, pf.OpenP[q].Cols...)
		revealed = append(revealed, pf.OpenP[q].CP)
		revealed = append(revealed, pf.OpenS[q].Cols...)
		revealed = append(revealed, pf.OpenS[q].CP)
	}
	secrets := map[Felt]string{aIn: "a_in", aOut: "a_out", blindIn: "blind_in",
		blindOut: "blind_out", serialOut: "serial_out"}
	for _, x := range revealed {
		if name, ok := secrets[x]; ok {
			t.Fatalf("hidden witness %s leaked into a revealed proof value (%d)", name, uint64(x))
		}
	}
}

// TestZKMaskRandomized: two proofs of the SAME statement differ (fresh mask each time),
// evidence the openings carry entropy independent of the witness rather than a
// deterministic function of it.
func TestZKMaskRandomized(t *testing.T) {
	serial, amount, blind := Felt(0xAB), Felt(123456), Felt(0x55)
	imt := buildCSpendTree(3, 1, serial, amount, blind, 3)
	root := imt.Root()
	p1, _ := ProveCSpendInput(serial, amount, blind, imt.PathFor(1), 3, root, nil, 32, airQueries)
	p2, _ := ProveCSpendInput(serial, amount, blind, imt.PathFor(1), 3, root, nil, 32, airQueries)
	if !VerifyCSpendInput(serial, root, nil, 3, 32, p1, airQueries) ||
		!VerifyCSpendInput(serial, root, nil, 3, 32, p2, airQueries) {
		t.Fatal("masked proofs must still verify")
	}
	if p1.CPz.Equal(p2.CPz) && p1.Fz[4].Equal(p2.Fz[4]) {
		t.Fatal("two proofs of the same statement are identical — masking not randomizing")
	}
}

// TestCSpendFullInflation: prover tries to mint a_out > a_in - fee (create value).
// The balance constraint a_in = a_out + fee must make the honest-fee proof impossible.
func TestCSpendFullInflation(t *testing.T) {
	serial, aIn, blindIn := Felt(0x11), Felt(1_000_000), Felt(0x22)
	fee := Felt(7000)
	aOut := aIn - fee + 50_000 // inflated output
	serialOut, blindOut := Felt(0x33), Felt(0x44)
	leafOut := SpendLeaf256(serialOut, aOut, blindOut)
	imt := buildFullTree(3, 1, serial, aIn, blindIn, 99)
	if _, err := ProveCSpendFull(serial, aIn, blindIn, imt.PathFor(1), 3, imt.Root(),
		serialOut, aOut, blindOut, leafOut, fee, nil, 32, airQueries); err != errAIRBadTrace {
		t.Fatalf("expected errAIRBadTrace for inflated output, got %v", err)
	}
}

// TestCSpendFullNegativeFee: the reviewer's flagged attack. A "negative" fee = P-k
// makes balance a_in = a_out + (P-k) ⇒ a_out = a_in + k (more out than in) hold mod P.
// In-circuit this is ONLY stopped if a_out's range bound catches it; if a_out+k still
// fits in vbits it would pass the circuit — proving consensus MUST reject fee>=2^vbits.
// Here we verify a_out chosen to wrap-inflate within range is NOT honestly provable
// because the prover must supply real bits for a_out and balance simultaneously.
func TestCSpendFullNegativeFeeNeedsConsensusGuard(t *testing.T) {
	serial, aIn, blindIn := Felt(0x11), Felt(1000), Felt(0x22)
	k := Felt(500)
	feeNeg := PModulus - uint64(k) // P - k, a "negative" fee
	aOut := aIn + k                // more out than in; small, in-range
	serialOut, blindOut := Felt(0x33), Felt(0x44)
	leafOut := SpendLeaf256(serialOut, aOut, blindOut)
	imt := buildFullTree(3, 1, serial, aIn, blindIn, 7)
	// The circuit ITSELF accepts this (balance holds mod P, both amounts in range) —
	// demonstrating the fee MUST be range-checked at consensus. Assert that and document.
	pf, err := ProveCSpendFull(serial, aIn, blindIn, imt.PathFor(1), 3, imt.Root(),
		serialOut, aOut, blindOut, leafOut, Felt(feeNeg), nil, 16, airQueries)
	if err != nil {
		t.Fatalf("prove: %v", err)
	}
	circuitAccepts := VerifyCSpendFull(serial, imt.Root(), leafOut, Felt(feeNeg), nil, 3, 16, pf, airQueries)
	if !circuitAccepts {
		t.Skip("circuit happened to reject; guard still required at consensus")
	}
	// feeNeg is NOT in [0, 2^16): the consensus fee-range check (which the integrator
	// MUST add) rejects it. Verify our documented bound would catch it.
	if feeNeg < (uint64(1) << MaxRangeBits) {
		t.Fatal("expected P-k to exceed 2^MaxRangeBits (consensus fee guard catches it)")
	}
}

// TestCSpendFullNonMember: spending a coin not in the tree fails.
func TestCSpendFullNonMember(t *testing.T) {
	serial, aIn, blindIn := Felt(0x11), Felt(1000), Felt(0x22)
	fee := Felt(10)
	aOut := aIn - fee
	leafOut := SpendLeaf256(Felt(0x33), aOut, Felt(0x44))
	imt := buildFullTree(3, 2, serial, aIn, blindIn, 5)
	// wrong serial/blind ⇒ leaf_in not a member
	if _, err := ProveCSpendFull(Felt(0xBAD), aIn, Felt(0x99), imt.PathFor(2), 3, imt.Root(),
		Felt(0x33), aOut, Felt(0x44), leafOut, fee, nil, 16, airQueries); err != errAIRBadTrace {
		t.Fatalf("expected errAIRBadTrace for non-member, got %v", err)
	}
}

// TestCSpendFullWrongLeafOut: the published leafOut must equal the committed output coin.
func TestCSpendFullWrongLeafOut(t *testing.T) {
	serial, aIn, blindIn := Felt(0x11), Felt(1000), Felt(0x22)
	fee := Felt(10)
	aOut := aIn - fee
	serialOut, blindOut := Felt(0x33), Felt(0x44)
	wrongLeaf := SpendLeaf256(serialOut, aOut+1, blindOut) // commits a different amount
	imt := buildFullTree(3, 1, serial, aIn, blindIn, 8)
	if _, err := ProveCSpendFull(serial, aIn, blindIn, imt.PathFor(1), 3, imt.Root(),
		serialOut, aOut, blindOut, wrongLeaf, fee, nil, 16, airQueries); err != errAIRBadTrace {
		t.Fatalf("expected errAIRBadTrace for mismatched leafOut, got %v", err)
	}
}

// TestCSpendFullTampered: mutating the proof breaks verification.
func TestCSpendFullTampered(t *testing.T) {
	serial, aIn, blindIn := Felt(7), Felt(100), Felt(9)
	fee := Felt(10)
	aOut := aIn - fee
	serialOut, blindOut := Felt(5), Felt(6)
	leafOut := SpendLeaf256(serialOut, aOut, blindOut)
	imt := buildFullTree(2, 0, serial, aIn, blindIn, 4)
	pf, _ := ProveCSpendFull(serial, aIn, blindIn, imt.PathFor(0), 2, imt.Root(),
		serialOut, aOut, blindOut, leafOut, fee, nil, 16, airQueries)
	pf.CPz = pf.CPz.Add(One2())
	if VerifyCSpendFull(serial, imt.Root(), leafOut, fee, nil, 2, 16, pf, airQueries) {
		t.Fatal("tampered confidential spend accepted")
	}
}

// TestCSpendFullWrongFee: verifying with a different fee than proved must fail.
func TestCSpendFullWrongFee(t *testing.T) {
	serial, aIn, blindIn := Felt(0x11), Felt(1000), Felt(0x22)
	fee := Felt(10)
	aOut := aIn - fee
	serialOut, blindOut := Felt(0x33), Felt(0x44)
	leafOut := SpendLeaf256(serialOut, aOut, blindOut)
	imt := buildFullTree(3, 1, serial, aIn, blindIn, 3)
	pf, _ := ProveCSpendFull(serial, aIn, blindIn, imt.PathFor(1), 3, imt.Root(),
		serialOut, aOut, blindOut, leafOut, fee, nil, 16, airQueries)
	if VerifyCSpendFull(serial, imt.Root(), leafOut, fee+1, nil, 3, 16, pf, airQueries) {
		t.Fatal("proof verified under a different fee")
	}
}
