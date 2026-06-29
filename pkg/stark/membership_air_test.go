package stark

import (
	"math/rand"
	"testing"
)

func buildTree(t *testing.T, depth int, seed int64) (*PoseidonMerkle, []Felt) {
	t.Helper()
	r := rand.New(rand.NewSource(seed))
	leaves := make([]Felt, 1<<depth)
	for i := range leaves {
		leaves[i] = randField(r)
	}
	return BuildPoseidonMerkle(leaves, depth), leaves
}

// TestMembershipHonest: a real member + path verifies, for several leaves/depths.
func TestMembershipHonest(t *testing.T) {
	for _, depth := range []int{2, 3, 5} {
		m, leaves := buildTree(t, depth, int64(depth))
		root := m.Root()
		for _, idx := range []int{0, 1, (1 << depth) - 1} {
			pf, err := ProveMembership(leaves[idx], m.PathFor(idx), depth, root, airQueries)
			if err != nil {
				t.Fatalf("depth=%d idx=%d prove: %v", depth, idx, err)
			}
			if !VerifyMembership(root, depth, pf, airQueries) {
				t.Fatalf("depth=%d idx=%d honest membership rejected", depth, idx)
			}
		}
	}
}

// TestMembershipNonMemberRejectedAtProve: a leaf+path that doesn't fold to the
// claimed root can't be proven (boundary division is non-exact).
func TestMembershipNonMemberRejectedAtProve(t *testing.T) {
	depth := 4
	m, leaves := buildTree(t, depth, 99)
	// Use leaf 2's path but a non-member leaf value.
	if _, err := ProveMembership(leaves[2].Add(1), m.PathFor(2), depth, m.Root(), airQueries); err != errAIRBadTrace {
		t.Fatalf("expected errAIRBadTrace for non-member, got %v", err)
	}
}

// TestMembershipWrongRoot: a valid member proof must fail against a different root.
func TestMembershipWrongRoot(t *testing.T) {
	depth := 4
	m, leaves := buildTree(t, depth, 7)
	pf, err := ProveMembership(leaves[5], m.PathFor(5), depth, m.Root(), airQueries)
	if err != nil {
		t.Fatal(err)
	}
	if VerifyMembership(m.Root().Add(1), depth, pf, airQueries) {
		t.Fatal("membership accepted against wrong root")
	}
}

// TestMembershipForgedPath: swapping in a wrong sibling fails to prove.
func TestMembershipForgedPath(t *testing.T) {
	depth := 4
	m, leaves := buildTree(t, depth, 11)
	p := m.PathFor(6)
	p.Siblings[1] = p.Siblings[1].Add(1) // corrupt the path
	if _, err := ProveMembership(leaves[6], p, depth, m.Root(), airQueries); err != errAIRBadTrace {
		t.Fatalf("expected errAIRBadTrace for forged path, got %v", err)
	}
}

// TestMembershipTampered: mutating the proof breaks verification.
func TestMembershipTampered(t *testing.T) {
	depth := 3
	m, leaves := buildTree(t, depth, 3)
	pf, _ := ProveMembership(leaves[1], m.PathFor(1), depth, m.Root(), airQueries)
	pf.Fz[0] = pf.Fz[0].Add(One2())
	if VerifyMembership(m.Root(), depth, pf, airQueries) {
		t.Fatal("tampered membership proof accepted")
	}
}

// TestMembershipHidesLeaf: the leaf value never appears in any revealed scalar.
func TestMembershipHidesLeaf(t *testing.T) {
	depth := 4
	m, leaves := buildTree(t, depth, 5)
	leaf := leaves[9]
	pf, _ := ProveMembership(leaf, m.PathFor(9), depth, m.Root(), airQueries)
	for _, v := range flattenExt(flattenExt(nil, pf.Fz...), pf.Fgz...) {
		if v == leaf {
			t.Fatal("leaf leaked into an out-of-domain evaluation")
		}
	}
}
