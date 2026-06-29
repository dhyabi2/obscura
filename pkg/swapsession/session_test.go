package swapsession

import (
	"context"
	"testing"

	"obscura/pkg/chain"
	"obscura/pkg/config"
	"obscura/pkg/miner"
	"obscura/pkg/swap"
	"obscura/pkg/tx"
	"obscura/pkg/wallet"
)

// ---- OBX test host: a thin adapter binding chain+wallet+miner to the session's
// MakerOBX / TakerOBX interfaces. It mirrors cmd/obscura-swap's mineWith devnet
// path (isolated in-process chain, no network).

type obxHost struct {
	c      *chain.Chain
	funder *wallet.Wallet // mines coinbase + funds/refunds (maker side)
	taker  *wallet.Wallet // receives the OBX claim
}

func (h *obxHost) Height() uint64 { return h.c.Height() }

func (h *obxHost) FindSwapOut(swapKey []byte) (swap.SwapOutput, bool) {
	e, ok := h.c.Swap(swapKey)
	if !ok {
		return swap.SwapOutput{}, false
	}
	return swap.SwapOutput{
		ClaimKey: e.ClaimKey, RefundKey: e.RefundKey, UnlockHeight: e.UnlockHeight,
		ClaimR: e.ClaimR, ClaimT: e.ClaimT,
		// F2: the AUTHORITATIVE on-chain locked value (SwapEntry.Amount is enforced by
		// the funding conservation proof in pkg/chain/validate.go — not funder-reported).
		Amount: e.Amount,
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
	// build with a placeholder sign that just captures the core hash; the real sig
	// is attached later in MineClaim (after the maker co-signs).
	t, err := h.taker.BuildSwapSpend(swapKey, obxAmount, false, fee, func(ch []byte) []byte {
		coreHash = append([]byte(nil), ch...)
		return make([]byte, 64) // placeholder, replaced before mining
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

// FindSwapSpend scans the chain for the mined CLAIM spend of swapKey and returns
// the published full claim signature plus the core hash it signed (F-B scrape).
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

var errTest = errTestErr("swapsession test: mining failed")

type errTestErr string

func (e errTestErr) Error() string { return string(e) }

// ---- mock Nano (reused via the swapd.MockNano semantics, but local to avoid the
// import cycle of capabilities — we use the real swapd.MockNano).

// newDevnet builds an isolated OBX devnet with the maker (funder) funded enough to
// fund the swap, plus a taker wallet to receive the OBX claim.
func newDevnet(t *testing.T) *obxHost {
	t.Helper()
	old := config.CoinbaseMaturity
	config.CoinbaseMaturity = 1
	// Swap fund-safety config for these fast tests. We keep BOTH knobs of the F-1
	// invariant non-trivial and internally consistent (rather than the old margin=2 +
	// tiny offsets that MASKED the unclaimable-window bug, review finding F-2): the
	// reorg margin AND the minimum open claim window are both > 0, so a funded SwapOut
	// must leave UnlockHeight >= fundHeight + margin + minWindow to be claimable. The
	// flow tests fund at offset +8 (see fundOffset), comfortably above the required
	// 1 + margin + minWindow = 6 after the fund block is mined.
	oldMargin, oldMinWin := config.SwapReorgMargin, config.SwapMinClaimWindow
	config.SwapReorgMargin = 2
	config.SwapMinClaimWindow = 3
	t.Cleanup(func() {
		config.CoinbaseMaturity = old
		config.SwapReorgMargin = oldMargin
		config.SwapMinClaimWindow = oldMinWin
	})

	dir := t.TempDir()
	c, err := chain.New(dir)
	if err != nil {
		t.Fatalf("chain: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	maker := wallet.FromSeed([]byte("swapsession-maker-00000000000000000"))
	taker := wallet.FromSeed([]byte("swapsession-taker-00000000000000000"))
	h := &obxHost{c: c, funder: maker, taker: taker}
	// fund the maker with several coinbase blocks (low count: the class-group
	// accumulator makes mining slow, so keep it minimal).
	for i := 0; i < 4; i++ {
		if err := h.mine(nil); err != nil {
			t.Fatalf("fund maker: %v", err)
		}
	}
	return h
}

func (h *obxHost) makerWallet() *wallet.Wallet { return h.funder }
func (h *obxHost) takerWallet() *wallet.Wallet { return h.taker }
