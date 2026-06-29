package swapsession

import (
	"bytes"
	"math/big"
	"path/filepath"
	"testing"

	"obscura/pkg/commit"
	"obscura/pkg/config"
	"obscura/pkg/swap"
	"obscura/pkg/swapd"
)

// handshakeFunded runs Init/MakerCommit and the maker's Fund leg, returning the
// parties and the (honest) Funded message — the shared prefix of the attack tests.
func handshakeFunded(t *testing.T, h *obxHost, id [32]byte) (*Maker, *Taker, *Funded) {
	t.Helper()
	maker := NewMaker(id, testOBXAmount, testXNO, testFee, "maker-xno-dest", h)
	taker := NewTaker(id, testOBXAmount, testXNO, testFee, h)
	mc, err := maker.HandleInit(taker.Init())
	if err != nil {
		t.Fatalf("HandleInit: %v", err)
	}
	if err := taker.HandleMakerCommit(mc); err != nil {
		t.Fatalf("HandleMakerCommit: %v", err)
	}
	funded, err := maker.Fund(h.Height() + fundOffset)
	if err != nil {
		t.Fatalf("Fund: %v", err)
	}
	return maker, taker, funded
}

// F1 ATTACK (TestRejectWrongXNOAccount): a malicious taker locks the XNO to a
// SELF-controlled account rather than the joint (sA+sB)·G key, then reports that
// lock. The maker MUST reject in ConfirmXNOLock and NOT co-sign — otherwise the
// taker takes the OBX while the maker's SweepXNO can never recover (the wrong
// account is not controlled by sA+sB) and the maker loses its OBX outright.
func TestRejectWrongXNOAccount(t *testing.T) {
	h := newDevnet(t)
	nano := swapd.NewMockNano()
	id := swapID(0x51)
	maker, _, _ := handshakeFunded(t, h, id)

	// attacker locks the agreed AMOUNT but to an account it alone controls.
	evilPub := commit.RandomScalar() // a key the taker (not sA+sB) controls
	evilAccount := pt(evilPub).Bytes()
	if bytes.Equal(evilAccount, maker.State().XNOAccountPub) {
		t.Fatal("test setup: evil account collided with the joint account")
	}
	lockID, err := nano.Lock(testXNO, evilAccount)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}

	// the maker must REFUSE to co-sign this lock.
	err = maker.ConfirmXNOLock(nano, &XNOLocked{SwapID: id, LockID: lockID})
	if err == nil {
		t.Fatal("maker accepted an XNO lock to the WRONG account — would lose OBX")
	}
	if maker.State().XNOLockID != "" || maker.State().Phase == PhaseXNOLock {
		t.Fatal("maker advanced state despite the wrong-account lock")
	}
	// and so it must still refuse to co-sign (no lock confirmed).
	if _, err := maker.CoSignClaim(&ClaimRequest{SwapID: id, CoreHash: []byte("x")}); err == nil {
		t.Fatal("maker co-signed despite never confirming a valid XNO lock")
	}
}

// F1 ATTACK (TestRejectWrongXNOAmount): the taker locks to the CORRECT joint
// account but for LESS than the agreed XNO. The maker MUST reject — co-signing
// would let the taker take the full OBX for an underweight XNO payment.
func TestRejectWrongXNOAmount(t *testing.T) {
	h := newDevnet(t)
	nano := swapd.NewMockNano()
	id := swapID(0x52)
	maker, _, _ := handshakeFunded(t, h, id)

	// underweight lock to the RIGHT account.
	lockID, err := nano.Lock(testXNOm1, maker.State().XNOAccountPub)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if err := maker.ConfirmXNOLock(nano, &XNOLocked{SwapID: id, LockID: lockID}); err == nil {
		t.Fatal("maker accepted an UNDERWEIGHT XNO lock")
	}
	if maker.State().Phase == PhaseXNOLock {
		t.Fatal("maker advanced despite the underweight lock")
	}

	// the HONEST full-amount lock to the joint account is accepted.
	good, err := nano.Lock(testXNO, maker.State().XNOAccountPub)
	if err != nil {
		t.Fatalf("Lock(honest): %v", err)
	}
	if err := maker.ConfirmXNOLock(nano, &XNOLocked{SwapID: id, LockID: good}); err != nil {
		t.Fatalf("maker rejected a correct XNO lock: %v", err)
	}
}

