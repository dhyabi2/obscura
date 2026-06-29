# New-User Critical-Issues Register (Obscura OBX + OBX↔XNO DEX)

**Scope:** what a brand-new user actually hits when they arrive at `obscura-blush.vercel.app` to *mine* or *use the wallet/swap* — nothing assumed about operator knowledge. Synthesized from 5 parallel role-play audits (149 raw findings → 101 deduplicated). Date: 2026-06-27.

**Severity legend:**
- 🔴 **FUND-LOSS / SECURITY** — irreversible loss, theft, drain, or deanonymization.
- 🟠 **BROKEN-FLOW** — the user cannot complete the core task at all, or silently fails.
- 🟡 **CONFUSING-UX** — works, but misleads the user into a mistake or dead end.

Each item: `what the user hits` · `file:line` · `one-line fix`.

---

## 0. STRUCTURAL SHOWSTOPPERS (read these first)

These five are not bugs — they're the gap between "looks like a product" and "is one." Everything below is downstream of them.

- **S1 🔴 You cannot mine from the website at all.** The site is static + a read-only RPC proxy; mining requires downloading a binary that *isn't linked anywhere*. `website/*`, `website/api/explorer.js` · **Fix:** add a Downloads page linking `dist/` bundles, or drop the "easily mine" promise from the landing copy.
- **S2 🟠 A fresh app mines a worthless ISOLATED fork.** `config.DefaultSeeds = ["192.0.2.1:18080"]` is an RFC5737 placeholder (unroutable), so a new node never finds the real network and mines its own dead chain. `pkg/config/params.go` · **Fix:** ship real seed IPs (or a DNS seeder) before any public "mine" CTA.
- **S3 🔴 Public swap-taking drains the OPERATOR's XNO, and the taker holds nothing.** `/swaps/take` routes XNO funding to the operator's configured account and OBX proceeds to the node's own key — the website user supplies no key and receives no asset. `pkg/rpc/swaps.go:434`, `cmd/obscura-node/swapwire.go:237-240,323` · **Fix:** gate `/swaps/take` behind operator auth until per-user XNO funding + receive-address are wired; until then it is a demo, not a DEX.
- **S4 🔴 The shared-node architecture nullifies the privacy promise.** Every website user routes through ONE Vercel proxy → ONE operator node; operator + Vercel see every IP, query, balance scan, and swap intent. A "privacy coin" with a central observer for all web users. `website/api/explorer.js:1-10` · **Fix:** disclose the trust model on the wallet page and document the run-your-own-node path; long-term, multi-node + client IP masking.
- **S5 🔴 Every downloadable binary is unsigned.** macOS Gatekeeper and Windows SmartScreen will block the `.app`/`.exe`; a new user sees "damaged / unverified developer" and quits. `scripts/package-desktop.sh`, `dist/*` · **Fix:** code-sign + notarize (macOS) and Authenticode-sign (Windows), or publish exact bypass instructions on the Downloads page.

---

## 1. 🔴 FUND-LOSS / SECURITY (irreversible)

### Key custody & seed
1. **Seed lives only in plaintext `localStorage`, no encryption.** Any injected script / extension / compromised CDN reads `obx_mnemonic` and drains the wallet. `website/wallet.html:286` · **Fix:** encrypt the seed with a user passphrase (WebCrypto) before persisting.
2. **Clearing browser data = irreversible total loss, never enforced backup.** No "write this down, confirm it back" gate before the wallet can transact. `website/wallet.html:267-292` · **Fix:** block first send until the user re-enters the mnemonic to confirm backup.
3. **No CSP / no Subresource-Integrity on `wasm_exec.js` + `wallet.wasm`.** A compromised Vercel deploy can swap in a key-exfiltrating WASM and nobody can tell. `website/wallet.html:239-240` · **Fix:** add a strict CSP header and SRI hash; publish the expected wasm hash.
4. **WASM binary authenticity unverifiable.** No signature/hash shown, so users trust the CDN blindly. `website/wallet.html:239` · **Fix:** display the build hash and link a reproducible-build attestation.
5. **12-vs-24-word restore mismatch silently yields a DIFFERENT wallet.** A copy-paste that drops words still decodes, derives a new address; funds sent there are unrecoverable. `cmd/obscura-wasm/main.go:47-73` · **Fix:** show the derived address + a checksum and make the user confirm it matches their record.
6. **Miner-payout seed (`miner.seed`) is never surfaced for backup.** Lose the node's data dir → lose all mined rewards; the user is never told it exists. `cmd/obscura-node/*` · **Fix:** on first mine, print + prompt-to-back-up the payout seed.

