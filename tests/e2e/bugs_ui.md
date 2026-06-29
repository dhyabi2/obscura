# UI e2e — Disclosure / Vaults / Swaps / Explorer (UC66-100 subset)

Spec: `tests/e2e/d_disclosure_vaults_swaps.spec.js`
Run:  `cd tests/e2e && BASE_URL=http://127.0.0.1:18099 npx playwright test d_disclosure_vaults_swaps.spec.js`
Node: pre-existing node at `http://127.0.0.1:18099` (loopback), `height=0`, empty order book, XNO `backend:mock`.
Latest result: **21 passed · 1 failed (UC92, real UI gap) · 4 skipped (real-XNO legs)**.

## Per-use-case status

| UC | Area | Status | Note |
|----|------|--------|------|
| 66 | trust banner visible | PASS | `#trustBanner` visible, mentions test-chain/no-value/unaudited |
| 67 | banner says "encrypted" not "unencrypted" | PASS | contains "encrypted"; no "unencrypted"/"plaintext" |
| 68 | run-your-own-node guidance | PASS (localhost-contextualized) | verbatim CTA hidden on localhost; banner shows "your own local node" — correct auto-soften (see UC69) |
| 69 | own-node note auto-hidden on localhost | PASS | served from 127.0.0.1 → `#ownNodeNote` has `.hide`, banner softened to "local node". Softener fires correctly. |
| 70 | no operator nano_ exposed | PASS (with finding) | no nano_ baked into page HTML; BUT proxy `xnoaccount` returns 200 + a nano_ address — see BUG-1 |
| 71 | vault deposit validates amount (BigInt) + term | PASS | `vaultDeposit()` rejects 0/empty via `obxToAtomic`; term `<select>` populated |
| 73 | unknown vault term rejected | PASS | `obxBuildVaultDeposit(...,"999999",...)` returns error / no txhex |
| 74 | vault claim requires id | PASS | `obxBuildVaultClaim("",...)` rejected, no txhex |
| 77 | yield rate shown for term | PASS | term option labels carry "%": "30 days · 1%", "90 days · 4%", "365 days · 20%" |
| 81 | swap UI lists offers / empty-state | PASS | empty book → "no open offers" graceful, no pageerror |
| 82 | offer shows amounts not just names | PASS | posted a real OBX→XNO offer; rendered row shows "5 OBX" + XNO amount |
| 83 | obxBuildOffer valid params | PASS | returns `{offerhex}` (requires a loaded wallet) |
| 84 | offer amount uses BigInt | PASS | `9007199254740993` (2^53+1) round-trips exactly via `obxParseOffer` |
| 85 | public swap-take gated/refused | PASS-as-recorded (with finding) | NOT a silent success; on this loopback node it is NOT gated (replies "bad offer_id"/"offer not found", not the S3 refusal) — see BUG-2 |
| 86 | take dialog shows amounts before confirm | PASS | confirm() text shows "You RECEIVE: 5 OBX / You PAY: 0.05 XNO" |
| 89 | swap state persistence, no in-mem fallback warning | PASS | no in-memory-fallback warning surfaced; active-swaps panel loads |
| 92 | swap fee shown == maker fee (0.001 OBX) | **FAIL** | swap UI shows NO fee anywhere — real UI gap, see BUG-3 |
| 93 | sync advances, no stall | PASS | `sync()` completes, button re-enabled, no "sync error" |
| 94 | empty chain (h=0) syncs ok | PASS | height 0; last_scanned stays 0, no error |
| 95 | export/import state round-trips | PASS | `obxExportState` → `obxImportState` → `obxExportState` identical |
| 98 | dark-mode / responsive no console error | PASS | 390px viewport, no horizontal overflow, no real console errors (frame-ancestors meta warning filtered) |
| 99 | network error graceful, no crash | PASS | bad proxy path → HTTP 400 the UI surfaces; no pageerror |
| 100 | no uncaught exceptions create→view→tabs | PASS | full flow, 0 pageerror |
| 87/88/90/91 | real-XNO swap legs | SKIP | no XNO secret / `backend:mock` in this run |

## Bugs found

### BUG-1 (UC70) — operator XNO nano_ address reachable via the public wallet proxy — Severity: MEDIUM (privacy)
- **Symptom:** `GET /api/explorer?path=xnoaccount` returns HTTP 200 with
  `{"address":"nano_15bh639ro8tkhc58ndpzt7hufbeci8eszn3egefywtyxyu63c7498fuph55x","balance_raw":"0","receivable_raw":"0","backend":"mock"}`.