// F2 ATTACK (TestRejectUnderfundedOBX): the maker funds HALF the agreed OBX but
// announces the swap normally. The taker MUST detect the on-chain locked amount is
// wrong in VerifyFundedAndLock/checkSwapOut and lock NO XNO (without the amount
// check the taker would lock full XNO and then be unable to claim — XNO frozen).
func TestRejectUnderfundedOBX(t *testing.T) {
	h := newDevnet(t)
	nano := swapd.NewMockNano()
	id := swapID(0x53)

	// maker and taker AGREE on testOBXAmount, but the maker funds only HALF on-chain.
	maker := NewMaker(id, testOBXAmount, testXNO, testFee, "maker-xno-dest", h)
	taker := NewTaker(id, testOBXAmount, testXNO, testFee, h)
	mc, err := maker.HandleInit(taker.Init())
	if err != nil {
		t.Fatalf("HandleInit: %v", err)
	}
	if err := taker.HandleMakerCommit(mc); err != nil {
		t.Fatalf("HandleMakerCommit: %v", err)
	}

	// the maker funds the SwapOut at the agreed key/binding but with HALF the amount,
	// then announces the (truthful) swap key. We drive FundSwapOut directly with the
	// underweight value to model a cheating maker.
	unlock := h.Height() + fundOffset
	swapKey := randID()
	half := testOBXAmount / 2
	if err := h.FundSwapOut(swapKey, half,
		maker.State().ClaimKey, pt(maker.b).Bytes(),
		taker.Init().A, pt(maker.b).Bytes(), taker.Init().PoPA, swap.ProvePossession(maker.b),
		maker.State().AggNonceR, maker.State().AdaptorT, unlock, testFee); err != nil {
		t.Fatalf("underfund FundSwapOut: %v", err)
	}

	// the taker verifies on-chain and MUST refuse to lock XNO (amount mismatch).
	funded := &Funded{SwapID: id, SwapKey: swapKey, UnlockHeight: unlock}
	if _, err := taker.VerifyFundedAndLock(nano, funded); err == nil {
		t.Fatal("taker locked XNO against an UNDERFUNDED OBX SwapOut — would freeze its XNO")
	}
	if nano.Balance("any").Sign() != 0 {
		t.Fatal("XNO moved despite the underfunded OBX")
	}
	if taker.State().Phase == PhaseXNOLock {
		t.Fatal("taker advanced to xno_lock despite the underfunded OBX")
	}
}

// F-1 ATTACK (TestRejectUnclaimableUnlockWindow): the FUND-FREEZE bug. A malicious
// maker funds the OBX SwapOut with an UnlockHeight too close to the current height —
// inside the reorg margin, so the claim path (valid iff height + SwapReorgMargin <=
// UnlockHeight) is already DEAD. The maker then announces the (truthful) swap key and
// matching unlock height. Without the fix the taker only checks so.UnlockHeight ==
// announcedUnlock and would lock XNO into a swap it can NEVER claim, while the maker
// refunds risk-free → frozen XNO. The taker MUST refuse to lock XNO, and (defense in
// depth) an honest Maker.Fund MUST refuse to choose such an unlock height.
func TestRejectUnclaimableUnlockWindow(t *testing.T) {
	h := newDevnet(t)
	nano := swapd.NewMockNano()
	id := swapID(0x55)

	maker := NewMaker(id, testOBXAmount, testXNO, testFee, "maker-xno-dest", h)
	taker := NewTaker(id, testOBXAmount, testXNO, testFee, h)
	mc, err := maker.HandleInit(taker.Init())
	if err != nil {
		t.Fatalf("HandleInit: %v", err)
	}
	if err := taker.HandleMakerCommit(mc); err != nil {
		t.Fatalf("HandleMakerCommit: %v", err)
	}

	// (1) DEFENSE-IN-DEPTH: an honest maker cannot misconfigure an unclaimable swap.
	// Any unlock inside [now, now+margin+minWindow) must be rejected by Maker.Fund.
	// margin=2, minWindow=3 in newDevnet, so the smallest claimable offset is 5; +1 is
	// the canonical attack value (the prompt's unlockHeight = currentHeight + 1).
	if _, err := maker.Fund(h.Height() + 1); err == nil {
		t.Fatal("Maker.Fund accepted an unlock height inside the reorg margin — would fund an unclaimable swap")
	}
	if maker.State().Phase == PhaseFunded {
		t.Fatal("maker advanced to funded despite the unclaimable unlock height")
	}

	// (2) PRIMARY: model the malicious maker funding on-chain DIRECTLY (bypassing the
	// honest Maker.Fund guard), then announcing the truthful swap key + unlock. The
	// taker must detect the too-tight claim window in VerifyFundedAndLock/checkSwapOut
	// and lock NO XNO. We deliberately pick an unlock that PASSES the consensus backstop
	// (UnlockHeight >= fundBlockHeight + margin) but still fails the taker's stricter
	// off-chain check (UnlockHeight < takerHeight + margin + minWindow) — proving the
	// TAKER layer is load-bearing on its own, not merely shadowed by consensus. The
	// FundSwapOut below mines one block, so the fund/taker height is h.Height()+1; we set
	// unlock = (h.Height()+1) + margin + 1, leaving a 1-block on-chain claim window which
	// is below the minWindow (=3) headroom the taker requires.
	fundBlockHeight := h.Height() + 1
	unlock := fundBlockHeight + config.SwapReorgMargin + 1
	swapKey := randID()
	if err := h.FundSwapOut(swapKey, testOBXAmount,
		maker.State().ClaimKey, pt(maker.b).Bytes(),
		taker.Init().A, pt(maker.b).Bytes(), taker.Init().PoPA, swap.ProvePossession(maker.b),
		maker.State().AggNonceR, maker.State().AdaptorT, unlock, testFee); err != nil {
		t.Fatalf("attacker FundSwapOut (within margin) unexpectedly rejected on-chain: %v", err)
	}

	funded := &Funded{SwapID: id, SwapKey: swapKey, UnlockHeight: unlock}
	if _, err := taker.VerifyFundedAndLock(nano, funded); err == nil {
		t.Fatal("taker locked XNO into an UNCLAIMABLE swap (dead claim window) — XNO would freeze")
	}
	if nano.Balance("any").Sign() != 0 {
		t.Fatal("XNO moved despite the unclaimable unlock window")
	}
	if taker.State().Phase == PhaseXNOLock {
		t.Fatal("taker advanced to xno_lock despite the unclaimable unlock window")
	}
}

