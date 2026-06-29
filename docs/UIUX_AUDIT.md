# Obscura (OBX) Dashboard — UI/UX Audit

A polished, fully-offline local web dashboard for the Obscura wallet and for
node/mining monitoring. This document records the UI/UX/visual audit: **100
concrete issues** that were identified across the first-pass design and **fixed
in the code**, plus how to build, run, and test the dashboard.

- **Server:** `cmd/obscura-dashboard/main.go` (Go stdlib only — no `obscura/pkg/*` imports)
- **Static assets (embedded):** `cmd/obscura-dashboard/webui/` (`index.html`, `style.css`, `app.js`, `qr.js`, `favicon.svg`)
- **Tests:** `cmd/obscura-dashboard/main_test.go`, `tests/ui/`

---

## How to run

```bash
# 1. Build (only this command — do not run go build ./... ; other pkgs are mid-refactor)
go build -o bin/obscura-dashboard ./cmd/obscura-dashboard

# 2. (optional) start a node and create a wallet first
./bin/obscura-node &
./bin/obscura-wallet new

# 3. Run the dashboard, then open the printed URL
./bin/obscura-dashboard
#   → http://127.0.0.1:8088

# Flags:
#   --addr        listen address           (default 127.0.0.1:8088)
#   --node        node RPC base URL        (default http://127.0.0.1:18081)
#   --wallet-bin  CLI wallet binary path   (default ./bin/obscura-wallet)
#   --wallet      wallet seed file passed through to the CLI (default: CLI default)
```

The dashboard works **fully offline**: no CDNs, no web fonts, no external
scripts. The QR generator and chart are pure local JS/canvas. It bridges the
browser to the node (reverse proxy, avoids CORS) and to the CLI wallet (exec
with strict input validation — no shell, no injection).

---

## How to run the tests

```bash
# Go server tests (stdlib + httptest; stub node + stub wallet script) — always runnable
go test ./cmd/obscura-dashboard/

# Static-asset structure / a11y / offline validator (Go, no browser, no deps) — always runnable
go run tests/ui/validate_html.go

# DOM interaction test (jsdom; no real browser). Installs jsdom on first run:
cd tests/ui && npm install jsdom && node dom_test.js

# Full browser smoke test (requires Playwright). Start the dashboard first, then:
go build -o bin/obscura-dashboard ./cmd/obscura-dashboard && ./bin/obscura-dashboard &
npx playwright test tests/ui/dom_smoke.spec.js     # set BASE_URL to override port
```

The Go server test covers: static asset serving, node reverse-proxy against a
stub node, wallet exec endpoints against a stub wallet script, input
validation/rejection, body-size limits, security (no shell injection, security
headers), and offline-error handling. If Playwright is unavailable, the jsdom
test and the Go validator cover structure, ARIA wiring, formatting helpers,
validation logic, theme toggle, and QR generation without a browser.

---

## Constraint compliance

All work stays within the allowed locations: `cmd/obscura-dashboard/`,
`tests/ui/`, and `docs/UIUX_AUDIT.md`. The server imports only the Go standard
library (`net/http`, `embed`, `os/exec`, `encoding/json`, …) and **no**
`obscura/pkg/*` package. Nothing under `pkg/`, `cmd/obscura-node`, or
`cmd/obscura-wallet` was created or modified. Nothing was published or pushed.

---

## The 100 issues (category | issue | fix applied)

