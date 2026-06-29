package stark

import (
	"math/rand"
	"testing"
)

func buildNfTree(depth, idx int, nk, amount, rho, blind Felt, seed int64) (*PoseidonIMT256, Node256) {
	r := rand.New(rand.NewSource(seed))
	cm, _ := NfNote(nk, amount, rho, blind)
	imt := NewPoseidonIMT256(depth)
	for i := 0; i < (1 << depth); i++ {
		if i == idx {
			imt.Append(cm)
		} else {
			imt.Append(randNode256(r))
		}
	}
	return imt, cm
}

// TestNfSpendHonest: a recipient-secret-nullifier coin spends + verifies, revealing nf
// and amount but NOT nk.
func TestNfSpendHonest(t *testing.T) {
	for _, depth := range []int{2, 3} {
		nk, amount, rho, blind := Felt(0x1234), Felt(777), Felt(0xBEEF), Felt(0x55)
		idx := 1
		imt, _ := buildNfTree(depth, idx, nk, amount, rho, blind, int64(depth+1))
		root := imt.Root()
		nf := NfNullifier(nk, rho)
		pf, err := ProveNfSpend(nk, amount, rho, blind, imt.PathFor(uint64(idx)), depth, root, nf, nil, airQueries)
		if err != nil {
			t.Fatalf("depth=%d prove: %v", depth, err)
		}
		if !VerifyNfSpend(amount, root, nf, nil, depth, pf, airQueries) {
			t.Fatalf("depth=%d honest nf spend rejected", depth)
		}
	}
}

// TestNfSpendAuthority: a thief who does NOT know nk cannot spend a coin they can see —
// any nk' ≠ nk yields pk' ≠ pk ⇒ a different note commitment ⇒ not a member.
func TestNfSpendAuthority(t *testing.T) {
	nk, amount, rho, blind := Felt(0x1234), Felt(777), Felt(0xBEEF), Felt(0x55)
	imt, _ := buildNfTree(3, 2, nk, amount, rho, blind, 7)
	root := imt.Root()
	// thief tries with a wrong secret nk' (knows amount/rho/blind, not nk).
	nfWrong := NfNullifier(Felt(0x9999), rho)
	if _, err := ProveNfSpend(Felt(0x9999), amount, rho, blind, imt.PathFor(2), 3, root, nfWrong, nil, airQueries); err != errAIRBadTrace {
		t.Fatalf("expected errAIRBadTrace for wrong-nk (no authority), got %v", err)
	}
}

// TestNfSpendNullifierBinding: the revealed nf MUST be H(nk,rho) for the SAME nk/rho that
// build the note — a forged/mismatched nf is rejected (constant-column link enforces it).
func TestNfSpendNullifierBinding(t *testing.T) {
	nk, amount, rho, blind := Felt(0x1234), Felt(777), Felt(0xBEEF), Felt(0x55)
	imt, _ := buildNfTree(3, 1, nk, amount, rho, blind, 4)
	root := imt.Root()
	// claim a nullifier for a DIFFERENT key (would let the spender hide the real nf).
	forged := NfNullifier(Felt(0xABCD), rho)
	if _, err := ProveNfSpend(nk, amount, rho, blind, imt.PathFor(1), 3, root, forged, nil, airQueries); err != errAIRBadTrace {
		t.Fatalf("expected errAIRBadTrace for forged nf, got %v", err)
	}
}

// TestNfSpendNonMember: a non-member note can't be spent.
func TestNfSpendNonMember(t *testing.T) {
	nk, amount, rho, blind := Felt(1), Felt(2), Felt(3), Felt(4)
	imt, _ := buildNfTree(3, 0, nk, amount, rho, blind, 5)
	// spend a coin (different blind) not in the tree.
	nf := NfNullifier(nk, rho)
	if _, err := ProveNfSpend(nk, amount, rho, Felt(99), imt.PathFor(0), 3, imt.Root(), nf, nil, airQueries); err != errAIRBadTrace {
		t.Fatalf("expected errAIRBadTrace for non-member, got %v", err)
	}
}

// TestNfSpendTampered: mutating the proof breaks verification.
func TestNfSpendTampered(t *testing.T) {
	nk, amount, rho, blind := Felt(5), Felt(6), Felt(7), Felt(8)
	imt, _ := buildNfTree(2, 0, nk, amount, rho, blind, 9)
	root := imt.Root()
	nf := NfNullifier(nk, rho)
	pf, _ := ProveNfSpend(nk, amount, rho, blind, imt.PathFor(0), 2, root, nf, nil, airQueries)
	pf.CPz = pf.CPz.Add(One2())
	if VerifyNfSpend(amount, root, nf, nil, 2, pf, airQueries) {
		t.Fatal("tampered nf spend accepted")
	}
}

// TestNfSpendSecretPrivacy: nk (and rho, blind) must not leak into any revealed proof
// value — sender↔spend unlinkability needs nk hidden (ZK-masked engine).
func TestNfSpendSecretPrivacy(t *testing.T) {
	nk, amount, rho, blind := Felt(0xDEAD), Felt(777), Felt(0xBEEF), Felt(0x55)
	imt, _ := buildNfTree(3, 1, nk, amount, rho, blind, 2)
	root := imt.Root()
	nf := NfNullifier(nk, rho)
	pf, _ := ProveNfSpend(nk, amount, rho, blind, imt.PathFor(1), 3, root, nf, nil, airQueries)
	revealed := flattenExt(nil, pf.Fz...)
	revealed = flattenExt(revealed, pf.Fgz...)
	revealed = flattenExt(revealed, pf.CPz)
	for q := range pf.OpenP {
		revealed = append(revealed, pf.OpenP[q].Cols...)
		revealed = append(revealed, pf.OpenS[q].Cols...)
	}
	for _, x := range revealed {
		if x == nk || x == rho || x == blind {
			t.Fatal("a hidden secret (nk/rho/blind) leaked into the proof")
		}
	}
}
