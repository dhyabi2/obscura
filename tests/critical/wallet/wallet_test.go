// Package wallet contains critical-workflow tests for the Obscura wallet,
// mempool, and economics paths. These exercise the public APIs end-to-end:
// key derivation, chain scanning, confidential transaction construction, and
// mempool admission/conflict handling.
package wallet

import (
	"bytes"
	"testing"

	"obscura/pkg/block"
	"obscura/pkg/commit"
	"obscura/pkg/config"
	"obscura/pkg/mempool"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"

	"obscura/tests/critical/harness"
)

// defaultFee is a comfortably-above-minimum fee for two-input/two-output txs.
const defaultFee = uint64(1_000_000_000)

// ---------------------------------------------------------------------------
// 1. Deterministic key derivation
// ---------------------------------------------------------------------------

func TestFromSeedDeterministic(t *testing.T) {
	seed := []byte("seed-one-padpadpadpadpadpadpadpadpad")
	w1 := wallet.FromSeed(seed)
	w2 := wallet.FromSeed(seed)
	if !bytes.Equal(w1.AddressBytes(), w2.AddressBytes()) {
		t.Fatalf("same seed produced different addresses")
	}

	w3 := wallet.FromSeed([]byte("seed-two-padpadpadpadpadpadpadpadpad"))
	if bytes.Equal(w1.AddressBytes(), w3.AddressBytes()) {
		t.Fatalf("different seeds produced identical addresses")
	}

	// New() should produce a usable, distinct wallet. The encoded address is 96 bytes:
	// A(32) || B(32) || NfPk(32), the recipient nullifier-key added for the unlinkable
	// recipient-secret spend (#96); it was 64 (A||B) before that landed.
	n := wallet.New()
	if len(n.AddressBytes()) != 96 {
		t.Fatalf("New() address length = %d, want 96", len(n.AddressBytes()))
	}
	if bytes.Equal(n.AddressBytes(), w1.AddressBytes()) {
		t.Fatalf("random New() wallet collided with seeded wallet (astronomically unlikely)")
	}
}

// ---------------------------------------------------------------------------
// 2. Address encode / round-trip
// ---------------------------------------------------------------------------

func TestAddressRoundTrip(t *testing.T) {
	w := harness.NewWallet("roundtrip")
	enc := w.AddressBytes()
	if len(enc) != 96 { // A(32) || B(32) || NfPk(32) since #96 (was 64)
		t.Fatalf("encoded address length = %d, want 96", len(enc))
	}
	dec, err := commit.DecodeAddress(enc)
	if err != nil {
		t.Fatalf("DecodeAddress: %v", err)
	}
	if !bytes.Equal(dec.Encode(), enc) {
		t.Fatalf("round-trip mismatch")
	}
	addr := w.Address()
	if dec.A.Equal(addr.A) != 1 || dec.B.Equal(addr.B) != 1 {
		t.Fatalf("decoded points differ from wallet address")
	}
}

// ---------------------------------------------------------------------------
// 3. Scan detects an owned coinbase output
// ---------------------------------------------------------------------------

func TestScanDetectsOwnedCoinbase(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	harness.Funded(t, c, alice, 3)

	if alice.Balance() == 0 {
		t.Fatalf("funded wallet has zero balance")
	}
	if len(alice.Outputs) == 0 {
		t.Fatalf("funded wallet has no outputs")
	}
	for _, o := range alice.Outputs {
		if !o.IsCoinbase {
			t.Fatalf("expected coinbase output, got non-coinbase")
		}
	}
}

// ---------------------------------------------------------------------------
// 4. Scan does NOT claim another wallet's outputs
// ---------------------------------------------------------------------------

func TestScanIgnoresForeignOutputs(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	harness.Funded(t, c, alice, 3)

	bob := harness.NewWallet("bob")
	harness.ScanAll(c, bob)
	if bob.Balance() != 0 {
		t.Fatalf("bob saw balance %d for alice's coinbase outputs", bob.Balance())
	}
	if len(bob.Outputs) != 0 {
		t.Fatalf("bob claimed %d foreign outputs", len(bob.Outputs))
	}
}

// ---------------------------------------------------------------------------
// 5. Scan marks an output Spent after it is spent in a mined block
// ---------------------------------------------------------------------------

