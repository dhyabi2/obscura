package rpc

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"filippo.io/edwards25519"

	"obscura/pkg/swapd"
)

// doBody issues a request with a string body (POST), like do() but with a payload.
func doBody(h http.Handler, method, target, remote, bearer, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.RemoteAddr = remote
	r.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// fakeXNOLedger is a real-backend stand-in: it records Send calls and serves
// fixed balance/receivable so /xno/account reports backend:"real" and
// /xno/withdraw exercises the validate+sign path without a live Nano node.
type fakeXNOLedger struct {
	bal       *big.Int
	recvHash  string
	recvAmt   string
	recvOK    bool
	sentTo    string
	sentAmt   *big.Int
	sendErr   error
	sendCalls int
}

func (f *fakeXNOLedger) Balance(dest string) *big.Int { return f.bal }
func (f *fakeXNOLedger) Receivable(account string) (string, string, bool) {
	return f.recvHash, f.recvAmt, f.recvOK
}
func (f *fakeXNOLedger) Send(sec *edwards25519.Scalar, amt *big.Int, dest string) error {
	f.sendCalls++
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sentTo = dest
	f.sentAmt = new(big.Int).Set(amt)
	return nil
}

// testSeed is a deterministic 32-byte miner seed for the XNO derivation.
var testSeed = []byte("obscura-xno-proceeds-test-seed-32")

// TestXNOAccountPublicMockBackend proves /xno/account is reachable WITHOUT auth
// (remote caller) and reports the derived address + the mock backend when no
// real Nano client is wired.
func TestXNOAccountPublicMockBackend(t *testing.T) {
	s := newTestServer(t)
	s.SetXNO(testSeed, nil) // explicit nil ledger -> mock backend
	h := s.Handler()

	w := do(h, "GET", "/xno/account", "8.8.8.8:5000", "") // remote, no token
	if w.Code != http.StatusOK {
		t.Fatalf("/xno/account remote: got %d want 200", w.Code)
	}
	var resp XNOAccountResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Backend != "mock" {
		t.Fatalf("backend: got %q want mock", resp.Backend)
	}
	_, _, wantAddr, err := swapd.MinerXNOAccount(testSeed)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if resp.Address != wantAddr {
		t.Fatalf("address: got %q want %q", resp.Address, wantAddr)
	}
	if !strings.HasPrefix(resp.Address, "nano_") {
		t.Fatalf("address not a nano_ address: %q", resp.Address)
	}
	if resp.BalanceRaw != "0" || resp.ReceivableRaw != "0" {
		t.Fatalf("mock balances not zero: bal=%q recv=%q", resp.BalanceRaw, resp.ReceivableRaw)
	}
}

// TestXNOAccountRealBackend proves the real ledger's balance/receivable surface
// through /xno/account with backend:"real".
func TestXNOAccountRealBackend(t *testing.T) {
	s := newTestServer(t)
	led := &fakeXNOLedger{bal: big.NewInt(12345), recvAmt: "9999", recvOK: true}
	s.SetXNO(testSeed, led)
	h := s.Handler()

	w := do(h, "GET", "/xno/account", "8.8.8.8:5000", "")
	if w.Code != http.StatusOK {
		t.Fatalf("got %d want 200", w.Code)
	}
	var resp XNOAccountResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Backend != "real" {
		t.Fatalf("backend: got %q want real", resp.Backend)
	}
	if resp.BalanceRaw != "12345" || resp.ReceivableRaw != "9999" {
		t.Fatalf("balances: bal=%q recv=%q", resp.BalanceRaw, resp.ReceivableRaw)
	}
}

// TestXNORecoveryGated proves /xno/recovery is REJECTED for a remote untokened
// caller and reveals the secret for loopback. The secret must match the seed
// derivation and never appear on /xno/account.
func TestXNORecoveryGated(t *testing.T) {
	s := newTestServer(t)
	s.SetXNO(testSeed, nil)
	h := s.Handler()

	// Remote, no token -> forbidden.
	if w := do(h, "GET", "/xno/recovery", "8.8.8.8:5000", ""); w.Code != http.StatusForbidden {
		t.Fatalf("recovery remote: got %d want 403", w.Code)
	}

	// Loopback reveals the secret.
	w := do(h, "GET", "/xno/recovery", "127.0.0.1:5000", "")
	if w.Code != http.StatusOK {
		t.Fatalf("recovery loopback: got %d want 200", w.Code)
	}
	var rec XNORecoveryResponse
	if err := json.NewDecoder(w.Body).Decode(&rec); err != nil {
		t.Fatalf("decode: %v", err)
	}
	sec, _, addr, _ := swapd.MinerXNOAccount(testSeed)
	if rec.Address != addr {
		t.Fatalf("recovery address mismatch")
	}
	if rec.SecretHex != hex.EncodeToString(sec.Bytes()) {
		t.Fatalf("recovery secret does not match seed derivation")
	}

	// The public account endpoint must NEVER carry the secret.
	wa := do(h, "GET", "/xno/account", "8.8.8.8:5000", "")
	if strings.Contains(wa.Body.String(), rec.SecretHex) {
		t.Fatalf("SECRET LEAKED on public /xno/account")
	}
}

// TestXNOWithdrawGated proves /xno/withdraw is rejected without operator auth,
// accepted (and signs) for loopback with a good dest, and validates a bad dest.
func TestXNOWithdrawGated(t *testing.T) {
	s := newTestServer(t)
	led := &fakeXNOLedger{bal: big.NewInt(0)}
	s.SetXNO(testSeed, led)
	h := s.Handler()

	// A valid external nano_ dest (derive a distinct one from another seed).
	_, _, dest, err := swapd.MinerXNOAccount([]byte("some-other-destination-seed-3232"))
	if err != nil {
		t.Fatalf("dest derive: %v", err)
	}
	bodyOK := `{"amount_raw":"1000","dest":"` + dest + `"}`

	// Remote, no token -> forbidden (and must NOT have signed).
	if w := doBody(h, "POST", "/xno/withdraw", "8.8.8.8:5000", "", bodyOK); w.Code != http.StatusForbidden {
		t.Fatalf("withdraw remote: got %d want 403", w.Code)
	}
	if led.sendCalls != 0 {
		t.Fatalf("withdraw signed for a forbidden caller")
	}

	// Loopback + bad dest -> 400, no send.
	if w := doBody(h, "POST", "/xno/withdraw", "127.0.0.1:5000", "", `{"amount_raw":"1000","dest":"not-a-nano-address"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("withdraw bad dest: got %d want 400", w.Code)
	}
	if led.sendCalls != 0 {
		t.Fatalf("withdraw signed despite bad dest")
	}

	// Loopback + good dest -> 200, signs once with the right amount/dest.
	w := doBody(h, "POST", "/xno/withdraw", "127.0.0.1:5000", "", bodyOK)
	if w.Code != http.StatusOK {
		t.Fatalf("withdraw loopback: got %d want 200 (body=%s)", w.Code, w.Body.String())
	}
	if led.sendCalls != 1 || led.sentTo != dest || led.sentAmt.String() != "1000" {
		t.Fatalf("withdraw did not sign correctly: calls=%d to=%q amt=%v", led.sendCalls, led.sentTo, led.sentAmt)
	}
}

// TestXNOWithdrawMockBackendRefused proves withdraw refuses on the mock backend
// (no real Nano) even for a loopback operator.
func TestXNOWithdrawMockBackendRefused(t *testing.T) {
	s := newTestServer(t)
	s.SetXNO(testSeed, nil) // mock backend
	h := s.Handler()
	_, _, dest, _ := swapd.MinerXNOAccount([]byte("some-other-destination-seed-3232"))
	body := `{"amount_raw":"1000","dest":"` + dest + `"}`
	if w := doBody(h, "POST", "/xno/withdraw", "127.0.0.1:5000", "", body); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("withdraw on mock backend: got %d want 503", w.Code)
	}
}
