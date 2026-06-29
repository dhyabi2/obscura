package block

import "testing"

// TestDeserializeBlockRejectsTrailingBytes covers the audit fix: a block payload
// with arbitrary trailing junk after the canonical structure must be rejected, so
// relays cannot re-broadcast padded (non-canonical) bytes for a valid block
// (1-hop malleability/amplification).
func TestDeserializeBlockRejectsTrailingBytes(t *testing.T) {
	b := &Block{Header: Header{
		Version:  1,
		Height:   7,
		AccValue: []byte{1, 2, 3},
		AccSize:  3,
	}}
	canonical := b.Serialize()

	// canonical bytes must round-trip.
	got, err := DeserializeBlock(canonical)
	if err != nil {
		t.Fatalf("canonical block failed to deserialize: %v", err)
	}
	if got.Header.Height != b.Header.Height {
		t.Fatalf("round-trip mismatch: height %d != %d", got.Header.Height, b.Header.Height)
	}

	// appending trailing junk must be rejected.
	padded := append(append([]byte{}, canonical...), 0xDE, 0xAD, 0xBE, 0xEF)
	if _, err := DeserializeBlock(padded); err == nil {
		t.Fatalf("expected error for block with trailing bytes, got nil")
	}

	// a single trailing zero byte (the cheapest amplification) must also fail.
	if _, err := DeserializeBlock(append(append([]byte{}, canonical...), 0x00)); err == nil {
		t.Fatalf("expected error for block with one trailing byte, got nil")
	}
}
