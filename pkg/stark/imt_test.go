package stark

import (
	"math/rand"
	"testing"
)

// TestIMTRootMatchesDense: the incremental root equals a from-scratch dense build.
func TestIMTRootMatchesDense(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	depth := 6
	imt := NewPoseidonIMT(depth)
	var leaves []Felt
	for n := 0; n < 25; n++ {
		leaf := randField(r)
		imt.Append(leaf)
		leaves = append(leaves, leaf)
		// dense reference: pad to 2^depth with zeros
		dense := make([]Felt, 1<<depth)
		copy(dense, leaves)
		ref := BuildPoseidonMerkle(dense, depth)
		if imt.Root() != ref.Root() {
			t.Fatalf("n=%d incremental root != dense root", n)
		}
	}
}

// TestIMTPathVerifies: every appended leaf's path verifies against the live root.
func TestIMTPathVerifies(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	depth := 6
	imt := NewPoseidonIMT(depth)
	n := 20
	for i := 0; i < n; i++ {
		imt.Append(randField(r))
	}
	root := imt.Root()
	for i := uint64(0); i < uint64(n); i++ {
		if !VerifyPoseidonPath(root, imt.leaves[i], imt.PathFor(i)) {
			t.Fatalf("path for leaf %d does not verify", i)
		}
	}
}

// TestIMTSpendEndToEnd: append a real coin leaf, then prove+verify a spend against
// the IMT root using the path the IMT produced.
func TestIMTSpendEndToEnd(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	depth := 5
	imt := NewPoseidonIMT(depth)
	serial, amount, blind := Felt(0xC011), Felt(500), Felt(0x9A9A)
	// scatter other coins around ours
	for i := 0; i < 3; i++ {
		imt.Append(randField(r))
	}
	idx := imt.Append(SpendLeaf(serial, amount, blind))
	for i := 0; i < 4; i++ {
		imt.Append(randField(r))
	}
	root := imt.Root()
	path := imt.PathFor(idx)
	pf, err := ProveSpend(serial, amount, blind, path, depth, root, airQueries)
	if err != nil {
		t.Fatalf("prove: %v", err)
	}
	if !VerifySpend(serial, amount, root, depth, pf, airQueries) {
		t.Fatal("spend against IMT root rejected")
	}
}

// TestIMTStateRoundTrip: node-side state survives marshal→load (root + frontier).
func TestIMTStateRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	depth := 8
	imt := NewPoseidonIMT(depth)
	for i := 0; i < 30; i++ {
		imt.Append(randField(r))
	}
	loaded, ok := LoadIMTState(imt.MarshalState())
	if !ok {
		t.Fatal("load failed")
	}
	if loaded.Root() != imt.Root() || loaded.Count() != imt.Count() {
		t.Fatal("restored state mismatch")
	}
	// continued appends from restored frontier must track the original.
	leaf := randField(r)
	imt.Append(leaf)
	loaded.Append(leaf)
	if loaded.Root() != imt.Root() {
		t.Fatal("restored frontier diverges on append")
	}
}
