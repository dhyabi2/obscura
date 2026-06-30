package nanorpc

import (
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"obscura/pkg/nanocrypto"
)

// fakeNano is a minimal Nano RPC stand-in that routes by the "action" field and
// records the requests it received, so tests can assert exactly what nanorpc sent.
type fakeNano struct {
	t        *testing.T
	requests []map[string]any
	handler  func(action string, req map[string]any) any
}

func (f *fakeNano) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		f.t.Fatalf("server got non-JSON body: %s", body)
	}
	f.requests = append(f.requests, req)
	action, _ := req["action"].(string)
	resp := f.handler(action, req)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func newFake(t *testing.T, h func(action string, req map[string]any) any) (*Client, *fakeNano) {
	f := &fakeNano{t: t, handler: h}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c, err := New(Config{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return c, f
}

func TestNewRequiresURL(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected error for empty URL")
	}
	if _, err := NewMulti([]Config{{}, {URL: ""}}); err == nil {
		t.Fatal("expected error when no usable endpoint URL")
	}
}

// TestFailoverAcrossEndpoints proves the client advances to the next endpoint on a
// transport failure (here: a dead primary) and succeeds on a live secondary, while
// a real Nano {"error":...} envelope is returned immediately without failover.
func TestFailoverAcrossEndpoints(t *testing.T) {
	// live secondary
	var secondaryHits int
	good := &fakeNano{t: t, handler: func(action string, req map[string]any) any {
		secondaryHits++
		return map[string]any{"frontier": strings.Repeat("A", 64), "representative": nanocrypto.DefaultRep, "balance": "7"}
	}}
	goodSrv := httptest.NewServer(good)
	defer goodSrv.Close()

	// dead primary: a URL that refuses connections
	deadURL := "http://127.0.0.1:1" // nothing listens on port 1

	c, err := NewMulti([]Config{{URL: deadURL}, {URL: goodSrv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	info, err := c.AccountInfo("nano_x")
	if err != nil {
		t.Fatalf("failover should have reached the live secondary: %v", err)
	}
	if info.Balance.Cmp(big.NewInt(7)) != 0 || secondaryHits == 0 {
		t.Fatalf("secondary not used (hits=%d, bal=%v)", secondaryHits, info.Balance)
	}

	// a Nano error envelope on the primary must NOT fail over (it is a real answer).
	var primaryHits, secHits int
	errPrimary := httptest.NewServer(&fakeNano{t: t, handler: func(a string, r map[string]any) any {
		primaryHits++
		return map[string]any{"error": "Bad account number"}
	}})
	defer errPrimary.Close()
	secondary := httptest.NewServer(&fakeNano{t: t, handler: func(a string, r map[string]any) any {
		secHits++
		return map[string]any{"frontier": "", "balance": "0"}
	}})
	defer secondary.Close()
	c2, _ := NewMulti([]Config{{URL: errPrimary.URL}, {URL: secondary.URL}})
	// block_info surfaces a Nano error envelope as an error; it must come from the
	// primary and the secondary must NOT be tried.
	if _, err := c2.BlockInfo("deadbeef"); err == nil {
		t.Fatal("expected the primary's Nano error envelope")
	}
	if secHits != 0 {
		t.Fatalf("a real Nano error must NOT fail over (secondary hit %d times)", secHits)
	}
}

func TestAccountInfoOpenedAndNotFound(t *testing.T) {
	c, _ := newFake(t, func(action string, req map[string]any) any {
		if req["account"] == "nano_missing" {
			return map[string]any{"error": "Account not found"}
		}
		return map[string]any{
			"frontier":       strings.Repeat("A", 64),
			"representative": nanocrypto.DefaultRep,
			"balance":        "1000000000000000000000000",
		}
	})

	info, err := c.AccountInfo("nano_present")
	if err != nil {
		t.Fatal(err)
	}
	if !info.Opened || info.Balance.Cmp(mustBig("1000000000000000000000000")) != 0 {
		t.Fatalf("unexpected opened account info: %+v", info)
	}

	missing, err := c.AccountInfo("nano_missing")
	if err != nil {
		t.Fatalf("not-found must NOT be an error: %v", err)
	}
	if missing.Opened || missing.Balance.Sign() != 0 {
		t.Fatalf("missing account should be unopened with zero balance: %+v", missing)
	}
}

func TestBlockInfoAndReceivable(t *testing.T) {
	c, _ := newFake(t, func(action string, req map[string]any) any {
		switch action {
		case "block_info":
			return map[string]any{
				"amount":    "5000000000000000000000000",
				"subtype":   "send",
				"confirmed": "true",
				"contents": map[string]any{
					"link":            strings.Repeat("BB", 32),
					"link_as_account": "nano_dest",
				},
			}
		case "receivable":
			return map[string]any{"blocks": map[string]any{
				"HASH_SMALL": "10",
				"HASH_BIG":   "9000000000000000000000000",
			}}
		}
		return map[string]any{}
	})

	bi, err := c.BlockInfo("somehash")
	if err != nil {
		t.Fatal(err)
	}
	if !bi.Confirmed || bi.Amount.Cmp(mustBig("5000000000000000000000000")) != 0 || bi.Subtype != "send" {
		t.Fatalf("bad block info: %+v", bi)
	}

	h, amt, ok := c.Receivable("nano_acct")
	if !ok || h != "HASH_BIG" || amt != "9000000000000000000000000" {
		t.Fatalf("Receivable should pick the largest: got %q %q %v", h, amt, ok)
	}
}

func TestPublishStateAssemblesWorkThenProcess(t *testing.T) {
	var gotBlock map[string]any
	var sawWork, sawProcess bool
	c, fake := newFake(t, func(action string, req map[string]any) any {
		switch action {
		case "work_generate":
			sawWork = true
			// difficulty for a send must be the higher threshold
			if req["difficulty"] != nanocrypto.SendDifficulty {
				t.Fatalf("send should use SendDifficulty, got %v", req["difficulty"])
			}
			return map[string]any{"work": "abcdef0123456789"}
		case "process":
			sawProcess = true
			if req["subtype"] != "send" {
				t.Fatalf("process subtype = %v, want send", req["subtype"])
			}
			gotBlock, _ = req["block"].(map[string]any)
			return map[string]any{"hash": "PUBLISHEDHASH"}
		}
		return map[string]any{}
	})

	acctPub := make([]byte, 32)
	for i := range acctPub {
		acctPub[i] = 0x11
	}
	sig := make([]byte, 64)
	for i := range sig {
		sig[i] = 0xab
	}
	hash, err := c.PublishState(StateBlock{
		AccountPub:  acctPub,
		PreviousHex: strings.Repeat("cd", 32),
		RepAddr:     nanocrypto.DefaultRep,
		Balance:     mustBig("42"),
		LinkHex:     strings.Repeat("ef", 32),
		Signature:   sig,
		Subtype:     "send",
	})
	if err != nil {
		t.Fatal(err)
	}
	if hash != "PUBLISHEDHASH" {
		t.Fatalf("hash = %q", hash)
	}
	if !sawWork || !sawProcess {
		t.Fatalf("expected work_generate then process (work=%v process=%v)", sawWork, sawProcess)
	}
	// work_generate must precede process
	if fake.requests[0]["action"] != "work_generate" || fake.requests[1]["action"] != "process" {
		t.Fatalf("request order wrong: %v", fake.requests)
	}
	// the assembled block must carry the EXTERNAL signature (uppercased hex) and our work
	if gotBlock["signature"] != strings.ToUpper(strings.Repeat("ab", 64)) { // 64-byte sig → 128 hex chars
		t.Fatalf("block signature not the provided one: %v", gotBlock["signature"])
	}
	if gotBlock["work"] != "abcdef0123456789" {
		t.Fatalf("block work missing: %v", gotBlock["work"])
	}
	if gotBlock["balance"] != "42" {
		t.Fatalf("block balance = %v", gotBlock["balance"])
	}
	if gotBlock["type"] != "state" {
		t.Fatalf("block type = %v", gotBlock["type"])
	}
}

func TestPublishStateRejectsBadInput(t *testing.T) {
	c, _ := newFake(t, func(action string, req map[string]any) any { return map[string]any{} })
	good := StateBlock{
		AccountPub:  make([]byte, 32),
		PreviousHex: strings.Repeat("00", 32),
		RepAddr:     nanocrypto.DefaultRep,
		Balance:     big.NewInt(1),
		LinkHex:     strings.Repeat("00", 32),
		Signature:   make([]byte, 64),
		Subtype:     "receive",
	}
	// each mutation should fail validation before any RPC call
	bad := good
	bad.AccountPub = make([]byte, 31)
	if _, err := c.PublishState(bad); err == nil {
		t.Fatal("short account pub should fail")
	}
	bad = good
	bad.Signature = make([]byte, 10)
	if _, err := c.PublishState(bad); err == nil {
		t.Fatal("short signature should fail")
	}
	bad = good
	bad.Balance = new(big.Int).Lsh(big.NewInt(1), 200) // > 128 bits
	if _, err := c.PublishState(bad); err == nil {
		t.Fatal("oversized balance should fail")
	}
	bad = good
	bad.PreviousHex = "xyz"
	if _, err := c.PublishState(bad); err == nil {
		t.Fatal("bad previous hex should fail")
	}
}

func mustBig(s string) *big.Int {
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		panic("bad big int: " + s)
	}
	return v
}