### Swap fund-drain / mis-ownership
7. **Operator XNO drained by any website visitor (no auth, no rate-limit).** Repeated `/swaps/take` locks operator XNO into joint accounts the taker half-controls. `pkg/rpc/swaps.go:434`, `pkg/rpc/server.go:233` · **Fix:** operator-auth + per-IP rate-limit on `/swaps/take`.
8. **Taker never receives the OBX they "bought"** — it's claimed to a node-internal key, not a user address. `cmd/obscura-node/swapwire.go:237-240` · **Fix:** require a user OBX receive-address in the take request.
9. **Taker cannot supply their own XNO funding** — only the operator's account funds locks, so a real user can't be an independent taker. `pkg/swapd/nanorpc.go:226-260` · **Fix:** accept a user-provided Nano funding secret/session.
10. **XNO locked before the maker's OBX funding is verified in some retry paths** → XNO stranded in a 2-of-2 the maker can ignore. `pkg/swapsession/session.go:555-586` · **Fix:** hard-gate `nano.Lock` on a confirmed on-chain Funded tx; make the lock idempotent on retry.
11. **No max-age refund for a stalled XNO lock.** Stuck in `xno_lock` → XNO frozen indefinitely, manual operator refund only. `cmd/obscura-node/swapwire.go:324` · **Fix:** auto-refund after a deadline.
12. **Swap state silently degrades to in-memory if the `swapstate` dir can't be created.** Node crash mid-swap = lost swap, locked funds unclaimable, user never warned. `cmd/obscura-node/swapwire.go:308-312` · **Fix:** fail loudly (refuse to start swaps) instead of warn-and-continue.
13. **Manual-only refund for stalled swaps.** If the taker never returns, only an operator CLI call frees the maker's funds. `cmd/obscura-node/swapwire.go:181-194` · **Fix:** automatic timelock refund driver.

### Privacy / deanonymization
14. **Operator + Vercel log every user IP, query, balance scan, swap intent.** Defeats stealth addresses for all web users. `website/api/explorer.js:1-10` · **Fix:** disclose + strip/forward-anonymize client IPs; offer local-node mode.
15. **`/xno/account` is public and returns the operator's real Nano address.** Anyone can read it and trace operator proceeds on the Nano chain. `pkg/rpc/xno.go:70-99`, `website/api/explorer.js:35` · **Fix:** remove `xnoaccount` from the public proxy whitelist.
16. **Auto-liquidity auto-sells a miner's freshly mined OBX**, leaking the miner→XNO link on a public ledger — ironic for a privacy coin, and on by default. `cmd/obscura-node/*` (auto-liquidity) · **Fix:** default auto-liquidity OFF; warn before enabling.
17. **Wallet scan-range timing deanonymizes wallet age/activity to the operator.** First-scan height + sync gaps reveal creation time and idle windows. `website/api/explorer.js:59-63` · **Fix:** batch/randomize scan requests, or push scanning to a local node.
18. **Operator can front-run / censor every swap and tx** (sees all bids pre-chain, controls `/submittx`). No user disclosure. `website/wallet.html:1-60` · **Fix:** state the trust model explicitly on the page.

### Balance correctness → fund mistakes
19. **No reorg rollback in the wallet** — outputs marked Spent stay Spent after a reorg, balance understated; user may re-send. `pkg/wallet/wallet.go:160-193` · **Fix:** implement `ScanBlockUndo` keyed to reorg depth.
20. **Reserved outputs never released on broadcast failure (no `ReleaseReservation` in WASM).** Retry → "insufficient funds"; user panics. `cmd/obscura-wasm/main.go:255-267` · **Fix:** expose a release call and call it on broadcast error.
21. **Multi-tab = double-spend.** Two tabs load the same seed, neither sees the other's reservations → second tx silently fails / double-spends. `website/wallet.html:267-273` · **Fix:** a BroadcastChannel/localStorage lock to enforce one active tab.
22. **Amount `×10⁶` rounding silently sends ZERO** for sub-micro inputs. `website/wallet.html:250-252` · **Fix:** parse the full 12-decimal value as BigInt; reject < 1 atom with a clear message.
23. **Large balances (> ~9000 OBX) display wrong** due to Number/1e12 float precision. `website/wallet.html:252` · **Fix:** format from BigInt, never `Number()`.

