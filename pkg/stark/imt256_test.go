package stark

import (
	"math/rand"
	"testing"
)

func randNode256(r *rand.Rand) Node256 {
	return Node256{randField(r), randField(r), randField(r), randField(r)}
}

func TestIMT256PathVerifies(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	depth := 6
	imt := NewPoseidonIMT256(depth)
	n := 20
	for i := 0; i < n; i++ {
		imt.Append(randNode256(r))
	}
	root := imt.Root()
	for i := uint64(0); i < uint64(n); i++ {
		if !VerifyPath256(root, imt.LeafAt(i), imt.PathFor(i)) {
			t.Fatalf("path for leaf %d does not verify", i)
		}
	}
}

func TestIMT256StateRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	depth := 8
	imt := NewPoseidonIMT256(depth)
	for i := 0; i < 30; i++ {
		imt.Append(randNode256(r))
	}
	loaded, ok := LoadIMT256State(imt.MarshalState())
	if !ok {
		t.Fatal("load failed")
	}
	if loaded.Root() != imt.Root() || loaded.Count() != imt.Count() {
		t.Fatal("restored state mismatch")
	}
	leaf := randNode256(r)
	imt.Append(leaf)
	loaded.Append(leaf)
	if loaded.Root() != imt.Root() {
		t.Fatal("restored frontier diverges on append")
	}
}

func TestNodeBytesRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	n := randNode256(r)
	if NodeFromBytes(NodeBytes(n)) != n {
		t.Fatal("node byte round-trip mismatch")
	}
}