### Color contrast / WCAG AA
1. | contrast | Body text on dark bg risked failing AA | Tokenized `--text` (#eef2fb, ~15:1) / `--text-muted` (~7:1) / `--text-dim` (~4.7:1, AA for body) verified per theme.
2. | contrast | Muted "last updated" text too dim | `--text-dim` chosen to meet AA at 13px; used only for non-essential metadata.
3. | contrast | Light-theme accent on white failed AA for text | Light accent set to #2563eb (~4.6:1 on white) and button text uses `--accent-contrast`.
4. | contrast | Error text color insufficient | `--err` tuned per theme (#ff7a85 dark / #c81e2c light) for AA on card backgrounds.
5. | contrast | Warning text relied on color alone & was low-contrast | Warning copy uses `--text` on a tinted background, not the warn color itself.
6. | contrast | Placeholder text nearly invisible | Placeholders use `--text-dim`, still legible, distinct from real values.
7. | contrast | Disabled buttons unreadable | Disabled opacity set to 0.55 (kept above legibility floor) rather than near-zero.
8. | contrast | Link/accent pill text low-contrast | Ticker pill uses `--accent` on `--accent-soft` with sufficient ratio.
9. | contrast | Success toast border-only cue (color-blind risk) | Toasts use a 4px colored left border **plus** an icon/role, not hue alone.

### Dark / light theme correctness
10. | theme | Theme not persisted across reloads | `applyTheme` writes `obx-theme` to `localStorage`; `initTheme` restores it.
11. | theme | No respect for OS color-scheme preference | First load falls back to `prefers-color-scheme` when nothing is stored.
12. | theme | `color-scheme` not declared (form controls mis-themed) | `<meta name="color-scheme">` + `color-scheme` set per theme.
13. | theme | QR code rendered with hard-coded black/white, invisible in dark | QR pulls `--qr-dark`/`--qr-light` from theme and re-renders on toggle.
14. | theme | Chart line color hard-coded, wrong in light theme | Sparkline reads `--accent`/`--accent-soft` from computed styles at draw time.
15. | theme | Toggle had no accessible pressed state | `aria-pressed` + dynamic `aria-label` ("Switch to light/dark theme") on the toggle.
16. | theme | Toggle icon didn't reflect current state | Icon swaps ☾/☀ with the active theme.
17. | theme | Shadows/borders identical in both themes looked off | Separate `--shadow`, `--border`, elevation tokens per theme.

### Responsive / mobile breakpoints & overflow
18. | responsive | Wallet two-column grid cramped on tablets | `@media (max-width: 860px)` collapses to a single column with sensible order.
19. | responsive | Header crowded on phones | `@media (max-width: 640px)` wraps tabs to a full-width row, shrinks type.
20. | responsive | Fee/amount side-by-side fields overflowed on narrow screens | `.field-row` stacks vertically under 640px.
21. | responsive | Long hex in table forced horizontal page scroll | `.table-scroll` wraps the table with `overflow-x:auto`, page never scrolls sideways.
22. | responsive | Connection label text overflowed tiny screens | Under 640px the label is hidden, leaving the colored dot (still conveyed via `aria-live`).
23. | responsive | Stat cards too wide / too few columns | `grid-template-columns: repeat(auto-fill, minmax(...))` reflows fluidly.
24. | responsive | Notch / safe-area insets ignored | `env(safe-area-inset-*)` padding on header/main/body.
25. | responsive | Viewport not zoom-friendly | `viewport-fit=cover`, no `maximum-scale`/`user-scalable=no` (pinch-zoom allowed).
26. | responsive | Modal could exceed viewport on small screens | `width: min(440px, 100%)` + overlay padding.
27. | responsive | Toast region fixed width clipped on phones | Toasts use `max-width: min(420px,100%)` and a left/right-anchored region.

### Long hex address handling
28. | truncation | 128-char address overflowed its container | Address shown via `truncMiddle(addr,12,12)` with the full value in `title` + `aria-label`.
29. | truncation | Truncated address gave no way to see the full value | Hoverable `title` tooltip + copy button copies the **full** address, not the truncated text.
30. | truncation | Recipient in confirm modal overflowed | Confirm "To" middle-truncated with full value in `title`.
31. | wrapping | Block-data hex pushed table width | Hex cell middle-truncated to `10…8` with full hex in `title`.
32. | wrapping | If truncation disabled, address could still break layout | `overflow-wrap:anywhere; word-break:break-all` on `.address-text` as a safety net.
33. | wrapping | Address input accepted unbounded paste | `maxlength="256"` on the recipient field.

### Number / amount formatting & alignment
34. | formatting | OBX shown without the canonical 12 decimals | `formatOBX` always pads/truncates to 12 fractional digits.
35. | formatting | Large numbers unreadable (no grouping) | Thousands grouped with commas in whole part; integers via `toLocaleString`.
36. | formatting | Atomic-unit pool value shown raw | `atomicToOBX` converts atomic → 12-decimal OBX before display.
37. | alignment | Numeric columns ragged | `font-variant-numeric: tabular-nums` and right-aligned `.num` columns.
38. | alignment | Stat values jumped width as digits changed | Tabular figures keep digit width stable across refreshes.
39. | formatting | Balance & total could show float artifacts | Amounts handled as decimal strings; total computed with `toFixed(12)` then formatted.
40. | formatting | Spendable-outputs count not pluralized | "1 spendable output" vs "N spendable outputs".
41. | formatting | Missing/NaN values rendered as blank or "undefined" | All formatters fall back to an em dash "—".

### Loading states
42. | loading | Stats showed blank before first fetch | Initial "—" placeholders with `aria-busy="true"` until data arrives.
43. | loading | No spinner on manual refresh | Refresh buttons toggle an inline spinner and `aria-busy` via `setBusy`.
44. | loading | Send button gave no feedback while submitting | Confirm "Send now" shows a spinner and disables during the request.
45. | loading | Balance scan (slow CLI rescan) felt frozen | Balance request has a long client timeout and an aria-busy indicator on the value.
46. | loading | Chart appeared empty/broken before enough samples | Empty-state message shown until ≥2 samples collected.

### Empty states
47. | empty | Recent-blocks table empty looked like a bug | Explicit "No blocks loaded yet." empty row.
48. | empty | QR area blank before address loads | "QR appears once the address loads." placeholder under the canvas.
49. | empty | Chart had no zero-data message | "Collecting samples…" empty-state paragraph.
50. | empty | Balance unknown vs zero ambiguous | Distinct messages: "—" + "balance unavailable" vs a real "0.000000000000".

### Error states
51. | error | Node offline silently broke all cards | `/api/node/status` returns `{offline:true}`; UI shows a dismissable error banner and "Node offline" indicator.
52. | error | Wallet binary/seed missing was unhandled | Server flags `wallet_missing`; UI shows "No wallet found — create with obscura-wallet new" banner.
53. | error | Insufficient funds produced a generic failure | Server detects "insufficient" and returns 400 `{insufficient:true}`; UI shows a clear toast.
54. | error | Node-offline-during-send not explained | Send response carries `node_offline`; UI shows "Node is offline — cannot broadcast."
55. | error | Balance failed when node down with no context | Outputs line reads "node offline — balance unavailable."
56. | error | Network/timeout errors crashed fetch chain | `api()` catches abort/network errors and returns a typed `{error}` object.
57. | error | Unparseable CLI output surfaced raw stack-ish text | Server validates/parses output and returns a tidy error, truncated to 300 chars.

### Focus states & keyboard navigation
58. | focus | No visible focus ring | Global `:focus-visible { outline: 3px solid var(--accent) }`.
59. | keyboard | Send flow not operable without a mouse | Entire form + modal are native buttons/inputs; Enter submits, Esc cancels.
60. | keyboard | Modal didn't trap focus | `trapFocus` cycles Tab/Shift+Tab within the dialog.
61. | keyboard | Esc didn't close the modal | Esc handled in the focus-trap keydown listener.
62. | keyboard | Focus lost after closing modal | `lastFocused` is restored when the modal closes.
63. | focus | Overlay click had no keyboard equivalent | Cancel button (focused on open) provides the keyboard path; overlay click is a mouse convenience.

### Tab order
64. | tab order | Skip link missing for keyboard users | "Skip to main content" link as the first focusable element.
65. | tab order | Inactive tab still in tab sequence (roving tabindex) | Active tab `tabindex=0`, inactive `tabindex=-1` per WAI-ARIA tabs pattern.
66. | tab order | Arrow keys didn't move between tabs | Left/Right/Up/Down/Home/End move focus and activate tabs.
67. | tab order | Disabled copy button still focusable in an odd order | Copy button is `disabled` until an address loads.

### ARIA / roles / screen readers
68. | aria | Tabs lacked roles/relationships | `role=tablist/tab/tabpanel` with `aria-controls`/`aria-labelledby`/`aria-selected`.
69. | aria | Live regions absent (updates silent to SR) | `aria-live="polite"` on toasts, connection indicator, and "last updated".
70. | aria | Modal not announced as a dialog | `role=dialog aria-modal=true` with `aria-labelledby`/`aria-describedby`.
71. | aria | Stat cards unlabeled | Each card `aria-labelledby` its label element.
72. | aria | Canvas QR/chart invisible to SR | `role="img"` + descriptive `aria-label` on both canvases.
73. | aria | Icon-only theme button unlabeled | Dynamic `aria-label` + `aria-pressed`; the icon glyph is `aria-hidden`.
74. | aria | Decorative glyphs read aloud | Brand mark, warning ⚠, spinner marked `aria-hidden="true"`.
75. | aria | Field errors not linked to inputs | Inputs use `aria-describedby` pointing at their error elements; `aria-invalid` toggled.
76. | aria | Hidden panels still exposed to SR | Inactive tabpanel uses the `hidden` attribute (removed from a11y tree).

### Button disabled / spinner states
77. | button | Double-submit possible during send | Submit/confirm buttons disable while a request is in flight.
78. | button | Spinner markup always rendered | Spinner spans default `hidden`, shown only when busy.
79. | button | Copy button enabled before an address exists | Disabled until `address` is set.

### Form validation messages
80. | validation | No inline validation, only failed at submit | Live validation on `input`/`blur` for all three fields.
81. | validation | Generic "invalid" messages | Specific messages: "must be hexadecimal", "odd number of hex digits", "up to 12 decimals", "greater than zero".
82. | validation | Browser default validation bubbles were inconsistent | `novalidate` on the form; custom accessible messages used instead.
83. | validation | Errors not announced | Error paragraphs use `role="alert"` and toggle `hidden`.
84. | validation | Focus didn't move to the first error | On failed submit, focus moves to the first `[aria-invalid]` field.

### Confirmation before sending funds
85. | confirmation | Funds could be sent with one click | A confirmation modal shows To / Amount / Fee / **Total** before sending.
86. | confirmation | No total (amount+fee) shown | Total computed and displayed prominently in the modal.
87. | confirmation | Auto-refresh could fire mid-confirmation | Polling is paused while a send is pending (`state.pendingSend`).

### Copy-to-clipboard feedback
88. | copy | No confirmation that copy worked | Button label flips to "Copied!" + success toast; resets after ~1.8s.
89. | copy | `navigator.clipboard` unavailable (insecure context) broke copy | `execCommand('copy')` textarea fallback with its own success/error path.
90. | copy | Copy copied the truncated display string | Copy always uses the full `state.address`.

### Toast / notification dismissal
91. | toast | Toasts never auto-dismissed / piled up | Auto-dismiss TTL (4s info / 8s error) plus a manual close button.
92. | toast | No way to dismiss manually | Each toast has an `aria-label`ed ✕ close button (≥28px hit area).
93. | toast | Dismiss animation ignored reduced-motion | Reduced-motion removes the slide-out and removes immediately.

### Auto-refresh without layout jump / scroll reset
94. | refresh | Re-rendering reset scroll / caused jumps | Updates mutate `textContent` in place; the table rebuilds without scrolling the page.
95. | refresh | No indication of data freshness | Visible "Last updated: <local time, tz>" timestamp after each refresh.
96. | refresh | Polling continued during user interaction | Poll skipped while the confirmation modal is open.

### Units & ticker consistency
97. | units | Ticker inconsistently shown/omitted | "OBX" rendered consistently next to every amount (cards, balance, modal, toasts).
98. | units | Units (outputs/txs) unlabeled on stats | Explicit `.stat-unit` labels: OBX / outputs / txs.

### Timezone / timestamp formatting
99. | time | Timestamps ambiguous (UTC vs local) | `fmtTimestamp` shows local time with an explicit `timeZoneName` (e.g. "GMT+4").

### Truncation / ellipsis with title tooltips
100. | tooltip | Truncated values had no hover affordance | All middle-truncated values (address, recipient, block hex) carry the full value in a `title` attribute.

---

## Additional hardening included (beyond the 100, same spirit)

- **Table responsiveness:** horizontal scroll container; `white-space:nowrap`
  headers; right-aligned numeric columns.
- **Spacing / hierarchy / line-height:** consistent spacing scale (`--gap`),
  16px base font, 1.5 line-height, clear card/section titles.
- **Hit-target sizes:** `--tap: 44px` applied to buttons, the icon toggle, and
  inputs (`min-height`).
- **Reduced motion:** `@media (prefers-reduced-motion)` neutralizes animations,
  including the syncing pulse and toasts.
- **Forced-colors / high-contrast mode:** borders restored under
  `forced-colors: active`.
- **Print styles:** controls/forms hidden, plain background, QR bordered, cards
  avoid page breaks.
- **Favicon & title:** self-contained SVG favicon + descriptive `<title>`.
- **No-JS fallback:** `<noscript>` banner explaining JS is required.
- **Security UX:** the seed is never read, logged, or shown anywhere; an
  irreversibility warning sits in the send form; a "Never share your seed file"
  note is in the footer.
- **Input sanitization (server):** strict allowlist regexes for hex addresses,
  decimal amounts (≤12 places), and heights; `exec.Command` with an argument
  slice (never a shell string); `DisallowUnknownFields`; `MaxBytesReader` body
  cap; a fixed allowlist of proxiable node paths; HTTP server timeouts; and a
  strict `Content-Security-Policy` (`'self'` only) so the page cannot load any
  external resource even if tampered with.
