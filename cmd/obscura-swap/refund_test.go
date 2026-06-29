package main

import (
	"math/big"
	"strings"
	"testing"

	"obscura/pkg/chain"
	"obscura/pkg/commit"
	"obscura/pkg/config"
	"obscura/pkg/swapd"
	"obscura/pkg/wallet"
)

// TestSwapRefundOnPreClaimFailure proves the OBX refund branch is actually executable
// and accepted by consensus (#7/#8). We fund the OBX swap output, then force a pre-claim
// failure (a corrupted joint key so the #12 pre-mine check rejects before the claim is
// mined). doAtomicSwap must then reclaim the locked OBX via refundOBX — the success
// sentinel in the returned error proves the refund spend was built, mined, and accepted
// by SwapOutput.VerifyRefund at the unlock height.
func TestSwapRefundOnPreClaimFailure(t *testing.T) {
	oldM, oldW, oldRM := config.CoinbaseMaturity, config.SwapTimelockWindow, config.SwapReorgMargin
	config.CoinbaseMaturity = 1
	config.SwapTimelockWindow = 4 // small window so the refund march is a few blocks, not 200
	// F-1 fund-safety: the consensus fund check rejects UnlockHeight < fundHeight +
	// SwapReorgMargin (a provably-dead claim window). With the tiny SwapTimelockWindow=4
	// above, shrink the margin to 1 so the small-window refund fixture still funds
	// (unlock = height+4 >= height+1). This is a refund test — the claim window length
	// is irrelevant, only that the output is fundable and refundable after the timelock.
	config.SwapReorgMargin = 1
	defer func() {
		config.CoinbaseMaturity, config.SwapTimelockWindow, config.SwapReorgMargin = oldM, oldW, oldRM
	}()

	c, err := chain.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	funder := wallet.FromSeed([]byte("refund-test-funder-00000000000000"))
	claimer := wallet.FromSeed([]byte("refund-test-claimr-00000000000000"))
	if err := fund(c, funder, 6); err != nil {
		t.Fatal(err)
	}

	mock := swapd.NewMockNano()
	obxAmt := 3 * config.AtomicPerCoin
	fee := uint64(1_000_000_000)
	sec, err := newSecrets()
	if err != nil {
		t.Fatal(err)
	}
	lock, _ := mock.Lock(big.NewInt(10_000), sec.xnoPub)

	// Corrupt the joint pubkey AFTER locking so the pre-mine key check (#12) fails and the
	// claim is never mined — exercising the funded-but-unclaimed refund path.
	sec.xnoPub = pt(commit.RandomScalar()).Bytes()

	err = doAtomicSwap(c, mock, funder, claimer, obxAmt, fee, sec, lock, "dest", nil)
	if err == nil {
		t.Fatal("expected the swap to fail on the corrupted key and refund")
	}
	if !strings.Contains(err.Error(), "refunded to the funder") {
		t.Fatalf("expected the refund branch to execute, got: %v", err)
	}
	// The claimer never received the OBX (claim was never mined).
	scanAll(c, claimer)
	if claimer.Balance() != 0 {
		t.Fatalf("claimer should hold 0 OBX (claim never mined), has %d", claimer.Balance())
	}
}
