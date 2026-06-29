// Package pow_test covers the RandomX-style VM proof-of-work: determinism,
// avalanche, sensitivity to the VM parameters and the seed key, and that
// Meets/Target behave monotonically.
package pow_test

import (
	"encoding/binary"
	"testing"

	"obscura/pkg/pow"
)

func input(n uint64) []byte {
	b := make([]byte, 40)
	copy(b, "obscura-pow-test")
	binary.LittleEndian.PutUint64(b[32:], n)
	return b
}

func TestDeterministic(t *testing.T) {
	in := input(12345)
	a := pow.Hash(in)
	for i := 0; i < 4; i++ {
		if pow.Hash(in) != a {
			t.Fatal("PoW hash is not deterministic")
		}
	}
}

func TestDistinctInputsDiffer(t *testing.T) {
	seen := map[[32]byte]bool{}
	for n := uint64(0); n < 500; n++ {
		h := pow.Hash(input(n))
		if seen[h] {
			t.Fatalf("collision at n=%d", n)
		}
		seen[h] = true
	}
}

func TestAvalanche(t *testing.T) {
	base := input(777)
	h0 := pow.Hash(base)
	total := 0
	const trials = 32
	for bit := 0; bit < trials; bit++ {
		b := append([]byte(nil), base...)
		b[bit%len(b)] ^= 1 << (bit % 8)
		h := pow.Hash(b)
		for i := range h0 {
			if h0[i] != h[i] {
				total++
			}
		}
	}
	avg := float64(total) / trials
	if avg < 20 { // expect ~30/32 bytes to change; demand a strong floor
		t.Fatalf("weak avalanche: avg %.1f/32 bytes changed", avg)
	}
}

func TestParamsAffectHash(t *testing.T) {
	in := input(42)
	a := pow.Hash(in)
	old := pow.RxIterations
	defer func() { pow.RxIterations = old }()
	pow.RxIterations = old + 17
	if pow.Hash(in) == a {
		t.Fatal("changing RxIterations did not change the hash")
	}
}

func TestSeedKeyAffectsHash(t *testing.T) {
	in := input(99)
	a := pow.Hash(in)
	old := pow.SeedKey
	defer func() { pow.SeedKey = old; pow.Hash(in) }() // restore + rebuild cache
	pow.SeedKey = []byte("a-different-seed-key")
	if pow.Hash(in) == a {
		t.Fatal("changing the seed key did not change the hash (cache not re-keyed)")
	}
}

func TestMeetsMonotonic(t *testing.T) {
	// difficulty 1 is satisfied by (almost) everything; very high difficulty by
	// (almost) nothing. Sample a batch and check the easy set ⊇ hard set.
	easy, hard := 0, 0
	for n := uint64(0); n < 300; n++ {
		h := pow.Hash(input(n))
		if pow.Meets(h, 1) {
			easy++
		}
		if pow.Meets(h, 1<<20) {
			hard++
		}
	}
	if easy < hard {
		t.Fatalf("more hashes met a HARDER target (%d) than an easy one (%d)", hard, easy)
	}
	if easy == 0 {
		t.Fatal("nothing met difficulty 1")
	}
}
