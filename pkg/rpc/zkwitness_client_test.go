package rpc

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"obscura/pkg/stark"
)

// TestZKWitnessClientRoundTrip proves the rpc.Client reconstructs a REAL (ok:true)
// membership witness byte-identically from the wire JSON the handler emits: the
// per-sibling encode (hex of stark.NodeBytes) and decode (stark.NodeFromBytes) must be
// exact inverses and the leaf Index must survive. The handler tests only cover the
// empty (ok:false) path, and the chain ZK e2e exercises the witness IN-PROCESS, so this
// closes the one risk the RPC layer adds: a lossy path serialization that would make
// zkspend build an invalid proof.
func TestZKWitnessClientRoundTrip(t *testing.T) {
	// Build a synthetic but realistic witness: known 32-byte siblings + an index.
	wantSibs := []stark.Node256{
		stark.NodeFromBytes(mustBytes32(0x11)),
		stark.NodeFromBytes(mustBytes32(0x22)),
		stark.NodeFromBytes(mustBytes32(0xAB)),
	}
	wantAnchor := mustBytes32(0x7E)
	wantIndex := 5

	// A stub node that serves exactly what handleZKWitness would for this witness.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/zkwitness" {
			http.Error(w, "no", 404)
			return
		}
		sibs := make([]string, len(wantSibs))
		for i, n := range wantSibs {
			sibs[i] = hex.EncodeToString(stark.NodeBytes(n))
		}
		_ = json.NewEncoder(w).Encode(ZKWitnessResponse{
			Anchor: hex.EncodeToString(wantAnchor), Path: sibs,
			Index: wantIndex, Depth: 32, OK: true,
		})
	}))
	defer srv.Close()

	c, err := NewClient(srv.URL)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	anchor, path, depth, ok := c.ZKWitnessFor(make([]byte, 32))
	if !ok {
		t.Fatal("ok=false, want true")
	}
	if depth != 32 {
		t.Fatalf("depth=%d want 32", depth)
	}
	if hex.EncodeToString(anchor) != hex.EncodeToString(wantAnchor) {
		t.Fatalf("anchor mismatch: %x != %x", anchor, wantAnchor)
	}
	if path.Index != wantIndex {
		t.Fatalf("index=%d want %d", path.Index, wantIndex)
	}
	if len(path.Siblings) != len(wantSibs) {
		t.Fatalf("siblings len=%d want %d", len(path.Siblings), len(wantSibs))
	}
	for i := range wantSibs {
		if hex.EncodeToString(stark.NodeBytes(path.Siblings[i])) != hex.EncodeToString(stark.NodeBytes(wantSibs[i])) {
			t.Fatalf("sibling %d mismatch after round-trip", i)
		}
	}
}

func mustBytes32(b byte) []byte {
	out := make([]byte, 32)
	for i := range out {
		out[i] = b
	}
	return out
}
