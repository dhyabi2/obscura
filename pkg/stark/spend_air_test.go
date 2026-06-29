package stark

import (
	"math/rand"
	"testing"
)

// buildSpendTree places a coin leaf L=SpendLeaf(serial,amount,blind) at index idx
// in a depth-D tree of otherwise random leaves.
func buildSpendTree(depth, idx int, serial, amount, blind Felt, seed int64) *PoseidonMerkle {
	r := rand.New(rand.NewSource(seed))
	leaves := make([]Felt, 1<<depth)
	for i := range leaves {
		leaves[i] = randField(r)
	}
	leaves[idx] = SpendLeaf(serial, amount, blind)
	return BuildPoseidonMerkle(leaves, depth)
}

// TestSpendHonest: a real coin spends and verifies, revealing only S, amount, root.
func TestSpendHonest(t *testing.T) {
	for _, depth := range []int{2, 3, 4} {
		serial, amount, blind := Felt(0xABCDEF+uint64(depth)), Felt(1000+uint64(depth)), Felt(0x55+uint64(depth))
		idx := 1
		m := buildSpendTree(depth, idx, serial, amount, blind, int64(depth))
		pf, err := ProveSpend(serial, amount, blind, m.PathFor(idx), depth, m.Root(), airQueries)
		if err != nil {
			t.Fatalf("depth=%d prove: %v", depth, err)
		}
		if !VerifySpend(serial, amount, m.Root(), depth, pf, airQueries) {
			t.Fatalf("depth=%d honest spend rejected", depth)
		}
	}
}

// TestSpendWrongNullifier: a different serial than bound into the leaf fails.
func TestSpendWrongNullifier(t *testing.T) {
	serial, amount, blind := Felt(42), Felt(7), Felt(3)
	idx := 2
	m := buildSpendTree(4, idx, serial, amount, blind, 1)
	pf, _ := ProveSpend(serial, amount, blind, m.PathFor(idx), 4, m.Root(), airQueries)
	if VerifySpend(serial.Add(1), amount, m.Root(), 4, pf, airQueries) {
		t.Fatal("spend accepted with a swapped nullifier")
	}
}

// TestSpendForgedAmount: claiming a different amount than bound into the leaf fails
// (this is the anti-inflation check).
func TestSpendForgedAmount(t *testing.T) {
	serial, amount, blind := Felt(42), Felt(7), Felt(3)
	idx := 2
	m := buildSpendTree(4, idx, serial, amount, blind, 1)
	pf, _ := ProveSpend(serial, amount, blind, m.PathFor(idx), 4, m.Root(), airQueries)
	if VerifySpend(serial, amount.Add(1000000), m.Root(), 4, pf, airQueries) {
		t.Fatal("spend accepted with an inflated amount")
	}
}

// TestSpendForgeNullifierForVictimCoin: an attacker who knows a victim's path (but
// not the secret blind) cannot spend it under a serial of their choosing.
func TestSpendForgeNullifierForVictimCoin(t *testing.T) {
	vS, vA, vB := Felt(0xDEAD), Felt(0xBEEF), Felt(0xF00D)
	idx := 5
	m := buildSpendTree(4, idx, vS, vA, vB, 2)
	if _, err := ProveSpend(Felt(0x1234), vA, vB, m.PathFor(idx), 4, m.Root(), airQueries); err != errAIRBadTrace {
		t.Fatalf("expected errAIRBadTrace forging nullifier, got %v", err)
	}
}

// TestSpendNonMember: a coin not in the tree cannot be spent.
func TestSpendNonMember(t *testing.T) {
	serial, amount, blind := Felt(99), Felt(11), Felt(5)
	idx := 3
	m := buildSpendTree(4, idx, serial, amount, blind, 3)
	if _, err := ProveSpend(Felt(100), Felt(12), Felt(6), m.PathFor(idx), 4, m.Root(), airQueries); err != errAIRBadTrace {
		t.Fatalf("expected errAIRBadTrace for non-member, got %v", err)
	}
}

// TestSpendTampered: mutating the proof breaks verification.
func TestSpendTampered(t *testing.T) {
	serial, amount, blind := Felt(7), Felt(8), Felt(9)
	idx := 0
	m := buildSpendTree(3, idx, serial, amount, blind, 4)
	pf, _ := ProveSpend(serial, amount, blind, m.PathFor(idx), 3, m.Root(), airQueries)
	pf.CPz = pf.CPz.Add(One2())
	if VerifySpend(serial, amount, m.Root(), 3, pf, airQueries) {
		t.Fatal("tampered spend proof accepted")
	}
}

// TestSpendHidesBlind: the blind never appears in any revealed scalar.
func TestSpendHidesBlind(t *testing.T) {
	serial, amount, blind := Felt(0x5555), Felt(0x7777), Felt(0x9999)
	idx := 6
	m := buildSpendTree(4, idx, serial, amount, blind, 5)
	pf, _ := ProveSpend(serial, amount, blind, m.PathFor(idx), 4, m.Root(), airQueries)
	for _, x := range flattenExt(flattenExt(nil, pf.Fz...), pf.Fgz...) {
		if x == blind {
			t.Fatal("blind leaked into an out-of-domain evaluation")
		}
	}
}
