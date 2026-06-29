package rpc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"obscura/pkg/chain"
)

// stubPeers implements PeerProvider with a fixed peer list for tests.
type stubPeers struct{ addrs []string }

func (s stubPeers) PeerCount() int                          { return len(s.addrs) }
func (s stubPeers) PeerAddrs() []string                     { return s.addrs }
func (s stubPeers) PeerForMaker([]byte) (string, bool)      { return "", false }

func newTestServer(t *testing.T) *Server {
	t.Helper()
	c, err := chain.New(t.TempDir())
	if err != nil {
		t.Fatalf("new chain: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return NewServer(c, nil, nil)
}

// do issues a request to the server's handler. remote sets RemoteAddr; bearer,
// when non-empty, is sent as an Authorization header.
func do(h http.Handler, method, target, remote, bearer string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	r.RemoteAddr = remote
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// TestOperatorEndpointsGated proves /blocktemplate and /submitblock reject
// non-loopback untokened callers but allow loopback.
func TestOperatorEndpointsGated(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()

	// Remote (non-loopback, no token) must be forbidden.
	for _, ep := range []string{"/blocktemplate?address=x", "/submitblock"} {
		w := do(h, "GET", ep, "8.8.8.8:5000", "")
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s from remote: got %d want 403", ep, w.Code)
		}
	}

	// Loopback reaches the handler: /blocktemplate then fails on bad address
	// (400), proving it passed the gate.
	w := do(h, "GET", "/blocktemplate?address=not-a-real-addr", "127.0.0.1:5000", "")
	if w.Code == http.StatusForbidden {
		t.Fatalf("loopback blocktemplate was forbidden: %d", w.Code)
	}
}

// TestBearerTokenGate proves a configured token authorizes a remote caller and a
// wrong/absent token does not.
func TestBearerTokenGate(t *testing.T) {
	s := newTestServer(t)
	s.authToken = "s3cret"
	h := s.Handler()

	if w := do(h, "GET", "/blocktemplate?address=bad", "8.8.8.8:5000", "wrong"); w.Code != http.StatusForbidden {
		t.Fatalf("wrong token: got %d want 403", w.Code)
	}
	// Correct token passes the gate (then fails address parsing with 400).
	if w := do(h, "GET", "/blocktemplate?address=bad", "8.8.8.8:5000", "s3cret"); w.Code == http.StatusForbidden {
		t.Fatalf("valid token was forbidden")
	}
}

// TestPeersLeakGuard proves the full peer IP list is hidden from untrusted
// callers (count only) but exposed to loopback.
func TestPeersLeakGuard(t *testing.T) {
	s := newTestServer(t)
	s.SetPeerProvider(stubPeers{addrs: []string{"10.0.0.1:1", "10.0.0.2:2"}})
	h := s.Handler()

	var remote PeersResponse
	w := do(h, "GET", "/peers", "8.8.8.8:5000", "")
	if err := json.NewDecoder(w.Body).Decode(&remote); err != nil {
		t.Fatalf("decode remote: %v", err)
	}
	if remote.Count != 2 {
		t.Fatalf("remote count: got %d want 2", remote.Count)
	}
	if len(remote.Peers) != 0 {
		t.Fatalf("remote leaked peer list: %v", remote.Peers)
	}

	var local PeersResponse
	w = do(h, "GET", "/peers", "127.0.0.1:5000", "")
	if err := json.NewDecoder(w.Body).Decode(&local); err != nil {
		t.Fatalf("decode local: %v", err)
	}
	if len(local.Peers) != 2 {
		t.Fatalf("loopback should see full list, got %v", local.Peers)
	}
}

// TestPublicEndpointsOpen proves read-only endpoints work without auth from a
// remote caller.
func TestPublicEndpointsOpen(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	for _, ep := range []string{"/status", "/height", "/feerate"} {
		w := do(h, "GET", ep, "8.8.8.8:5000", "")
		if w.Code != http.StatusOK {
			t.Fatalf("%s: got %d want 200", ep, w.Code)
		}
		if !strings.Contains(w.Body.String(), "{") {
			t.Fatalf("%s did not return JSON", ep)
		}
	}
}

// TestLoopbackHelper sanity-checks the loopback classifier across address forms.
func TestLoopbackHelper(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:5000": true,
		"[::1]:5000":     true,
		"8.8.8.8:5000":   false,
		"10.0.0.5:1":     false,
	}
	for addr, want := range cases {
		r := httptest.NewRequest("GET", "/peers", nil)
		r.RemoteAddr = addr
		if got := isLoopback(r); got != want {
			t.Fatalf("isLoopback(%q)=%v want %v", addr, got, want)
		}
	}
}
