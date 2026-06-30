package takerdrive_test

import (
	"context"
	"math/big"
	"testing"
	"time"

	"obscura/pkg/chain"
	"obscura/pkg/commit"
	"obscura/pkg/config"
	"obscura/pkg/miner"
	"obscura/pkg/swap"
	"obscura/pkg/swapd"
	"obscura/pkg/swapnet"
	"obscura/pkg/swapsession"
	"obscura/pkg/takerdrive"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
)

// TestKindWireCompat pins takerdrive's local Kind bytes to pkg/swapnet's wire
// Kind constants. takerdrive deliberately does NOT import swapnet (to stay
// wasm-safe), so this guard is the single point that keeps the node relay able to
// forward takerdrive envelopes onto the existing p2p swap transport unchanged.
func TestKindWireCompat(t *testing.T) {
	cases := []struct {
		name string
		got  takerdrive.Kind
		want swapnet.Kind
	}{
		{"Init", takerdrive.KindInit, swapnet.KindInit},
		{"MakerCommit", takerdrive.KindMakerCommit, swapnet.KindMakerCommit},
		{"Funded", takerdrive.KindFunded, swapnet.KindFunded},
		{"XNOLocked", takerdrive.KindXNOLocked, swapnet.KindXNOLocked},
		{"ClaimRequest", takerdrive.KindClaimRequest, swapnet.KindClaimRequest},
		{"ClaimPreSig", takerdrive.KindClaimPreSig, swapnet.KindClaimPreSig},
		{"Abort", takerdrive.KindAbortInternal, swapnet.KindAbort},
		{"ClaimDone", takerdrive.KindClaimDone, swapnet.KindClaimDone},
	}
	for _, c := range cases {
		if byte(c.got) != byte(c.want) {
			t.Errorf("%s: takerdrive=%d swapnet=%d — wire kinds diverge", c.name, c.got, c.want)
		}
	}
}

