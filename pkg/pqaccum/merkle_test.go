package pqaccum

import (
	"bytes"
	"fmt"
	"testing"
)

func TestAddProveVerify(t *testing.T) {
	for _, count := range []int{1, 2, 3, 4, 5, 7, 8, 9, 16, 31, 100} {
		a := New()
		datas := make([][]byte, count)
		for i := 0; i < count; i++ {
			datas[i] = []byte(fmt.Sprintf("output-commitment-%d", i))
			a.Add(datas[i])
		}
		root := a.Root()
		for i := 0; i < count; i++ {
			pr, err := a.Prove(i)
			if err != nil {
				t.Fatalf("count=%d prove(%d): %v", count, i, err)
			}
			if !Verify(root, datas[i], pr) {
				t.Fatalf("count=%d: valid membership proof rejected for leaf %d", count, i)
			}
		}
	}
}

func TestNonMemberRejected(t *testing.T) {
	a := New()
	a.Add([]byte("a"))
	a.Add([]byte("b"))
	a.Add([]byte("c"))
	root := a.Root()
	pr, _ := a.Prove(1)
	if Verify(root, []byte("not-in-set"), pr) {
		t.Fatal("accepted a non-member with a borrowed path")
	}
}

func TestTamperedPathRejected(t *testing.T) {
	a := New()
	for i := 0; i < 8; i++ {
		a.Add([]byte{byte(i)})
	}
	root := a.Root()
	pr, _ := a.Prove(3)
	if len(pr.Path) == 0 {
		t.Fatal("expected a non-empty path")
	}
	pr.Path[0].Hash[0] ^= 0xff
	if Verify(root, []byte{3}, pr) {
		t.Fatal("accepted a tampered authentication path")
	}
}

func TestRootChangesOnAdd(t *testing.T) {
	a := New()
	a.Add([]byte("x"))
	r1 := a.Root()
	a.Add([]byte("y"))
	r2 := a.Root()
	if bytes.Equal(r1, r2) {
		t.Fatal("root did not change after adding an element")
	}
}

// CVE-2012-2459 style: a leaf's data must not be confusable with an internal
// node. Domain separation (0x00 vs 0x01 prefix) guarantees this.
func TestLeafNodeDomainSeparation(t *testing.T) {
	left := leafHash([]byte("l"))
	right := leafHash([]byte("r"))
	internal := nodeHash(left, right)
	// Adding the raw concatenation of two leaf hashes as a single leaf must NOT
	// hash to the same value as the internal node over those leaves.
	if bytes.Equal(leafHash(append(left, right...)), internal) {
		t.Fatal("leaf/internal hash collision possible (CVE-2012-2459)")
	}
}

func TestEmptyAndBounds(t *testing.T) {
	a := New()
	if got := a.Root(); !bytes.Equal(got, make([]byte, HashSize)) {
		t.Fatal("empty root should be zero")
	}
	if _, err := a.Prove(0); err == nil {
		t.Fatal("Prove on empty accumulator should error")
	}
	a.Add([]byte("only"))
	if _, err := a.Prove(1); err == nil {
		t.Fatal("Prove out of range should error")
	}
	if Verify(make([]byte, 4), []byte("x"), &Proof{}) {
		t.Fatal("Verify accepted short root")
	}
}
