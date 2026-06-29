package chain

import (
	"bytes"
	"encoding/gob"
	"testing"

	"obscura/pkg/config"
	"obscura/pkg/wallet"
)

func decodeTransfer(t *testing.T, data []byte) *transferSnapshot {
	t.Helper()
	var ts transferSnapshot
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&ts); err != nil {
		t.Fatalf("decode transfer: %v", err)
	}
	return &ts
}

func encodeTransfer(t *testing.T, ts *transferSnapshot) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(ts); err != nil {
		t.Fatalf("encode transfer: %v", err)
	}
	return buf.Bytes()
}

// buildSource mines a coinbase-only chain with frequent snapshots and returns the source chain
// plus a valid transfer snapshot and its state height H.
func buildSource(t *testing.T) (*Chain, []byte, uint64) {
	t.Helper()
	oldI, oldM := SnapshotInterval, config.CoinbaseMaturity
	SnapshotInterval, config.CoinbaseMaturity = 3, 1
	t.Cleanup(func() { SnapshotInterval, config.CoinbaseMaturity = oldI, oldM })

	src, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new src: %v", err)
	}
	w := wallet.FromSeed([]byte("snap-import-seed-0000000000000000"))
	mineCoinbaseBlocks(t, src, w, 8) // snapshots saved at 3 and 6; tip = 8

	src.mu.Lock()
	data, h, err := src.encodeTransferSnapshotLocked()
	src.mu.Unlock()
	if err != nil {
		t.Fatalf("encodeTransferSnapshot: %v", err)
	}
	if h == 0 {
		t.Fatalf("transfer state height = 0")
	}
	return src, data, h
}

// TestSnapshotImportPositive: a FRESH node verifies + imports a real transfer snapshot, lands at
// the right height, and can validate + APPLY the next real block on top (proving the import is a
// faithful, consensus-valid state — block validation re-checks every root incl. the StateRoot).
func TestSnapshotImportPositive(t *testing.T) {
	src, data, h := buildSource(t)
	defer src.Close()

	fresh, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new fresh: %v", err)
	}
	defer fresh.Close()

	gotH, err := fresh.VerifyAndImportSnapshot(data)
	if err != nil {
		t.Fatalf("import must succeed, got: %v", err)
	}
	if gotH != h || fresh.Height() != h {
		t.Fatalf("imported height %d/%d, want %d", gotH, fresh.Height(), h)
	}
	// the next real block (coinbase-only) from the source must validate + apply on the imported
	// node — this exercises the post-state roots AND the residual StateRoot against live state.
	nb, ok := src.BlockByHeight(h + 1)
	if !ok {
		t.Fatalf("source missing block %d", h+1)
	}
	if err := fresh.AddBlock(nb); err != nil {
		t.Fatalf("imported node must apply block %d: %v", h+1, err)
	}
	if fresh.Height() != h+1 {
		t.Fatalf("after apply height = %d, want %d", fresh.Height(), h+1)
	}
}

// TestSnapshotImportRejects: every forgery a malicious peer could attempt is rejected, and a
// rejected import never mutates the fresh node (it stays at genesis).
func TestSnapshotImportRejects(t *testing.T) {
	src, good, _ := buildSource(t)
	defer src.Close()

	fresh, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new fresh: %v", err)
	}
	defer fresh.Close()

	mustReject := func(name string, mutate func(ts *transferSnapshot)) {
		ts := decodeTransfer(t, good)
		mutate(ts)
		if _, err := fresh.VerifyAndImportSnapshot(encodeTransfer(t, ts)); err == nil {
			t.Fatalf("%s: must be rejected", name)
		}
		if fresh.Height() != 0 {
			t.Fatalf("%s: fresh node mutated on reject (height %d)", name, fresh.Height())
		}
	}

	// tampered residual scalar (emitted) inside the state blob -> residual state-root mismatch.
	mustReject("tampered emitted", func(ts *transferSnapshot) {
		s := decodeSnap(t, ts.State)
		s.Emitted += 1
		ts.State = reencode(t, s)
	})
	// tampered pqUtxo would also be caught; emitted covers the scalar path. Now a disk-set member.
	mustReject("tampered outPrime member", func(ts *transferSnapshot) {
		if len(ts.OutPrimeMembers) == 0 {
			t.Fatal("expected non-empty outPrime members (coinbase outputs)")
		}
		ts.OutPrimeMembers[0] = append([]byte(nil), ts.OutPrimeMembers[0]...)
		ts.OutPrimeMembers[0][0] ^= 0xff
	})
	// dropped disk-set member -> count mismatch / commitment mismatch.
	mustReject("dropped member", func(ts *transferSnapshot) {
		if len(ts.OutPrimeMembers) > 0 {
			ts.OutPrimeMembers = ts.OutPrimeMembers[1:]
		}
	})
	// fake PoW: zero a header nonce.
	mustReject("fake PoW header", func(ts *transferSnapshot) {
		ts.Headers[len(ts.Headers)-1].Nonce = 0
	})
	// foreign / tampered genesis.
	mustReject("foreign genesis", func(ts *transferSnapshot) {
		ts.Headers[0].Timestamp ^= 0x1
	})
	// missing the H+1 header (truncate headers to the state height) -> pre-state check impossible.
	mustReject("no child header", func(ts *transferSnapshot) {
		s := decodeSnap(t, ts.State)
		if uint64(len(ts.Headers)) > s.Height+1 {
			ts.Headers = ts.Headers[:s.Height+1] // 0..H only, no H+1
		}
	})

	// after all rejects, a clean import still works (rejects left no residue).
	if _, err := fresh.VerifyAndImportSnapshot(good); err != nil {
		t.Fatalf("clean import after rejects must succeed, got: %v", err)
	}
}