---

## 2. 🟠 BROKEN-FLOW (user cannot complete the task)

### Onboarding / mining
24. **No Downloads page or link anywhere** — a new user literally cannot get the miner. `website/*` · **Fix:** add `/download`.
25. **Unsigned binaries blocked by Gatekeeper/SmartScreen** (see S5). · **Fix:** sign + notarize.
26. **PoR requires a full ~2-week (PoRWindow=10,000-block) sync before the first reward**, with no ETA shown — the user thinks it's broken and quits. `pkg/config/params.go`, PoR mining path · **Fix:** show sync %/ETA and explain the retrievability requirement up front.
27. **`--ui` silently auto-relaxes the prototype-PoW start guard** (`OBX_ALLOW_PROTOTYPE_POW=1`), so a one-click miner runs insecure PoW and can fork without warning. `cmd/obscura-node/main.go` · **Fix:** surface a visible "insecure prototype PoW" banner in `--ui` mode.
28. **Zero mining feedback** — no hashrate, no "you found a block," no peer count, no sync %. The user can't tell mining from hung. `cmd/obscura-node/*`, UI · **Fix:** a mining status panel (hashrate / accepted blocks / peers / sync%).
29. **No mining pool / stratum — solo lottery only.** A new miner statistically never wins a block and sees nothing for it. · **Fix:** document the solo-only reality; longer-term add pool support.
30. **No `--network testnet` preset.** New users must hand-assemble flags; easy to misconfigure into an isolated chain. `cmd/obscura-node/main.go` · **Fix:** add a `testnet` preset bundling seeds + sane defaults.
31. **README says Argon2id but the PoW backend is RandomX.** The default is now canonical, KAT-verified `randomx-canonical` RandomX (the insecure `vm-randomx-style` VM is opt-in via `-tags protopow`); the remaining drift is the stale "Argon2id" wording. `README.md` · **Fix:** correct the Argon2id wording and label the default backend clearly.
32. **Coinbase maturity defined but NOT enforced** — mined coins may appear spendable before maturity, then fail. mining/consensus path · **Fix:** enforce maturity in the spend check and reflect it in wallet "available vs locked."

### Swap routing / settlement
33. **"Maker peer unknown" aborts most takes in any network > 2 nodes** (offer gossip lacks peer provenance). `pkg/rpc/swaps.go:404-417` · **Fix:** carry the source-peer hint with gossiped offers, or add a maker-discovery query.
34. **Multi-rung reserve but single-rung settle** — a take spanning several offers settles only the best rung; if it fails the whole take fails and the rest is released. `pkg/rpc/swaps.go:386-396` · **Fix:** implement multi-rung fan-out settlement.
35. **Released-rung reservations rely on an async goroutine that, if it dies, never releases** → book shows phantom-drained liquidity. `pkg/rpc/swaps.go:447-464` · **Fix:** reconcile reservations against live sessions on a timer.
36. **Tiny XNO amounts (< 10¹⁸ raw) floor to 0 offer-units and are silently rejected.** `pkg/swapd/nano.go:74-84` · **Fix:** reject with an explicit "below minimum" message and a stated minimum.

### Wallet sync / state
37. **Sync aborts on the first failed block fetch and doesn't persist partial progress** → next load rescans from 0; transient node blips look like total failure. `website/wallet.html:308-323` · **Fix:** save `last_scanned` per block; resume from it; retry transient fetches.
38. **State export failure is swallowed (`if(st.state)` falsy → nothing saved)** → wallet silently forgets all outputs on next load. `website/wallet.html:319` · **Fix:** detect the error object, surface it, and refuse to claim "synced."
39. **`localStorage` quota-exceeded on large state throws uncaught** → scan progress lost. `website/wallet.html:319` · **Fix:** catch QuotaExceededError and warn/compact.
40. **Whole site silently breaks when the single node is down** — 8s timeouts, errors only after 3+ failures, no health banner. `website/api/explorer.js:10-14`, UI · **Fix:** a node-health indicator + fast-fail with a clear "node offline" message.
41. **Non-operator users CANNOT reveal/withdraw their XNO** — `/xno/recovery` + `/xno/withdraw` are operator-gated and unreachable via the public proxy, yet the UI offers the buttons. `website/wallet.html:686-720` · **Fix:** hide these controls unless a node token is present, and explain why.
42. **Hardcoded vault fee (0.01 OBX) and no fee-rate fetch** → deposits stick in mempool if the network wants more, overpay if less; `/feerate` exists but is never called. `website/wallet.html:98-99,349` · **Fix:** fetch `/feerate` and use it; let the user override.
43. **Vault metadata lives only in `localStorage`, unverifiable against chain** → corruption/clear = the on-chain vault is unclaimable from the UI. `website/wallet.html:355-357` · **Fix:** allow re-deriving vault IDs by scanning, or back vault metadata into wallet state.

