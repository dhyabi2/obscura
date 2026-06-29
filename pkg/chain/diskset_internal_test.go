package chain

import (
	"testing"

	"golang.org/x/crypto/blake2b"
	bolt "go.etcd.io/bbolt"
)

func newTestDiskSet(t *testing.T) *diskSet {
	t.Helper()
	db, err := bolt.Open(t.TempDir()+"/ds.db", 0600, nil)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	idx, set, com := []byte("i"), []byte("s"), []byte("c")
	if err := db.Update(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{idx, set, com} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("buckets: %v", err)
	}
	return newDiskSet(db, idx, set, com)
}

// TestDiskSetCommit proves the set commitment is order-independent, predictable via
// CommitAfter, idempotent on duplicates, and reorg/restore-safe (truncate + setCount restore
// the exact commit for a given count). This is the foundation of the header StateRoot.
func TestDiskSetCommit(t *testing.T) {
	var zero [32]byte

	// empty set commits to zero.
	s := newTestDiskSet(t)
	if s.Commit() != zero {
		t.Fatal("empty set commit must be zero")
	}

	// CommitAfter predicts the commit Add produces.
	pred := s.CommitAfter([]string{"a", "b", "c"})
	s.Add("a")
	s.Add("b")
	s.Add("c")
	if s.Commit() != pred {
		t.Fatal("CommitAfter must predict the post-Add commit")
	}
	if s.Commit() == zero {
		t.Fatal("non-empty set commit must be non-zero")
	}
	commitABC := s.Commit()

	// order independence: a different set with the same members reaches the same commit.
	s2 := newTestDiskSet(t)
	s2.Add("c")
	s2.Add("a")
	s2.Add("b")
	if s2.Commit() != commitABC {
		t.Fatal("commit must be independent of insertion order")
	}

	// idempotent: re-adding an existing key changes neither commit nor (effective) members.
	s.Add("b")
	if s.Commit() != commitABC {
		t.Fatal("duplicate Add must not change the commit")
	}

	// reorg rollback: capture commit at count 1, add more, truncate back, expect exact match.
	r := newTestDiskSet(t)
	r.Add("x")
	commitX := r.Commit()
	countX := r.Count()
	r.Add("y")
	r.Add("z")
	if r.Commit() == commitX {
		t.Fatal("commit must advance as members are added")
	}
	r.truncate(countX)
	if r.Commit() != commitX {
		t.Fatal("truncate must restore the exact commit for the target count")
	}
	if r.Has("y") || r.Has("z") {
		t.Fatal("truncate must remove members above the target count")
	}
	// re-adding after truncate reaches the same commit as the original {x,y,z}.
	r.Add("y")
	r.Add("z")
	if r.Commit() != add256(add256(blakeKey("x"), blakeKey("y")), blakeKey("z")) {
		t.Fatal("re-add after truncate must reproduce the full commit")
	}

	// restore path: setCount(n) recovers the commit recorded at n (simulates snapshot restore).
	got := r.readCommit(countX)
	if got != commitX {
		t.Fatal("readCommit must return the recorded commit for the count")
	}
	r.setCount(countX)
	if r.Commit() != commitX {
		t.Fatal("setCount must restore the commit for the target count")
	}
}

// blakeKey mirrors the diskSet's per-key hash for test assertions.
func blakeKey(k string) [32]byte {
	return blake2b.Sum256([]byte(k))
}
