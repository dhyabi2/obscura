package stark

import (
	"math/rand"
	"testing"
)

func buildCnfTree(depth, idx int, nkIn, aIn, rhoIn, blindIn Felt, seed int64) *PoseidonIMT256 {
	r := rand.New(rand.NewSource(seed))
	cmIn, _ := NfNote(nkIn, aIn, rhoIn, blindIn)
	imt := NewPoseidonIMT256(depth)
	for i := 0; i < (1 << depth); i++ {
		if i == idx {
			imt.Append(cmIn)
		} else {
			imt.Append(randNode256(r))
		}
	}
	return imt
}

// cnfFixture sets up an honest confidential+unlinkable spend: spend a coin owned by nkIn
// to a recipient address pkOut, conserving value (a_in = a_out + fee), amounts hidden.
func cnfFixture(t *testing.T, depth, idx int, seed int64) (imt *PoseidonIMT256, nkIn, aIn, rhoIn, blindIn Felt,
	pkOut Node256, aOut, rhoOut, blindOut, fee Felt, nf, cmOut Node256) {
	nkIn, aIn, rhoIn, blindIn = Felt(0x1111), Felt(5_000_000), Felt(0xAAA), Felt(0xBBB)
	fee = Felt(7000)
	aOut = aIn - fee
	rhoOut, blindOut = Felt(0xCCC), Felt(0xDDD)
	pkOut = NfAddress(Felt(0x2222)) // recipient's address (their nk=0x2222)
	imt = buildCnfTree(depth, idx, nkIn, aIn, rhoIn, blindIn, seed)
	nf = NfNullifier(nkIn, rhoIn)
	cmOut = NfNoteFromPk(pkOut, aOut, rhoOut, blindOut)
	return
}

func TestCnfSpendHonest(t *testing.T) {
	for _, depth := range []int{2, 3} {
		idx := 1
		imt, nkIn, aIn, rhoIn, blindIn, pkOut, aOut, rhoOut, blindOut, fee, nf, cmOut := cnfFixture(t, depth, idx, int64(depth+20))
		root := imt.Root()
		pf, err := ProveCnfSpend(nkIn, aIn, rhoIn, blindIn, imt.PathFor(uint64(idx)), depth, root,
			pkOut, aOut, rhoOut, blindOut, nf, cmOut, fee, nil, 32, airQueries)
		if err != nil {
			t.Fatalf("depth=%d prove: %v", depth, err)
		}
		if !VerifyCnfSpend(nf, root, cmOut, fee, nil, depth, 32, pf, airQueries) {
			t.Fatalf("depth=%d honest confidential+unlinkable spend rejected", depth)
		}
	}
}

func TestCnfSpendInflation(t *testing.T) {
	imt, nkIn, aIn, rhoIn, blindIn, pkOut, _, rhoOut, blindOut, fee, nf, _ := cnfFixture(t, 3, 1, 5)
	aOutBad := aIn - fee + 100_000 // inflated
	cmOutBad := NfNoteFromPk(pkOut, aOutBad, rhoOut, blindOut)
	if _, err := ProveCnfSpend(nkIn, aIn, rhoIn, blindIn, imt.PathFor(1), 3, imt.Root(),
		pkOut, aOutBad, rhoOut, blindOut, nf, cmOutBad, fee, nil, 32, airQueries); err != errAIRBadTrace {
		t.Fatalf("expected errAIRBadTrace for inflation, got %v", err)
	}
}

func TestCnfSpendAuthority(t *testing.T) {
	// thief knows the note's amounts/rho/blind + recipient, but NOT nk_in.
	imt, _, aIn, rhoIn, blindIn, pkOut, aOut, rhoOut, blindOut, fee, _, cmOut := cnfFixture(t, 3, 2, 6)
	thiefNk := Felt(0x9999)
	thiefNf := NfNullifier(thiefNk, rhoIn)
	if _, err := ProveCnfSpend(thiefNk, aIn, rhoIn, blindIn, imt.PathFor(2), 3, imt.Root(),
		pkOut, aOut, rhoOut, blindOut, thiefNf, cmOut, fee, nil, 32, airQueries); err != errAIRBadTrace {
		t.Fatalf("expected errAIRBadTrace for wrong-nk (no authority), got %v", err)
	}
}

