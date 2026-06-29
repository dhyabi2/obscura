package stark

import (
	"math/rand"
	"testing"
)

func randNode(r *rand.Rand) Node256 {
	return Node256{randField(r), randField(r), randField(r), randField(r)}
}

// TestWidePermBijective: the width-8 permutation is injective over a sample.
func TestWidePermBijective(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	seen := make(map[[poseidonWideT]uint64]struct{})
	for i := 0; i < 20000; i++ {
		var in [poseidonWideT]Felt
		for j := range in {
			in[j] = randField(r)
		}
		out := poseidonWidePermute(in)
		var key [poseidonWideT]uint64
		for j := range out {
			key[j] = uint64(out[j])
		}
		if _, dup := seen[key]; dup {
			t.Fatal("width-8 permutation collision (not bijective)")
		}
		seen[key] = struct{}{}
	}
}

// TestJiveDeterministicAndSensitive: deterministic + a 1-element change flips output.
func TestJiveDeterministicAndSensitive(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for i := 0; i < 2000; i++ {
		a, b := randNode(r), randNode(r)
		if JiveCompress(a, b) != JiveCompress(a, b) {
			t.Fatal("Jive not deterministic")
		}
		a2 := a
		a2[0] = a2[0].Add(1)
		if JiveCompress(a, b) == JiveCompress(a2, b) {
			t.Fatal("Jive insensitive to a 1-element change")
		}
	}
}

// TestJiveNoCollisionsSample: no collisions in a large sample of Jive outputs (a
// 256-bit output should be collision-free far beyond this; this only sanity-checks
// the construction isn't degenerate).
func TestJiveNoCollisionsSample(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	seen := make(map[[4]uint64]struct{})
	for i := 0; i < 100000; i++ {
		o := JiveCompress(randNode(r), randNode(r))
		k := [4]uint64{uint64(o[0]), uint64(o[1]), uint64(o[2]), uint64(o[3])}
		if _, dup := seen[k]; dup {
			t.Fatal("Jive collision in sample (degenerate construction)")
		}
		seen[k] = struct{}{}
	}
}

// TestWideMDSInvertible: distinct unit vectors map differently.
func TestWideMDSInvertible(t *testing.T) {
	mul := func(v [poseidonWideT]Felt) [poseidonWideT]Felt {
		var out [poseidonWideT]Felt
		for i := 0; i < poseidonWideT; i++ {
			acc := Felt(0)
			for j := 0; j < poseidonWideT; j++ {
				acc = acc.Add(poseidonWideMDS[i][j].Mul(v[j]))
			}
			out[i] = acc
		}
		return out
	}
	var e0, e1 [poseidonWideT]Felt
	e0[0], e1[1] = 1, 1
	if mul(e0) == mul(e1) {
		t.Fatal("wide MDS maps distinct basis vectors equally")
	}
}
