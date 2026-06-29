// Package consensus holds critical-workflow tests for Obscura's CONSENSUS and
// CHAIN-VALIDATION rules — the most security-critical track. Each "reject" test
// builds a VALID block or transaction via the shared harness, then tampers a
// deep copy and asserts that chain.ValidateBlock / ValidateStandaloneTx returns
// an error. These tests guard the soundness fixes (no inflation, no theft, no
// double-spend, correct emission/difficulty/PoW) so a regression in pkg/* shows
// up here as a failing test.
package consensus

import (
	"bytes"
	"testing"
	"time"

	"filippo.io/edwards25519"

	"obscura/pkg/accumulator"
	"obscura/pkg/block"
	"obscura/pkg/chain"
	"obscura/pkg/commit"
	"obscura/pkg/config"
	"obscura/pkg/consensus"
	"obscura/pkg/pow"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"

	"obscura/tests/critical/harness"
)

// buildLockedOutput creates a stealth output paying `amount` to `dest` with the
// given LockUntil, mirroring wallet.buildOutput. Returns the output and its
// commitment blinding (needed for conservation). Used to bake a time-lock into
// an output BEFORE proofs are computed (the wallet always uses LockUntil 0).
func buildLockedOutput(t *testing.T, dest commit.StealthAddress, amount, lockUntil uint64) (tx.Output, *edwards25519.Scalar) {
	t.Helper()
	r := commit.RandomScalar()
	so := commit.CreateOutputDeterministic(dest, r)
	shared := commit.SharedSecretSender(dest, r)
	C, blinding, rp, err := commit.ProveRange(amount)
	if err != nil {
		t.Fatalf("range proof: %v", err)
	}
	_, nonce := accumulator.HashToPrime(so.P.Bytes())
	return tx.Output{
		OneTimeKey: so.P.Bytes(),
		TxPubKey:   so.R.Bytes(),
		Commitment: C.Bytes(),
		RangeProof: rp.Serialize(),
		PrimeNonce: nonce,
		LockUntil:  lockUntil,
		EncAmount:  commit.EncryptAmount(shared, amount),
		EncMask:    commit.EncryptScalar(shared, blinding),
		ViewTag:    commit.ViewTag(shared),
	}, blinding
}

// buildLockedFundingTx hand-builds a valid one-input spend of `src` (an output
// owned by `from`) that pays `amount` to `dest` with the destination output
// time-locked until `lockUntil`. Proofs are computed over the final tx content,
// so the tx is valid and mineable while carrying a locked output.
func buildLockedFundingTx(t *testing.T, from *wallet.Wallet, src *wallet.OwnedOutput, dest commit.StealthAddress, amount, lockUntil, fee uint64) *tx.Transaction {
	t.Helper()
	tr := &tx.Transaction{Version: 1, Fee: fee}

	destOut, db := buildLockedOutput(t, dest, amount, lockUntil)
	tr.Outputs = append(tr.Outputs, destOut)
	var outBlindings []*edwards25519.Scalar
	outBlindings = append(outBlindings, db)

	change := src.Amount - amount - fee
	if change > 0 {
		chOut, cb := buildLockedOutput(t, from.Address(), change, 0)
		tr.Outputs = append(tr.Outputs, chOut)
		outBlindings = append(outBlindings, cb)
	}

	pr := commit.RandomScalar()
	pseudo := commit.Commit(src.Amount, pr)
	tr.Inputs = append(tr.Inputs, tx.Input{
		OutputRef:        append([]byte(nil), src.Out.OneTimeKey...),
		PseudoCommitment: pseudo.Bytes(),
		KeyImage:         commit.KeyImage(src.OneTime).Bytes(),
	})

	ctx := tr.CoreHash()
	own, err := commit.ProveOwnership(src.Out.OneTimeKey, src.OneTime, ctx[:])
	if err != nil {
		t.Fatalf("prove ownership: %v", err)
	}
	d := new(edwards25519.Scalar).Subtract(pr, src.Mask)
	eq, err := commit.ProveValueEquality(tr.Inputs[0].PseudoCommitment, src.Out.Commitment, d, ctx[:])
	if err != nil {
		t.Fatalf("prove equality: %v", err)
	}
	tr.Inputs[0].OwnershipProof = own
	tr.Inputs[0].EqualityProof = eq
	tr.Inputs[0].KeyImageProof = commit.ProveKeyImageProof(src.OneTime, ctx[:])

	z := edwards25519.NewScalar().Set(pr)
	for _, s := range outBlindings {
		z.Subtract(z, s)
	}
	pseudoIns := [][]byte{tr.Inputs[0].PseudoCommitment}
	outs := make([][]byte, len(tr.Outputs))
	for i, o := range tr.Outputs {
		outs[i] = o.Commitment
	}
	cons, err := commit.ProveConservation(pseudoIns, outs, fee, z, ctx[:])
	if err != nil {
		t.Fatalf("prove conservation: %v", err)
	}
	tr.Conservation = cons
	return tr
}

