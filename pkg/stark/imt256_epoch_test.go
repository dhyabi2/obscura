package stark

import (
	"math/rand"
	"testing"
)

// TestEpochIMTUnlimitedCoinsFixedDepth: append MORE than one epoch's capacity and
// confirm coins roll into new fixed-depth epochs, every coin's path verifies against
// its epoch root, and a real spend works against an OLD (finalized) epoch root —
// demonstrating unlimited coins at constant per-proof depth.
func TestEpochIMTUnlimitedCoinsFixedDepth(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	depth := 3 // cap = 8 coins/epoch (tiny, to force rollover fast)
	e := NewEpochIMT(depth)

	// our spendable coin, placed in epoch 0
	serial, amount, blind := Felt(0xC0), Felt(500), Felt(0x9A)
	coinLeaf := SpendLeaf256(serial, amount, blind)
	e.Append(randNode256(r))
	myEpoch, myIdx := e.Append(coinLeaf) // epoch 0
	// keep appending past several epoch boundaries
	for i := 0; i < 25; i++ {
		e.Append(randNode256(r))
	}
	if e.Epochs() < 3 {
		t.Fatalf("expected ≥3 epochs from 27 coins at cap 8, got %d", e.Epochs())
	}
	if e.TotalCount() != 27 {
		t.Fatalf("total %d, want 27", e.TotalCount())
	}
	if myEpoch != 0 {
		t.Fatalf("coin epoch %d, want 0", myEpoch)
	}

	// every coin's path verifies against its epoch root (constant depth)
	for ep := 0; ep < e.Epochs(); ep++ {
		root, _ := e.RootAt(ep)
		t2 := e.trees[ep]
		for i := uint64(0); i < t2.Count(); i++ {
			path := t2.PathFor(i)
			if !VerifyPath256(root, t2.LeafAt(i), path) {
				t.Fatalf("epoch %d leaf %d path fails", ep, i)
			}
			if len(path.Siblings) != depth {
				t.Fatalf("path depth %d != fixed %d", len(path.Siblings), depth)
			}
		}
	}

	// SPEND the coin in the OLD finalized epoch 0 against its (still-valid) root.
	path, root, ok := e.PathFor(myEpoch, myIdx)
	if !ok {
		t.Fatal("PathFor failed")
	}
	pf, err := ProveSpend256(serial, amount, blind, path, depth, root, []byte("bind"), airQueries)
	if err != nil {
		t.Fatalf("spend against old epoch: %v", err)
	}
	if !VerifySpend256(serial, amount, root, []byte("bind"), depth, pf, airQueries) {
		t.Fatal("spend against finalized epoch root rejected")
	}
}

func TestEpochIMTStateRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	e := NewEpochIMT(3)
	for i := 0; i < 20; i++ {
		e.Append(randNode256(r))
	}
	loaded, ok := LoadEpochIMTState(e.MarshalState())
	if !ok {
		t.Fatal("load failed")
	}
	if loaded.Epochs() != e.Epochs() || loaded.TotalCount() != e.TotalCount() {
		t.Fatal("epoch state mismatch")
	}
	for ep := 0; ep < e.Epochs(); ep++ {
		a, _ := e.RootAt(ep)
		b, _ := loaded.RootAt(ep)
		if a != b {
			t.Fatalf("epoch %d root mismatch after restore", ep)
		}
	}
	// continued append tracks
	leaf := randNode256(r)
	e.Append(leaf)
	loaded.Append(leaf)
	if e.CurrentRoot() != loaded.CurrentRoot() {
		t.Fatal("restored frontier diverges")
	}
}