func TestScanMarksSpent(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	harness.Funded(t, c, alice, 3)
	bob := harness.NewWallet("bob")

	before := alice.Balance()
	if before == 0 {
		t.Fatalf("alice unfunded")
	}
	txn, err := alice.CreateTransaction(c, bob.Address(), before/4, defaultFee)
	if err != nil {
		t.Fatalf("CreateTransaction: %v", err)
	}
	// record which refs are being spent
	spent := map[string]bool{}
	for _, in := range txn.Inputs {
		spent[string(in.OutputRef)] = true
	}

	harness.MineBlock(t, c, harness.NewWallet("miner"), []*tx.Transaction{txn})
	harness.ScanAll(c, alice)

	var sawSpent bool
	for _, o := range alice.Outputs {
		if spent[string(o.Out.OneTimeKey)] {
			if !o.Spent {
				t.Fatalf("input output was not marked spent after inclusion")
			}
			sawSpent = true
		}
	}
	if !sawSpent {
		t.Fatalf("none of the spent inputs were found among alice's outputs")
	}
}

// ---------------------------------------------------------------------------
// 6. Balance equals sum of unspent owned outputs
// ---------------------------------------------------------------------------

func TestBalanceIsSumOfUnspent(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	harness.Funded(t, c, alice, 4)

	var sum uint64
	for _, o := range alice.Outputs {
		if !o.Spent {
			sum += o.Amount
		}
	}
	if sum != alice.Balance() {
		t.Fatalf("Balance()=%d but sum of unspent=%d", alice.Balance(), sum)
	}
}

// ---------------------------------------------------------------------------
// 7. Send happy path
// ---------------------------------------------------------------------------

func TestSendHappyPath(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	harness.Funded(t, c, alice, 4)
	bob := harness.NewWallet("bob")

	aliceBefore := alice.Balance()
	amount := aliceBefore / 4
	txn, err := alice.CreateTransaction(c, bob.Address(), amount, defaultFee)
	if err != nil {
		t.Fatalf("CreateTransaction: %v", err)
	}

	harness.MineBlock(t, c, harness.NewWallet("miner"), []*tx.Transaction{txn})

	// Bob receives the amount.
	harness.ScanAll(c, bob)
	if bob.Balance() != amount {
		t.Fatalf("bob balance = %d, want %d", bob.Balance(), amount)
	}

	// Alice's balance decreases by amount+fee (change returns to her).
	harness.ScanAll(c, alice)
	want := aliceBefore - amount - defaultFee
	if alice.Balance() != want {
		t.Fatalf("alice balance = %d, want %d (before=%d amount=%d fee=%d)",
			alice.Balance(), want, aliceBefore, amount, defaultFee)
	}
}

// ---------------------------------------------------------------------------
// 8. Change output: spend less than a single output's value
// ---------------------------------------------------------------------------

func TestChangeOutputConserves(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	// single mature coinbase => one big output
	harness.Funded(t, c, alice, 1)
	if len(alice.Outputs) != 1 {
		t.Fatalf("expected exactly 1 owned output, got %d", len(alice.Outputs))
	}
	single := alice.Outputs[0].Amount
	bob := harness.NewWallet("bob")

	amount := single / 10 // much less than the single output's value
	txn, err := alice.CreateTransaction(c, bob.Address(), amount, defaultFee)
	if err != nil {
		t.Fatalf("CreateTransaction: %v", err)
	}
	// destination + change => 2 outputs.
	if len(txn.Outputs) != 2 {
		t.Fatalf("expected 2 outputs (dest+change), got %d", len(txn.Outputs))
	}

	harness.MineBlock(t, c, harness.NewWallet("miner"), []*tx.Transaction{txn})

	bobScan := harness.NewWallet("bob")
	harness.ScanAll(c, bobScan)
	if bobScan.Balance() != amount {
		t.Fatalf("bob got %d, want %d", bobScan.Balance(), amount)
	}

	aliceScan := harness.NewWallet("alice")
	harness.ScanAll(c, aliceScan)
	change := single - amount - defaultFee
	if aliceScan.Balance() != change {
		t.Fatalf("alice change = %d, want %d", aliceScan.Balance(), change)
	}
	// Conservation: spent + change + amount + fee should balance.
	if amount+change+defaultFee != single {
		t.Fatalf("totals not conserved: %d != %d", amount+change+defaultFee, single)
	}
}

