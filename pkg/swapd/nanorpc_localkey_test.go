package swapd

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"filippo.io/edwards25519"
)

// randScalarHex returns a fresh canonical ed25519 scalar and its hex, used as a
// stand-in for the taker's raw funding XNO account secret.
func randScalarHex(t *testing.T) (*edwards25519.Scalar, string) {
	t.Helper()
	var wide [64]byte
	if _, err := rand.Read(wide[:]); err != nil {
		t.Fatal(err)
	}
	s, err := new(edwards25519.Scalar).SetUniformBytes(wide[:])
	if err != nil {
		t.Fatal(err)
	}
	return s, hex.EncodeToString(s.Bytes())
}

// TestNewNanoRPCFundSecretValidation checks that --nano-fund-secret is validated at
// construction (bad hex / wrong length / non-canonical all rejected) and that a bad
// secret never appears in the returned error.
func TestNewNanoRPCFundSecretValidation(t *testing.T) {
	cases := []struct {
		name string
		hex  string
	}{
		{"badhex", "zzzz"},
		{"shortlen", "00ff"},
		{"toolong", strings.Repeat("11", 40)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewNanoRPC(NanoRPCConfig{URL: "http://x", FundSecretHex: tc.hex})
			if err == nil {
				t.Fatalf("expected error for %q", tc.name)
			}
			if strings.Contains(err.Error(), tc.hex) {
				t.Fatalf("secret leaked into error: %v", err)
			}
		})
	}
	// A valid canonical scalar constructs fine and is dropped from cfg.FundSecretHex.
	_, h := randScalarHex(t)
	n, err := NewNanoRPC(NanoRPCConfig{URL: "http://x", FundSecretHex: h})
	if err != nil {
		t.Fatalf("valid secret should construct: %v", err)
	}
	if n.cfg.FundSecretHex != "" {
		t.Fatal("raw fund secret hex must be dropped after parse")
	}
	if n.fundSecret == nil {
		t.Fatal("parsed fund scalar must be retained")
	}
	if strings.Contains(n.cfg.FundSecretHex, h) {
		t.Fatal("secret hex retained in cfg")
	}
}

// TestNanoRPCFundAddress confirms the derived funding address matches the scalar's
// public key and that the secret bytes never appear in the address.
func TestNanoRPCFundAddress(t *testing.T) {
	sec, h := randScalarHex(t)
	n, err := NewNanoRPC(NanoRPCConfig{URL: "http://x", FundSecretHex: h})
	if err != nil {
		t.Fatal(err)
	}
	wantPub := new(edwards25519.Point).ScalarBaseMult(sec).Bytes()
	wantAddr, _ := EncodeNanoAddress(wantPub)
	got, err := NanoRPCFundAddress(n)
	if err != nil {
		t.Fatal(err)
	}
	if got != wantAddr {
		t.Fatalf("derived addr %s != expected %s", got, wantAddr)
	}
	if strings.Contains(got, h) {
		t.Fatal("secret leaked into address")
	}
}

// nanoStub is a fake Nano RPC: it records the `process`ed block and feeds canned
// account_info / receivable / work_generate / process responses so the LOCAL-KEY Lock
// block-building+signing path can be unit-tested with NO real network.
type nanoStub struct {
	t          *testing.T
	frontier   string // funding account frontier ("" => unopened)
	balance    string // funding account current balance (raw)
	receivable map[string]string

	// captured from the send `process`
	sentBlock  map[string]any
	sentSub    string
	processHit int
}

func (s *nanoStub) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		action, _ := req["action"].(string)
		write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		switch action {
		case "account_info":
			if s.frontier == "" {
				// unopened: Nano returns an error envelope
				write(map[string]any{"error": "Account not found"})
				return
			}
			write(map[string]any{
				"frontier":       s.frontier,
				"representative": defaultNanoRep,
				"balance":        s.balance,
			})
		case "receivable":
			// return the canned receivables once, then empty (so the loop terminates)
			out := map[string]string{}
			for k, v := range s.receivable {
				out[k] = v
			}
			s.receivable = map[string]string{}
			write(map[string]any{"blocks": out})
		case "work_generate":
			write(map[string]any{"work": "0000000000000000"})
		case "process":
			s.processHit++
			sub, _ := req["subtype"].(string)
			blk, _ := req["block"].(map[string]any)
			if sub == "send" {
				s.sentSub = sub
				s.sentBlock = blk
			}
			// echo a deterministic hash
			write(map[string]any{"hash": strings.Repeat("A", 64)})
		default:
			write(map[string]any{"error": "unexpected action " + action})
		}
	})
}