// F-1 happy-path coverage (TestHappyPathNonTrivialClaimWindow): the same full swap as
// TestHappyPath but asserts the config under test has a NON-TRIVIAL fund-safety bound
// (SwapReorgMargin + SwapMinClaimWindow > 1) and that the claim still succeeds — so the
// F-1 fix is verified to pass because the open claim window is REAL, not because it is
// tiny/degenerate. (newDevnet sets margin=2, minWindow=3 → bound 5; fundOffset=8.)
func TestHappyPathNonTrivialClaimWindow(t *testing.T) {
	h := newDevnet(t)
	if config.SwapReorgMargin+config.SwapMinClaimWindow <= 1 {
		t.Fatalf("test config has a degenerate fund-safety bound (margin %d + minWindow %d) — the F-1 fix would pass trivially",
			config.SwapReorgMargin, config.SwapMinClaimWindow)
	}
	nano := swapd.NewMockNano()
	id := swapID(0x56)

	maker, taker := drive(t, h, nano, id)
	if maker.State().Phase != PhaseSwept {
		t.Fatalf("maker phase = %s, want swept", maker.State().Phase)
	}
	if taker.State().Phase != PhaseClaimed {
		t.Fatalf("taker phase = %s, want claimed (claim succeeded inside a non-trivial window)", taker.State().Phase)
	}
	if got := nano.Balance("maker-xno-dest"); got.Cmp(testXNO) != 0 {
		t.Fatalf("XNO at maker dest = %s, want %s", got, testXNO)
	}
}