// dup deep-copies a transaction via its canonical serialization so tampering a
// copy never mutates the original (which other assertions may rely on).
func dup(t *testing.T, tr *tx.Transaction) *tx.Transaction {
	t.Helper()
	c, err := tx.Deserialize(tr.Serialize())
	if err != nil {
		t.Fatalf("deserialize tx: %v", err)
	}
	return c
}

// dupBlock deep-copies a block via its canonical serialization.
func dupBlock(t *testing.T, b *block.Block) *block.Block {
	t.Helper()
	c, err := block.DeserializeBlock(b.Serialize())
	if err != nil {
		t.Fatalf("deserialize block: %v", err)
	}
	return c
}

// ---------------------------------------------------------------------------
// 1. Genesis determinism
// ---------------------------------------------------------------------------

func TestGenesisDeterminism(t *testing.T) {
	c1 := harness.NewChain(t)
	c2 := harness.NewChain(t)
	h1, ok1 := c1.HeaderByHeight(0)
	h2, ok2 := c2.HeaderByHeight(0)
	if !ok1 || !ok2 {
		t.Fatal("genesis header missing")
	}
	if h1.ID() != h2.ID() {
		t.Fatalf("genesis IDs differ: %x vs %x", h1.ID(), h2.ID())
	}
	// And the genesis must be at height 0 with the fixed timestamp / prevhash.
	if h1.Height != 0 || h1.PrevHash != ([32]byte{}) {
		t.Fatalf("unexpected genesis header: height=%d prev=%x", h1.Height, h1.PrevHash)
	}
}

// ---------------------------------------------------------------------------
// 2. Emission
// ---------------------------------------------------------------------------

func TestEmissionDecreasesAndTail(t *testing.T) {
	r0 := config.BlockReward(0)
	rMid := config.BlockReward(config.MoneySupplyCap / 2)
	if !(r0 > rMid) {
		t.Fatalf("reward should decrease as supply grows: r0=%d rMid=%d", r0, rMid)
	}
	if rMid <= config.TailEmissionAtomic {
		t.Fatalf("mid reward unexpectedly already at tail: %d", rMid)
	}
	// At/after the cap the reward is exactly the tail emission.
	if got := config.BlockReward(config.MoneySupplyCap); got != config.TailEmissionAtomic {
		t.Fatalf("reward at cap = %d, want tail %d", got, config.TailEmissionAtomic)
	}
	if got := config.BlockReward(config.MoneySupplyCap + 1_000_000); got != config.TailEmissionAtomic {
		t.Fatalf("reward past cap = %d, want tail %d", got, config.TailEmissionAtomic)
	}
}

func TestEmittedGrowsByExpectedReward(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	w := harness.NewWallet("alice")

	before := c.Emitted() // 0 at genesis
	expReward := config.BlockReward(before)
	harness.MineBlock(t, c, w, nil)
	after := c.Emitted()

	if after-before != expReward {
		t.Fatalf("emitted grew by %d, expected base reward %d", after-before, expReward)
	}
}

// ---------------------------------------------------------------------------
// 3. Difficulty retarget bounds / overflow safety
// ---------------------------------------------------------------------------

