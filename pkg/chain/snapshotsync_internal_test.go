package chain

import (
	"bytes"
	"context"
	"encoding/gob"
	"testing"

	"obscura/pkg/config"
	"obscura/pkg/miner"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
)

// mineCoinbaseBlocks mines n coinbase-only blocks (real PoW) on c.
func mineCoinbaseBlocks(t *testing.T, c *Chain, w *wallet.Wallet, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		minted := c.ExpectedCoinbaseMinted(0, nil)
		cb, err := w.BuildCoinbase(c.Height()+1, minted, nil)
		if err != nil {
			t.Fatalf("coinbase: %v", err)
		}
		tmpl, err := c.BlockTemplate([]*tx.Transaction{cb})
		if err != nil {
			t.Fatalf("template: %v", err)
		}
		if !miner.Mine(context.Background(), tmpl, 0) {
			t.Fatal("mine failed")
		}
		if err := c.AddBlock(tmpl); err != nil {
			t.Fatalf("addblock height %d: %v", tmpl.Header.Height, err)
		}
	}
}

func snapshotBytes(t *testing.T, c *Chain) []byte {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	data, err := c.encodeSnapshotLocked()
	if err != nil {
		t.Fatalf("encode snapshot: %v", err)
	}
	return data
}

func decodeSnap(t *testing.T, data []byte) *chainSnapshot {
	t.Helper()
	var s chainSnapshot
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&s); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	return &s
}

func reencode(t *testing.T, s *chainSnapshot) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(s); err != nil {
		t.Fatalf("reencode snapshot: %v", err)
	}
	return buf.Bytes()
}

// TestSnapshotAuthenticity proves VerifySnapshotAuthenticity accepts an authentic snapshot and
// REJECTS every forgery a malicious peer could attempt: substituted (valid-but-wrong-height)
// accumulator state, fake PoW, a foreign/tampered genesis, and a malformed header chain — and
// that verification is read-only (the verifier never adopts the state). This is the safety gate
// for the snapshot-sync verification core (audit: fresh-node bootstrap past PoRWindow).
func TestSnapshotAuthenticity(t *testing.T) {
	old := config.CoinbaseMaturity
	config.CoinbaseMaturity = 1
	defer func() { config.CoinbaseMaturity = old }()

	// Source chain: mine to height 5, snapshot; mine one more, snapshot again. The two
	// snapshots give us a VALID accumulator state (snap5) that is WRONG for snap6's tip.
	src, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new src: %v", err)
	}
	defer src.Close()
	w := wallet.FromSeed([]byte("snap-seed-000000000000000000000000"))

	mineCoinbaseBlocks(t, src, w, 5)
	snap5 := decodeSnap(t, snapshotBytes(t, src))
	mineCoinbaseBlocks(t, src, w, 1)
	good := snapshotBytes(t, src)

	// Fresh verifier node sharing OUR genesis (same config => same genesis).
	ver, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new ver: %v", err)
	}
	defer ver.Close()

	// (1) authentic snapshot verifies.
	if err := ver.VerifySnapshotAuthenticity(good); err != nil {
		t.Fatalf("authentic snapshot must verify, got: %v", err)
	}

	// (2) substituted accumulator state (valid bytes, wrong root) -> AccValue mismatch.
	{
		s := decodeSnap(t, good)
		s.Acc = snap5.Acc // a perfectly valid accumulator state, but for height 5 not 6
		if err := ver.VerifySnapshotAuthenticity(reencode(t, s)); err == nil {
			t.Fatal("substituted accumulator state must be rejected (AccValue mismatch)")
		}
	}

	// NOTE: the NullRoot/PQAccRoot/CMRoot checks are the same bytes.Equal-against-tip pattern
	// as AccValue (case 2), but those accumulators are EMPTY on a coinbase-only chain (no
	// spends/PQ/ZK), so a state substitution there is a genuine no-op and cannot be exercised
	// without a PQ/ZK spend. Case (2) proves the root-mismatch rejection mechanism.

	// (4) fake PoW: zero the tip header's nonce -> PoW no longer meets target.
	{
		s := decodeSnap(t, good)
		s.Headers[len(s.Headers)-1].Nonce = 0
		if err := ver.VerifySnapshotAuthenticity(reencode(t, s)); err == nil {
			t.Fatal("fake-PoW tip header must be rejected")
		}
	}

	// (5) foreign/tampered genesis -> genesis-binding rejects (ID mismatch).
	{
		s := decodeSnap(t, good)
		s.Headers[0].Timestamp ^= 0x1
		if err := ver.VerifySnapshotAuthenticity(reencode(t, s)); err == nil {
			t.Fatal("tampered genesis must be rejected (foreign network)")
		}
	}

	// (6) empty header chain -> rejected.
	{
		s := decodeSnap(t, good)
		s.Headers = nil
		if err := ver.VerifySnapshotAuthenticity(reencode(t, s)); err == nil {
			t.Fatal("empty header chain must be rejected")
		}
	}

	// (7) verification is read-only: the verifier never adopted any state.
	if ver.Height() != 0 {
		t.Fatalf("verifier must remain at genesis (read-only), got height %d", ver.Height())
	}
}