// ---------------------------------------------------------------------------
// 9. Insufficient funds
// ---------------------------------------------------------------------------

func TestInsufficientFunds(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	harness.Funded(t, c, alice, 2)
	bob := harness.NewWallet("bob")

	_, err := alice.CreateTransaction(c, bob.Address(), alice.Balance()+1, defaultFee)
	if err == nil {
		t.Fatalf("expected insufficient-funds error, got nil")
	}
}

// ---------------------------------------------------------------------------
// 10. Immature coinbase skipped in selection
// ---------------------------------------------------------------------------

func TestImmatureCoinbaseSkipped(t *testing.T) {
	// Deliberately use a HIGH maturity so a freshly-mined coinbase is immature.
	old := config.CoinbaseMaturity
	config.CoinbaseMaturity = 100
	defer func() { config.CoinbaseMaturity = old }()

	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	// mine a few blocks; none mature under maturity=100
	harness.MineN(t, c, alice, 3)
	harness.ScanAll(c, alice)

	if alice.Balance() == 0 {
		t.Fatalf("expected scanned (but immature) outputs to show in Balance()")
	}
	bob := harness.NewWallet("bob")
	_, err := alice.CreateTransaction(c, bob.Address(), 1, defaultFee)
	if err == nil {
		t.Fatalf("expected insufficient (immature) error, got nil")
	}
}

// ---------------------------------------------------------------------------
// 11. Recipient amount verification / corrupted EncAmount dropped
// ---------------------------------------------------------------------------

func TestCorruptedEncAmountDropped(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	harness.Funded(t, c, alice, 3)
	bob := harness.NewWallet("bob")

	amount := alice.Balance() / 4
	txn, err := alice.CreateTransaction(c, bob.Address(), amount, defaultFee)
	if err != nil {
		t.Fatalf("CreateTransaction: %v", err)
	}

	// Locate bob's destination output (the one bob owns) by scanning each output
	// individually in a synthetic block.
	destIdx := -1
	for i := range txn.Outputs {
		w := harness.NewWallet("bob")
		w.ScanBlock(synthBlock(txn.Outputs[i]))
		if w.Balance() > 0 {
			destIdx = i
			break
		}
	}
	if destIdx < 0 {
		t.Fatalf("could not locate bob's destination output")
	}

	// Sanity: a correctly-built destination output IS recorded, and the
	// commitment opens to (amount, mask).
	good := harness.NewWallet("bob")
	good.ScanBlock(synthBlock(txn.Outputs[destIdx]))
	if good.Balance() != amount {
		t.Fatalf("correctly-built output not recorded: bal=%d want=%d", good.Balance(), amount)
	}
	owned := good.Outputs[0]
	if !bytes.Equal(commit.Commit(owned.Amount, owned.Mask).Bytes(), owned.Out.Commitment) {
		t.Fatalf("Commit(amount,mask) != on-chain Commitment")
	}

	// Now corrupt the EncAmount: the recipient must NOT record it because the
	// decrypted (amount,mask) no longer open the on-chain commitment.
	corrupted := txn.Outputs[destIdx]
	corrupted.EncAmount = append([]byte(nil), corrupted.EncAmount...)
	corrupted.EncAmount[0] ^= 0xff
	victim := harness.NewWallet("bob")
	victim.ScanBlock(synthBlock(corrupted))
	if victim.Balance() != 0 {
		t.Fatalf("wallet recorded an output with inconsistent EncAmount (bal=%d)", victim.Balance())
	}
}

// synthBlock wraps a single output in a non-coinbase tx inside a block so a
// wallet can scan it directly (used to exercise the recipient claim/verify path
// in isolation).
func synthBlock(out tx.Output) *block.Block {
	return &block.Block{
		Header: block.Header{Height: 1},
		Txs:    []*tx.Transaction{{Version: 1, Outputs: []tx.Output{out}}},
	}
}

// ---------------------------------------------------------------------------
// 12. Reserved outputs not reselected
// ---------------------------------------------------------------------------

