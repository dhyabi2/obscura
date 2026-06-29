package pqaccum

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// The streaming (O(log n) peaks) accumulator MUST produce byte-identical roots to
// the leaf-retaining one for every prefix of every sequence — otherwise switching
// nullAcc to streaming would silently change the header NullRoot (a hard fork).
func TestStreamingRootsIdenticalToLeaf(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 4, 5, 7, 8, 9, 15, 16, 17, 31, 63, 64, 100, 255, 256, 257, 1000} {
		leaf := New()
		stream := NewStreaming()
		for i := 0; i < n; i++ {
			var d [8]byte
			binary.BigEndian.PutUint64(d[:], uint64(i*2654435761)) // arbitrary spread
			leaf.Add(d[:])
			stream.Add(d[:])
			if !bytes.Equal(leaf.Root(), stream.Root()) {
				t.Fatalf("root mismatch at size %d (n=%d)", i+1, n)
			}
		}
		if leaf.Len() != stream.Len() {
			t.Fatalf("len mismatch at n=%d: %d vs %d", n, leaf.Len(), stream.Len())
		}
	}
}

// RootAfter must also match between the two modes (this is what predicts/verifies
// the header NullRoot in consensus).
func TestStreamingRootAfterMatches(t *testing.T) {
	leaf := New()
	stream := NewStreaming()
	for i := 0; i < 200; i++ {
		var d [8]byte
		binary.BigEndian.PutUint64(d[:], uint64(i))
		leaf.Add(d[:])
		stream.Add(d[:])
	}
	for _, k := range []int{0, 1, 2, 5, 13, 64, 100} {
		extra := make([][]byte, k)
		for j := 0; j < k; j++ {
			var d [8]byte
			binary.BigEndian.PutUint64(d[:], uint64(10_000+j))
			extra[j] = d[:]
		}
		if !bytes.Equal(leaf.RootAfter(extra), stream.RootAfter(extra)) {
			t.Fatalf("RootAfter mismatch for k=%d", k)
		}
		// RootAfter must not mutate either accumulator
		if !bytes.Equal(leaf.Root(), stream.Root()) {
			t.Fatalf("RootAfter mutated state (k=%d)", k)
		}
	}
}

// Clone of a streaming accumulator must be independent and equal-rooted.
func TestStreamingCloneIndependent(t *testing.T) {
	s := NewStreaming()
	for i := 0; i < 50; i++ {
		var d [8]byte
		binary.BigEndian.PutUint64(d[:], uint64(i))
		s.Add(d[:])
	}
	cp := s.Clone()
	if !bytes.Equal(s.Root(), cp.Root()) {
		t.Fatal("clone root differs")
	}
	s.Add([]byte("more"))
	if bytes.Equal(s.Root(), cp.Root()) {
		t.Fatal("clone not independent (mutating original changed clone)")
	}
	if _, err := s.Prove(0); err == nil {
		t.Fatal("streaming Prove should error")
	}
}