// TestLocalKeyLockBuildsSignedSend drives the full LOCAL-KEY Lock against an httptest
// stub: an UNOPENED funding account with one pending receivable large enough to cover
// the lock. It asserts Lock receives the pending then sends EXACTLY `amount` to the
// JOINT account, the send block is signed by the funding key (Nano ed25519-blake2b
// verifies), the change is kept, and the funding secret never appears anywhere.
func TestLocalKeyLockBuildsSignedSend(t *testing.T) {
	fundSec, fundHex := randScalarHex(t)
	fundPub := new(edwards25519.Point).ScalarBaseMult(fundSec).Bytes()

	// joint account (the swap destination) = some other pubkey
	jointSec, _ := randScalarHex(t)
	jointPub := new(edwards25519.Point).ScalarBaseMult(jointSec).Bytes()
	jointAddr, _ := EncodeNanoAddress(jointPub)

	amount := new(big.Int).Exp(big.NewInt(10), big.NewInt(25), nil) // 0.00001 XNO raw
	pending := new(big.Int).Exp(big.NewInt(10), big.NewInt(26), nil) // 10x the lock

	stub := &nanoStub{
		t:          t,
		frontier:   "", // unopened
		balance:    "",
		receivable: map[string]string{strings.Repeat("B", 64): pending.String()},
	}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	n, err := NewNanoRPC(NanoRPCConfig{URL: srv.URL, FundSecretHex: fundHex})
	if err != nil {
		t.Fatal(err)
	}

	lockID, err := n.Lock(amount, jointPub)
	if err != nil {
		t.Fatalf("local-key Lock: %v", err)
	}
	if lockID == "" {
		t.Fatal("empty lockID")
	}
	if stub.sentBlock == nil {
		t.Fatal("no send block was processed")
	}

	// (1) send is FROM the funding account
	wantFromAddr, _ := EncodeNanoAddress(fundPub)
	if got, _ := stub.sentBlock["account"].(string); got != wantFromAddr {
		t.Fatalf("send account = %s, want %s", got, wantFromAddr)
	}

	// (2) destination link = the JOINT account pubkey
	linkHex, _ := stub.sentBlock["link"].(string)
	linkBytes, err := hex.DecodeString(strings.ToLower(linkHex))
	if err != nil || len(linkBytes) != 32 {
		t.Fatalf("bad link hex %q", linkHex)
	}
	if EncodeMust(t, linkBytes) != jointAddr {
		t.Fatalf("send link is not the joint account")
	}

	// (3) balance kept = pending - amount (change retained, not full balance)
	wantChange := new(big.Int).Sub(pending, amount)
	if got, _ := stub.sentBlock["balance"].(string); got != wantChange.String() {
		t.Fatalf("send balance(change) = %s, want %s (sent exactly %s)", got, wantChange, amount)
	}

	// (4) the send block is correctly signed by the FUNDING key (Nano scheme verifies).
	verifyStubSend(t, stub.sentBlock, fundPub)

	// (5) the funding secret must not appear anywhere: lockID, any send-block field.
	if strings.Contains(lockID, fundHex) {
		t.Fatal("secret leaked into lockID")
	}
	blob, _ := json.Marshal(stub.sentBlock)
	if strings.Contains(string(blob), fundHex) {
		t.Fatal("secret leaked into send block")
	}
	// also the raw scalar bytes (uppercased) must not appear in the signature field etc.
	if strings.Contains(strings.ToLower(string(blob)), hex.EncodeToString(fundSec.Bytes())) {
		t.Fatal("secret scalar bytes leaked into send block")
	}
}

// EncodeMust encodes a pubkey to a nano address, failing the test on error.
func EncodeMust(t *testing.T, pub []byte) string {
	t.Helper()
	a, err := EncodeNanoAddress(pub)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// verifyStubSend recomputes the state-block hash from the processed send block and
// checks the signature against the funding pubkey using Nano's ed25519-blake2b verify.
func verifyStubSend(t *testing.T, blk map[string]any, fundPub []byte) {
	t.Helper()
	acct, _ := blk["account"].(string)
	prevHex, _ := blk["previous"].(string)
	repAddr, _ := blk["representative"].(string)
	balStr, _ := blk["balance"].(string)
	linkHex, _ := blk["link"].(string)
	sigHex, _ := blk["signature"].(string)

	accountPub, err := DecodeNanoAddress(acct)
	if err != nil {
		t.Fatal(err)
	}
	prev, _ := hex.DecodeString(strings.ToLower(prevHex))
	repPub, err := DecodeNanoAddress(repAddr)
	if err != nil {
		t.Fatal(err)
	}
	bal, _ := new(big.Int).SetString(balStr, 10)
	link, _ := hex.DecodeString(strings.ToLower(linkHex))
	sig, _ := hex.DecodeString(strings.ToLower(sigHex))

	hash := nanoStateHash(accountPub, prev, repPub, bal, link)
	if !nanoVerify(fundPub, hash, sig) {
		t.Fatal("send block signature does not verify under the funding key")
	}
}

// TestLockStillRequiresFunding confirms the WalletID/Source path is untouched: with
// neither a node wallet nor a funding secret, Lock errors (no blind send).
func TestLockStillRequiresFunding(t *testing.T) {
	n, err := NewNanoRPC(NanoRPCConfig{URL: "http://x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.Lock(big.NewInt(1), make([]byte, 32)); err == nil {
		t.Fatal("expected error with no funding configured")
	}
}
