// Package ledger holds critical-workflow tests for Obscura's transaction and
// block serialization, the merkle/PoW commitment structure, and proof-of-work
// target math. These tests exercise the public APIs of pkg/tx, pkg/block,
// pkg/pow and pkg/commit only; they never mutate those packages.
package ledger

import (
	"bytes"
	"encoding/binary"
	"math/big"
	"testing"

	"obscura/pkg/block"
	"obscura/pkg/commit"
	"obscura/pkg/pow"
	"obscura/pkg/tx"

	"obscura/tests/critical/harness"

	"filippo.io/edwards25519"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// sampleTx builds a small, fully-populated non-coinbase transaction from
// deterministic literal byte slices (no crypto), suitable for serialization
// round-trip and bounds tests.
func sampleTx() *tx.Transaction {
	return &tx.Transaction{
		Version:    2,
		IsCoinbase: false,
		Inputs: []tx.Input{
			{
				OutputRef:        bytes.Repeat([]byte{0xA1}, 32),
				OwnershipProof:   bytes.Repeat([]byte{0xB2}, 64),
				PseudoCommitment: bytes.Repeat([]byte{0xC3}, 32),
				EqualityProof:    bytes.Repeat([]byte{0xD4}, 64),
			},
		},
		Outputs: []tx.Output{
			{
				OneTimeKey: bytes.Repeat([]byte{0x11}, 32),
				TxPubKey:   bytes.Repeat([]byte{0x22}, 32),
				Commitment: bytes.Repeat([]byte{0x33}, 32),
				RangeProof: bytes.Repeat([]byte{0x44}, 100),
				PrimeNonce: 7,
				LockUntil:  0,
				EncAmount:  bytes.Repeat([]byte{0x55}, 8),
				EncMask:    bytes.Repeat([]byte{0x66}, 8),
			},
		},
		Fee:          1234,
		Conservation: bytes.Repeat([]byte{0x77}, 64),
		Height:       0,
		Minted:       0,
		ReferrerTag:  nil,
		ExtraNonce:   99,
	}
}

func eqHash(a, b [32]byte) bool { return a == b }

// ---------------------------------------------------------------------------
// 1. tx round-trip preserves Hash()
// ---------------------------------------------------------------------------

func TestTxSerializeDeserializeRoundTripHash(t *testing.T) {
	orig := sampleTx()
	want := orig.Hash()
	raw := orig.Serialize()

	got, err := tx.Deserialize(raw)
	if err != nil {
		t.Fatalf("Deserialize: %v", err)
	}
	if !eqHash(want, got.Hash()) {
		t.Fatalf("Hash changed across round-trip: %x != %x", want, got.Hash())
	}
	// Re-serialize must be byte-identical (canonical encoding).
	if !bytes.Equal(raw, got.Serialize()) {
		t.Fatalf("re-serialized bytes differ from original")
	}
}

// ---------------------------------------------------------------------------
// 2. CoreHash excludes proof fields; full Hash includes them
// ---------------------------------------------------------------------------

func TestCoreHashExcludesProofFields(t *testing.T) {
	base := sampleTx()
	baseCore := base.CoreHash()
	baseHash := base.Hash()

	// Mutate only the proof/signature fields.
	mut := sampleTx()
	mut.Inputs[0].OwnershipProof = bytes.Repeat([]byte{0xEE}, 64)
	mut.Inputs[0].EqualityProof = bytes.Repeat([]byte{0xFF}, 64)
	mut.Conservation = bytes.Repeat([]byte{0x01}, 64)

	if !eqHash(baseCore, mut.CoreHash()) {
		t.Fatalf("CoreHash changed when only proof fields changed: %x != %x",
			baseCore, mut.CoreHash())
	}
	if eqHash(baseHash, mut.Hash()) {
		t.Fatalf("full Hash did NOT change when proof fields changed (expected change)")
	}
}

// ---------------------------------------------------------------------------
// 3. CoreHash DOES change when content changes
// ---------------------------------------------------------------------------

func TestCoreHashChangesOnContent(t *testing.T) {
	base := sampleTx()
	baseCore := base.CoreHash()

	cases := []struct {
		name string
		mut  func(*tx.Transaction)
	}{
		{"fee", func(t *tx.Transaction) { t.Fee = 9999 }},
		{"outputRef", func(t *tx.Transaction) { t.Inputs[0].OutputRef = bytes.Repeat([]byte{0x00}, 32) }},
		{"pseudoCommitment", func(t *tx.Transaction) { t.Inputs[0].PseudoCommitment = bytes.Repeat([]byte{0x00}, 32) }},
		{"outputCommitment", func(t *tx.Transaction) { t.Outputs[0].Commitment = bytes.Repeat([]byte{0x00}, 32) }},
		{"outputOneTimeKey", func(t *tx.Transaction) { t.Outputs[0].OneTimeKey = bytes.Repeat([]byte{0x00}, 32) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := sampleTx()
			tc.mut(m)
			if eqHash(baseCore, m.CoreHash()) {
				t.Fatalf("CoreHash unchanged after mutating %s (expected change)", tc.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 4. Deserialize rejects an oversized field length
// ---------------------------------------------------------------------------

func TestDeserializeRejectsOversizedField(t *testing.T) {
	var buf bytes.Buffer
	// Version(2) + isCoinbase(1)
	buf.Write([]byte{0x00, 0x02})
	buf.WriteByte(0)
	// nin = 1
	var u8 [8]byte
	binary.BigEndian.PutUint64(u8[:], 1)
	buf.Write(u8[:])
	// First field: OutputRef with a huge 4-byte length prefix.
	var huge [4]byte
	binary.BigEndian.PutUint32(huge[:], uint32(tx.MaxFieldBytes+1))
	buf.Write(huge[:])
	// (no actual payload; the length check should fire first)

	if _, err := tx.Deserialize(buf.Bytes()); err == nil {
		t.Fatalf("expected error for oversized field length, got nil")
	}
}

// ---------------------------------------------------------------------------
// 5. Deserialize rejects > MaxInputs and > MaxOutputs
// ---------------------------------------------------------------------------

func TestDeserializeRejectsTooManyInputs(t *testing.T) {
	var buf bytes.Buffer
	buf.Write([]byte{0x00, 0x02})
	buf.WriteByte(0)
	var u8 [8]byte
	binary.BigEndian.PutUint64(u8[:], uint64(tx.MaxInputs)+1)
	buf.Write(u8[:])

	if _, err := tx.Deserialize(buf.Bytes()); err == nil {
		t.Fatalf("expected error for too many inputs, got nil")
	}
}

func TestDeserializeRejectsTooManyOutputs(t *testing.T) {
	var buf bytes.Buffer
	buf.Write([]byte{0x00, 0x02})
	buf.WriteByte(0)
	var u8 [8]byte
	// nin = 0
	binary.BigEndian.PutUint64(u8[:], 0)
	buf.Write(u8[:])
	// nout = MaxOutputs+1
	binary.BigEndian.PutUint64(u8[:], uint64(tx.MaxOutputs)+1)
	buf.Write(u8[:])

	if _, err := tx.Deserialize(buf.Bytes()); err == nil {
		t.Fatalf("expected error for too many outputs, got nil")
	}
}

// ---------------------------------------------------------------------------
// 6. Deserialize rejects a truncated input
// ---------------------------------------------------------------------------

func TestDeserializeRejectsTruncatedInput(t *testing.T) {
	full := sampleTx().Serialize()
	// Cut the buffer somewhere inside the first input's fields.
	truncated := full[:15]
	if _, err := tx.Deserialize(truncated); err == nil {
		t.Fatalf("expected error for truncated input, got nil")
	}
}

// ---------------------------------------------------------------------------
// 7. Deserialize rejects > MaxTxBytes total
// ---------------------------------------------------------------------------

func TestDeserializeRejectsOversizedTx(t *testing.T) {
	data := make([]byte, tx.MaxTxBytes+1)
	if _, err := tx.Deserialize(data); err == nil {
		t.Fatalf("expected error for oversized tx, got nil")
	}
}

// ---------------------------------------------------------------------------
// 8. Block round-trip via harness
// ---------------------------------------------------------------------------

func TestBlockSerializeDeserializeRoundTrip(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	w := harness.NewWallet("ledger-block-rt")
	b := harness.MineBlock(t, c, w, nil)

	raw := b.Serialize()
	got, err := block.DeserializeBlock(raw)
	if err != nil {
		t.Fatalf("DeserializeBlock: %v", err)
	}
	if got.Header.ID() != b.Header.ID() {
		t.Fatalf("header ID mismatch after round-trip")
	}
	if len(got.Txs) != len(b.Txs) {
		t.Fatalf("tx count mismatch: got %d want %d", len(got.Txs), len(b.Txs))
	}
	for i := range b.Txs {
		if got.Txs[i].Hash() != b.Txs[i].Hash() {
			t.Fatalf("tx[%d] hash mismatch after round-trip", i)
		}
	}
}

// ---------------------------------------------------------------------------
// 9. DeserializeBlock rejects oversized / truncated / absurd tx count
// ---------------------------------------------------------------------------

func TestDeserializeBlockRejectsOversized(t *testing.T) {
	// 2_000_001 bytes (MaxBlockBytes is 2_000_000).
	data := make([]byte, 2_000_001)
	if _, err := block.DeserializeBlock(data); err == nil {
		t.Fatalf("expected error for oversized block, got nil")
	}
}

func TestDeserializeBlockRejectsTruncated(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	w := harness.NewWallet("ledger-block-trunc")
	b := harness.MineBlock(t, c, w, nil)
	raw := b.Serialize()
	if _, err := block.DeserializeBlock(raw[:len(raw)-5]); err == nil {
		t.Fatalf("expected error for truncated block, got nil")
	}
}

func TestDeserializeBlockRejectsAbsurdTxCount(t *testing.T) {
	// Craft a minimal valid header followed by an enormous tx count.
	h := &block.Header{Version: 1, Height: 1}
	hb := h.Serialize()

	var buf bytes.Buffer
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(hb)))
	buf.Write(l[:])
	buf.Write(hb)
	// tx count far larger than remaining bytes -> must be rejected.
	var u8 [8]byte
	binary.BigEndian.PutUint64(u8[:], ^uint64(0))
	buf.Write(u8[:])

	if _, err := block.DeserializeBlock(buf.Bytes()); err == nil {
		t.Fatalf("expected error for absurd tx count, got nil")
	}
}

// ---------------------------------------------------------------------------
// 10. Header round-trip; ID deterministic
// ---------------------------------------------------------------------------

func TestHeaderSerializeParseRoundTrip(t *testing.T) {
	h := &block.Header{
		Version:    1,
		Height:     42,
		PrevHash:   [32]byte{1, 2, 3},
		Timestamp:  1_700_000_000,
		Difficulty: 5,
		Nonce:      123456,
		MerkleRoot: [32]byte{9, 8, 7},
		AccValue:   bytes.Repeat([]byte{0xAB}, 48),
		AccSize:    17,
	}
	raw := h.Serialize()
	got, err := block.ParseHeader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}
	if got.ID() != h.ID() {
		t.Fatalf("header ID changed across round-trip")
	}
	if !bytes.Equal(got.Serialize(), raw) {
		t.Fatalf("re-serialized header differs")
	}
	// Determinism: same header -> same ID twice.
	if h.ID() != h.ID() {
		t.Fatalf("header ID not deterministic")
	}
}

// ---------------------------------------------------------------------------
// 11. MerkleRoot determinism and set-sensitivity
// ---------------------------------------------------------------------------

func TestMerkleRootDeterministicAndDistinct(t *testing.T) {
	a := sampleTx()
	b := sampleTx()
	b.Fee = 4242 // distinct hash

	set1 := []*tx.Transaction{a, b}
	if block.MerkleRoot(set1) != block.MerkleRoot(set1) {
		t.Fatalf("MerkleRoot not deterministic for the same set")
	}

	set2 := []*tx.Transaction{a} // different set
	if block.MerkleRoot(set1) == block.MerkleRoot(set2) {
		t.Fatalf("different tx sets produced the same merkle root")
	}

	// Ordering matters too.
	set3 := []*tx.Transaction{b, a}
	if block.MerkleRoot(set1) == block.MerkleRoot(set3) {
		t.Fatalf("reordered tx set produced the same merkle root")
	}
}

// ---------------------------------------------------------------------------
// 12. Merkle leaf/internal domain separation
// ---------------------------------------------------------------------------

func TestMerkleRootLeafDomainSeparation(t *testing.T) {
	a := sampleTx()
	root := block.MerkleRoot([]*tx.Transaction{a})
	raw := a.Hash()
	if root == raw {
		t.Fatalf("single-tx merkle root equals raw tx hash (no leaf tagging)")
	}
}

// ---------------------------------------------------------------------------
// 13. PoWHash determinism and nonce-sensitivity
// ---------------------------------------------------------------------------

func TestPoWHashNonceSensitivity(t *testing.T) {
	h := block.Header{
		Version:    1,
		Height:     1,
		MerkleRoot: [32]byte{5, 5, 5},
		AccValue:   bytes.Repeat([]byte{0x01}, 16),
		AccSize:    1,
		Nonce:      1000,
	}
	h1 := h.PoWHash()
	if h.PoWHash() != h1 {
		t.Fatalf("PoWHash not deterministic for the same nonce")
	}
	h.Nonce = 1001
	if h.PoWHash() == h1 {
		t.Fatalf("PoWHash unchanged when nonce changed")
	}
}

// ---------------------------------------------------------------------------
// 14. PoW preimage binds AccValue length (field-boundary safety)
// ---------------------------------------------------------------------------

func TestPoWPreimageBindsAccValueLength(t *testing.T) {
	// Two headers where the concatenation (MerkleRoot || AccValue) would be
	// identical if the AccValue length were not bound. The length-prefix must
	// keep their PoW hashes distinct.
	mr := [32]byte{}
	for i := range mr {
		mr[i] = byte(i)
	}

	h1 := block.Header{MerkleRoot: mr, AccValue: []byte{0xAA, 0xBB}, AccSize: 1, Nonce: 7}

	// Shift one byte from MerkleRoot tail into AccValue head: same overall byte
	// stream if length weren't prefixed.
	var mr2 [32]byte
	copy(mr2[:], mr[:31])
	mr2[31] = 0x00 // changed last MerkleRoot byte
	h2 := block.Header{
		MerkleRoot: mr2,
		AccValue:   []byte{mr[31], 0xAA, 0xBB}, // absorbs the displaced byte
		AccSize:    1,
		Nonce:      7,
	}

	if h1.PoWHash() == h2.PoWHash() {
		t.Fatalf("PoWHash collided across different (MerkleRoot,AccValue) splits")
	}
}

// ---------------------------------------------------------------------------
// 15. Target / Meets behaviour
// ---------------------------------------------------------------------------

func TestPoWTargetAndMeets(t *testing.T) {
	t1 := pow.Target(1)
	t100 := pow.Target(100)
	if t100.Cmp(t1) >= 0 {
		t.Fatalf("higher difficulty did not yield a smaller target: t1=%s t100=%s", t1, t100)
	}

	// A hash equal to (target for diff=2) must meet difficulty 2.
	target2 := pow.Target(2)
	var hb [32]byte
	target2.FillBytes(hb[:])
	if !pow.Meets(hb, 2) {
		t.Fatalf("hash == target should meet difficulty (Meets is <=)")
	}

	// difficulty 1 has the maximal target; any hash meets it.
	allOnes := [32]byte{}
	for i := range allOnes {
		allOnes[i] = 0xFF
	}
	if !pow.Meets(allOnes, 1) {
		t.Fatalf("difficulty 1 should be met by any hash (max target)")
	}

	// A hash one above target2 must NOT meet difficulty 2.
	over := new(big.Int).Add(target2, big.NewInt(1))
	if over.BitLen() <= 256 { // guard: stays in range
		var ob [32]byte
		over.FillBytes(ob[:])
		if pow.Meets(ob, 2) {
			t.Fatalf("hash above target should not meet difficulty")
		}
	}
}

// ---------------------------------------------------------------------------
// 16. HashDifficulty sanity / monotonicity
// ---------------------------------------------------------------------------

func TestHashDifficultySanity(t *testing.T) {
	// A very large hash value (all 0xFF) achieves difficulty ~1 (lowest).
	high := [32]byte{}
	for i := range high {
		high[i] = 0xFF
	}
	dHigh := pow.HashDifficulty(high)
	if dHigh != 1 {
		t.Fatalf("all-0xFF hash should give difficulty 1, got %d", dHigh)
	}

	// A smaller hash value achieves higher difficulty (monotonic: lower value ->
	// higher difficulty).
	var low [32]byte
	low[0] = 0x00
	low[1] = 0x00
	low[2] = 0x01 // very small value -> very high difficulty
	dLow := pow.HashDifficulty(low)
	if dLow <= dHigh {
		t.Fatalf("smaller hash value should give higher difficulty: dLow=%d dHigh=%d", dLow, dHigh)
	}

	// Zero hash -> max difficulty sentinel.
	if pow.HashDifficulty([32]byte{}) != ^uint64(0) {
		t.Fatalf("zero hash should yield max difficulty sentinel")
	}
}

// ---------------------------------------------------------------------------
// 17. RangeProof round-trip then VerifyRange passes
// ---------------------------------------------------------------------------

func TestRangeProofRoundTripVerify(t *testing.T) {
	C, _, proof, err := commit.ProveRange(123456789)
	if err != nil {
		t.Fatalf("ProveRange: %v", err)
	}
	raw := proof.Serialize()
	parsed, err := commit.ParseRangeProof(raw)
	if err != nil {
		t.Fatalf("ParseRangeProof: %v", err)
	}
	if !commit.VerifyRange(C, parsed) {
		t.Fatalf("VerifyRange failed on round-tripped proof")
	}
	// Verifying against the wrong commitment must fail.
	wrong := edwards25519.NewIdentityPoint()
	if commit.VerifyRange(wrong, parsed) {
		t.Fatalf("VerifyRange passed against the wrong commitment")
	}
}

// ---------------------------------------------------------------------------
// 18. Multi-input/output tx round-trips with a stable hash
// ---------------------------------------------------------------------------

func TestMultiIOTxStableHash(t *testing.T) {
	m := sampleTx()
	for i := 0; i < 3; i++ {
		m.Inputs = append(m.Inputs, tx.Input{
			OutputRef:        bytes.Repeat([]byte{byte(0x10 + i)}, 32),
			OwnershipProof:   bytes.Repeat([]byte{byte(0x20 + i)}, 64),
			PseudoCommitment: bytes.Repeat([]byte{byte(0x30 + i)}, 32),
			EqualityProof:    bytes.Repeat([]byte{byte(0x40 + i)}, 64),
		})
		m.Outputs = append(m.Outputs, tx.Output{
			OneTimeKey: bytes.Repeat([]byte{byte(0x50 + i)}, 32),
			TxPubKey:   bytes.Repeat([]byte{byte(0x60 + i)}, 32),
			Commitment: bytes.Repeat([]byte{byte(0x70 + i)}, 32),
			RangeProof: bytes.Repeat([]byte{byte(0x80 + i)}, 50),
			PrimeNonce: uint64(i),
			EncAmount:  bytes.Repeat([]byte{byte(0x90 + i)}, 8),
			EncMask:    bytes.Repeat([]byte{byte(0xA0 + i)}, 8),
		})
	}
	h1 := m.Hash()
	got, err := tx.Deserialize(m.Serialize())
	if err != nil {
		t.Fatalf("Deserialize: %v", err)
	}
	if got.Hash() != h1 {
		t.Fatalf("multi-IO tx hash changed across round-trip")
	}
	// Re-serialize the parsed tx; hash must remain stable.
	if got.Hash() != got.Hash() {
		t.Fatalf("hash not stable across repeated calls")
	}
	if len(got.Inputs) != 4 || len(got.Outputs) != 4 {
		t.Fatalf("input/output counts not preserved: %d/%d", len(got.Inputs), len(got.Outputs))
	}
}

// ---------------------------------------------------------------------------
// 19. Identical zero/empty-value txs serialize deterministically
// ---------------------------------------------------------------------------

func TestZeroValueTxDeterministic(t *testing.T) {
	a := &tx.Transaction{Version: 1}
	b := &tx.Transaction{Version: 1}
	if !bytes.Equal(a.Serialize(), b.Serialize()) {
		t.Fatalf("two identical zero-value txs serialized differently")
	}
	if a.Hash() != b.Hash() {
		t.Fatalf("two identical zero-value txs hashed differently")
	}
	// A round-trip of the empty tx is also stable.
	got, err := tx.Deserialize(a.Serialize())
	if err != nil {
		t.Fatalf("Deserialize empty tx: %v", err)
	}
	if got.Hash() != a.Hash() {
		t.Fatalf("empty tx hash changed across round-trip")
	}
}

// ---------------------------------------------------------------------------
// 20. MerkleRoot of a mined block matches the header MerkleRoot
// ---------------------------------------------------------------------------

func TestMinedBlockMerkleRootMatchesHeader(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	w := harness.NewWallet("ledger-merkle-match")
	b := harness.MineBlock(t, c, w, nil)

	computed := block.MerkleRoot(b.Txs)
	if computed != b.Header.MerkleRoot {
		t.Fatalf("computed merkle root %x != header merkle root %x",
			computed, b.Header.MerkleRoot)
	}
}