func TestReservedOutputsNotReselected(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	// single output so the second build has nothing left to select
	harness.Funded(t, c, alice, 1)
	if len(alice.Outputs) != 1 {
		t.Fatalf("expected 1 output, got %d", len(alice.Outputs))
	}
	bob := harness.NewWallet("bob")

	amount := alice.Outputs[0].Amount / 4
	tx1, err := alice.CreateTransaction(c, bob.Address(), amount, defaultFee)
	if err != nil {
		t.Fatalf("first CreateTransaction: %v", err)
	}
	_ = tx1

	// Second build must not reuse the now-reserved sole output => insufficient.
	_, err = alice.CreateTransaction(c, bob.Address(), amount, defaultFee)
	if err == nil {
		t.Fatalf("second build reused reserved output (expected insufficient)")
	}
}

// ---------------------------------------------------------------------------
// 13. amount=0 rejected
// ---------------------------------------------------------------------------

func TestZeroAmountRejected(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	harness.Funded(t, c, alice, 2)
	bob := harness.NewWallet("bob")

	_, err := alice.CreateTransaction(c, bob.Address(), 0, defaultFee)
	if err == nil {
		t.Fatalf("expected zero-amount rejection, got nil")
	}
}

// ---------------------------------------------------------------------------
// 14. amount+fee overflow rejected
// ---------------------------------------------------------------------------

func TestAmountFeeOverflowRejected(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	harness.Funded(t, c, alice, 2)
	bob := harness.NewWallet("bob")

	_, err := alice.CreateTransaction(c, bob.Address(), ^uint64(0), 1)
	if err == nil {
		t.Fatalf("expected overflow rejection, got nil")
	}
}

// ---------------------------------------------------------------------------
// 15. Mempool accepts a valid tx
// ---------------------------------------------------------------------------

func TestMempoolAcceptsValid(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	harness.Funded(t, c, alice, 4)
	bob := harness.NewWallet("bob")

	txn, err := alice.CreateTransaction(c, bob.Address(), alice.Balance()/4, defaultFee)
	if err != nil {
		t.Fatalf("CreateTransaction: %v", err)
	}
	mp := mempool.New(c)
	if err := mp.Add(txn); err != nil {
		t.Fatalf("mempool Add: %v", err)
	}
	if mp.Size() != 1 {
		t.Fatalf("mempool size = %d, want 1", mp.Size())
	}
}

// ---------------------------------------------------------------------------
// 16. Mempool rejects a coinbase tx
// ---------------------------------------------------------------------------

func TestMempoolRejectsCoinbase(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")

	cb, err := alice.BuildCoinbase(c.Height()+1, config.AtomicPerCoin, nil)
	if err != nil {
		t.Fatalf("BuildCoinbase: %v", err)
	}
	mp := mempool.New(c)
	if err := mp.Add(cb); err == nil {
		t.Fatalf("mempool accepted a coinbase tx")
	}
	if mp.Size() != 0 {
		t.Fatalf("mempool size = %d after rejecting coinbase", mp.Size())
	}
}

// ---------------------------------------------------------------------------
// 17. Mempool rejects a below-min-fee (fee 0) tx
// ---------------------------------------------------------------------------

func TestMempoolRejectsLowFee(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	harness.Funded(t, c, alice, 4)
	bob := harness.NewWallet("bob")

	txn, err := alice.CreateTransaction(c, bob.Address(), alice.Balance()/4, 0)
	if err != nil {
		t.Fatalf("CreateTransaction (fee 0): %v", err)
	}
	mp := mempool.New(c)
	if err := mp.Add(txn); err == nil {
		t.Fatalf("mempool accepted a fee-0 tx")
	}
}

// ---------------------------------------------------------------------------
// 18. Mempool rejects a pending double-spend
// ---------------------------------------------------------------------------

func TestMempoolRejectsPendingDoubleSpend(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	harness.Funded(t, c, alice, 4)
	bob := harness.NewWallet("bob")

	tx1, err := alice.CreateTransaction(c, bob.Address(), alice.Balance()/8, defaultFee)
	if err != nil {
		t.Fatalf("tx1: %v", err)
	}
	// Build a conflicting tx reusing tx1's first input ref. We forge a second tx
	// from scratch by reaching into a fresh wallet scan that still considers the
	// output unspent (reserved is local-only), so it can produce a conflicting
	// OutputRef.
	alice2 := harness.NewWallet("alice")
	harness.ScanAll(c, alice2)
	tx2, err := alice2.CreateTransaction(c, bob.Address(), alice2.Balance()/8, defaultFee)
	if err != nil {
		t.Fatalf("tx2: %v", err)
	}

	mp := mempool.New(c)
	if err := mp.Add(tx1); err != nil {
		t.Fatalf("add tx1: %v", err)
	}
	// tx2 shares at least one OutputRef with tx1 (same greedy selection order).
	if !sharesInput(tx1, tx2) {
		t.Skip("tx2 selected disjoint inputs; cannot test conflict deterministically")
	}
	if err := mp.Add(tx2); err == nil {
		t.Fatalf("mempool accepted a conflicting pending double-spend")
	}
}

