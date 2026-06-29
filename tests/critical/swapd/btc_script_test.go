// Package swapd_test, BTC leg: the on-chain HTLC SCRIPT builder is real and tested.
// The BTC swap orchestration + node backend are NOT implemented — BTC is gated OFF
// in config.SettleableAssets until a real backend lands (docs/INVENTION_CROSSCHAIN_SWAPS.md).
package swapd_test

import (
	"bytes"
	"testing"

	"obscura/pkg/swapd"
)

// TestBtcHTLCScript sanity-checks the real P2WSH redeem script structure.
func TestBtcHTLCScript(t *testing.T) {
	hash := swapd.HashPreimage([]byte("x"))
	redeem := append([]byte{0x02}, bytes.Repeat([]byte{0x11}, 32)...)
	refund := append([]byte{0x03}, bytes.Repeat([]byte{0x22}, 32)...)
	s, err := swapd.BtcHTLCScript(hash, redeem, refund, 750_000)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []byte{0x63, 0xa8, 0xb1, 0x67, 0x68} { // OP_IF/SHA256/CLTV/ELSE/ENDIF
		if !bytes.Contains(s, []byte{op}) {
			t.Fatalf("script missing opcode 0x%02x", op)
		}
	}
	if !bytes.Contains(s, hash) {
		t.Fatal("script does not commit the hashlock")
	}
	if wp := swapd.BtcWitnessProgram(s); len(wp) != 32 {
		t.Fatalf("witness program must be 32 bytes, got %d", len(wp))
	}
	var zero [32]byte
	if _, err := swapd.BtcHTLCScript(zero[:31], redeem, refund, 1); err == nil {
		t.Fatal("accepted a 31-byte hash")
	}
}