// F3 (TestNonceGuardSurvivesReload): a maker co-signs ONE core hash, then crashes.
// After Save + LoadState + ResumeMaker, the resumed maker MUST still refuse to
// co-sign a SECOND DISTINCT core hash under the same committed nonce rb (which would
// leak b). Re-co-signing the SAME hash must remain a harmless retry.
func TestNonceGuardSurvivesReload(t *testing.T) {
	h := newDevnet(t)
	nano := swapd.NewMockNano()
	id := swapID(0x54)

	maker, taker, funded := handshakeFunded(t, h, id)
	locked, err := taker.VerifyFundedAndLock(nano, funded)
	if err != nil {
		t.Fatalf("VerifyFundedAndLock: %v", err)
	}
	if err := maker.ConfirmXNOLock(nano, locked); err != nil {
		t.Fatalf("ConfirmXNOLock: %v", err)
	}
	req, err := taker.BuildClaimRequest()
	if err != nil {
		t.Fatalf("BuildClaimRequest: %v", err)
	}

	// co-sign ONCE.
	ps1, err := maker.CoSignClaim(req)
	if err != nil {
		t.Fatalf("CoSignClaim: %v", err)
	}

	// CRASH: persist the state and reload into a FRESH maker.
	path := filepath.Join(t.TempDir(), "maker-state.json")
	if err := maker.State().Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	st, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(st.CoSignedCoreHash) == 0 {
		t.Fatal("co-signed core hash was not persisted — nonce guard lost on crash")
	}
	resumed, err := ResumeMaker(st, h)
	if err != nil {
		t.Fatalf("ResumeMaker: %v", err)
	}

	// the resumed maker re-co-signs the SAME core hash identically (benign retry).
	ps1b, err := resumed.CoSignClaim(req)
	if err != nil {
		t.Fatalf("resumed retry of same core hash rejected: %v", err)
	}
	if !bytes.Equal(ps1.Sb, ps1b.Sb) {
		t.Fatal("resumed maker produced a DIFFERENT half for the same core hash")
	}

	// ATTACK: a SECOND DISTINCT core hash under the same nonce → MUST be rejected even
	// across the crash/reload boundary.
	evil := &ClaimRequest{SwapID: id, CoreHash: append([]byte(nil), req.CoreHash...)}
	evil.CoreHash[0] ^= 0xff
	if _, err := resumed.CoSignClaim(evil); err == nil {
		t.Fatal("resumed maker co-signed a SECOND distinct claim — b is leakable across a crash")
	}
}

// GRIEFING FIX (TestMakerExtractsSAIndependently): after co-signing, the maker must be
// able to recover sA and sweep the XNO from the ON-CHAIN claim ALONE — its stored ŝ_a
// (verified in CoSignClaim) and its own recomputed ŝ_b — WITHOUT any taker-relayed
// aggregate pre-signature. This is the core of the fix: the maker's fund recovery no
// longer depends on taker cooperation after co-signing.
func TestMakerExtractsSAIndependently(t *testing.T) {
	h := newDevnet(t)
	nano := swapd.NewMockNano()
	id := swapID(0x61)

	maker, taker, funded := handshakeFunded(t, h, id)
	locked, err := taker.VerifyFundedAndLock(nano, funded)
	if err != nil {
		t.Fatalf("VerifyFundedAndLock: %v", err)
	}
	if err := maker.ConfirmXNOLock(nano, locked); err != nil {
		t.Fatalf("ConfirmXNOLock: %v", err)
	}

	// taker builds the claim request (now carrying ŝ_a), maker verifies+co-signs+stores ŝ_a.
	req, err := taker.BuildClaimRequest()
	if err != nil {
		t.Fatalf("BuildClaimRequest: %v", err)
	}
	if len(req.TakerHalf) != 32 {
		t.Fatalf("ClaimRequest missing TakerHalf (got %d bytes)", len(req.TakerHalf))
	}
	presig, err := maker.CoSignClaim(req)
	if err != nil {
		t.Fatalf("CoSignClaim: %v", err)
	}
	if !bytes.Equal(maker.State().PeerClaimHalf, req.TakerHalf) {
		t.Fatal("maker did not durably store the taker's verified ŝ_a")
	}

	// taker finalizes (mines the claim, publishing S_full on-chain). We DISCARD the
	// returned aggregate pre-sig — the maker must not need it.
	_, _, err = taker.FinalizeClaim(presig)
	if err != nil {
		t.Fatalf("FinalizeClaim: %v", err)
	}

	// MAKER: scrape ONLY the on-chain full sig and extract sA independently. No taker
	// aggregate pre-sig is used anywhere here.
	fullSigBytes, _, ok := h.FindSwapSpend(maker.State().SwapKey)
	if !ok {
		t.Fatal("no mined claim found on-chain")
	}
	fullSig, err := commit.ParseFullSig(fullSigBytes)
	if err != nil {
		t.Fatalf("ParseFullSig: %v", err)
	}
	if err := maker.SweepXNOIndependent(nano, fullSig); err != nil {
		t.Fatalf("SweepXNOIndependent: %v", err)
	}
	if maker.State().Phase != PhaseSwept {
		t.Fatalf("maker phase = %s, want swept", maker.State().Phase)
	}
	if got := nano.Balance("maker-xno-dest"); got.Cmp(testXNO) != 0 {
		t.Fatalf("XNO at maker dest = %s, want %s", got, testXNO)
	}

	// CROSS-CHECK: the independent sA must equal the happy-path Extract on the relayed
	// aggregate — same convention, same secret. Re-derive ŝ_a+ŝ_b and confirm
	// S_full − (ŝ_a+ŝ_b) == the recovered sA·G == Sa.
	if !bytes.Equal(pt(taker.sA).Bytes(), maker.State().PeerXNOShare) {
		t.Fatal("test setup: taker sA·G != stored Sa")
	}
}

