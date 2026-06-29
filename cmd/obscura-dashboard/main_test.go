package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// newTestServer builds a *server wired to the given node URL and a stub wallet
// script written to a temp dir.
func newTestServer(t *testing.T, nodeURL string) (*server, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub wallet script is a POSIX shell script")
	}
	dir := t.TempDir()
	walletPath := filepath.Join(dir, "stub-wallet.sh")
	script := `#!/bin/sh
cmd="$1"; shift
case "$cmd" in
  address)
    echo "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899" ;;
  balance)
    echo "Balance: 12.500000000000 OBX"
    echo "Spendable outputs: 3" ;;
  send)
    to=""; amt=""; fee="0.0001"
    while [ $# -gt 0 ]; do
      case "$1" in
        --to) to="$2"; shift 2 ;;
        --amount) amt="$2"; shift 2 ;;
        --fee) fee="$2"; shift 2 ;;
        --node) shift 2 ;;
        --wallet) shift 2 ;;
        *) shift ;;
      esac
    done
    # echo args back so tests can assert exactly what was passed
    echo "ARGS to=$to amt=$amt fee=$fee"
    echo "Sent $amt OBX (fee $fee) — txid abcdef1234567890" ;;
  *) echo "unknown command"; exit 1 ;;
esac
`
	if err := os.WriteFile(walletPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &server{cfg: config{
		nodeURL:   nodeURL,
		walletBin: walletPath,
	}}, dir
}

// stubNode returns an httptest server emulating the obscura node RPC.
func stubNode(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"coin":"Obscura","ticker":"OBX","height":42,"difficulty":12345,"emitted_atomic":21000000000000,"emitted_obx":"21.000000000000","incentive_pool_atomic":1050000000000,"accumulator_size":42,"accumulator_backend":"rsa2048","mempool_size":0}`))
	})
	mux.HandleFunc("/height", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"height":42}`))
	})
	mux.HandleFunc("/block", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"block":"0102abcd"}`))
	})
	mux.HandleFunc("/submittx", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"txid":"deadbeef"}`))
	})
	return httptest.NewServer(mux)
}

func do(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, target, rdr)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// ---- static assets ----

