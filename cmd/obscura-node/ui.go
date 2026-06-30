// Desktop-app UI server for obscura-node.
//
// When the node is started with --ui it becomes a turnkey desktop app: the
// website (wallet + swap + explorer) is EMBEDDED into the binary with //go:embed,
// served on a local port, and the user's browser is opened to it. A local
// /api/explorer proxy mirrors the Vercel serverless proxy (website/api/explorer.js)
// so the embedded site works UNCHANGED — it forwards a whitelisted set of paths to
// this node's own in-process RPC server.
//
// This file is fully self-contained: nothing here runs unless --ui is passed, so
// the default (headless) node behaviour is untouched.
package main

import (
	"context"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// website holds the embedded static site. The files live in cmd/obscura-node/website
// (a copy of the repo-root website/ dir, minus the Vercel-only api/ + .vercel bits).
// go:embed cannot reach a parent directory (no ".."), so the assets are vendored
// here; keep them in sync with the repo-root website/ (see SyncNote below).
//
//go:embed all:website
var website embed.FS

// SyncNote: cmd/obscura-node/website is a build-time COPY of the repo-root website/
// directory (explorer.html, wallet.html, index.html, assets/, wasm_exec.js,
// wallet.wasm, lightweight-charts*.js). Re-copy it after changing the site:
//
//	rsync -a --exclude='.vercel' --exclude='api' --exclude='*.sh' \
//	      --exclude='vercel.json' --exclude='.gitignore' \
//	      website/ cmd/obscura-node/website/
const SyncNote = "cmd/obscura-node/website is a copy of repo-root website/ for //go:embed"

// startUI serves the embedded site on uiAddr and proxies /api/explorer to the
// in-process node RPC on rpcAddr, then opens the browser. Best-effort: it logs the
// URL if the browser cannot be opened. It must be called AFTER the RPC server is
// listening.
func startUI(uiAddr, rpcAddr string) {
	// Sub-FS rooted at the embedded "website" dir so URLs map to /explorer.html etc.
	sub, err := fs.Sub(website, "website")
	if err != nil {
		log.Printf("--ui: cannot open embedded website: %v (UI disabled)", err)
		return
	}

	// rpcBase is the in-process node RPC the proxy forwards to. Always loopback HTTP.
	rpcBase := "http://" + rpcAddr

	// Is this UI serving the PUBLIC (hosted) or a single local desktop operator? A non-loopback
	// UI bind is hosted; OBX_UI_PUBLIC=1 forces it (for operators behind a reverse proxy that
	// binds the UI to loopback). Hosted => proxied requests are marked untrusted (audit BUG-1/2).
	hosted := os.Getenv("OBX_UI_PUBLIC") == "1" || !uiAddrLoopback(uiAddr)
	if hosted {
		log.Printf("UI in PUBLIC/hosted mode: wallet-proxy callers are treated as UNTRUSTED (swap-take gated, operator XNO account hidden). Unset OBX_UI_PUBLIC + bind --ui-addr to loopback for single-operator desktop mode.")
	} else {
		log.Printf("UI in local desktop mode (loopback): the local operator is trusted. If you are exposing this UI to the public (incl. via a reverse proxy), set OBX_UI_PUBLIC=1.")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/explorer", uiExplorerProxy(rpcBase, hosted))

	fileServer := http.FileServer(http.FS(sub))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Land on the wallet page (the primary app surface) for the bare root; serve
		// every other path straight from the embedded FS (explorer.html, assets, wasm).
		if r.URL.Path == "/" {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/wallet.html"
			fileServer.ServeHTTP(w, r2)
			return
		}
		// Marketing landing (audit #2/#4): on a local node `/` is the wallet, and
		// http.FileServer REDIRECTS index.html -> `/`, so the intro/diagram page was
		// unreachable. Serve its bytes DIRECTLY (no redirect) at /home, /index, /landing.
		switch r.URL.Path {
		case "/home", "/index", "/landing", "/index.html":
			serveEmbeddedHTML(w, sub, "index.html")
			return
		}
		// CLEAN URLs (match the hosted site's vercel.json cleanUrls): an
		// extensionless path like /explorer or /wallet maps to /explorer.html etc.
		// so the in-page nav links work under --ui too (they 404'd before).
		if p := r.URL.Path; !strings.Contains(p[strings.LastIndexByte(p, '/')+1:], ".") {
			cand := strings.TrimSuffix(p, "/") + ".html"
			if f, err := sub.Open(strings.TrimPrefix(cand, "/")); err == nil {
				_ = f.Close()
				r2 := r.Clone(r.Context())
				r2.URL.Path = cand
				fileServer.ServeHTTP(w, r2)
				return
			}
		}
		fileServer.ServeHTTP(w, r)
	})

	srv := &http.Server{
		Addr:              uiAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Printf("UI listening on http://%s  (wallet + swap + explorer)", uiAddr)
		if err := srv.ListenAndServe(); err != nil {
			log.Printf("--ui: UI server stopped: %v", err)
		}
	}()

	// Present as a NATIVE DESKTOP APP, not a browser tab: launch a Chromium-class
	// browser (Chrome/Edge/Brave/Chromium) in --app mode — a borderless, tab-less,
	// URL-bar-less window that looks like a native application — pointed at the local
	// UI, in a dedicated profile so it maps to OUR window. Closing that window quits the
	// app (the desktop-app lifecycle). Pure os/exec, no cgo, so the binary still
	// cross-compiles to every OS. If no Chromium-class browser is present, fall back to
	// opening the default browser tab (still fully functional).
	uiURL := "http://" + uiAddr + "/"
	if cmd := openAppWindow(uiURL); cmd != nil {
		log.Printf("Obscura desktop app window opened (%s) — close it to quit", uiURL)
		go func() {
			start := time.Now()
			_ = cmd.Wait()
			if time.Since(start) < 3*time.Second {
				// The browser handed our --app request to an already-running instance and
				// the launcher returned immediately; we do NOT own that window, so keep the
				// app running rather than quitting on a phantom "close".
				log.Printf("app window handed to a running browser; UI still at %s", uiURL)
				return
			}
			log.Printf("app window closed — shutting down Obscura")
			os.Exit(0)
		}()
		return
	}
	log.Printf("no Chrome/Edge/Brave/Chromium found for a native app window — opening default browser instead")
	openBrowser(uiURL)
}

