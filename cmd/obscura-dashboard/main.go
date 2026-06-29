// Command obscura-dashboard is a small, dependency-free local web dashboard for
// the Obscura (OBX) privacy coin. It serves a single-page wallet + node/mining
// monitor UI and bridges the browser to:
//
//   - the node JSON-RPC (reverse-proxied under /api/node/* to avoid CORS), and
//   - the prebuilt CLI wallet binary (exec'd under /api/wallet/*).
//
// It imports ONLY the Go standard library. It does not import any obscura/pkg/*
// package, so it builds and runs independently of the rest of the project.
//
// Build:  go build -o bin/obscura-dashboard ./cmd/obscura-dashboard
// Run:    ./bin/obscura-dashboard            (then open the printed URL)
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Static assets live in ./webui next to this file. go:embed cannot reach
// across directories, so the canonical authored copy is also mirrored to the
// repo-root /webui via scripts/sync-webui.sh for editing convenience; the
// embedded copy here is what the binary actually serves.
//
//go:embed all:webui
var webuiFS embed.FS

const (
	// maxBodyBytes caps incoming POST bodies to defend against abuse.
	maxBodyBytes = 1 << 16 // 64 KiB
	// execTimeout bounds how long a wallet CLI invocation may run.
	execTimeout = 60 * time.Second
	// nodeProxyTimeout bounds an upstream node RPC request.
	nodeProxyTimeout = 15 * time.Second
)

// validation patterns — strict allowlists, never used to build shell strings.
var (
	hexAddrRe = regexp.MustCompile(`^[0-9a-fA-F]{2,256}$`)         // hex stealth address
	amountRe  = regexp.MustCompile(`^[0-9]{1,12}(\.[0-9]{1,12})?$`) // decimal OBX, <=12 frac digits
	heightRe  = regexp.MustCompile(`^[0-9]{1,18}$`)
)

type config struct {
	addr      string
	nodeURL   string
	walletBin string
	walletF   string
}

func main() {
	cfg := config{}
	flag.StringVar(&cfg.addr, "addr", "127.0.0.1:8088", "listen address for the dashboard")
	flag.StringVar(&cfg.nodeURL, "node", "http://127.0.0.1:18081", "node RPC base URL to proxy")
	flag.StringVar(&cfg.walletBin, "wallet-bin", "./bin/obscura-wallet", "path to the CLI wallet binary")
	flag.StringVar(&cfg.walletF, "wallet", "", "wallet seed file (passed to the CLI; empty = CLI default)")
	flag.Parse()

	if _, err := url.Parse(cfg.nodeURL); err != nil {
		log.Fatalf("invalid --node URL: %v", err)
	}

	srv := &server{cfg: cfg}
	handler := srv.routes()

	httpSrv := &http.Server{
		Addr:              cfg.addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      90 * time.Second, // > execTimeout so wallet calls can finish
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 14,
	}

	log.Printf("Obscura dashboard on http://%s  (node=%s, wallet-bin=%s)", cfg.addr, cfg.nodeURL, cfg.walletBin)
	log.Printf("Open http://%s in your browser. Press Ctrl+C to stop.", cfg.addr)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server: %v", err)
	}
}

type server struct {
	cfg config
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()

	// Static assets, served from the embedded webui/ tree rooted at "/".
	static, err := fs.Sub(webuiFS, "webui")
	if err != nil {
		log.Fatalf("embed: %v", err)
	}
	fileServer := http.FileServer(http.FS(static))
	mux.Handle("/", securityHeaders(fileServer))

	// Node RPC reverse proxy.
	mux.HandleFunc("/api/node/", s.handleNodeProxy)

	// Wallet actions (exec the CLI).
	mux.HandleFunc("/api/wallet/address", s.handleWalletAddress)
	mux.HandleFunc("/api/wallet/balance", s.handleWalletBalance)
	mux.HandleFunc("/api/wallet/send", s.handleWalletSend)

	// Dashboard meta.
	mux.HandleFunc("/api/health", s.handleHealth)

	return logRequests(mux)
}

