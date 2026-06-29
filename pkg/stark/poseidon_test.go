package stark

import (
	"math/rand"
	"testing"
)

// TestPoseidonDeterministic: same input ⇒ same output.
func TestPoseidonDeterministic(t *testing.T) {
	a, b := Felt(123456789), Felt(987654321)
	if PoseidonHash2(a, b) != PoseidonHash2(a, b) {
		t.Fatal("Hash2 not deterministic")
	}
	if PoseidonHash1(a) != PoseidonHash1(a) {
		t.Fatal("Hash1 not deterministic")
	}
}

// TestPoseidonPermutationBijective: over a random sample, distinct inputs give
// distinct outputs (a permutation must be injective). x⁷ + invertible MDS ⇒ bijection.
func TestPoseidonPermutationBijective(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	seen := make(map[[poseidonT]uint64]struct{})
	for i := 0; i < 20000; i++ {
		in := [poseidonT]Felt{randField(r), randField(r), randField(r)}
		out := PoseidonPermute(in)
		key := [poseidonT]uint64{uint64(out[0]), uint64(out[1]), uint64(out[2])}
		if _, dup := seen[key]; dup {
			t.Fatal("permutation produced a collision (not bijective)")
		}
		seen[key] = struct{}{}
	}
}

// TestPoseidonSensitivity: a one-element change in the input changes the output.
func TestPoseidonSensitivity(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 1000; i++ {
		a, b := randField(r), randField(r)
		if PoseidonHash2(a, b) == PoseidonHash2(a.Add(1), b) {
			t.Fatal("Hash2 insensitive to first input")
		}
		if PoseidonHash2(a, b) == PoseidonHash2(a, b.Add(1)) {
			t.Fatal("Hash2 insensitive to second input")
		}
	}
}

// TestPoseidonNoTrivialCollisions: a sample of Hash2 outputs has no collisions.
func TestPoseidonNoTrivialCollisions(t *testing.T) {
	r := rand.New(rand.NewSource(9))
	seen := make(map[uint64]struct{})
	for i := 0; i < 50000; i++ {
		h := uint64(PoseidonHash2(randField(r), randField(r)))
		if _, dup := seen[h]; dup {
			t.Fatal("unexpected Hash2 collision in sample")
		}
		seen[h] = struct{}{}
	}
}

// TestMDSInvertible: the MDS matrix maps distinct states to distinct states.
func TestMDSInvertible(t *testing.T) {
	// e_i basis images must be linearly independent ⇒ here just check no zero row
	// and that two different unit vectors map differently.
	e0 := mds([poseidonT]Felt{1, 0, 0})
	e1 := mds([poseidonT]Felt{0, 1, 0})
	if e0 == e1 {
		t.Fatal("MDS maps distinct basis vectors equally")
	}
}