// GRIEFING FIX (TestCoSignRejectsForgedTakerHalf): a malicious/garbled ŝ_a in the
// ClaimRequest must be REJECTED by CoSignClaim — the maker must NOT release its own
// half against a taker half it cannot later subtract out of S_full to reveal sA. A
// correct ŝ_a is then accepted.
func TestCoSignRejectsForgedTakerHalf(t *testing.T) {
	h := newDevnet(t)
	nano := swapd.NewMockNano()
	id := swapID(0x62)

	maker, taker, funded := handshakeFunded(t, h, id)
	locked, err := taker.VerifyFundedAndLock(nano, funded)
	if err != nil {
		t.Fatalf("VerifyFundedAndLock: %v", err)
	}
	if err := maker.ConfirmXNOLock(nano, locked); err != nil {
		t.Fatalf("ConfirmXNOLock: %v", err)
	}
	req, err := taker.BuildClaimRequest()
	if err != nil {
		t.Fatalf("BuildClaimRequest: %v", err)
	}

	// FORGE: a different canonical scalar in TakerHalf (ŝ_a·G != Ra + e·A) → reject.
	forged := &ClaimRequest{
		SwapID:    id,
		CoreHash:  append([]byte(nil), req.CoreHash...),
		TakerHalf: commit.RandomScalar().Bytes(),
	}
	if !bytes.Equal(forged.TakerHalf, req.TakerHalf) { // overwhelmingly true
		if _, err := maker.CoSignClaim(forged); err == nil {
			t.Fatal("maker co-signed against a FORGED taker half ŝ_a — would be unable to extract sA")
		}
	}
	// the maker must not have advanced its co-sign guard / stored a bad half.
	if len(maker.State().PeerClaimHalf) != 0 || len(maker.State().CoSignedCoreHash) != 0 {
		t.Fatal("maker stored state after rejecting a forged taker half")
	}

	// the HONEST ŝ_a is accepted and stored.
	if _, err := maker.CoSignClaim(req); err != nil {
		t.Fatalf("maker rejected the correct ŝ_a: %v", err)
	}
	if !bytes.Equal(maker.State().PeerClaimHalf, req.TakerHalf) {
		t.Fatal("maker did not store the verified ŝ_a after accepting")
	}
}

// testXNO is the agreed XNO amount (raw, 128-bit *big.Int) for the swap flow tests.
// It is a *big.Int (not a uint64 const) because the production NanoClient interface
// now carries raw XNO as *big.Int. testXNOm1 / testXNOp1 are the off-by-one variants
// used by the wrong-amount attack tests.
var (
	testXNO   = big.NewInt(10_000) // abstract mock units (raw)
	testXNOm1 = new(big.Int).Sub(testXNO, big.NewInt(1))
	testXNOp1 = new(big.Int).Add(testXNO, big.NewInt(1))
)

const (
	testOBXAmount = 3 * config.AtomicPerCoin
	testFee       = uint64(1_000_000_000)
	takerXNODest  = "taker-xno-dest"

	// fundOffset is the unlock-height offset (above the CURRENT height at the Fund
	// call) used by the happy-path / ordering / nonce flow tests. With newDevnet's
	// SwapReorgMargin=2 and SwapMinClaimWindow=3, an honest swap needs UnlockHeight >=
	// fundHeight + margin + minWindow; after the fund block is mined the taker checks
	// at fundHeight+1, so the binding requirement is offset >= 1+2+3 = 6. We use 8 for
	// comfortable headroom so the claim (mined a couple of blocks later) also stays in
	// the open window — i.e. the fix is NOT passing merely because the window is tiny.
	fundOffset = uint64(8)
)

func swapID(tag byte) [32]byte {
	var id [32]byte
	for i := range id {
		id[i] = tag
	}
	return id
}