// openAppWindow launches a Chromium-class browser in borderless --app mode pointed at
// url, as a CHILD process (so its lifetime is the app window's lifetime). Returns the
// command, or nil if no suitable browser is installed. Pure os/exec — no cgo, so this
// builds for every GOOS.
func openAppWindow(target string) *exec.Cmd {
	bin := findChromium()
	if bin == "" {
		return nil
	}
	profile := filepath.Join(os.TempDir(), "obscura-app-window")
	cmd := exec.Command(bin,
		"--app="+target,
		"--user-data-dir="+profile,
		"--no-first-run",
		"--no-default-browser-check",
		"--new-window",
	)
	if err := cmd.Start(); err != nil {
		return nil
	}
	return cmd
}

func fileExists(p string) bool { st, err := os.Stat(p); return err == nil && !st.IsDir() }

// findChromium locates a Chromium-based browser binary for app-mode windows.
func findChromium() string {
	switch runtime.GOOS {
	case "darwin":
		for _, p := range []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		} {
			if fileExists(p) {
				return p
			}
		}
	case "windows":
		for _, p := range []string{
			os.Getenv("ProgramFiles") + `\Google\Chrome\Application\chrome.exe`,
			os.Getenv("ProgramFiles(x86)") + `\Google\Chrome\Application\chrome.exe`,
			os.Getenv("LocalAppData") + `\Google\Chrome\Application\chrome.exe`,
			os.Getenv("ProgramFiles(x86)") + `\Microsoft\Edge\Application\msedge.exe`,
			os.Getenv("ProgramFiles") + `\Microsoft\Edge\Application\msedge.exe`,
			os.Getenv("ProgramFiles") + `\BraveSoftware\Brave-Browser\Application\brave.exe`,
		} {
			if fileExists(p) {
				return p
			}
		}
	default: // linux + other unixes
		for _, n := range []string{
			"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
			"microsoft-edge", "microsoft-edge-stable", "brave-browser",
		} {
			if p, err := exec.LookPath(n); err == nil {
				return p
			}
		}
	}
	return ""
}