func TestCnfSpendForgedNullifier(t *testing.T) {
	imt, nkIn, aIn, rhoIn, blindIn, pkOut, aOut, rhoOut, blindOut, fee, _, cmOut := cnfFixture(t, 3, 1, 7)
	forged := NfNullifier(Felt(0xBEEF), rhoIn) // nf for a different key
	if _, err := ProveCnfSpend(nkIn, aIn, rhoIn, blindIn, imt.PathFor(1), 3, imt.Root(),
		pkOut, aOut, rhoOut, blindOut, forged, cmOut, fee, nil, 32, airQueries); err != errAIRBadTrace {
		t.Fatalf("expected errAIRBadTrace for forged nf, got %v", err)
	}
}

func TestCnfSpendWrongCmOut(t *testing.T) {
	imt, nkIn, aIn, rhoIn, blindIn, pkOut, aOut, rhoOut, blindOut, fee, nf, _ := cnfFixture(t, 3, 1, 8)
	wrong := NfNoteFromPk(pkOut, aOut+1, rhoOut, blindOut) // commits a different amount
	if _, err := ProveCnfSpend(nkIn, aIn, rhoIn, blindIn, imt.PathFor(1), 3, imt.Root(),
		pkOut, aOut, rhoOut, blindOut, nf, wrong, fee, nil, 32, airQueries); err != errAIRBadTrace {
		t.Fatalf("expected errAIRBadTrace for mismatched cm_out, got %v", err)
	}
}

func TestCnfSpendTamperedAndWrongFee(t *testing.T) {
	imt, nkIn, aIn, rhoIn, blindIn, pkOut, aOut, rhoOut, blindOut, fee, nf, cmOut := cnfFixture(t, 2, 0, 9)
	root := imt.Root()
	pf, _ := ProveCnfSpend(nkIn, aIn, rhoIn, blindIn, imt.PathFor(0), 2, root,
		pkOut, aOut, rhoOut, blindOut, nf, cmOut, fee, nil, 32, airQueries)
	if VerifyCnfSpend(nf, root, cmOut, fee+1, nil, 2, 32, pf, airQueries) {
		t.Fatal("verified under a different fee")
	}
	pf.CPz = pf.CPz.Add(One2())
	if VerifyCnfSpend(nf, root, cmOut, fee, nil, 2, 32, pf, airQueries) {
		t.Fatal("tampered proof accepted")
	}
}

func TestCnfSpendPrivacy(t *testing.T) {
	imt, nkIn, aIn, rhoIn, blindIn, pkOut, aOut, rhoOut, blindOut, fee, nf, cmOut := cnfFixture(t, 3, 1, 11)
	pf, _ := ProveCnfSpend(nkIn, aIn, rhoIn, blindIn, imt.PathFor(1), 3, imt.Root(),
		pkOut, aOut, rhoOut, blindOut, nf, cmOut, fee, nil, 32, airQueries)
	revealed := flattenExt(nil, pf.Fz...)
	revealed = flattenExt(revealed, pf.Fgz...)
	revealed = flattenExt(revealed, pf.CPz)
	for q := range pf.OpenP {
		revealed = append(revealed, pf.OpenP[q].Cols...)
		revealed = append(revealed, pf.OpenS[q].Cols...)
	}
	secrets := map[Felt]string{nkIn: "nk_in", aIn: "a_in", aOut: "a_out", rhoIn: "rho_in",
		blindIn: "blind_in", rhoOut: "rho_out", blindOut: "blind_out"}
	for _, x := range revealed {
		if name, ok := secrets[x]; ok {
			t.Fatalf("hidden secret %s leaked into the proof", name)
		}
	}
}