// drive runs the full happy-path handshake+settlement between a fresh maker and
// taker against the given host + mock nano, returning both party objects so the
// caller can assert on their state. The maker sweeps XNO to its own dest.
func drive(t *testing.T, h *obxHost, nano *swapd.MockNano, id [32]byte) (*Maker, *Taker) {
	t.Helper()
	maker := NewMaker(id, testOBXAmount, testXNO, testFee, "maker-xno-dest", h)
	taker := NewTaker(id, testOBXAmount, testXNO, testFee, h)

	// 1) handshake: Init -> MakerCommit (serialize through the wire to exercise it).
	initMsg, err := ParseInit(taker.Init().Serialize())
	if err != nil {
		t.Fatalf("init roundtrip: %v", err)
	}
	mcRaw, err := maker.HandleInit(initMsg)
	if err != nil {
		t.Fatalf("HandleInit: %v", err)
	}
	mc, err := ParseMakerCommit(mcRaw.Serialize())
	if err != nil {
		t.Fatalf("makercommit roundtrip: %v", err)
	}
	if err := taker.HandleMakerCommit(mc); err != nil {
		t.Fatalf("HandleMakerCommit: %v", err)
	}

	// 2) maker funds OBX FIRST.
	unlock := h.Height() + fundOffset // leaves a non-trivial open claim window
	fundedRaw, err := maker.Fund(unlock)
	if err != nil {
		t.Fatalf("Fund: %v", err)
	}
	funded, err := ParseFunded(fundedRaw.Serialize())
	if err != nil {
		t.Fatalf("funded roundtrip: %v", err)
	}

	// 3) taker verifies the on-chain OBX lock, THEN locks XNO.
	lockedRaw, err := taker.VerifyFundedAndLock(nano, funded)
	if err != nil {
		t.Fatalf("VerifyFundedAndLock: %v", err)
	}
	locked, err := ParseXNOLocked(lockedRaw.Serialize())
	if err != nil {
		t.Fatalf("xnolocked roundtrip: %v", err)
	}
	if err := maker.ConfirmXNOLock(nano, locked); err != nil {
		t.Fatalf("ConfirmXNOLock: %v", err)
	}

	// 4) claim co-signing: taker builds claim -> maker co-signs -> taker finalizes.
	reqRaw, err := taker.BuildClaimRequest()
	if err != nil {
		t.Fatalf("BuildClaimRequest: %v", err)
	}
	req, err := ParseClaimRequest(reqRaw.Serialize())
	if err != nil {
		t.Fatalf("claimrequest roundtrip: %v", err)
	}
	presigRaw, err := maker.CoSignClaim(req)
	if err != nil {
		t.Fatalf("CoSignClaim: %v", err)
	}
	presig, err := ParseClaimPreSig(presigRaw.Serialize())
	if err != nil {
		t.Fatalf("claimpresig roundtrip: %v", err)
	}
	aggPre, fullSig, err := taker.FinalizeClaim(presig)
	if err != nil {
		t.Fatalf("FinalizeClaim: %v", err)
	}

	// 5) maker observes the on-chain claim, extracts sA, sweeps XNO.
	if err := maker.SweepXNO(nano, aggPre, fullSig); err != nil {
		t.Fatalf("SweepXNO: %v", err)
	}
	return maker, taker
}

// (a) happy path: two parties each holding ONLY their share complete the swap;
// XNO ends at the maker's dest (the maker is the XNO receiver); the taker gets the
// OBX; and NEITHER party object ever held both shares.
func TestHappyPath(t *testing.T) {
	h := newDevnet(t)
	nano := swapd.NewMockNano()
	id := swapID(0x11)

	takerBefore := h.takerWallet().Balance()

	maker, taker := drive(t, h, nano, id)

	if maker.State().Phase != PhaseSwept {
		t.Fatalf("maker phase = %s, want swept", maker.State().Phase)
	}
	if taker.State().Phase != PhaseClaimed {
		t.Fatalf("taker phase = %s, want claimed", taker.State().Phase)
	}

	// XNO ended at the maker's sweep dest.
	if got := nano.Balance("maker-xno-dest"); got.Cmp(testXNO) != 0 {
		t.Fatalf("XNO at maker dest = %s, want %s", got, testXNO)
	}

	// the taker received the OBX claim (its balance grew by ~obxAmount−fee).
	if h.takerWallet().Balance() <= takerBefore {
		t.Fatalf("taker OBX balance did not grow (before %d, after %d)", takerBefore, h.takerWallet().Balance())
	}

	// SHARE ISOLATION: the maker holds b,sB and the peer's A,Sa (public). It must
	// NOT hold the taker's PRIVATE a or sA. Verify by checking the maker's stored
	// own shares are b,sB (their public points are NOT the taker's A,Sa) and that
	// the maker's state never carries the taker's private scalars.
	assertShareIsolation(t, maker, taker)
}

