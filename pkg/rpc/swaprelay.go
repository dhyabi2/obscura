package rpc

// HTTP surface for the NON-CUSTODIAL browser swap (pkg/swaprelay + pkg/nanorpc).
//
// These endpoints are SAFE TO EXPOSE publicly (via the website proxy) because the
// browser holds every key: it signs the XNO lock (pkg/nanocrypto in WASM) and the
// OBX claim itself. The node only (a) relays swap envelopes between the browser
// taker and the local maker session, and (b) reads/publishes Nano blocks the
// browser already signed. Unlike /swaps/take (operator-funded, gated), nothing
// here can move the operator's funds — so there is no operator gate.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"strings"
	"time"

	"obscura/pkg/nanocrypto"
	"obscura/pkg/nanorpc"
	"obscura/pkg/swaprelay"
)

// SetSwapRelay wires the browser swap relay and the secret-free Nano RPC client.
// nano may be nil (no real Nano configured) — the Nano relay endpoints then
// return 503 while the OBX-side relay still works for mock/dev.
func (s *Server) SetSwapRelay(r *swaprelay.Relay, nano *nanorpc.Client) {
	s.swapRelay = r
	s.nanoPub = nano
}

// relayPollTimeout bounds one long-poll Recv. Kept UNDER the website proxy's
// per-request timeout (api/explorer.js uses an 8s AbortSignal, and Vercel
// functions cap ~10s), so Recv returns {empty:true} before the proxy aborts and
// the browser simply re-polls.
const relayPollTimeout = 6 * time.Second

