package rpc

import (
	"encoding/json"
	"testing"
)

// TestHandleBlocksRange covers the /blocks range endpoint: it returns the available
// blocks from the cursor, stops early at the tip, caps the count, and the bytes match
// the single-block /block endpoint.
func TestHandleBlocksRange(t *testing.T) {
	s := newTestServer(t) // genesis-only chain, tip height = 0
	h := s.Handler()

	// from=0 returns exactly the genesis block (tip is 0), even with a large count.
	w := do(h, "GET", "/blocks?from=0&count=50", "127.0.0.1:5000", "")
	if w.Code != 200 {
		t.Fatalf("/blocks from=0: got %d", w.Code)
	}
	var got struct {
		Blocks []string `json:"blocks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Blocks) != 1 {
		t.Fatalf("from=0 count=50 on a height-0 chain: got %d blocks, want 1 (genesis only)", len(got.Blocks))
	}

	// The range block bytes must equal what /block?height=0 returns.
	w2 := do(h, "GET", "/block?height=0", "127.0.0.1:5000", "")
	var one struct {
		Block string `json:"block"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &one); err != nil {
		t.Fatalf("decode /block: %v", err)
	}
	if got.Blocks[0] != one.Block {
		t.Fatal("range block[0] hex != /block?height=0 hex")
	}

	// from past the tip returns an empty array (not an error), so a synced wallet stops.
	w3 := do(h, "GET", "/blocks?from=1&count=50", "127.0.0.1:5000", "")
	if w3.Code != 200 {
		t.Fatalf("/blocks from=1: got %d", w3.Code)
	}
	var past struct {
		Blocks []string `json:"blocks"`
	}
	if err := json.Unmarshal(w3.Body.Bytes(), &past); err != nil {
		t.Fatalf("decode past-tip: %v", err)
	}
	if len(past.Blocks) != 0 {
		t.Fatalf("from past tip: got %d blocks, want 0", len(past.Blocks))
	}
}
