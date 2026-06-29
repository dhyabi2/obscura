package stark

import (
	"math/rand"
	"testing"
)

func buildSpend256Tree(depth, idx int, serial, amount, blind Felt, seed int64) *PoseidonIMT256 {
	r := rand.New(rand.NewSource(seed))
	imt := NewPoseidonIMT256(depth)
	n := 1 << depth
	for i := 0; i < n; i++ {
		if i == idx {
			imt.Append(SpendLeaf256(serial, amount, blind))
		} else {
			imt.Append(randNode256(r))
		}
	}
	return imt
}

func TestSpend256Honest(t *testing.T) {
	for _, depth := range []int{2, 3, 4} {
		serial, amount, blind := Felt(0xAB+uint64(depth)), Felt(1000+uint64(depth)), Felt(0x55+uint64(depth))
		idx := 1
		imt := buildSpend256Tree(depth, idx, serial, amount, blind, int64(depth))
		root := imt.Root()
		pf, err := ProveSpend256(serial, amount, blind, imt.PathFor(uint64(idx)), depth, root, nil, airQueries)
		if err != nil {
			t.Fatalf("depth=%d prove: %v", depth, err)
		}
		if !VerifySpend256(serial, amount, root, nil, depth, pf, airQueries) {
			t.Fatalf("depth=%d honest wide spend rejected", depth)
		}
	}
}

func TestSpend256WrongNullifier(t *testing.T) {
	serial, amount, blind := Felt(42), Felt(7), Felt(3)
	imt := buildSpend256Tree(4, 2, serial, amount, blind, 1)
	pf, _ := ProveSpend256(serial, amount, blind, imt.PathFor(2), 4, imt.Root(), nil, airQueries)
	if VerifySpend256(serial.Add(1), amount, imt.Root(), nil, 4, pf, airQueries) {
		t.Fatal("wide spend accepted with swapped nullifier")
	}
}

func TestSpend256ForgedAmount(t *testing.T) {
	serial, amount, blind := Felt(42), Felt(7), Felt(3)
	imt := buildSpend256Tree(4, 2, serial, amount, blind, 1)
	pf, _ := ProveSpend256(serial, amount, blind, imt.PathFor(2), 4, imt.Root(), nil, airQueries)
	if VerifySpend256(serial, amount.Add(1000000), imt.Root(), nil, 4, pf, airQueries) {
		t.Fatal("wide spend accepted with inflated amount")
	}
}

func TestSpend256NonMember(t *testing.T) {
	serial, amount, blind := Felt(99), Felt(11), Felt(5)
	imt := buildSpend256Tree(4, 3, serial, amount, blind, 3)
	if _, err := ProveSpend256(Felt(100), Felt(12), Felt(6), imt.PathFor(3), 4, imt.Root(), nil, airQueries); err != errAIRBadTrace {
		t.Fatalf("expected errAIRBadTrace non-member, got %v", err)
	}
}

func TestSpend256Tampered(t *testing.T) {
	serial, amount, blind := Felt(7), Felt(8), Felt(9)
	imt := buildSpend256Tree(3, 0, serial, amount, blind, 4)
	pf, _ := ProveSpend256(serial, amount, blind, imt.PathFor(0), 3, imt.Root(), nil, airQueries)
	pf.CPz = pf.CPz.Add(One2())
	if VerifySpend256(serial, amount, imt.Root(), nil, 3, pf, airQueries) {
		t.Fatal("tampered wide spend accepted")
	}
}
