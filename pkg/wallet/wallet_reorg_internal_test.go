package wallet

import (
	"bytes"
	"testing"

	"filippo.io/edwards25519"

	"obscura/pkg/stark"
	"obscura/pkg/tx"
)

func otk(b byte) []byte { k := make([]byte, 32); k[0] = b; return k }

// TestScanBlockUndo proves a reorg rolls back orphaned spends + receipts (audit #19):
// an output marked Spent in an orphaned block is un-spent, an output received in an orphaned
// block is removed, and a sent payment mined only in the orphaned suffix is un-confirmed.
func TestScanBlockUndo(t *testing.T) {
	w := &Wallet{
		Outputs: []*OwnedOutput{
			{Out: tx.Output{OneTimeKey: otk(1)}, Amount: 100, Height: 5},                            // kept, unspent
			{Out: tx.Output{OneTimeKey: otk(2)}, Amount: 200, Height: 5, Spent: true, SpentHeight: 8}, // spend orphaned
			{Out: tx.Output{OneTimeKey: otk(3)}, Amount: 300, Height: 9},                            // received in orphaned block
		},
		Sent:        []*SentTx{{TxID: "x", Height: 8}},
		lastScanned: 10,
	}
	if got := w.Balance(); got != 400 { // A(100) + C(300); B is spent
		t.Fatalf("pre-undo balance = %d, want 400", got)
	}

	w.ScanBlockUndo(7) // reorg forks at height 7

	if len(w.Outputs) != 2 {
		t.Fatalf("after undo: %d outputs, want 2 (C removed)", len(w.Outputs))
	}
	if got := w.Balance(); got != 300 { // A(100) + B(200, now un-spent); C removed
		t.Fatalf("post-undo balance = %d, want 300", got)
	}
	for _, o := range w.Outputs {
		if o.Spent || o.SpentHeight != 0 {
			t.Fatalf("output %x still marked spent after undo", o.Out.OneTimeKey[:1])
		}
	}
	if w.Sent[0].Height != 0 {
		t.Fatal("orphaned sent payment must be un-confirmed (Height=0)")
	}
	if w.lastScanned != 6 {
		t.Fatalf("lastScanned = %d, want 6", w.lastScanned)
	}
}

// TestSpentHeightRoundTrip proves SpentHeight survives MarshalState/RestoreState (so reorg
// undo still works after the wallet is persisted + reloaded) and that the section is optional.
func TestSpentHeightRoundTrip(t *testing.T) {
	z := new(edwards25519.Scalar) // zero scalar — valid canonical, .Bytes() = 32 zero bytes
	w := &Wallet{
		Outputs: []*OwnedOutput{
			{Out: tx.Output{OneTimeKey: otk(1), Commitment: otk(9)}, Amount: 100, Mask: z, Height: 5, Spent: true, SpentHeight: 8},
			{Out: tx.Output{OneTimeKey: otk(2), Commitment: otk(8)}, Amount: 200, Mask: z, Height: 6},
		},
	}
	w2 := &Wallet{}
	if err := w2.RestoreState(w.MarshalState()); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(w2.Outputs) != 2 {
		t.Fatalf("restored %d outputs, want 2", len(w2.Outputs))
	}
	if w2.Outputs[0].SpentHeight != 8 {
		t.Fatalf("SpentHeight not persisted: got %d want 8", w2.Outputs[0].SpentHeight)
	}
	if w2.Outputs[1].SpentHeight != 0 {
		t.Fatalf("unspent output SpentHeight got %d want 0", w2.Outputs[1].SpentHeight)
	}
}

// TestZKCoinRoundTrip proves ZK note secret material (rho, blind, nsk, leaf, amount)
// survives MarshalState/RestoreState, so a minted/received coin stays spendable after a
// wallet reload. The section is optional: a state with no ZK coins still restores cleanly.
func TestZKCoinRoundTrip(t *testing.T) {
	leaf := make([]byte, 32)
	for i := range leaf {
		leaf[i] = byte(i + 1)
	}
	coin := &ZKCoin{
		Amount: 7_000_000_000,
		Rho:    stark.NewFelt(0x1122334455667788),
		Blind:  stark.NewFelt(0x99aabbccddeeff00),
		Nsk:    stark.NewFelt(0xdeadbeefcafef00d),
		Leaf:   leaf,
	}
	w := &Wallet{}
	w.AddZKCoin(coin)
	// AddZKCoin must de-dup by leaf (re-scan safety).
	w.AddZKCoin(coin)
	if len(w.ZKCoins()) != 1 {
		t.Fatalf("AddZKCoin did not de-dup: %d coins", len(w.ZKCoins()))
	}

	w2 := &Wallet{}
	if err := w2.RestoreState(w.MarshalState()); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got := w2.ZKCoins()
	if len(got) != 1 {
		t.Fatalf("restored %d ZK coins, want 1", len(got))
	}
	g := got[0]
	if g.Amount != coin.Amount || g.Rho != coin.Rho || g.Blind != coin.Blind || g.Nsk != coin.Nsk {
		t.Fatalf("ZK coin secrets not preserved: got %+v want %+v", g, coin)
	}
	if !bytes.Equal(g.Leaf, coin.Leaf) {
		t.Fatalf("ZK coin leaf not preserved: got %x want %x", g.Leaf, coin.Leaf)
	}

	// A state with NO ZK coins still restores cleanly (older-file compatibility).
	empty := &Wallet{}
	if err := (&Wallet{}).RestoreState(empty.MarshalState()); err != nil {
		t.Fatalf("restore empty: %v", err)
	}
}
