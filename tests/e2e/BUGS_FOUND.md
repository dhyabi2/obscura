# Obscura Web Wallet — e2e Test Results + Bugs Found

Playwright e2e against a live `obscura-node --ui` (latest wallet) at `127.0.0.1:18099`.
See `USE_CASES_100.md` for the full plan; per-batch detail in `bugs_restore.md`, `bugs_amounts.md`, `bugs_ui.md`.

## Test totals (automated this run)
| Batch (spec) | Pass | Fail | Skip | Bugs |
|---|---|---|---|---|
| A/B load+security+encryption (`a_load_security.spec.js`) | 6 | 0 | 0 | 0 |
| Restore + backup gate (`b_restore_backup.spec.js`) | 14 | 0 | 0 | 0 |
| Amounts/BigInt + multi-tab (`c_amounts_multitab.spec.js`) | 16 | 0 | 7 | 0 |
| Disclosure/vaults/swaps/explorer (`d_disclosure_vaults_swaps.spec.js`) | 21 | 1 | 4 | 3 |
| **Total** | **57** | **1** | **11** | **3** |

Skips are funded-only (need mined balance) or real-XNO legs (need `OBX_NANO_FUND_SECRET`); tests are wired to run when those are present.

## Verified-correct (no bugs) — highlights
- WASM SHA-384 integrity gate passes; all `obx*` functions present; build-hash footer matches; `obxReady` never precedes function availability.
- Seed encrypted at rest (PBKDF2 ≥200k + AES-GCM); plaintext `obx_mnemonic` never written; legacy migration; no-lockout recovery.
- Backup-confirm gate (send disabled until confirmed); audit-#5 12/24-word guard (no silent different-wallet); restore address-confirmation; deterministic derivation.
- BigInt amounts: exact 12-decimal fixed-point; `90000.000000000001` survives (float53 collapse avoided); >12 decimals / zero / negative / sub-atom rejected; malformed address rejected with no exception.
- Multi-tab lock (heartbeat + BroadcastChannel) with owner handover and no false lockout.
- Trust banner present + says "encrypted"; run-your-own-node guidance (auto-softened on localhost); export/import state round-trip; empty-chain sync; graceful network-error; zero uncaught exceptions across the full flow.

---

## BUGS

### BUG-1 — operator XNO `nano_` address exposed via the node's UI proxy whitelist — MEDIUM (privacy)
- **Where:** `cmd/obscura-node/ui.go` (UI `/api/explorer` proxy whitelists `xnoaccount` → `/xno/account`).
- **Symptom:** `curl 'http://<node>/api/explorer?path=xnoaccount'` → 200 + `{"address":"nano_…", "balance_raw":…, "backend":…}`.
- **Impact:** On a HOSTED node, any anonymous visitor reads the operator's real Nano proceeds account (links operator on-chain XNO activity). Note: the Vercel proxy was already fixed for this (audit S3a) — but the **node's embedded UI proxy** still exposes it. On a local desktop node it's the user's own account (low impact there).
- **Expected:** not reachable by untrusted callers (audit S3a, UC70).
- **Fix:** gate operator-sensitive XNO paths behind trust/`publicSwaps` (see BUG-2 fix — same root cause), or drop `xnoaccount` from the public UI-proxy whitelist on a hosted node.

### BUG-2 — S3 swap-take gate bypassed for ALL UI-proxy callers — HIGH (on a hosted node)
- **Where:** `cmd/obscura-node/ui.go` (`uiExplorerProxy` forwards to the in-process RPC over loopback) + `pkg/rpc/server.go` `s.trusted(r)` (= loopback || bearer) + the gate at `pkg/rpc/swaps.go:283` (`!trusted && !publicSwaps`).
- **Symptom:** `POST /api/explorer?path=swapstake` is treated as a TRUSTED loopback caller, so the "swap-taking disabled on this public node" refusal never fires; the take proceeds past the gate (replies `bad offer_id`/`offer not found`, not the S3 refusal).
- **Impact:** On a hosted node serving the wallet to the public with `OBX_PUBLIC_SWAPS` off, an anonymous visitor's swap-take executes **as trusted** — exactly the operator-funded drain S3 was meant to prevent. The gate is effectively dead for proxy traffic.
- **Root cause:** the UI proxy makes every public request look like loopback to the RPC.
- **Fix (recommended):** distinguish a **hosted** UI from a **local desktop** UI (operator config: a `--ui-public`/`OBX_UI_PUBLIC` flag, or the UI bound to a non-loopback address). In hosted mode the UI proxy must mark forwarded requests UNTRUSTED (e.g. an internal `X-OBX-Proxied` header that `s.trusted` treats as untrusted), so operator-sensitive paths (`swapstake`, `xnoaccount`, XNO withdraw/recovery) are gated by `publicSwaps`/trust. Default desktop mode (UI on 127.0.0.1, single local operator) stays trusted. This fixes BUG-1 and BUG-2 together.

### BUG-3 — swap/maker fee never shown in the swap UI — LOW (UX/disclosure)
- **Where:** `website/wallet.html` swap tab (`#t-swap`): order book, post-offer form, `offerHint`, and the take confirm dialog.
- **Symptom:** no fee string anywhere in the swap section; the take dialog shows receive/pay amounts but not the maker fee (default 0.001 OBX, `pkg/rpc/server.go` swapFee).
- **Impact:** a taker confirms a trade without seeing the fee they implicitly pay.
- **Fix:** render the configured maker fee in the offer hint + take confirm dialog.

## Fix status (2026-06-28)
- **BUG-1 FIXED + verified.** Hosted UI marks proxied requests `X-OBX-Proxied`; `/xno/account` refuses them (`pkg/rpc/xno.go`). Verified: `/api/explorer?path=xnoaccount` -> HTTP 403 in hosted mode (was 200+nano_). Direct/local-operator access unchanged (public direct-call unit test still green).
- **BUG-2 FIXED + verified.** `s.trusted()` returns false for `X-OBX-Proxied` requests (`pkg/rpc/server.go`), so the S3 `/swaps/take` gate now fires for hosted public callers. Verified: swapstake via proxy -> "swap-taking is disabled on this public node" (was slipping through to offer logic). Hosted detection: non-loopback `--ui-addr` or `OBX_UI_PUBLIC=1` (`cmd/obscura-node/ui.go`), with a startup warning. Desktop (loopback) stays trusted.
- **BUG-3 FIXED.** Maker fee (0.001 OBX, SWAP_FEE_OBX mirroring pkg/rpc swapFee) now shown in the offer hint + the take-confirm dialog (website/wallet.html). The fee is not in any proxied response, so it is a labeled constant.
- Build + vet clean; `pkg/rpc` tests green.