// ---- middleware ----

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Strict, self-contained CSP — everything is served locally, no CDNs.
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"node":       s.cfg.nodeURL,
		"wallet_bin": s.cfg.walletBin,
		"time":       time.Now().UTC().Format(time.RFC3339),
	})
}

// ---- node reverse proxy ----

// handleNodeProxy forwards GET/POST requests under /api/node/<path> to the
// configured node RPC base URL. Only a fixed allowlist of upstream paths is
// permitted, so the browser cannot probe arbitrary upstream endpoints.
func (s *server) handleNodeProxy(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/node/")
	allowed := map[string]bool{
		"status":   true,
		"height":   true,
		"block":    true,
		"accvalue": true,
		"submittx": true,
	}
	if !allowed[rest] {
		writeErr(w, http.StatusNotFound, "unknown node endpoint")
		return
	}

	target := strings.TrimRight(s.cfg.nodeURL, "/") + "/" + rest
	if r.URL.RawQuery != "" {
		// Only forward known-safe query params with validated values.
		q := url.Values{}
		if h := r.URL.Query().Get("height"); h != "" {
			if !heightRe.MatchString(h) {
				writeErr(w, http.StatusBadRequest, "invalid height")
				return
			}
			q.Set("height", h)
		}
		if p := r.URL.Query().Get("prime"); p != "" {
			if !hexAddrRe.MatchString(p) {
				writeErr(w, http.StatusBadRequest, "invalid prime")
				return
			}
			q.Set("prime", p)
		}
		if enc := q.Encode(); enc != "" {
			target += "?" + enc
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), nodeProxyTimeout)
	defer cancel()

	var body io.Reader
	if r.Method == http.MethodPost {
		body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	}

	upReq, err := http.NewRequestWithContext(ctx, r.Method, target, body)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "bad upstream request")
		return
	}
	if r.Method == http.MethodPost {
		upReq.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(upReq)
	if err != nil {
		// Node offline / unreachable — surface a clean, typed error to the UI.
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error":   "node unreachable",
			"offline": true,
			"detail":  err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 8<<20))
}

// ---- wallet exec ----

// runWallet executes the CLI wallet with the given subcommand and extra args.
// Inputs are passed as a slice (never a shell string), so shell injection is
// impossible by construction. Common flags (--wallet, --node) are appended.
func (s *server) runWallet(ctx context.Context, sub string, extra ...string) (string, error) {
	args := []string{sub}
	if s.cfg.walletF != "" {
		args = append(args, "--wallet", s.cfg.walletF)
	}
	// address/new don't take --node; balance/send/status do.
	if sub == "balance" || sub == "send" || sub == "status" {
		args = append(args, "--node", s.cfg.nodeURL)
	}
	args = append(args, extra...)

	cctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, s.cfg.walletBin, args...)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if cctx.Err() == context.DeadlineExceeded {
		return text, errors.New("wallet command timed out")
	}
	if err != nil {
		// The CLI prints a human message then exits non-zero; bubble that up.
		if text == "" {
			text = err.Error()
		}
		return text, errors.New(text)
	}
	return text, nil
}

func (s *server) handleWalletAddress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	out, err := s.runWallet(r.Context(), "address")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": walletErrMsg(out), "wallet_missing": isWalletMissing(out)})
		return
	}
	addr := strings.TrimSpace(lastLine(out))
	if !hexAddrRe.MatchString(addr) {
		writeErr(w, http.StatusBadGateway, "unexpected wallet output")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"address": strings.ToLower(addr)})
}

var (
	balanceRe = regexp.MustCompile(`Balance:\s+([0-9.]+)\s+(\S+)`)
	outputsRe = regexp.MustCompile(`Spendable outputs:\s+([0-9]+)`)
	txidRe    = regexp.MustCompile(`txid\s+([0-9a-fA-F]+)`)
	sentRe    = regexp.MustCompile(`Sent\s+([0-9.]+)\s+(\S+)\s+\(fee\s+([0-9.]+)\)`)
)