// assertShareIsolation proves NEITHER party object ever held BOTH secret shares:
// the maker's own shares (b,sB) differ from the taker's (a,sA), each party stores
// only its own private scalars, and the peer fields hold only PUBLIC points.
func assertShareIsolation(t *testing.T, maker *Maker, taker *Taker) {
	t.Helper()
	// own private scalars
	mb, msB := maker.b, maker.sB
	ta, tsA := taker.a, taker.sA

	// the four private shares must be pairwise distinct (independently minted).
	priv := [][]byte{mb.Bytes(), msB.Bytes(), ta.Bytes(), tsA.Bytes()}
	for i := 0; i < len(priv); i++ {
		for j := i + 1; j < len(priv); j++ {
			if bytes.Equal(priv[i], priv[j]) {
				t.Fatalf("private shares %d and %d collide — shares not independently minted", i, j)
			}
		}
	}

	// the maker must NOT hold the taker's private a or sA anywhere in its state.
	makerSecrets := [][]byte{maker.st.OwnShareClaim, maker.st.OwnShareXNO}
	for _, s := range makerSecrets {
		if bytes.Equal(s, ta.Bytes()) || bytes.Equal(s, tsA.Bytes()) {
			t.Fatal("maker state holds the taker's PRIVATE share — share isolation violated")
		}
	}
	// the taker must NOT hold the maker's private b or sB anywhere in its state.
	takerSecrets := [][]byte{taker.st.OwnShareClaim, taker.st.OwnShareXNO}
	for _, s := range takerSecrets {
		if bytes.Equal(s, mb.Bytes()) || bytes.Equal(s, msB.Bytes()) {
			t.Fatal("taker state holds the maker's PRIVATE share — share isolation violated")
		}
	}
	// the maker's "peer" fields must be PUBLIC points (A=a·G, Sa=sA·G), not the
	// private scalars.
	if bytes.Equal(maker.st.PeerClaimShare, ta.Bytes()) || bytes.Equal(maker.st.PeerXNOShare, tsA.Bytes()) {
		t.Fatal("maker peer field holds a PRIVATE taker scalar")
	}
	wantA := pt(ta).Bytes()
	wantSa := pt(tsA).Bytes()
	if !bytes.Equal(maker.st.PeerClaimShare, wantA) || !bytes.Equal(maker.st.PeerXNOShare, wantSa) {
		t.Fatal("maker peer fields are not the taker's public points")
	}
}

// (b) safe ordering: the taker REFUSES to lock XNO unless the maker's OBX SwapOut
// is on-chain at the agreed key/amount/timelock/binding.
func TestMakerFundsFirstOrderingEnforced(t *testing.T) {
	h := newDevnet(t)
	nano := swapd.NewMockNano()
	id := swapID(0x22)

	maker := NewMaker(id, testOBXAmount, testXNO, testFee, "maker-xno-dest", h)
	taker := NewTaker(id, testOBXAmount, testXNO, testFee, h)

	mc, err := maker.HandleInit(taker.Init())
	if err != nil {
		t.Fatalf("HandleInit: %v", err)
	}
	if err := taker.HandleMakerCommit(mc); err != nil {
		t.Fatalf("HandleMakerCommit: %v", err)
	}

	// taker tries to lock against a Funded message whose SwapOut was NEVER funded
	// on-chain → must be rejected, and NO XNO may be locked.
	bogus := &Funded{SwapID: id, SwapKey: randID(), UnlockHeight: h.Height() + fundOffset}
	if _, err := taker.VerifyFundedAndLock(nano, bogus); err == nil {
		t.Fatal("taker locked XNO against an unfunded OBX SwapOut — ordering NOT enforced")
	}
	if nano.Balance("maker-xno-dest").Sign() != 0 {
		t.Fatal("XNO moved despite the OBX not being funded")
	}

	// now fund for real, but tamper the announced unlock height → still rejected
	// (the on-chain SwapOut's unlock won't match the lied-about value).
	unlock := h.Height() + fundOffset
	funded, err := maker.Fund(unlock)
	if err != nil {
		t.Fatalf("Fund: %v", err)
	}
	tampered := &Funded{SwapID: id, SwapKey: funded.SwapKey, UnlockHeight: unlock + 99}
	if _, err := taker.VerifyFundedAndLock(nano, tampered); err == nil {
		t.Fatal("taker accepted a tampered unlock height")
	}

	// the honest Funded message passes and locks XNO.
	if _, err := taker.VerifyFundedAndLock(nano, funded); err != nil {
		t.Fatalf("honest VerifyFundedAndLock failed: %v", err)
	}
	if taker.State().Phase != PhaseXNOLock {
		t.Fatalf("taker phase = %s, want xno_lock", taker.State().Phase)
	}
}

