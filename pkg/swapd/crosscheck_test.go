package swapd

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"testing"

	"filippo.io/edwards25519"

	"obscura/pkg/commit"
)

// TestNanoCrossCheckWithLibrary validates the from-scratch Nano signer against the
// reference nanocurrency-web library (cross-checked BEFORE any real funds move):
//
//	TEST 1: our nanoStateHash + nanoVerify must ACCEPT a block signed by nanocurrency-web
//	        (proves our block-hash preimage AND EdDSA-blake2b verification match the spec).
//	TEST 2: our nanoSign output must be ACCEPTED by nanocurrency-web's verify (proves our
//	        signing matches) — checked by /tmp/nano_verify.js after this test writes go_sig.
//
// Skips cleanly if /tmp/nano_ref.json is absent (run node /tmp/nano_ref.js first).
func TestNanoCrossCheckWithLibrary(t *testing.T) {
	raw, err := os.ReadFile("/tmp/nano_ref.json")
	if err != nil {
		t.Skip("no /tmp/nano_ref.json — run `node /tmp/nano_ref.js` to enable the library cross-check")
	}
	var ref struct {
		AccountPub string `json:"accountPub"`
		Previous   string `json:"previous"`
		RepPub     string `json:"repPub"`
		BalanceRaw string `json:"balanceRaw"`
		LinkHex    string `json:"linkHex"`
		SigHex     string `json:"sigHex"`
	}
	if err := json.Unmarshal(raw, &ref); err != nil {
		t.Fatal(err)
	}
	hx := func(s string) []byte { b, _ := hex.DecodeString(s); return b }
	acct, prev, rep, link, sig := hx(ref.AccountPub), hx(ref.Previous), hx(ref.RepPub), hx(ref.LinkHex), hx(ref.SigHex)
	bal, _ := new(big.Int).SetString(ref.BalanceRaw, 10)

	// TEST 1: our hash + verify accept nanocurrency-web's signature over the SAME block.
	h := nanoStateHash(acct, prev, rep, bal, link)
	if !nanoVerify(acct, h, sig) {
		t.Fatalf("TEST1 FAIL: our nanoStateHash/nanoVerify reject nanocurrency-web's signature — block-hash preimage or signature scheme mismatch (would be rejected by a real node)")
	}
	t.Logf("TEST1 PASS ✓ — our block-hash + EdDSA-blake2b verify accept nanocurrency-web's signature")

	// TEST 2: emit a FULL state block signed by OUR signer; nanocurrency-web's verifyBlock
	// recomputes the hash and verifies the signature EXACTLY like a real node would.
	x := commit.RandomScalar()
	A := new(edwards25519.Point).ScalarBaseMult(x).Bytes()
	addr, _ := EncodeNanoAddress(A)
	repPub, _ := DecodeNanoAddress(defaultNanoRep)
	prevZero := make([]byte, 32)
	linkB := make([]byte, 32)
	for i := range linkB {
		linkB[i] = 0xbb
	}
	balance, _ := new(big.Int).SetString("1000000000000000000000000", 10)
	H := nanoStateHash(A, prevZero, repPub, balance, linkB)
	gblock := map[string]string{
		"type":           "state",
		"account":        addr,
		"previous":       hex.EncodeToString(prevZero),
		"representative": defaultNanoRep,
		"balance":        "1000000000000000000000000",
		"link":           hex.EncodeToString(linkB),
		"signature":      hex.EncodeToString(nanoSign(x, H)),
		"work":           "0000000000000000",
	}
	b, _ := json.Marshal(map[string]any{"pubHex": hex.EncodeToString(A), "block": gblock})
	if err := os.WriteFile("/tmp/go_block.json", b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("TEST2 block -> /tmp/go_block.json (verify with `node /tmp/nano_verify.js`)")
}