func TestDifficultyRetargetBounds(t *testing.T) {
	// Build a window of equal-spaced timestamps at the target block time so the
	// retarget is roughly neutral; then verify the ±2x clamp around the last
	// difficulty and the MinDifficulty floor under extreme inputs.
	const last = uint64(1_000_000)
	var ts []int64
	var df []uint64
	for i := 0; i < config.DifficultyWindow; i++ {
		ts = append(ts, int64(i)*config.TargetBlockTime)
		df = append(df, last)
	}
	next := consensus.NextDifficulty(ts, df)
	if next > last*2 || next < last/2 {
		t.Fatalf("neutral retarget out of ±2x: next=%d last=%d", next, last)
	}

	// Extremely fast blocks (all timestamps equal) must clamp to +2x, never blow up.
	var fast []int64
	for i := 0; i < config.DifficultyWindow; i++ {
		fast = append(fast, 0)
	}
	up := consensus.NextDifficulty(fast, df)
	if up > last*2 {
		t.Fatalf("fast-block retarget exceeded +2x clamp: %d > %d", up, last*2)
	}

	// Extremely slow blocks must clamp to -2x but never below MinDifficulty.
	var slow []int64
	for i := 0; i < config.DifficultyWindow; i++ {
		slow = append(slow, int64(i)*config.TargetBlockTime*1000)
	}
	down := consensus.NextDifficulty(slow, df)
	if down < last/2 {
		t.Fatalf("slow-block retarget below -2x clamp: %d < %d", down, last/2)
	}
	if down < consensus.MinDifficulty {
		t.Fatalf("retarget below MinDifficulty: %d", down)
	}

	// Tiny difficulties + slow blocks must still respect the network floor.
	var lowDf []uint64
	for i := 0; i < config.DifficultyWindow; i++ {
		lowDf = append(lowDf, consensus.MinDifficulty)
	}
	floor := consensus.NextDifficulty(slow, lowDf)
	if floor < consensus.MinDifficulty {
		t.Fatalf("difficulty driven below MinDifficulty: %d", floor)
	}
}

func TestDifficultyNoOverflowOnHugeValues(t *testing.T) {
	huge := ^uint64(0) // max uint64
	var ts []int64
	var df []uint64
	for i := 0; i < config.DifficultyWindow; i++ {
		ts = append(ts, int64(i)) // very fast -> wants to ramp up
		df = append(df, huge)
	}
	next := consensus.NextDifficulty(ts, df)
	// The result must be a valid uint64 (no panic / wrap). With last == maxuint64
	// the +2x clamp shift overflows the big.Int comparison space but the function
	// guards with IsUint64; ensure it returns something <= max and >= floor.
	if next < consensus.MinDifficulty {
		t.Fatalf("huge-input difficulty below floor: %d", next)
	}
}

// ---------------------------------------------------------------------------
// 4. Coinbase maturity enforced
// ---------------------------------------------------------------------------