### DoS / availability (blocks everyone)
44. **No rate limiting on any public RPC endpoint.** Trivial flood of `/height`, `/offers`, `/submittx` takes the single node — and the whole site — down. `pkg/rpc/*` · **Fix:** per-IP limiter middleware.
45. **`/submittx` memory-amplification via huge malformed txs** (cap is 2×MaxTxBytes; deserialize allocs before reject). `pkg/rpc/server.go:668-704` · **Fix:** stream-validate / bound allocation before full deserialize.
46. **`/offer` parse has no panic guard** — a crafted offer can crash the node (and with it P2P). `pkg/rpc/server.go:632-666` · **Fix:** wrap RPC handlers in `recover()`.
47. **`/candles?limit=5000` forces expensive in-memory aggregation per call**, no eviction on the tape. `pkg/rpc/swaps.go:548-576` · **Fix:** cap limit, precompute/cache buckets.
48. **`/blocktemplate` 1s cache + lock acquisition** lets a polling miner-farm starve P2P and wallet scans. `pkg/rpc/server.go:397-446` · **Fix:** rate-limit template requests; serve a slightly staler cached template.
49. **No `http.Server` connection cap** → socket exhaustion via connection floods. `cmd/obscura-node/main.go:230-238` · **Fix:** set MaxConns / use a limiter listener.
50. **Mempool flooding unbounded** (no per-address fee-gate) → mempool bloat starves real txs. `pkg/mempool/mempool.go:101-130` · **Fix:** per-sender mempool quota + min-fee floor.
51. **Mining and RPC contend on the same chain lock** → heavy RPC stalls block production and vice-versa. `cmd/obscura-node/main.go`, `pkg/chain/chain.go` · **Fix:** read-mostly snapshots for RPC paths.
52. **Poll-storm: each tab fetches every block + polls `/swaps/active` every 3s**, multiplied across tabs/users. `website/wallet.html:308-322,592` · **Fix:** dedupe requests, back off, share state across tabs.
53. **Single point of failure, no failover/health check.** One node crash = full outage. `website/api/explorer.js:10` · **Fix:** multi-node proxy + health checks.
54. **Node data dir + keys on one droplet, no backup automation** — disk loss = chain + seed gone. `cmd/obscura-node/main.go:104-112` · **Fix:** scheduled off-box snapshot of state + seed.

---

## 3. 🟡 CONFUSING-UX (works, but misleads into mistakes)

### Real-vs-mock & swap confirmation
55. **MockNano (the default) is visually identical to real Nano** — a user "swaps" against fake balances, then loses them on restart. `website/wallet.html:203-206`, `pkg/rpc/xno.go:88` · **Fix:** a persistent "SIMULATED XNO" badge on every swap surface when backend=mock.
56. **No warning that "Take" locks REAL XNO** when a real Nano RPC is configured. `website/wallet.html:578-587` · **Fix:** an explicit "⚠ real XNO will be locked, irreversible" confirm.
57. **Take-offer confirmation shows asset NAMES, not AMOUNTS** ("maker gives OBX for XNO") — user can't see how much. `website/wallet.html:577-587` · **Fix:** render give/get amounts + rate in the dialog.
58. **No send-confirmation screen at all** — address/amount/fee never echoed before broadcast; mis-pasted address = silent loss. `website/wallet.html:325-338` · **Fix:** a review-before-broadcast modal.
59. **No slippage / price-protection on takes** — large take fills at a blended worse rate with no cap. `pkg/rpc/swaps.go:255-475`, `website/wallet.html:395-396` · **Fix:** accept a max-slippage param and a quoted worst-case in the dialog.
60. **Fee deducted from proceeds but not shown at take time** — user receives less OBX than the displayed `get_out`. `pkg/rpc/swaps.go:434` · **Fix:** show net-of-fee in the quote.
61. **Self-send not detected** — sending to your own address returns funds to a fresh stealth output; balance looks unchanged, user re-sends. `cmd/obscura-wasm/main.go:147` · **Fix:** warn (not block) on self-address.
62. **`txid undefined` shown on a malformed broadcast response** ("Broadcast ✓ txid undefined"). `website/wallet.html:334-335` · **Fix:** validate the txid hex before claiming success.
63. **Broadcast failure indistinguishable from network error** → user double-sends. `website/wallet.html:325-338` · **Fix:** distinguish rejection vs transport error in the message.
64. **Async post-send sync isn't awaited** — a sync error after a successful send reads as a send failure. `website/wallet.html:336` · **Fix:** await + label "sync" vs "send" outcomes separately.