// uiExplorerProxy mirrors website/api/explorer.js: it reads ?path=NAME, maps it to a
// node RPC path using the SAME whitelist + GET/POST split, forwards the request to
// the in-process node RPC (rpcBase), and returns the response. Operator-gated paths
// (/xno/recovery, /xno/withdraw) are deliberately NOT in any table, so they cannot be
// reached through this proxy.
// hosted: when the UI serves the PUBLIC (not a single local desktop operator), forwarded
// requests are marked X-OBX-Proxied so the RPC treats them as untrusted public callers — this
// makes the /swaps/take gate fire (audit BUG-2) and keeps the operator's XNO account private
// (audit BUG-1). In local desktop mode (UI on loopback, single operator) it stays trusted.
// uiAddrLoopback reports whether the UI bind address is loopback (single-operator desktop).
// An all-interfaces (":port"/"0.0.0.0") or public bind is NOT loopback (hosted).
func uiAddrLoopback(uiAddr string) bool {
	host, _, err := net.SplitHostPort(uiAddr)
	if err != nil {
		host = uiAddr
	}
	host = strings.TrimSpace(host)
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func uiExplorerProxy(rpcBase string, hosted bool) http.HandlerFunc {
	root := strings.TrimRight(rpcBase, "/")

	// GET read endpoints (no forwarded query params).
	get := map[string]string{
		"summary":       "/explorer/summary",
		"mempool":       "/explorer/mempool",
		"vaults":        "/explorer/vaults",
		"height":        "/height",
		"feerate":       "/feerate",
		"offers":        "/offers",
		"offersjson":    "/offers/json",
		"pricehistory":  "/explorer/pricehistory",
		"swaps":         "/explorer/swaps",
		"liquidity":     "/liquidity",
		"swapsactive":   "/swaps/active",
		"autoliquidity": "/auto-liquidity",
		// node identity + machine-readable params (decimals/fees/terms/network), so
		// UIs and third-party clients read them instead of hardcoding (UI-decoupling audit).
		"status":  "/status",
		"version": "/version",
		"params":  "/params",
		// PUBLIC, read-only XNO proceeds account (address + balance/receivable). The
		// operator-gated /xno/recovery and /xno/withdraw are DELIBERATELY absent.
		"xnoaccount": "/xno/account",
	}
	// GET endpoints that forward sanitized market-data query params.
	getQ := map[string]string{
		"trades":  "/trades",
		"candles": "/candles",
		"stats":   "/stats",
		"orders":  "/orders",
		// depth-aware quote + ladder (node already computes these — UIs shouldn't
		// reimplement pricing) and tx-by-hash lookup (UI-decoupling + API audits).
		"quote": "/quote",
		"depth": "/depth",
		"tx":    "/tx",
	}
	// NON-CUSTODIAL browser swap relay GET endpoints. They forward swap_id /
	// account / hash / key params (a nano_ address is 65 chars, longer than the
	// market-data params), so they use a dedicated 128-char sanitizer below.
	relayGetQ := map[string]string{
		"swaprecv":       "/swaps/relay/recv",
		"swapout":        "/swaps/swapout",
		"nanoaccount":    "/swaps/nano/account",
		"nanoreceivable": "/swaps/nano/receivable",
		"nanoblock":      "/swaps/nano/block",
	}
	// POST write endpoints (body forwarded as-is).
	post := map[string]string{
		"submittx":  "/submittx",
		"offer":     "/offer",
		"swapstake": "/swaps/take",
		// browser swap relay writes (browser holds every key; node only relays).
		"swapopen":    "/swaps/relay/open",
		"swapsend":    "/swaps/relay/send",
		"nanopublish": "/swaps/nano/publish",
	}

	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		path := q.Get("path")

		var target, method string
		switch {
		case path == "block":
			// privacy-redacted explorer block view. A non-numeric height is a 400, not
			// silently digit-stripped to genesis (audit #94). NOTE: sanitizeDigits
			// returns "0" for all-non-digit input, so validate the RAW param here.
			if !isAllDigits(q.Get("height")) {
				writeJSONError(w, http.StatusBadRequest, "bad height (want a non-negative integer)")
				return
			}
			target = root + "/explorer/block?height=" + q.Get("height")
			method = http.MethodGet
		case path == "rawblock":
			// full serialized block (hex) — the wallet scans this for its outputs
			if !isAllDigits(q.Get("height")) {
				writeJSONError(w, http.StatusBadRequest, "bad height (want a non-negative integer)")
				return
			}
			target = root + "/block?height=" + q.Get("height")
			method = http.MethodGet
		case path == "rawblocks":
			// RANGE of full serialized blocks (hex). The web wallet's sync() scans
			// EXCLUSIVELY via this path; it was missing from the local --ui proxy, so a
			// self-hosted wallet could not sync its balance (UI-decoupling audit, HIGH).
			from := sanitizeDigits(q.Get("from"))
			cnt := sanitizeDigits(q.Get("count"))
			if cnt == "" || cnt == "0" {
				cnt = "256"
			}
			if n, err := strconv.Atoi(cnt); err == nil && n > 256 {
				cnt = "256"
			}
			target = root + "/blocks?from=" + from + "&count=" + cnt
			method = http.MethodGet
		case get[path] != "":
			target = root + get[path]
			method = http.MethodGet
		case getQ[path] != "":
			// forward sanitized market-data + quote/tx params to the node.
			fwd := url.Values{}
			for _, k := range []string{"pair", "limit", "interval", "maker", "give", "get", "size", "hash"} {
				if v := q.Get(k); v != "" {
					fwd.Set(k, sanitizeParam(v))
				}
			}
			target = root + getQ[path]
			if qs := fwd.Encode(); qs != "" {
				target += "?" + qs
			}
			method = http.MethodGet
		case relayGetQ[path] != "":
			// forward the swap relay params (swap_id/account/hash/key), 128-char cap.
			fwd := url.Values{}
			for _, k := range []string{"swap_id", "account", "hash", "key"} {
				if v := q.Get(k); v != "" {
					fwd.Set(k, sanitizeParamLong(v))
				}
			}
			target = root + relayGetQ[path]
			if qs := fwd.Encode(); qs != "" {
				target += "?" + qs
			}
			method = http.MethodGet
		case path == "order":
			target = root + "/order/" + sanitizeHex(q.Get("id"))
			method = http.MethodGet
		case post[path] != "":
			if r.Method != http.MethodPost {
				writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
				return
			}
			target = root + post[path]
			method = http.MethodPost
		default:
			writeJSONError(w, http.StatusBadRequest, "unknown path")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()

		var body io.Reader
		if method == http.MethodPost {
			body = r.Body
		}
		req, err := http.NewRequestWithContext(ctx, method, target, body)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, "node unreachable")
			return
		}
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/json")
		}
		if hosted {
			req.Header.Set("X-OBX-Proxied", "1") // public web visitor: never trusted as loopback
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, "node unreachable: "+err.Error())
			return
		}
		defer resp.Body.Close()

		w.Header().Set("Content-Type", "application/json")
		if method == http.MethodGet {
			w.Header().Set("Cache-Control", "public, max-age=2")
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}
}

