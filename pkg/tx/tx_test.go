package tx

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func sampleTx() *Transaction {
	return &Transaction{
		Version: 1,
		Inputs: []Input{{
			OutputRef:        bytes.Repeat([]byte{1}, 32),
			OwnershipProof:   bytes.Repeat([]byte{2}, 70),
			PseudoCommitment: bytes.Repeat([]byte{3}, 32),
			EqualityProof:    bytes.Repeat([]byte{4}, 70),
		}},
		Outputs: []Output{{
			OneTimeKey: bytes.Repeat([]byte{5}, 32),
			TxPubKey:   bytes.Repeat([]byte{6}, 32),
			Commitment: bytes.Repeat([]byte{7}, 32),
			RangeProof: bytes.Repeat([]byte{8}, 100),
			EncAmount:  bytes.Repeat([]byte{9}, 8),
			EncMask:    bytes.Repeat([]byte{10}, 32),
		}},
		Fee:          12345,
		Conservation: bytes.Repeat([]byte{11}, 70),
	}
}

func TestSerializeRoundTrip(t *testing.T) {
	orig := sampleTx()
	got, err := Deserialize(orig.Serialize())
	if err != nil {
		t.Fatalf("deserialize: %v", err)
	}
	if got.Hash() != orig.Hash() {
		t.Fatal("round-trip hash mismatch")
	}
}

func TestCoreHashExcludesProofs(t *testing.T) {
	a := sampleTx()
	b := sampleTx()
	// changing a proof field must NOT change the CoreHash (it binds content, not
	// the proofs themselves), but MUST change the full Hash.
	b.Inputs[0].OwnershipProof = bytes.Repeat([]byte{0xff}, 70)
	b.Conservation = bytes.Repeat([]byte{0xfe}, 70)
	if a.CoreHash() != b.CoreHash() {
		t.Fatal("CoreHash should ignore proof fields")
	}
	if a.Hash() == b.Hash() {
		t.Fatal("full Hash should include proof fields")
	}
	// changing real content MUST change CoreHash.
	b2 := sampleTx()
	b2.Fee = 999
	if a.CoreHash() == b2.CoreHash() {
		t.Fatal("CoreHash must cover content like fee")
	}
}

func TestDeserializeRejectsOversizedField(t *testing.T) {
	// craft: version(2)+coinbase(1)+ninputs(8)=1 + first field length = huge
	var buf bytes.Buffer
	buf.Write([]byte{0, 1}) // version
	buf.WriteByte(0)        // not coinbase
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], 1) // 1 input
	buf.Write(n[:])
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], 0xFFFFFFF) // absurd field length
	buf.Write(l[:])
	if _, err := Deserialize(buf.Bytes()); err == nil {
		t.Fatal("expected rejection of oversized field")
	}
}

func TestDeserializeRejectsTooManyInputs(t *testing.T) {
	var buf bytes.Buffer
	buf.Write([]byte{0, 1})
	buf.WriteByte(0)
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], MaxInputs+1)
	buf.Write(n[:])
	if _, err := Deserialize(buf.Bytes()); err == nil {
		t.Fatal("expected rejection of too many inputs")
	}
}

func TestDeserializeRejectsTruncated(t *testing.T) {
	full := sampleTx().Serialize()
	if _, err := Deserialize(full[:len(full)-5]); err == nil {
		t.Fatal("expected rejection of truncated input")
	}
}