### Amount/unit confusion
65. **Offer amounts entered as raw "atomic" with misleading placeholders** (`5000000000000`, `50000000`) and no unit label; both OBX and XNO tagged 12-dec in UI though XNO is 10³⁰ on-ledger. `website/wallet.html:166-169,382` · **Fix:** human-unit inputs (e.g., "5 OBX") with live atomic preview.
66. **Offer-unit→raw `×10¹⁸` conversion is fragile** and has regressed before; any new path that skips it reintroduces the bug. `pkg/swapd/nano.go:54-84`, `pkg/rpc/swaps.go:424-427` · **Fix:** a single typed conversion boundary + a unit test guarding it.
67. **Silent partial fill when requested size > offer capacity** — response carries the real fill but a naive user assumes full. `pkg/rpc/swaps.go:356-375` · **Fix:** surface "filled X of Y requested" prominently.

### Empty / loading states
68. **Fresh wallet shows "0 / — OBX" with no hint that a sync is required** → user thinks a received payment failed and asks for a re-send. `website/wallet.html:82-86,299-304` · **Fix:** "Sync to see your balance" empty-state CTA.
69. **Empty states stuck on "loading…/building…"** with no terminal "nothing here yet." UI · **Fix:** explicit empty copy after first load.
70. **No loading/spinner during a long block-by-block sync** — the disabled button reads as frozen; user closes tab and loses progress. `website/wallet.html:308-323` · **Fix:** progress bar + "safe to wait" copy (pairs with #37 persistence).
71. **Malformed offers silently filtered** → order book shows "empty" with no reason. `website/wallet.html:449-479` · **Fix:** show "N offers hidden (unparseable)".
72. **Stale XNO balance (no "last updated") on a real but lagging Nano RPC** → user over-withdraws. `website/wallet.html:201-206` · **Fix:** show fetch timestamp; warn on staleness.
73. **Offer expiry hardcoded to +1h with no input; a wrong client clock makes it expire instantly** with no feedback. `website/wallet.html:515-527` · **Fix:** let the user pick expiry; warn on clock skew.

### Onboarding clarity & trust
74. **No testnet / known-bugs / "no real value" warning on the UI** — looks like a live product. `website/*` · **Fix:** a persistent test-chain banner.
75. **No first-action CTA / onboarding** — a new user lands with no idea whether to mine, swap, or create a wallet. UI · **Fix:** a guided first-run path.
76. **Jargon with no glossary** (stealth address, anon spend, PoR, vault, adaptor). UI · **Fix:** inline tooltips + a glossary page.
77. **3.6MB `wallet.wasm` blocks first paint** with no loading indicator → looks dead on slow links. `website/wallet.html:239` · **Fix:** show a "loading wallet engine…" state; consider streaming/compression.
78. **No view-key export → no watch-only wallet**, so the "non-custodial" claim is single-device only. `cmd/obscura-wasm/main.go:255-267` · **Fix:** expose `ViewKey()` for watch-only setups.
79. **Vault yield never shown before claim** — user doesn't know what they'll get until it's mined. `website/wallet.html:362-368` · **Fix:** compute + display projected principal+yield.
80. **Vault claim doesn't pre-validate principal vs chain** — corrupt local metadata → confusing on-chain rejection. `website/wallet.html:369-379` · **Fix:** verify against chain before building the claim.

### Accessibility / mobile / platform
81. **No focus outlines; modals lack `role="dialog"`/ARIA** — keyboard & screen-reader users blocked. UI · **Fix:** restore focus styles + ARIA roles.
82. **Mobile tables unscrollable / overflow** — order book unusable on phones. UI · **Fix:** responsive/scrollable tables.
83. **Mouse-only copy (`onclick`/`title="click to copy"`)** — mobile users can't copy the address easily. `website/wallet.html:305,680` · **Fix:** explicit Copy buttons.
84. **Clipboard copy fails silently on non-HTTPS/old browsers** → user hand-types address → typo → misdirected funds. `website/wallet.html:305` · **Fix:** fallback + visible "copied/failed" toast.
85. **Browser back/forward kills the SPA wallet tab** — becomes unresponsive, confuses the user. `website/wallet.html:275-280` · **Fix:** guard navigation / restore state on return.
86. **3s swap poll + per-tab fetches feel sluggish** and compound the poll-storm. UI · **Fix:** adaptive polling / websocket push.

### Operational / disclosure
87. **Operator seed-derivative details can leak into error logs** on `--nano-fund-secret` validation failure. `cmd/obscura-node/main.go:170-177` · **Fix:** redact derivation info from logs.
88. **`/peers` exposes peer COUNT publicly** — minor topology leak. `pkg/rpc/server.go:508-521` · **Fix:** gate the count behind auth.
89. **No user-facing fee guidance** — the 0.001 default is a guess; congestion → stuck tx with no explanation. `website/wallet.html:98-99` · **Fix:** fetch + recommend a fee, explain "pending due to low fee."
90. **XNO withdrawal address only checked for `nano_` prefix, not checksum** → a typo'd address fails late with no txid/proof. `website/wallet.html:709-720` · **Fix:** full Nano checksum validation client-side.
91. **No "are you sure / irreversible" framing anywhere in the swap or send flows** generally. UI · **Fix:** consistent destructive-action confirmations.
92. **Difficulty oscillation** can make a new solo miner's already-tiny odds swing further. consensus/difficulty path · **Fix:** smoother retarget; document expected variance.
93. **No indication which network/chain the app is on** (mainnet-looking UI on a test fork). UI · **Fix:** show chain-id/genesis-hash + "test" label.
94. **App window close = `os.Exit`, no "node still syncing / mining will stop" prompt.** `cmd/obscura-node/ui.go` · **Fix:** confirm-on-close when mining/syncing.
95. **No download integrity (SHA256SUMS) for the binaries** — a user can't verify what they grabbed (compounds S5). `dist/*` · **Fix:** publish signed checksums on the Downloads page.

### Edge / correctness niggles
96. **Concurrent takes can re-lock partially-released liquidity** (stale reserved metadata) → transient over-reservation. `pkg/rpc/swaps.go:386-396` · **Fix:** atomic reserve/release with versioned book state.
97. **`/blocktemplate` can diverge from miner view** mid-mempool-change → occasional wasted external-miner work. `pkg/rpc/server.go:397-446` · **Fix:** include a mempool snapshot id; reject stale submits gracefully.
98. **Swap design is trustless only if both parties act**; a refusing maker forces the taker onto the timelock-refund path the UI never explains. `pkg/swapsession/*` · **Fix:** document + surface the refund timeline to the taker.
99. **No per-user account/session tracking on swaps** — reservations can be griefed anonymously (pairs with #44). `pkg/rpc/swaps.go:266-275` · **Fix:** lightweight session token per taker.
100. **No "minimum viable funds" preflight before a send/swap** — user starts a flow they can't finish (e.g., balance < amount+fee) and learns only at broadcast. `website/wallet.html:325-338` · **Fix:** disable the action + explain until funds suffice.
101. **No global "this is alpha / report bugs here" channel** — a stuck new user has nowhere to go. UI/README · **Fix:** a support/issues link.

---

## What a brand-new user *actually* experiences today

1. Lands on the site → no test-chain warning, no "start here," 3.6MB wasm load with no spinner (#74, #75, #77).
2. Wants to mine → **no download link exists** (S1, #24); if they find a binary it's **blocked unsigned** (S5, #25) and would mine a **dead isolated fork** anyway (S2).
3. Creates a wallet → seed saved in plaintext, no backup gate; one cache-clear later it's **gone forever** (#1, #2).
4. Tries a swap → MockNano looks real (#55); if it's a real node, **clicking "Take" drains the operator's XNO and the user receives nothing** (S3, #7–#9).
5. Throughout → the **operator sees their IP and every move** (S4, #14–#18), and **one node down = the whole site is dead** (#40, #53).

**Bottom line:** today this is a *demo of a swap/privacy engine*, not a self-serve product. The honest near-term framing for new users is "run your own local node"; the public website should disclose the trust model and drop the "easily mine / swap" promise until S1–S5 are closed.