// (c) abort: the taker never locks XNO (and never claims). The maker reclaims the
// OBX via the refund branch after the timelock.
func TestAbortMakerRefunds(t *testing.T) {
	h := newDevnet(t)
	id := swapID(0x33)

	arbXNO := new(big.Int).SetUint64(testFee + testOBXAmount)
	maker := NewMaker(id, testOBXAmount, arbXNO, testFee, "maker-xno-dest", h)
	taker := NewTaker(id, testOBXAmount, arbXNO, testFee, h)
	// note: amounts only need to agree; xno amount value is irrelevant to the OBX refund.

	mc, err := maker.HandleInit(taker.Init())
	if err != nil {
		t.Fatalf("HandleInit: %v", err)
	}
	if err := taker.HandleMakerCommit(mc); err != nil {
		t.Fatalf("HandleMakerCommit: %v", err)
	}

	unlock := h.Height() + fundOffset
	funded, err := maker.Fund(unlock)
	if err != nil {
		t.Fatalf("Fund: %v", err)
	}
	// the OBX SwapOut is live on-chain (locked, unspent) at the agreed key.
	if _, ok := h.FindSwapOut(funded.SwapKey); !ok {
		t.Fatal("funded SwapOut not on-chain")
	}

	// the taker walks away (never locks XNO). The maker reclaims the OBX via the
	// refund branch once the timelock opens.
	if err := maker.Refund(); err != nil {
		t.Fatalf("Refund: %v", err)
	}
	if maker.State().Phase != PhaseRefunded {
		t.Fatalf("maker phase = %s, want refunded", maker.State().Phase)
	}
	// the swap output is now SPENT (refunded): a second refund must fail because
	// consensus rejects re-spending it.
	if err := maker.Refund(); err == nil {
		t.Fatal("double refund of the same SwapOut unexpectedly succeeded")
	}
}

// (e) NONCE-REUSE GUARD (review fix): the maker must refuse to co-sign a SECOND
// DISTINCT claim core hash under the same committed nonce rb — otherwise a malicious
// taker recovers b = (sb1-sb2)/(e1-e2), gains the full claim key K=A+B, and claims the
// OBX WITHOUT revealing sA (maker loses OBX, XNO frozen). Re-co-signing the SAME core
// hash (a benign network retry) must still succeed and return the identical half.
func TestMakerRefusesSecondDistinctCoSign(t *testing.T) {
	h := newDevnet(t)
	nano := swapd.NewMockNano()
	id := swapID(0x44)

	maker := NewMaker(id, testOBXAmount, testXNO, testFee, "maker-xno-dest", h)
	taker := NewTaker(id, testOBXAmount, testXNO, testFee, h)

	mc, err := maker.HandleInit(taker.Init())
	if err != nil {
		t.Fatalf("HandleInit: %v", err)
	}
	if err := taker.HandleMakerCommit(mc); err != nil {
		t.Fatalf("HandleMakerCommit: %v", err)
	}
	unlock := h.Height() + fundOffset
	funded, err := maker.Fund(unlock)
	if err != nil {
		t.Fatalf("Fund: %v", err)
	}
	locked, err := taker.VerifyFundedAndLock(nano, funded)
	if err != nil {
		t.Fatalf("VerifyFundedAndLock: %v", err)
	}
	if err := maker.ConfirmXNOLock(nano, locked); err != nil {
		t.Fatalf("ConfirmXNOLock: %v", err)
	}

	req, err := taker.BuildClaimRequest()
	if err != nil {
		t.Fatalf("BuildClaimRequest: %v", err)
	}

	// first co-sign → OK
	ps1, err := maker.CoSignClaim(req)
	if err != nil {
		t.Fatalf("first CoSignClaim: %v", err)
	}
	// benign retry of the SAME core hash → OK + identical half
	ps1b, err := maker.CoSignClaim(req)
	if err != nil {
		t.Fatalf("retry CoSignClaim (same core hash) rejected: %v", err)
	}
	if !bytes.Equal(ps1.Sb, ps1b.Sb) {
		t.Fatal("retry produced a DIFFERENT half for the same core hash")
	}

	// ATTACK: a SECOND DISTINCT core hash under the same nonce → MUST be rejected.
	evil := &ClaimRequest{SwapID: id, CoreHash: append([]byte(nil), req.CoreHash...)}
	evil.CoreHash[0] ^= 0xff // flip a bit → a different challenge e
	if _, err := maker.CoSignClaim(evil); err == nil {
		t.Fatal("maker co-signed a SECOND distinct claim under the same nonce — b is leakable")
	}
}