// ---------------------------------------------------------------------------
// 19. Mempool rejects a tx whose output is already spent on-chain
// ---------------------------------------------------------------------------

func TestMempoolRejectsConfirmedSpend(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	harness.Funded(t, c, alice, 4)
	bob := harness.NewWallet("bob")

	tx1, err := alice.CreateTransaction(c, bob.Address(), alice.Balance()/8, defaultFee)
	if err != nil {
		t.Fatalf("tx1: %v", err)
	}
	// Build a conflicting tx2 from a fresh scan (sees inputs as unspent).
	alice2 := harness.NewWallet("alice")
	harness.ScanAll(c, alice2)
	tx2, err := alice2.CreateTransaction(c, bob.Address(), alice2.Balance()/8, defaultFee)
	if err != nil {
		t.Fatalf("tx2: %v", err)
	}
	if !sharesInput(tx1, tx2) {
		t.Skip("tx2 selected disjoint inputs; cannot test confirmed-spend conflict")
	}

	// Confirm tx1 on-chain, spending the shared output.
	harness.MineBlock(t, c, harness.NewWallet("miner"), []*tx.Transaction{tx1})

	mp := mempool.New(c)
	if err := mp.Add(tx2); err == nil {
		t.Fatalf("mempool accepted a tx spending an already-confirmed-spent output")
	}
}

// ---------------------------------------------------------------------------
// 20. Mempool Remove frees reservation; Select returns pending
// ---------------------------------------------------------------------------

func TestMempoolRemoveFreesReservation(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	harness.Funded(t, c, alice, 4)
	bob := harness.NewWallet("bob")

	tx1, err := alice.CreateTransaction(c, bob.Address(), alice.Balance()/8, defaultFee)
	if err != nil {
		t.Fatalf("tx1: %v", err)
	}
	alice2 := harness.NewWallet("alice")
	harness.ScanAll(c, alice2)
	tx2, err := alice2.CreateTransaction(c, bob.Address(), alice2.Balance()/8, defaultFee)
	if err != nil {
		t.Fatalf("tx2: %v", err)
	}
	if !sharesInput(tx1, tx2) {
		t.Skip("tx2 selected disjoint inputs; cannot test reservation release")
	}

	mp := mempool.New(c)
	if err := mp.Add(tx1); err != nil {
		t.Fatalf("add tx1: %v", err)
	}
	// Select returns the pending tx.
	sel := mp.Select(10)
	if len(sel) != 1 || sel[0].HashHex() != tx1.HashHex() {
		t.Fatalf("Select did not return the single pending tx (got %d)", len(sel))
	}
	// Conflicting tx2 currently rejected (ref reserved).
	if err := mp.Add(tx2); err == nil {
		t.Fatalf("tx2 unexpectedly admitted while tx1 reserves the ref")
	}
	// Remove tx1 => frees its OutputRef reservation.
	mp.Remove([]*tx.Transaction{tx1})
	if mp.Size() != 0 {
		t.Fatalf("size = %d after removing tx1", mp.Size())
	}
	// Now tx2 (same ref, still unspent on-chain) should be admissible.
	if err := mp.Add(tx2); err != nil {
		t.Fatalf("tx2 rejected after Remove freed the reservation: %v", err)
	}
	if mp.Size() != 1 {
		t.Fatalf("size = %d after adding tx2", mp.Size())
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// sharesInput reports whether two txs spend at least one common OutputRef.
func sharesInput(a, b *tx.Transaction) bool {
	set := map[string]bool{}
	for _, in := range a.Inputs {
		set[string(in.OutputRef)] = true
	}
	for _, in := range b.Inputs {
		if set[string(in.OutputRef)] {
			return true
		}
	}
	return false
}