func (s *server) handleWalletBalance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	out, err := s.runWallet(r.Context(), "balance")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error":          walletErrMsg(out),
			"wallet_missing": isWalletMissing(out),
			"node_offline":   isNodeOffline(out),
		})
		return
	}
	resp := map[string]any{}
	if m := balanceRe.FindStringSubmatch(out); m != nil {
		resp["balance"] = m[1]
		resp["ticker"] = m[2]
	}
	if m := outputsRe.FindStringSubmatch(out); m != nil {
		n, _ := strconv.Atoi(m[1])
		resp["spendable_outputs"] = n
	}
	if _, ok := resp["balance"]; !ok {
		writeErr(w, http.StatusBadGateway, "could not parse balance: "+truncate(out, 200))
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

type sendReq struct {
	To     string `json:"to"`
	Amount string `json:"amount"`
	Fee    string `json:"fee"`
}

func (s *server) handleWalletSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req sendReq
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	req.To = strings.TrimSpace(req.To)
	req.Amount = strings.TrimSpace(req.Amount)
	req.Fee = strings.TrimSpace(req.Fee)
	if req.Fee == "" {
		req.Fee = "0.0001"
	}

	// Strict validation — these become exec.Command args, but we also reject
	// anything that isn't a clean hex address / decimal amount.
	if !hexAddrRe.MatchString(req.To) {
		writeErr(w, http.StatusBadRequest, "recipient address must be hex")
		return
	}
	if len(req.To)%2 != 0 {
		writeErr(w, http.StatusBadRequest, "recipient address has odd hex length")
		return
	}
	if !amountRe.MatchString(req.Amount) {
		writeErr(w, http.StatusBadRequest, "amount must be a positive decimal (max 12 decimals)")
		return
	}
	if !amountRe.MatchString(req.Fee) {
		writeErr(w, http.StatusBadRequest, "fee must be a positive decimal (max 12 decimals)")
		return
	}
	if isZeroAmount(req.Amount) {
		writeErr(w, http.StatusBadRequest, "amount must be greater than zero")
		return
	}

	out, err := s.runWallet(r.Context(), "send",
		"--to", strings.ToLower(req.To),
		"--amount", req.Amount,
		"--fee", req.Fee)
	if err != nil {
		status := http.StatusBadGateway
		msg := walletErrMsg(out)
		if strings.Contains(strings.ToLower(out), "insufficient") {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]any{
			"error":          msg,
			"insufficient":   strings.Contains(strings.ToLower(out), "insufficient"),
			"wallet_missing": isWalletMissing(out),
			"node_offline":   isNodeOffline(out),
		})
		return
	}

	resp := map[string]any{"ok": true, "raw": out}
	if m := sentRe.FindStringSubmatch(out); m != nil {
		resp["amount"] = m[1]
		resp["ticker"] = m[2]
		resp["fee"] = m[3]
	}
	if m := txidRe.FindStringSubmatch(out); m != nil {
		resp["txid"] = strings.ToLower(m[1])
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---- small string utils ----

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func walletErrMsg(out string) string {
	if out == "" {
		return "wallet command failed"
	}
	return truncate(out, 300)
}

func isWalletMissing(out string) bool {
	l := strings.ToLower(out)
	return strings.Contains(l, "no wallet") || strings.Contains(l, "run `obscura-wallet new`")
}

func isNodeOffline(out string) bool {
	l := strings.ToLower(out)
	return strings.Contains(l, "rpc:") || strings.Contains(l, "connection refused") || strings.Contains(l, "dial tcp")
}

// isZeroAmount reports whether a validated decimal string equals zero.
func isZeroAmount(s string) bool {
	for _, c := range s {
		if c >= '1' && c <= '9' {
			return false
		}
	}
	return true
}