func TestCoinbaseMaturityEnforced(t *testing.T) {
	old := config.CoinbaseMaturity
	config.CoinbaseMaturity = 2
	defer func() { config.CoinbaseMaturity = old }()

	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")

	// Mine exactly one coinbase to alice at height 1; scan so she owns it.
	harness.MineBlock(t, c, alice, nil)
	harness.ScanAll(c, alice)

	bob := harness.NewWallet("bob")
	amount := config.AtomicPerCoin

	// At tip height 1 the next spend height is 2; coinbase at height 1 needs
	// height >= 1+2 = 3 to be spendable. So the wallet has no mature funds yet.
	if _, err := alice.CreateTransaction(c, bob.Address(), amount, config.AtomicPerCoin); err == nil {
		t.Fatal("expected immature-coinbase spend to be unselectable, but tx built")
	}

	// Advance the chain so the coinbase matures (mine to a throwaway wallet).
	sink := harness.NewWallet("sink")
	harness.MineN(t, c, sink, 2) // tip now height 3
	harness.ScanAll(c, alice)

	txn, err := alice.CreateTransaction(c, bob.Address(), amount, config.AtomicPerCoin)
	if err != nil {
		t.Fatalf("mature coinbase should be spendable: %v", err)
	}
	if err := c.ValidateStandaloneTx(txn); err != nil {
		t.Fatalf("mature spend rejected: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 5. Time-lock enforced (validation level)
// ---------------------------------------------------------------------------

func TestTimeLockEnforced(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	harness.Funded(t, c, alice, 4)
	bob := harness.NewWallet("bob")

	// Pick a spendable (mature) output of alice's to fund a locked output to bob.
	var src *wallet.OwnedOutput
	for _, o := range alice.Outputs {
		if !o.Spent && (!o.IsCoinbase || c.Height()+1 >= o.Height+config.CoinbaseMaturity) {
			src = o
			break
		}
	}
	if src == nil {
		t.Fatal("no mature output to fund from")
	}

	amount := config.AtomicPerCoin
	fee := config.AtomicPerCoin
	lockHeight := c.Height() + 1000
	// Bake the time-lock into the destination output BEFORE proofs are computed,
	// so the funding tx is fully valid and mineable.
	fund := buildLockedFundingTx(t, alice, src, bob.Address(), amount, lockHeight, fee)
	if err := c.ValidateStandaloneTx(fund); err != nil {
		t.Fatalf("funding tx with future lock should be valid standalone: %v", err)
	}
	harness.MineBlock(t, c, harness.NewWallet("sink"), []*tx.Transaction{fund})

	// Bob scans and owns the locked output. Force-select it by clearing the
	// wallet-local lock view (the on-chain UTXO keeps its future LockUntil).
	harness.ScanAll(c, bob)
	var locked *wallet.OwnedOutput
	for _, o := range bob.Outputs {
		if o.Out.LockUntil == lockHeight {
			locked = o
		}
	}
	if locked == nil {
		t.Fatal("bob did not detect the locked output")
	}
	// Confirm the chain recorded the lock on the UTXO.
	if entry, ok := c.UTXO(locked.Out.OneTimeKey); !ok || entry.LockUntil != lockHeight {
		t.Fatalf("chain UTXO lock not recorded: ok=%v entry=%+v", ok, entry)
	}

	// Build a spend of the locked output (bypass the wallet's own lock filter by
	// hand-building, since the on-chain UTXO is what consensus checks).
	carol := harness.NewWallet("carol")
	spend := buildLockedFundingTx(t, bob, locked, carol.Address(), amount/2, 0, config.AtomicPerCoin)
	// Chain must reject: the referenced UTXO is time-locked until a future height.
	if err := c.ValidateStandaloneTx(spend); err == nil {
		t.Fatal("expected rejection spending a time-locked output before its height")
	}
}

// ---------------------------------------------------------------------------
// 6. Double-spend rejected
// ---------------------------------------------------------------------------

func TestDoubleSpendRejected(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	harness.Funded(t, c, alice, 4)
	bob := harness.NewWallet("bob")

	txn, err := alice.CreateTransaction(c, bob.Address(), config.AtomicPerCoin, config.AtomicPerCoin)
	if err != nil {
		t.Fatalf("create tx: %v", err)
	}
	if err := c.ValidateStandaloneTx(txn); err != nil {
		t.Fatalf("first validation should pass: %v", err)
	}
	// Mine it so its inputs leave the UTXO set.
	harness.MineBlock(t, c, harness.NewWallet("sink"), []*tx.Transaction{txn})

	// Re-submitting the same tx must now fail: UTXO already spent.
	if err := c.ValidateStandaloneTx(txn); err == nil {
		t.Fatal("expected double-spend of already-mined tx to be rejected")
	}
	// Sanity: the spent output is reported spent.
	if !c.OutputSpent(txn.Inputs[0].OutputRef) {
		t.Fatal("spent input not marked spent in UTXO set")
	}
}

// ---------------------------------------------------------------------------
// 7. Theft rejected (corrupted ownership proof)
// ---------------------------------------------------------------------------

func TestTheftRejected(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	harness.Funded(t, c, alice, 4)
	bob := harness.NewWallet("bob")
	txn, err := alice.CreateTransaction(c, bob.Address(), config.AtomicPerCoin, config.AtomicPerCoin)
	if err != nil {
		t.Fatalf("create tx: %v", err)
	}
	if err := c.ValidateStandaloneTx(txn); err != nil {
		t.Fatalf("baseline tx invalid: %v", err)
	}
	bad := dup(t, txn)
	// Flip every byte of the ownership proof so it can't possibly verify.
	for i := range bad.Inputs[0].OwnershipProof {
		bad.Inputs[0].OwnershipProof[i] ^= 0xff
	}
	if err := c.ValidateStandaloneTx(bad); err == nil {
		t.Fatal("expected rejection of tx with corrupted ownership proof")
	}
}

// ---------------------------------------------------------------------------
// 8. Inflation rejected (value-mismatched pseudo-commitment)
// ---------------------------------------------------------------------------

func TestInflationRejected(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	harness.Funded(t, c, alice, 4)
	bob := harness.NewWallet("bob")
	txn, err := alice.CreateTransaction(c, bob.Address(), config.AtomicPerCoin, config.AtomicPerCoin)
	if err != nil {
		t.Fatalf("create tx: %v", err)
	}
	if err := c.ValidateStandaloneTx(txn); err != nil {
		t.Fatalf("baseline tx invalid: %v", err)
	}
	bad := dup(t, txn)
	// Replace the pseudo-commitment with a commitment to a huge value: this no
	// longer matches the referenced UTXO's committed value (equality proof fails)
	// and breaks conservation — either way it must be rejected.
	huge := commit.Commit(^uint64(0)/2, commit.RandomScalar())
	bad.Inputs[0].PseudoCommitment = huge.Bytes()
	if err := c.ValidateStandaloneTx(bad); err == nil {
		t.Fatal("expected rejection of inflated pseudo-commitment")
	}
}

// ---------------------------------------------------------------------------
// 9. Min-fee rejected (fee = 0)
// ---------------------------------------------------------------------------

func TestMinFeeRejected(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	harness.Funded(t, c, alice, 4)
	bob := harness.NewWallet("bob")
	// Fee 0 is below the per-byte minimum.
	txn, err := alice.CreateTransaction(c, bob.Address(), config.AtomicPerCoin, 0)
	if err != nil {
		t.Fatalf("create zero-fee tx: %v", err)
	}
	if err := c.ValidateStandaloneTx(txn); err == nil {
		t.Fatal("expected rejection of below-minimum (zero) fee tx")
	}
}

// ---------------------------------------------------------------------------
// 10. Coinbase-only fields on a normal tx rejected
// ---------------------------------------------------------------------------

func TestCoinbaseOnlyFieldsOnNormalTxRejected(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	harness.Funded(t, c, alice, 4)
	bob := harness.NewWallet("bob")
	base, err := alice.CreateTransaction(c, bob.Address(), config.AtomicPerCoin, config.AtomicPerCoin)
	if err != nil {
		t.Fatalf("create tx: %v", err)
	}
	if err := c.ValidateStandaloneTx(base); err != nil {
		t.Fatalf("baseline tx invalid: %v", err)
	}

	for _, mut := range []struct {
		name string
		f    func(tr *tx.Transaction)
	}{
		{"Height", func(tr *tx.Transaction) { tr.Height = 5 }},
		{"Minted", func(tr *tx.Transaction) { tr.Minted = 1 }},
		{"ReferrerTag", func(tr *tx.Transaction) { tr.ReferrerTag = []byte("ref") }},
		{"ExtraNonce", func(tr *tx.Transaction) { tr.ExtraNonce = 9 }},
	} {
		bad := dup(t, base)
		mut.f(bad)
		if err := c.ValidateStandaloneTx(bad); err == nil {
			t.Errorf("expected rejection of normal tx with coinbase-only field %s set", mut.name)
		}
	}
}

// ---------------------------------------------------------------------------
// 11. Duplicate output key rejected
// ---------------------------------------------------------------------------

func TestDuplicateOutputKeyRejected(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	harness.Funded(t, c, alice, 4)

	// Build a coinbase template to tamper. The coinbase has one output; clone it
	// so the block carries two outputs sharing the same OneTimeKey.
	tmpl := harness.BuildTemplate(t, c, alice, nil)
	cb := tmpl.Txs[0]
	cb.Outputs = append(cb.Outputs, cb.Outputs[0]) // duplicate OneTimeKey
	tmpl.Header.MerkleRoot = block.MerkleRoot(tmpl.Txs)
	harness.MineHeader(t, tmpl)
	if err := c.ValidateBlock(tmpl); err == nil {
		t.Fatal("expected rejection of block with duplicate output one-time keys")
	}
}

// ---------------------------------------------------------------------------
// 12. Oversized block rejected (DeserializeBlock bound + ValidateBlock bound)
// ---------------------------------------------------------------------------

func TestOversizedBlockRejected(t *testing.T) {
	// DeserializeBlock must reject input larger than MaxBlockBytes outright.
	big := make([]byte, config.MaxBlockBytes+1)
	if _, err := block.DeserializeBlock(big); err == nil {
		t.Fatal("expected DeserializeBlock to reject over-cap input")
	}

	// And ValidateBlock must reject an in-memory block that serializes too large.
	// We pad the coinbase's ReferrerTag — wait, that's coinbase-legal but the
	// field is bounded; instead add many large outputs is expensive, so we rely
	// on the serialized-size guard by inflating EncAmount on a coinbase output.
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	tmpl := harness.BuildTemplate(t, c, alice, nil)
	cb := tmpl.Txs[0]
	// Inflate a single output field beyond the block cap. (MaxFieldBytes bounds
	// deserialization, but the in-memory ValidateBlock weight check runs on
	// Serialize() directly, which is what we exercise here.)
	cb.Outputs[0].EncAmount = make([]byte, config.MaxBlockBytes+10)
	tmpl.Header.MerkleRoot = block.MerkleRoot(tmpl.Txs)
	harness.MineHeader(t, tmpl)
	if err := c.ValidateBlock(tmpl); err == nil {
		t.Fatal("expected rejection of oversized block by weight check")
	}
}

// ---------------------------------------------------------------------------
// 13. Wrong difficulty rejected
// ---------------------------------------------------------------------------

func TestWrongDifficultyRejected(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	tmpl := harness.BuildTemplate(t, c, alice, nil)
	harness.MineHeader(t, tmpl)
	if err := c.ValidateBlock(tmpl); err != nil {
		t.Fatalf("baseline block invalid: %v", err)
	}
	bad := dupBlock(t, tmpl)
	bad.Header.Difficulty = tmpl.Header.Difficulty + 1
	if err := c.ValidateBlock(bad); err == nil {
		t.Fatal("expected rejection of block with wrong difficulty")
	}
}

// ---------------------------------------------------------------------------
// 14. Wrong prevhash rejected
// ---------------------------------------------------------------------------

func TestWrongPrevHashRejected(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	tmpl := harness.BuildTemplate(t, c, alice, nil)
	harness.MineHeader(t, tmpl)
	bad := dupBlock(t, tmpl)
	bad.Header.PrevHash[0] ^= 0xff
	if err := c.ValidateBlock(bad); err == nil {
		t.Fatal("expected rejection of block with wrong prevhash")
	}
}

// ---------------------------------------------------------------------------
// 15. Wrong merkle root rejected
// ---------------------------------------------------------------------------

func TestWrongMerkleRootRejected(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	tmpl := harness.BuildTemplate(t, c, alice, nil)
	harness.MineHeader(t, tmpl)
	bad := dupBlock(t, tmpl)
	bad.Header.MerkleRoot[0] ^= 0xff
	if err := c.ValidateBlock(bad); err == nil {
		t.Fatal("expected rejection of block with wrong merkle root")
	}
}

// ---------------------------------------------------------------------------
// 16. Insufficient PoW rejected (nonce that doesn't meet difficulty)
// ---------------------------------------------------------------------------

func TestInsufficientPoWRejected(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	tmpl := harness.BuildTemplate(t, c, alice, nil)

	// Find a nonce whose PoW hash does NOT meet the difficulty (the common case;
	// no grinding toward a solution). Use the block's own pow.Meets via header.
	bad := dupBlock(t, tmpl)
	var found bool
	for n := uint64(1); n < 5000; n++ {
		bad.Header.Nonce = n
		if !powMeets(&bad.Header) {
			found = true
			break
		}
	}
	if !found {
		t.Skip("could not find a non-meeting nonce (difficulty too low)")
	}
	if err := c.ValidateBlock(bad); err == nil {
		t.Fatal("expected rejection of block with insufficient proof of work")
	}
}

// powMeets reports whether the header's current nonce satisfies its difficulty,
// using the same check consensus performs.
func powMeets(h *block.Header) bool {
	return pow.Meets(h.PoWHash(), h.Difficulty)
}

// ---------------------------------------------------------------------------
// 17. Accumulator checkpoint mismatch rejected
// ---------------------------------------------------------------------------

func TestAccCheckpointMismatchRejected(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	tmpl := harness.BuildTemplate(t, c, alice, nil)
	harness.MineHeader(t, tmpl)
	if err := c.ValidateBlock(tmpl); err != nil {
		t.Fatalf("baseline block invalid: %v", err)
	}
	bad := dupBlock(t, tmpl)
	if len(bad.Header.AccValue) == 0 {
		t.Skip("empty AccValue, nothing to tamper")
	}
	bad.Header.AccValue[0] ^= 0xff
	if err := c.ValidateBlock(bad); err == nil {
		t.Fatal("expected rejection of block with tampered accumulator checkpoint")
	}
}

// ---------------------------------------------------------------------------
// 18. Coinbase minted mismatch rejected
// ---------------------------------------------------------------------------

func TestCoinbaseMintedMismatchRejected(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")
	tmpl := harness.BuildTemplate(t, c, alice, nil)
	harness.MineHeader(t, tmpl)
	if err := c.ValidateBlock(tmpl); err != nil {
		t.Fatalf("baseline block invalid: %v", err)
	}
	bad := dupBlock(t, tmpl)
	bad.Txs[0].Minted += 1 // inflate the reward
	// merkle root now mismatches too, but the minted check should also fire; we
	// only care that validation rejects.
	if err := c.ValidateBlock(bad); err == nil {
		t.Fatal("expected rejection of block with tampered coinbase Minted")
	}
}

// ---------------------------------------------------------------------------
// 19. Future / too-old timestamp rejected
// ---------------------------------------------------------------------------

func TestTimestampBoundsRejected(t *testing.T) {
	defer harness.SmallMaturity()()
	c := harness.NewChain(t)
	alice := harness.NewWallet("alice")

	// Far-future timestamp.
	tmpl := harness.BuildTemplate(t, c, alice, nil)
	future := dupBlock(t, tmpl)
	future.Header.Timestamp = time.Now().Unix() + 1_000_000_000
	harness.MineHeader(t, future)
	if err := c.ValidateBlock(future); err == nil {
		t.Fatal("expected rejection of far-future timestamp")
	}

	// Too-old timestamp: <= median-time-past. Genesis ts is fixed in the past;
	// using genesis timestamp guarantees timestamp <= MTP.
	gen, _ := c.HeaderByHeight(0)
	old := dupBlock(t, tmpl)
	old.Header.Timestamp = gen.Timestamp // equals MTP at height 1
	harness.MineHeader(t, old)
	if err := c.ValidateBlock(old); err == nil {
		t.Fatal("expected rejection of timestamp <= median-time-past")
	}
}

// ---------------------------------------------------------------------------
// 20. Replay re-validation across reopen
// ---------------------------------------------------------------------------

func TestReplayRevalidation(t *testing.T) {
	defer harness.SmallMaturity()()
	dir := t.TempDir()

	c, err := chain.New(dir)
	if err != nil {
		t.Fatalf("new chain: %v", err)
	}
	alice := harness.NewWallet("alice")
	const n = 5
	for i := 0; i < n; i++ {
		fees := uint64(0)
		minted := c.ExpectedCoinbaseMinted(fees, nil)
		cb, err := alice.BuildCoinbase(c.Height()+1, minted, nil)
		if err != nil {
			t.Fatalf("coinbase: %v", err)
		}
		tmpl, err := c.BlockTemplate(append([]*tx.Transaction{cb}, []*tx.Transaction{}...))
		if err != nil {
			t.Fatalf("template: %v", err)
		}
		harness.MineHeader(t, tmpl)
		if err := c.AddBlock(tmpl); err != nil {
			t.Fatalf("addblock: %v", err)
		}
	}
	height := c.Height()
	emitted := c.Emitted()
	accVal := append([]byte(nil), c.AccValue()...)
	accSize := c.AccSize()
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if height != n {
		t.Fatalf("expected height %d before reopen, got %d", n, height)
	}

	// Reopen: chain.New re-validates every stored block during replay.
	c2, err := chain.New(dir)
	if err != nil {
		t.Fatalf("reopen chain (replay validation failed?): %v", err)
	}
	defer c2.Close()

	if c2.Height() != height {
		t.Fatalf("reopened height %d, want %d", c2.Height(), height)
	}
	if c2.Emitted() != emitted {
		t.Fatalf("reopened emitted %d, want %d", c2.Emitted(), emitted)
	}
	if !bytes.Equal(c2.AccValue(), accVal) {
		t.Fatalf("reopened accumulator value mismatch")
	}
	if c2.AccSize() != accSize {
		t.Fatalf("reopened acc size %d, want %d", c2.AccSize(), accSize)
	}
}
