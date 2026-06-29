//go:build ignore

// validate_html.go — dependency-free HTML/JS/CSS structure validator for the
// Obscura dashboard UI. Always runnable (stdlib only, no browser):
//
//	go run tests/ui/validate_html.go
//
// It asserts the presence of the elements, ARIA hooks, accessibility features
// and offline-safety properties the UI relies on, so a regression in the
// static assets fails CI even without a headless browser.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	root, _ := filepath.Abs(filepath.Join("cmd", "obscura-dashboard", "webui"))
	html := mustRead(filepath.Join(root, "index.html"))
	css := mustRead(filepath.Join(root, "style.css"))
	app := mustRead(filepath.Join(root, "app.js"))
	qr := mustRead(filepath.Join(root, "qr.js"))

	fails := 0
	check := func(name string, cond bool) {
		if cond {
			fmt.Println("PASS", name)
		} else {
			fmt.Println("FAIL", name)
			fails++
		}
	}

	// --- structure ---
	check("doctype present", strings.HasPrefix(strings.TrimSpace(html), "<!DOCTYPE html>"))
	check("lang attribute", strings.Contains(html, `<html lang="en"`))
	check("viewport meta", strings.Contains(html, `name="viewport"`))
	check("title is Obscura", strings.Contains(html, "<title>Obscura"))
	check("favicon linked", strings.Contains(html, `rel="icon"`))
	check("stylesheet linked", strings.Contains(html, `href="style.css"`))
	check("app.js linked with defer", strings.Contains(html, `src="app.js" defer`))
	check("qr.js linked", strings.Contains(html, `src="qr.js"`))

	// --- node panel elements ---
	for _, id := range []string{
		"panel-node", "panel-wallet", "stat-height", "stat-diff", "stat-supply",
		"stat-pool", "stat-anon", "stat-mempool", "stat-backend", "stat-status",
		"height-chart", "blocks-body", "node-offline-banner", "conn-indicator",
		"node-updated",
	} {
		check("node element id="+id, strings.Contains(html, `id="`+id+`"`))
	}

	// --- wallet panel elements ---
	for _, id := range []string{
		"wallet-address", "copy-address", "address-qr", "wallet-balance",
		"wallet-outputs", "send-form", "send-to", "send-amount", "send-fee",
		"send-submit", "confirm-modal", "confirm-send", "confirm-cancel",
		"wallet-missing-banner", "toasts",
	} {
		check("wallet element id="+id, strings.Contains(html, `id="`+id+`"`))
	}

	// --- accessibility ---
	check("skip link", strings.Contains(html, `class="skip-link"`))
	check("noscript fallback", strings.Contains(html, "<noscript>"))
	check("tablist role", strings.Contains(html, `role="tablist"`))
	check("tab role", strings.Contains(html, `role="tab"`))
	check("tabpanel role", strings.Contains(html, `role="tabpanel"`))
	check("dialog role", strings.Contains(html, `role="dialog"`))
	check("aria-modal", strings.Contains(html, `aria-modal="true"`))
	check("aria-live region", strings.Contains(html, `aria-live="polite"`))
	check("aria-label on copy", strings.Contains(html, `aria-label="Copy full address to clipboard"`))
	check("form novalidate", strings.Contains(html, `id="send-form" novalidate`))
	check("status role on conn", strings.Contains(html, `id="conn-indicator"`) && strings.Contains(html, `role="status"`))
	check("alert role for errors", strings.Contains(html, `role="alert"`))
	check("send warning present", strings.Contains(html, "irreversible"))
	check("seed warning in footer", strings.Contains(html, "Never share your seed"))

	// --- offline safety: no external resources ---
	check("no http(s) CDN refs", !strings.Contains(html, "http://") && !strings.Contains(strings.ReplaceAll(html, "https://example", ""), "https://"))
	check("no external fonts in css", !strings.Contains(css, "@import") && !strings.Contains(css, "fonts.googleapis"))
	check("no external script src", !strings.Contains(html, "src=\"http"))

	// --- CSS: themes, responsive, a11y media queries ---
	check("dark theme tokens", strings.Contains(css, `[data-theme="dark"]`))
	check("light theme tokens", strings.Contains(css, `[data-theme="light"]`))
	check("reduced-motion query", strings.Contains(css, "prefers-reduced-motion"))
	check("print styles", strings.Contains(css, "@media print"))
	check("responsive breakpoint", strings.Contains(css, "max-width: 640px"))
	check("focus-visible styles", strings.Contains(css, ":focus-visible"))
	check("44px tap target var", strings.Contains(css, "--tap: 44px"))
	check("tabular-nums for amounts", strings.Contains(css, "tabular-nums"))
	check("forced-colors support", strings.Contains(css, "forced-colors"))

	// --- JS behaviors ---
	check("theme persistence", strings.Contains(app, "localStorage") && strings.Contains(app, "obx-theme"))
	check("confirmation modal logic", strings.Contains(app, "openConfirm"))
	check("focus trap", strings.Contains(app, "trapFocus"))
	check("clipboard copy", strings.Contains(app, "clipboard"))
	check("toast dismissal", strings.Contains(app, "toast-close") || strings.Contains(app, "dismiss"))
	check("OBX 12 decimals", strings.Contains(app, "DECIMALS = 12"))
	check("no seed logging", !strings.Contains(app, "seed") || !strings.Contains(strings.ToLower(app), "console.log"))
	check("send validation", strings.Contains(app, "validateSend"))

	// --- QR is self-contained ---
	check("QR exposes OBXQR", strings.Contains(qr, "window.OBXQR"))
	check("QR has render+generate", strings.Contains(qr, "render:") && strings.Contains(qr, "generate:"))

	if fails == 0 {
		fmt.Println("\nALL PASS (" + countPass(html) + ")")
		os.Exit(0)
	}
	fmt.Printf("\n%d FAILURE(S)\n", fails)
	os.Exit(1)
}

func countPass(_ string) string { return "structure OK" }

func mustRead(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		fmt.Println("cannot read", p, "-", err)
		fmt.Println("run from the repository root:  go run tests/ui/validate_html.go")
		os.Exit(2)
	}
	return string(b)
}