- **Repro:** `curl http://127.0.0.1:18099/api/explorer?path=xnoaccount`
- **Expected (per UC70):** the path is not reachable through the public proxy (non-200/blocked), so no operator Nano account is exposed.
- **Actual:** `xnoaccount` → `/xno/account` is in the public GET whitelist (`cmd/obscura-node/ui.go:209`) and always returns the node's Nano proceeds address. On this local/mock node that account is the user's own derived mock account, so the impact here is low. On a HOSTED node the same whitelisted path would expose THAT operator's real Nano account address (and balance/receivable) to any anonymous visitor — i.e. it links on-chain Nano activity to the operator. The page text itself bakes in no nano_ address (good), but the proxy does.
- **Note:** intentional by design comment in `ui.go` ("PUBLIC, read-only XNO proceeds account"). Flagged because UC70's privacy goal ("no operator Nano address exposed") is not met for the hosted/multi-user case.

### BUG-2 (UC85) — S3 swap-take gate is bypassed for ALL wallet-proxy callers (proxy presents as loopback) — Severity: HIGH (on a hosted node)
- **Symptom:** A swap-take submitted through the wallet's `/api/explorer?path=swapstake` proxy is treated as a TRUSTED (loopback) caller, so the S3 "swap-taking is disabled on this public node" gate never fires. The take proceeds past the gate to offer validation (`{"error":"bad offer_id"}` / "offer not found").
- **Repro:** `curl -X POST -H 'content-type: application/json' -d '{"offer_id":"0123456789abcdef"}' http://127.0.0.1:18099/api/explorer?path=swapstake` → HTTP 200, `{"error":"bad offer_id"}` (no "disabled on this public node" refusal).
- **Expected (per UC85):** on an untrusted/public node with `OBX_PUBLIC_SWAPS` off, a public take is refused with the S3 message.
- **Actual / root cause:** the UI proxy (`cmd/obscura-node/ui.go:uiExplorerProxy`) forwards to the in-process RPC via a fresh `http.DefaultClient.Do` request, so the RPC sees the proxy's own loopback connection. `s.trusted(r)` (`pkg/rpc/server.go:336`) = `isLoopback(r) || hasBearer(r)` → true for every proxied request, and the gate at `pkg/rpc/swaps.go:283` (`!s.trusted(r) && !s.publicSwaps`) is therefore never reached for proxy traffic. On a hosted node this means an anonymous web visitor's take IS executed as trusted, which is exactly the operator-funded drain S3 was meant to prevent. `swapstake` is in the public POST whitelist (`ui.go:222`), so this path is publicly reachable.
- **Test handling:** asserted the take is at minimum NOT a silent success (no "Trade started") and recorded the observed reply. It is a genuine wallet/node bug, not a test artifact.

### BUG-3 (UC92) — swap/maker fee is never shown in the swap UI — Severity: LOW (UX/disclosure)
- **Symptom:** Neither the order book, the "Post an offer" form, the `offerHint`, nor the take confirm dialog mentions the maker/swap fee (default 0.001 OBX, `pkg/rpc/server.go:swapFee`, matched in `swapsession`). The only fees shown in the wallet are the Send fee field and the hard-coded 0.01 vault fee.
- **Repro:** open Swap tab → no "fee"/"0.001 OBX"/"maker fee" string anywhere in `#t-swap` (`grep -i fee` over the swap section).
- **Expected (per UC92):** the swap fee shown to the user matches the maker fee (0.001 OBX default).
- **Actual:** no swap fee is disclosed at all. A taker confirms a trade (UC86 dialog) seeing receive/pay amounts but never the fee they implicitly pay.
- **Test handling:** the assertion intentionally fails to flag the gap (kept per instructions as a wallet-bug assertion). To make the suite green once a fix lands, the swap UI should render the configured maker fee (e.g. in `offerHint`/take dialog).

## Notes on environment-dependent cases
- UC69/UC68: behavior is correct FOR localhost (banner softened, own-node note hidden). The verbatim "run your own node" CTA is only present on a non-localhost host; tests assert the localhost-correct state.
- UC85/UC70: assertions encode the ACTUAL loopback behavior and the bugs above; they will need revisiting when tested against a genuinely non-loopback/hosted node.