// TestRunTakerFullSwap drives a COMPLETE real-crypto XNO↔OBX swap through
// takerdrive.RunTaker (the browser-side taker loop) against a real
// swapsession.Maker, over an in-memory transport, on an in-process OBX chain with
// a shared MockNano. It proves the browser-hosted taker interoperates with the
// unchanged protocol: the taker claims its OBX and the maker sweeps the XNO.
func TestRunTakerFullSwap(t *testing.T) {
	h := newDevnet(t)
	nano := swapd.NewMockNano()
	id := swapID(0xA1)

	obxAmount := 3 * config.AtomicPerCoin     // 3 OBX (matches swapsession test sizing)
	const fee = uint64(1_000_000_000)         // 0.001 OBX — covers MinFeePerByte for the large swap txs
	xno := big.NewInt(10_000)                 // abstract MockNano raw units (exact-equality checked)

	// in-memory directed transport pair: taker <-> maker.
	t2m := make(chan env, 8) // taker -> maker
	m2t := make(chan env, 8) // maker -> taker
	takerTr := &memTransport{out: t2m, in: m2t}

	// run the TAKER (package under test) on a goroutine; drive the real
	// swapsession.Maker inline so a maker-side error surfaces immediately instead
	// of deadlocking the taker's blocking Recv.
	takerOBXBefore := h.taker.Balance()
	takerErr := make(chan error, 1)
	go func() {
		takerErr <- takerdrive.RunTaker(
			takerdrive.Params{SwapID: id, OBXAmount: obxAmount, XNOAmount: xno, Fee: fee},
			h, nano, takerTr, func(string) {},
		)
	}()

	if err := runMaker(h, nano, id, obxAmount, xno, fee, t2m, m2t); err != nil {
		t.Fatalf("maker: %v", err)
	}
	select {
	case err := <-takerErr:
		if err != nil {
			t.Fatalf("RunTaker: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("taker did not finish")
	}

	// the taker received its OBX (claim mined into its wallet).
	h.scan()
	if h.taker.Balance() <= takerOBXBefore {
		t.Fatalf("taker OBX balance did not grow: before=%d after=%d", takerOBXBefore, h.taker.Balance())
	}
	// the maker swept the agreed XNO to its destination.
	if got := nano.Balance(makerXNODest); got.Cmp(xno) != 0 {
		t.Fatalf("maker XNO swept = %s, want %s", got, xno)
	}
}

const makerXNODest = "maker-xno-dest"

// runMaker is a faithful inline maker: it mirrors swapnet.driveMaker's happy path
// using the real swapsession.Maker, recovering sA INDEPENDENTLY from the on-chain
// claim (it never needs the taker's ClaimDone for safety).
func runMaker(h *obxHost, nano *swapd.MockNano, id [32]byte, obxAmount uint64, xno *big.Int, fee uint64, in <-chan env, out chan<- env) error {
	maker := swapsession.NewMaker(id, obxAmount, xno, fee, makerXNODest, h)

	// recv Init -> MakerCommit
	e := <-in
	init, err := swapsession.ParseInit(e.payload)
	if err != nil {
		return err
	}
	mc, err := maker.HandleInit(init)
	if err != nil {
		return err
	}
	out <- env{takerdrive.KindMakerCommit, mc.Serialize()}

	// fund OBX -> Funded
	unlock := h.Height() + 8
	funded, err := maker.Fund(unlock)
	if err != nil {
		return err
	}
	out <- env{takerdrive.KindFunded, funded.Serialize()}

	// recv XNOLocked -> confirm
	e = <-in
	xl, err := swapsession.ParseXNOLocked(e.payload)
	if err != nil {
		return err
	}
	if err := maker.ConfirmXNOLock(nano, xl); err != nil {
		return err
	}

	// recv ClaimRequest -> co-sign
	e = <-in
	cr, err := swapsession.ParseClaimRequest(e.payload)
	if err != nil {
		return err
	}
	ps, err := maker.CoSignClaim(cr)
	if err != nil {
		return err
	}
	out <- env{takerdrive.KindClaimPreSig, ps.Serialize()}

	// wait for the courtesy ClaimDone (signals the taker has mined the claim), then
	// recover sA from the on-chain claim and sweep — the independent path.
	<-in
	fullSigBytes, _, ok := h.FindSwapSpend(maker.State().SwapKey)
	if !ok {
		return errTest
	}
	full, err := commit.ParseFullSig(fullSigBytes)
	if err != nil {
		return err
	}
	return maker.SweepXNOIndependent(nano, full)
}

// ---- in-memory transport ----------------------------------------------------

type env struct {
	kind    takerdrive.Kind
	payload []byte
}

type memTransport struct {
	out chan<- env
	in  <-chan env
}

func (m *memTransport) Send(k takerdrive.Kind, p []byte) error {
	m.out <- env{k, append([]byte(nil), p...)}
	return nil
}

func (m *memTransport) Recv() (takerdrive.Kind, []byte, error) {
	e := <-m.in
	return e.kind, e.payload, nil
}

// ---- OBX test host (mirrors pkg/swapsession's session_test obxHost) ----------

type obxHost struct {
	c      *chain.Chain
	funder *wallet.Wallet
	taker  *wallet.Wallet
}

func (h *obxHost) Height() uint64 { return h.c.Height() }

func (h *obxHost) FindSwapOut(swapKey []byte) (swap.SwapOutput, bool) {
	e, ok := h.c.Swap(swapKey)
	if !ok {
		return swap.SwapOutput{}, false
	}
	return swap.SwapOutput{
		ClaimKey: e.ClaimKey, RefundKey: e.RefundKey, UnlockHeight: e.UnlockHeight,
		ClaimR: e.ClaimR, ClaimT: e.ClaimT, Amount: e.Amount,
	}, true
}

func (h *obxHost) mine(txs []*tx.Transaction) error {
	fees := chain.CollectedFees(txs)
	cb, err := h.funder.BuildCoinbase(h.c.Height()+1, h.c.ExpectedCoinbaseMinted(fees, nil), nil)
	if err != nil {
		return err
	}
	all := append([]*tx.Transaction{cb}, txs...)
	tmpl, err := h.c.BlockTemplate(all)
	if err != nil {
		return err
	}
	if !miner.Mine(context.Background(), tmpl, 0) {
		return errTest
	}
	if err := h.c.AddBlock(tmpl); err != nil {
		return err
	}
	h.scan()
	return nil
}

func (h *obxHost) scan() {
	for hh := uint64(0); hh <= h.c.Height(); hh++ {
		if b, ok := h.c.BlockByHeight(hh); ok {
			h.funder.ScanBlock(b)
			h.taker.ScanBlock(b)
		}
	}
}

func (h *obxHost) FundSwapOut(swapKey []byte, obxAmount uint64, claimKey, refundKey, claimA, claimB, popA, popB, claimR, claimT []byte, unlockHeight, fee uint64) error {
	fund, err := h.funder.FundSwap(h.c, swapKey, obxAmount, claimKey, refundKey, claimA, claimB, popA, popB, claimR, claimT, unlockHeight, fee)
	if err != nil {
		return err
	}
	return h.mine([]*tx.Transaction{fund})
}

func (h *obxHost) BuildClaim(swapKey []byte, obxAmount, fee uint64) (*tx.Transaction, []byte, error) {
	var coreHash []byte
	t, err := h.taker.BuildSwapSpend(swapKey, obxAmount, false, fee, func(ch []byte) []byte {
		coreHash = append([]byte(nil), ch...)
		return make([]byte, 64)
	})
	if err != nil {
		return nil, nil, err
	}
	return t, coreHash, nil
}

func (h *obxHost) MineClaim(t *tx.Transaction, sig []byte) error {
	t.SwapInputs[0].Sig = sig
	return h.mine([]*tx.Transaction{t})
}

func (h *obxHost) FindSwapSpend(swapKey []byte) (fullSig []byte, coreHash []byte, ok bool) {
	for hh := uint64(0); hh <= h.c.Height(); hh++ {
		b, got := h.c.BlockByHeight(hh)
		if !got {
			continue
		}
		for _, t := range b.Txs {
			for _, in := range t.SwapInputs {
				if in.IsRefund || string(in.SwapKey) != string(swapKey) {
					continue
				}
				ch := t.CoreHash()
				return append([]byte(nil), in.Sig...), append([]byte(nil), ch[:]...), true
			}
		}
	}
	return nil, nil, false
}

func (h *obxHost) MineRefund(swapKey []byte, obxAmount, fee, unlockHeight uint64, sign func(coreHash []byte) []byte) error {
	for h.c.Height() < unlockHeight {
		if err := h.mine(nil); err != nil {
			return err
		}
	}
	refund, err := h.funder.BuildSwapSpend(swapKey, obxAmount, true, fee, sign)
	if err != nil {
		return err
	}
	return h.mine([]*tx.Transaction{refund})
}

type errString string

func (e errString) Error() string { return string(e) }

const errTest = errString("takerdrive test: mining failed")

func swapID(b byte) [32]byte {
	var id [32]byte
	for i := range id {
		id[i] = b
	}
	return id
}

func newDevnet(t *testing.T) *obxHost {
	t.Helper()
	oldMat := config.CoinbaseMaturity
	oldMargin, oldMinWin := config.SwapReorgMargin, config.SwapMinClaimWindow
	oldDiff := config.GenesisDifficulty
	config.CoinbaseMaturity = 1
	config.SwapReorgMargin = 2
	config.SwapMinClaimWindow = 3
	config.GenesisDifficulty = 16 // low PoW for fast test mining (matches pkg/chain tests)
	t.Cleanup(func() {
		config.CoinbaseMaturity = oldMat
		config.SwapReorgMargin = oldMargin
		config.SwapMinClaimWindow = oldMinWin
		config.GenesisDifficulty = oldDiff
	})

	dir := t.TempDir()
	c, err := chain.New(dir)
	if err != nil {
		t.Fatalf("chain: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	maker := wallet.FromSeed([]byte("takerdrive-maker-00000000000000000"))
	taker := wallet.FromSeed([]byte("takerdrive-taker-00000000000000000"))
	h := &obxHost{c: c, funder: maker, taker: taker}
	for i := 0; i < 4; i++ {
		if err := h.mine(nil); err != nil {
			t.Fatalf("fund maker: %v", err)
		}
	}
	return h
}
