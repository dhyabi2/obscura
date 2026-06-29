package stark

import (
	"math/rand"
	"testing"
)

// TestPoseidonMerkleHonestPath: every leaf's path verifies against the root.
func TestPoseidonMerkleHonestPath(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	depth := 5
	leaves := make([]Felt, 1<<depth)
	for i := range leaves {
		leaves[i] = randField(r)
	}
	m := BuildPoseidonMerkle(leaves, depth)
	root := m.Root()
	for i := range leaves {
		if !VerifyPoseidonPath(root, leaves[i], m.PathFor(i)) {
			t.Fatalf("honest path for leaf %d rejected", i)
		}
	}
}

// TestPoseidonMerkleWrongLeaf: a non-member leaf fails.
func TestPoseidonMerkleWrongLeaf(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	depth := 4
	leaves := make([]Felt, 1<<depth)
	for i := range leaves {
		leaves[i] = randField(r)
	}
	m := BuildPoseidonMerkle(leaves, depth)
	if VerifyPoseidonPath(m.Root(), leaves[3].Add(1), m.PathFor(3)) {
		t.Fatal("tampered leaf accepted")
	}
}

// TestPoseidonMerkleWrongSibling: a corrupted path fails.
func TestPoseidonMerkleWrongSibling(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	depth := 4
	leaves := make([]Felt, 1<<depth)
	for i := range leaves {
		leaves[i] = randField(r)
	}
	m := BuildPoseidonMerkle(leaves, depth)
	p := m.PathFor(5)
	p.Siblings[1] = p.Siblings[1].Add(1)
	if VerifyPoseidonPath(m.Root(), leaves[5], p) {
		t.Fatal("corrupted sibling accepted")
	}
}

// TestNullifierDeterministicAndDistinct: N=H(s) is deterministic and collision-free
// on a sample.
func TestNullifierDeterministicAndDistinct(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	if Nullifier(Felt(42)) != Nullifier(Felt(42)) {
		t.Fatal("nullifier not deterministic")
	}
	seen := make(map[uint64]struct{})
	for i := 0; i < 20000; i++ {
		n := uint64(Nullifier(randField(r)))
		if _, dup := seen[n]; dup {
			t.Fatal("nullifier collision in sample")
		}
		seen[n] = struct{}{}
	}
}