// POST /swaps/relay/open {"swap_id":"<hex32>"}
func (s *Server) handleSwapRelayOpen(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method == http.MethodOptions {
		return
	}
	if s.swapRelay == nil {
		http.Error(w, "swap relay unavailable", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		SwapID string `json:"swap_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := s.swapRelay.Open(req.SwapID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// POST /swaps/relay/send {"swap_id":"<hex32>","kind":<int>,"payload":"<hex>"}
func (s *Server) handleSwapRelaySend(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method == http.MethodOptions {
		return
	}
	if s.swapRelay == nil {
		http.Error(w, "swap relay unavailable", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		SwapID  string `json:"swap_id"`
		Kind    int    `json:"kind"`
		Payload string `json:"payload"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<17)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if req.Kind < 0 || req.Kind > 255 {
		http.Error(w, "bad kind", http.StatusBadRequest)
		return
	}
	payload, err := hex.DecodeString(req.Payload)
	if err != nil {
		http.Error(w, "bad payload hex", http.StatusBadRequest)
		return
	}
	if err := s.swapRelay.Submit(req.SwapID, byte(req.Kind), payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// GET /swaps/relay/recv?swap_id=<hex32> — long-poll for the next maker→browser
// message. Returns {"empty":true} on timeout (the browser re-polls).
func (s *Server) handleSwapRelayRecv(w http.ResponseWriter, r *http.Request) {
	cors(w)
	// A long-poll MUST NOT be cached: a cached reply would redeliver an old swap
	// envelope to the next (same-URL) recv. Belt to the client-side cache-buster.
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	if r.Method == http.MethodOptions {
		return
	}
	if s.swapRelay == nil {
		http.Error(w, "swap relay unavailable", http.StatusServiceUnavailable)
		return
	}
	swapID := r.URL.Query().Get("swap_id")
	ctx, cancel := context.WithTimeout(r.Context(), relayPollTimeout)
	defer cancel()
	kind, payload, ok := s.swapRelay.Recv(ctx, swapID)
	if !ok {
		writeJSON(w, map[string]any{"empty": true})
		return
	}
	writeJSON(w, map[string]any{"kind": int(kind), "payload": hex.EncodeToString(payload)})
}

// GET /swaps/swapout?key=<hex> — the on-chain SwapOutput the taker verifies before
// locking XNO (public structure: keys, binding, unlock height, amount — no secret).
func (s *Server) handleSwapOut(w http.ResponseWriter, r *http.Request) {
	cors(w)
	key, err := hex.DecodeString(r.URL.Query().Get("key"))
	if err != nil || len(key) == 0 {
		http.Error(w, "bad key", http.StatusBadRequest)
		return
	}
	e, ok := s.chain.Swap(key)
	if !ok {
		writeJSON(w, map[string]any{"found": false})
		return
	}
	writeJSON(w, map[string]any{
		"found":        true,
		"claim_key":    hex.EncodeToString(e.ClaimKey),
		"refund_key":   hex.EncodeToString(e.RefundKey),
		"unlock_height": e.UnlockHeight,
		"claim_r":      hex.EncodeToString(e.ClaimR),
		"claim_t":      hex.EncodeToString(e.ClaimT),
		"amount":       u64s(e.Amount),
	})
}

// ---- Nano relay (secret-free; the browser signs, the node publishes) ---------

// validNanoAccount checks a nano_/xrb_ address shape + checksum (audit #96: reject
// bad input with a clear 400 here instead of forwarding it and echoing the
// upstream Nano-RPC error string back to an outside caller).
func validNanoAccount(a string) bool {
	_, err := nanocrypto.DecodeAddress(a)
	return err == nil
}

// validNanoHash checks a 64-hex Nano block hash.
func validNanoHash(h string) bool {
	if len(h) != 64 {
		return false
	}
	_, err := hex.DecodeString(h)
	return err == nil
}

// GET /swaps/nano/account?account=nano_...
func (s *Server) handleNanoAccount(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if s.nanoPub == nil {
		http.Error(w, "nano rpc not configured", http.StatusServiceUnavailable)
		return
	}
	acct := r.URL.Query().Get("account")
	if acct == "" {
		http.Error(w, "account required", http.StatusBadRequest)
		return
	}
	if !validNanoAccount(acct) {
		http.Error(w, "bad account: expected a nano_ address", http.StatusBadRequest)
		return
	}
	info, err := s.nanoPub.AccountInfo(acct)
	if err != nil {
		http.Error(w, "nano backend unavailable", http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{
		"frontier":       info.Frontier,
		"representative": info.Representative,
		"balance":        info.Balance.String(),
		"opened":         info.Opened,
	})
}

// GET /swaps/nano/receivable?account=nano_...
func (s *Server) handleNanoReceivable(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if s.nanoPub == nil {
		http.Error(w, "nano rpc not configured", http.StatusServiceUnavailable)
		return
	}
	acct := r.URL.Query().Get("account")
	if !validNanoAccount(acct) {
		http.Error(w, "account required (nano_ address)", http.StatusBadRequest)
		return
	}
	hash, amount, ok := s.nanoPub.Receivable(acct)
	writeJSON(w, map[string]any{"found": ok, "hash": hash, "amount": amount})
}

// GET /swaps/nano/block?hash=...
func (s *Server) handleNanoBlock(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if s.nanoPub == nil {
		http.Error(w, "nano rpc not configured", http.StatusServiceUnavailable)
		return
	}
	hsh := r.URL.Query().Get("hash")
	if !validNanoHash(hsh) {
		http.Error(w, "hash required (64 hex)", http.StatusBadRequest)
		return
	}
	bi, err := s.nanoPub.BlockInfo(hsh)
	if err != nil {
		http.Error(w, "nano backend unavailable", http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{
		"confirmed":       bi.Confirmed,
		"amount":          bi.Amount.String(),
		"link_hex":        bi.LinkHex,
		"link_as_account": bi.LinkAsAccount,
		"subtype":         bi.Subtype,
	})
}

// POST /swaps/nano/publish — submit a browser-signed Nano state block. The node
// attaches proof-of-work and processes it; it NEVER signs. Body fields are the
// StateBlock the browser already signed via pkg/nanocrypto.
func (s *Server) handleNanoPublish(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method == http.MethodOptions {
		return
	}
	if s.nanoPub == nil {
		http.Error(w, "nano rpc not configured", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		AccountPub  string `json:"account_pub"`
		PreviousHex string `json:"previous"`
		RepAddr     string `json:"representative"`
		Balance     string `json:"balance"`
		LinkHex     string `json:"link"`
		Signature   string `json:"signature"`
		Subtype     string `json:"subtype"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	acctPub, e1 := hex.DecodeString(req.AccountPub)
	sig, e2 := hex.DecodeString(req.Signature)
	bal, ok := new(big.Int).SetString(req.Balance, 10)
	if e1 != nil || e2 != nil || !ok {
		http.Error(w, "bad account_pub / signature / balance", http.StatusBadRequest)
		return
	}
	// AUDIT H-2: VERIFY THE SIGNATURE LOCALLY BEFORE SPENDING PROOF-OF-WORK.
	// PublishState generates work_generate (an expensive operation on the operator's
	// — possibly paid/rate-limited — Nano endpoint) BEFORE `process` rejects a bad
	// signature. Without this check a public, unauthenticated caller could submit
	// well-formed-but-bogus blocks to force unbounded work computation (a PoW
	// amplification DoS). Recomputing the canonical block hash and checking the
	// ed25519-blake2b signature here makes a bogus block free to reject. (The node
	// still re-validates on `process`; this is a cheap pre-filter, not the authority.)
	prevB, e3 := hex.DecodeString(req.PreviousHex)
	linkB, e4 := hex.DecodeString(req.LinkHex)
	repPub, e5 := nanocrypto.DecodeAddress(req.RepAddr)
	if e3 != nil || e4 != nil || e5 != nil || len(prevB) != 32 || len(linkB) != 32 ||
		len(acctPub) != 32 || len(sig) != 64 || bal.Sign() < 0 || len(bal.Bytes()) > 16 {
		http.Error(w, "bad block fields", http.StatusBadRequest)
		return
	}
	if !nanocrypto.Verify(acctPub, nanocrypto.StateHash(acctPub, prevB, repPub, bal, linkB), sig) {
		http.Error(w, "block signature does not verify — refusing to spend work", http.StatusBadRequest)
		return
	}
	hash, err := s.nanoPub.PublishState(nanorpc.StateBlock{
		AccountPub:  acctPub,
		PreviousHex: req.PreviousHex,
		RepAddr:     req.RepAddr,
		Balance:     bal,
		LinkHex:     req.LinkHex,
		Signature:   sig,
		Subtype:     req.Subtype,
		Opened:      req.PreviousHex == strings.Repeat("0", 64),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"hash": hash})
}
