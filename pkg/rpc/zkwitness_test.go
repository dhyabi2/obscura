package rpc

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestHandleZKWitness covers the /zkwitness endpoint wiring on a genesis-only chain:
// a witness request for a leaf that is not in the commitment tree returns ok:false
// (with the tree depth still reported), and a malformed leaf is rejected with 400.
func TestHandleZKWitness(t *testing.T) {
	s := newTestServer(t) // genesis-only chain, no ZK coins minted yet
	h := s.Handler()

	// A 32-byte leaf that was never minted: the node has no witness, so ok:false.
	leaf := strings.Repeat("ab", 32) // 64 hex chars = 32 bytes
	w := do(h, "GET", "/zkwitness?leaf="+leaf, "127.0.0.1:5000", "")
	if w.Code != 200 {
		t.Fatalf("/zkwitness: got %d", w.Code)
	}
	var got ZKWitnessResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.OK {
		t.Fatal("witness for a non-existent leaf must report ok:false")
	}
	if got.Depth <= 0 {
		t.Fatalf("depth should be reported even on miss: got %d", got.Depth)
	}

	// A malformed (non-32-byte) leaf is rejected.
	w2 := do(h, "GET", "/zkwitness?leaf=zz", "127.0.0.1:5000", "")
	if w2.Code != 400 {
		t.Fatalf("malformed leaf: got %d want 400", w2.Code)
	}
}