// sanitizeDigits keeps only ASCII digits (block heights).
// serveEmbeddedHTML writes an embedded HTML file's bytes directly (no FileServer
// index.html→/ redirect), so a page like the marketing landing is reachable at a
// stable path on a local node.
func serveEmbeddedHTML(w http.ResponseWriter, sub fs.FS, name string) {
	f, err := sub.Open(name)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "read error")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

// isAllDigits reports whether s is a non-empty run of ASCII digits (a valid
// non-negative integer for a height/param, before any forwarding).
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func sanitizeDigits(s string) string {
	var b strings.Builder
	for _, c := range s {
		if c >= '0' && c <= '9' {
			b.WriteRune(c)
		}
	}
	if b.Len() == 0 {
		return "0"
	}
	return b.String()
}

// sanitizeHex keeps only hex chars, capped at 64 (order ids).
func sanitizeHex(s string) string {
	var b strings.Builder
	for _, c := range s {
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			b.WriteRune(c)
			if b.Len() >= 64 {
				break
			}
		}
	}
	return b.String()
}

// sanitizeParam mirrors the JS proxy's market-data param sanitation: cap at 64 and
// allow only [A-Za-z0-9/_.-].
func sanitizeParam(s string) string {
	if len(s) > 64 {
		s = s[:64]
	}
	var b strings.Builder
	for _, c := range s {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '/' || c == '_' || c == '.' || c == '-' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// sanitizeParamLong is sanitizeParam with a 128-char cap, for the swap-relay
// params (a nano_ address is 65 chars; a 64-hex swap id/key/hash fits too).
func sanitizeParamLong(s string) string {
	if len(s) > 128 {
		s = s[:128]
	}
	var b strings.Builder
	for _, c := range s {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '/' || c == '_' || c == '.' || c == '-' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// writeJSONError writes a small JSON error body with the given status.
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":%q}`+"\n", msg)
}

// openBrowser opens the user's default browser at url (cross-platform, no cgo).
// Best-effort: on failure it logs the URL so the user can open it manually.
func openBrowser(target string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		// rundll32 is the most reliable shell-free way to hand a URL to the default
		// handler; `cmd /c start` is the documented fallback.
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default: // linux and other unixes
		cmd = exec.Command("xdg-open", target)
	}
	if err := cmd.Start(); err != nil {
		if runtime.GOOS == "windows" {
			// Fallback: cmd /c start "" <url>
			if alt := exec.Command("cmd", "/c", "start", "", target); alt.Start() == nil {
				log.Printf("opened browser at %s", target)
				return
			}
		}
		log.Printf("could not open browser automatically (%v) — open this URL manually: %s", err, target)
		return
	}
	log.Printf("opened browser at %s", target)
}
