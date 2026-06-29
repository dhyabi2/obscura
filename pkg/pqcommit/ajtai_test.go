package pqcommit

import (
	"crypto/rand"
	"encoding/binary"
	"testing"
)

func randR(t testing.TB) []int32 {
	r := make([]int32, RandLen)
	buf := make([]byte, 4)
	for i := range r {
		rand.Read(buf)
		r[i] = int32(binary.LittleEndian.Uint32(buf)%(2*RandB+1)) - RandB
	}
	return r
}

func TestCommitOpen(t *testing.T) {
	r := randR(t)
	c, err := Commit(42, r)
	if err != nil {
		t.Fatal(err)
	}
	if !Open(c, 42, r) {
		t.Fatal("valid opening rejected")
	}
	if Open(c, 43, r) {
		t.Fatal("opening to wrong value accepted")
	}
}

func TestDeterministic(t *testing.T) {
	r := randR(t)
	c1, _ := Commit(7, r)
	c2, _ := Commit(7, r)
	if !c1.Equal(c2) {
		t.Fatal("Commit not deterministic")
	}
}

// Homomorphism is the property the conservation proof depends on.
func TestHomomorphic(t *testing.T) {
	r1, r2 := randR(t), randR(t)
	v1, v2 := uint64(1000), uint64(2345)
	c1, _ := Commit(v1, r1)
	c2, _ := Commit(v2, r2)
	sum := c1.Add(c2)

	rsum := make([]int32, RandLen)
	for i := range rsum {
		rsum[i] = r1[i] + r2[i] // may exceed RandB; use linearMap (no bound check)
	}
	want := linearMap(v1+v2, rsum)
	if !sum.Equal(want) {
		t.Fatal("commitment is not additively homomorphic")
	}
}

// Conservation: Σ inputs − Σ outputs commits to 0 when amounts balance, with
// the blinding factors arranged to cancel — exactly the check the chain runs.
func TestConservation(t *testing.T) {
	rin, rout := randR(t), randR(t)
	in, _ := Commit(5000, rin)
	out, _ := Commit(5000, rout)
	diff := in.Sub(out)
	// diff = Commit(0; rin-rout). Verify it opens to 0 under the difference.
	rdiff := make([]int32, RandLen)
	for i := range rdiff {
		rdiff[i] = rin[i] - rout[i]
	}
	if !diff.Equal(linearMap(0, rdiff)) {
		t.Fatal("balanced in/out did not yield a commitment to zero")
	}
	// and an UNbalanced pair must NOT commit to zero under the same difference
	badOut, _ := Commit(4999, rout)
	if in.Sub(badOut).Equal(linearMap(0, rdiff)) {
		t.Fatal("unbalanced amounts passed the conservation check (inflation!)")
	}
}

func TestBoundEnforced(t *testing.T) {
	r := randR(t)
	r[0] = RandB + 1
	if _, err := Commit(1, r); err == nil {
		t.Fatal("accepted out-of-bound randomness")
	}
	if _, err := Commit(1, make([]int32, RandLen-1)); err == nil {
		t.Fatal("accepted wrong randomness length")
	}
	if _, err := Commit(Q, randR(t)); err == nil {
		t.Fatal("accepted amount >= Q")
	}
}

func TestDistinctValues(t *testing.T) {
	r := randR(t)
	seen := map[string]uint64{}
	for v := uint64(0); v < 300; v++ {
		c, _ := Commit(v, r)
		key := string(c.Bytes())
		if other, ok := seen[key]; ok {
			t.Fatalf("collision: values %d and %d share a commitment", other, v)
		}
		seen[key] = v
	}
}

func BenchmarkCommit(b *testing.B) {
	r := randR(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Commit(uint64(i), r)
	}
}
