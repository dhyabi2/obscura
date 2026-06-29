package pqaccum

import (
	"fmt"
	"testing"
)

// Regression tests for the audit fix: Verify must derive path direction from
// (Index, Size) and reject a tampered index/size; ParseProof must bound-check.

func buildProof(t *testing.T, n, target int) ([]byte, *Accumulator, []byte) {
	t.Helper()
	a := New()
	var data []byte
	for i := 0; i < n; i++ {
		d := []byte(fmt.Sprintf("leaf-%d", i))
		a.Add(d)
		if i == target {
			data = d
		}
	}
	pr, err := a.Prove(target)
	if err != nil {
		t.Fatal(err)
	}
	return data, a, pr.Marshal()
}

func TestMarshalRoundTripWithSize(t *testing.T) {
	for _, n := range []int{1, 2, 5, 8, 13, 100} {
		for target := 0; target < n; target++ {
			data, a, raw := buildProof(t, n, target)
			pr, err := ParseProof(raw)
			if err != nil {
				t.Fatalf("n=%d target=%d parse: %v", n, target, err)
			}
			if pr.Index != target || pr.Size != n {
				t.Fatalf("n=%d target=%d: got index=%d size=%d", n, target, pr.Index, pr.Size)
			}
			if !Verify(a.Root(), data, pr) {
				t.Fatalf("n=%d target=%d: round-tripped proof rejected", n, target)
			}
		}
	}
}

func TestVerifyRejectsTamperedIndex(t *testing.T) {
	data, a, raw := buildProof(t, 8, 3)
	pr, _ := ParseProof(raw)
	root := a.Root()
	if !Verify(root, data, pr) {
		t.Fatal("honest proof rejected")
	}
	for _, badIdx := range []int{0, 1, 2, 4, 7} {
		bad := &Proof{Index: badIdx, Size: pr.Size, Path: pr.Path}
		if Verify(root, data, bad) {
			t.Fatalf("accepted proof with tampered index %d (was 3)", badIdx)
		}
	}
}

func TestVerifyRejectsTamperedSize(t *testing.T) {
	data, a, raw := buildProof(t, 8, 3)
	pr, _ := ParseProof(raw)
	root := a.Root()
	// Sizes that change the derived path structure (inner/border) must be
	// rejected. (A size change that leaves the structure — and thus the folding —
	// identical still reproduces the real root, i.e. data is still a genuine
	// member of the anchor's tree; that is sound, not a forgery, so we don't
	// assert rejection for those.)
	for _, badSize := range []int{2, 4, 9, 16} {
		bad := &Proof{Index: pr.Index, Size: badSize, Path: pr.Path}
		if Verify(root, data, bad) {
			t.Fatalf("accepted proof with structure-changing tampered size %d (was 8)", badSize)
		}
	}
}

func TestParseProofBounds(t *testing.T) {
	// index >= size must be rejected.
	bad := (&Proof{Index: 5, Size: 5}).Marshal()
	if _, err := ParseProof(bad); err == nil {
		t.Fatal("ParseProof accepted index>=size")
	}
	// size zero rejected.
	bad2 := (&Proof{Index: 0, Size: 0}).Marshal()
	if _, err := ParseProof(bad2); err == nil {
		t.Fatal("ParseProof accepted size==0")
	}
	// truncated buffer rejected (no panic).
	if _, err := ParseProof([]byte{1, 2, 3}); err == nil {
		t.Fatal("ParseProof accepted short buffer")
	}
}