func TestStaticIndexServed(t *testing.T) {
	s := &server{cfg: config{}}
	rec := do(t, s.routes(), "GET", "/", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("index status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"<title>Obscura", "panel-node", "panel-wallet", "send-form"} {
		if !strings.Contains(body, want) {
			t.Errorf("index missing %q", want)
		}
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("index content-type = %q", ct)
	}
}

func TestStaticAssetsServed(t *testing.T) {
	s := &server{cfg: config{}}
	for _, path := range []string{"/style.css", "/app.js", "/qr.js", "/favicon.svg"} {
		rec := do(t, s.routes(), "GET", path, "")
		if rec.Code != http.StatusOK {
			t.Errorf("%s status = %d", path, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("%s empty", path)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	s := &server{cfg: config{}}
	rec := do(t, s.routes(), "GET", "/", "")
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("missing CSP: %q", csp)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Error("missing X-Frame-Options")
	}
}

// ---- node proxy ----

func TestNodeProxyStatus(t *testing.T) {
	node := stubNode(t)
	defer node.Close()
	s := &server{cfg: config{nodeURL: node.URL}}
	rec := do(t, s.routes(), "GET", "/api/node/status", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"ticker":"OBX"`) {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
}

func TestNodeProxyBlockValidHeight(t *testing.T) {
	node := stubNode(t)
	defer node.Close()
	s := &server{cfg: config{nodeURL: node.URL}}
	rec := do(t, s.routes(), "GET", "/api/node/block?height=5", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"block"`) {
		t.Errorf("unexpected: %s", rec.Body.String())
	}
}

func TestNodeProxyRejectsBadHeight(t *testing.T) {
	node := stubNode(t)
	defer node.Close()
	s := &server{cfg: config{nodeURL: node.URL}}
	// Values are URL-encoded exactly as a browser would send them, so the
	// server sees the literal malicious string in the height parameter.
	for _, h := range []string{"abc", "5;rm", "-1", "5 OR 1=1", "../status", "5e3", "0x10"} {
		target := "/api/node/block?height=" + url.QueryEscape(h)
		rec := do(t, s.routes(), "GET", target, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("height=%q expected 400, got %d", h, rec.Code)
		}
	}
}

func TestNodeProxyRejectsUnknownPath(t *testing.T) {
	s := &server{cfg: config{nodeURL: "http://127.0.0.1:1"}}
	for _, p := range []string{"/api/node/evil", "/api/node/admin", "/api/node/"} {
		rec := do(t, s.routes(), "GET", p, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("path %q expected 404, got %d", p, rec.Code)
		}
	}
}

func TestNodeProxyOfflineHandled(t *testing.T) {
	// Point at a closed port → upstream unreachable.
	s := &server{cfg: config{nodeURL: "http://127.0.0.1:1"}}
	rec := do(t, s.routes(), "GET", "/api/node/status", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"offline":true`) {
		t.Errorf("expected offline flag, got %s", rec.Body.String())
	}
}

// ---- wallet exec ----

func TestWalletAddress(t *testing.T) {
	s, _ := newTestServer(t, "http://127.0.0.1:1")
	rec := do(t, s.routes(), "GET", "/api/wallet/address", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "aabbccddeeff") {
		t.Errorf("unexpected: %s", rec.Body.String())
	}
}

func TestWalletBalanceParsed(t *testing.T) {
	s, _ := newTestServer(t, "http://127.0.0.1:1")
	rec := do(t, s.routes(), "GET", "/api/wallet/balance", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	b := rec.Body.String()
	if !strings.Contains(b, `"balance":"12.500000000000"`) {
		t.Errorf("balance not parsed: %s", b)
	}
	if !strings.Contains(b, `"spendable_outputs":3`) {
		t.Errorf("outputs not parsed: %s", b)
	}
}

func TestWalletSendValid(t *testing.T) {
	s, _ := newTestServer(t, "http://127.0.0.1:1")
	rec := do(t, s.routes(), "POST", "/api/wallet/send", `{"to":"aabbcc","amount":"1.5","fee":"0.0001"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	b := rec.Body.String()
	if !strings.Contains(b, `"txid":"abcdef1234567890"`) {
		t.Errorf("txid not parsed: %s", b)
	}
	// confirm the exact args reached the CLI (no shell mangling)
	if !strings.Contains(b, "to=aabbcc") || !strings.Contains(b, "amt=1.5") {
		t.Errorf("args not passed cleanly: %s", b)
	}
}

func TestWalletSendValidation(t *testing.T) {
	s, _ := newTestServer(t, "http://127.0.0.1:1")
	cases := []struct {
		name, body string
	}{
		{"non-hex addr", `{"to":"xyz","amount":"1"}`},
		{"shell injection addr", `{"to":"aa; rm -rf /","amount":"1"}`},
		{"odd hex addr", `{"to":"abc","amount":"1"}`},
		{"zero amount", `{"to":"aabb","amount":"0"}`},
		{"negative amount", `{"to":"aabb","amount":"-1"}`},
		{"too many decimals", `{"to":"aabb","amount":"1.1234567890123"}`},
		{"bad fee", `{"to":"aabb","amount":"1","fee":"abc"}`},
		{"amount with space", `{"to":"aabb","amount":"1 2"}`},
		{"empty amount", `{"to":"aabb","amount":""}`},
		{"unknown field", `{"to":"aabb","amount":"1","evil":"x"}`},
		{"not json", `not json`},
	}
	for _, c := range cases {
		rec := do(t, s.routes(), "POST", "/api/wallet/send", c.body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d (%s)", c.name, rec.Code, rec.Body.String())
		}
	}
}

func TestWalletSendMethodNotAllowed(t *testing.T) {
	s, _ := newTestServer(t, "http://127.0.0.1:1")
	rec := do(t, s.routes(), "GET", "/api/wallet/send", "")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestBodySizeLimit(t *testing.T) {
	s, _ := newTestServer(t, "http://127.0.0.1:1")
	huge := `{"to":"aabb","amount":"1","junk":"` + strings.Repeat("A", 1<<20) + `"}`
	rec := do(t, s.routes(), "POST", "/api/wallet/send", huge)
	if rec.Code == http.StatusOK {
		t.Errorf("oversized body should be rejected, got %d", rec.Code)
	}
}

func TestWalletMissingDetected(t *testing.T) {
	// Point at a wallet binary that prints the "no wallet" message.
	dir := t.TempDir()
	wp := filepath.Join(dir, "w.sh")
	_ = os.WriteFile(wp, []byte("#!/bin/sh\necho \"no wallet at /x (run `obscura-wallet new`)\"\nexit 1\n"), 0o755)
	s := &server{cfg: config{walletBin: wp, nodeURL: "http://127.0.0.1:1"}}
	rec := do(t, s.routes(), "GET", "/api/wallet/address", "")
	if !strings.Contains(rec.Body.String(), `"wallet_missing":true`) {
		t.Errorf("expected wallet_missing flag: %s", rec.Body.String())
	}
}

// ---- unit helpers ----

func TestIsZeroAmount(t *testing.T) {
	for _, s := range []string{"0", "0.0", "0.000000000000", "00.00"} {
		if !isZeroAmount(s) {
			t.Errorf("%q should be zero", s)
		}
	}
	for _, s := range []string{"1", "0.1", "0.000000000001", "10"} {
		if isZeroAmount(s) {
			t.Errorf("%q should not be zero", s)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("abcdef", 3); got != "abc…" {
		t.Errorf("truncate = %q", got)
	}
	if got := truncate("ab", 5); got != "ab" {
		t.Errorf("truncate short = %q", got)
	}
}
