Confirmed. The `Maker` field is the contact slot (comment at line 33 says "contact/pubkey" but the audits confirm no usable endpoint/dial path is wired), amounts are uint64 everywhere, no incentives file exists for swap rewards, and `s.nano` is wired but unused. Writing the final plan.

# Obscura Cross-Chain Swaps — Final "Live, Working, Friendly, Automated" Plan

## 1. CURRENT REALITY

The cryptographic core of Obscura's XNO↔OBX swap is real and the hard part is done — but **no two real people on two machines can complete a swap today.** What genuinely works end-to-end: the **OBX leg is real, consensus-enforced** (`wallet.FundSwap`/`BuildSwapSpend` → validated in `pkg/chain/validate.go:441-471`, including the timelock refund path), the **order book is real and decentralized** (signed, PoW-stamped, P2P-gossiped offers via `pkg/swapbook` + `pkg/p2p`, postable/listable from CLI, web, and WASM), and the **from-scratch Nano signer/sweeper is testnet-grade real code** (`pkg/swapd/nanorpc.go`). What is local/mock/manual: the **only executor (`obscura-swap live`/`selftest`) runs ONE process playing both maker and taker**, generating all four secrets (`sA,sB,a,b`) itself, settling the OBX leg on a **throwaway in-memory devnet** (`os.MkdirTemp`), and the only "live" interaction is a **human manually pasting XNO into a printed joint address**. The order book and the executor are **two disconnected universes** — nothing takes an offer and runs a swap (`grep` for take/accept/execute returns nothing; `s.nano` is wired into the RPC server but only read by `NanoEnabled()`). **BTC and Monero are mock-only** (no `bitcoind`/Electrum/monero-wallet-rpc client exists), yet are offered as assets in the UI. There is **no swap session protocol, no counterparty handshake, no auto-refund watcher, no persisted swap state, and no recovery command** — on any failure the tool prints "recover it with sA+sB — keep these logs" and the secrets die with the process. The **XNO=2× BTC liquidity reward does not exist in code anywhere.** Amounts are **uint64 everywhere** (`nano.go:26`, `swapbook.go:39-40`), which overflows for any realistic XNO amount (1 XNO = 1e30 raw). Bottom line: a **verified single-process orchestration demo + a live bulletin board**, not a product.

---

## 2. BLOCKERS (must-fix, ordered)

> Ordered so each unblocks the next. Effort: S = days, M = 1–2 weeks, L = multi-week.

| # | Blocker | Concrete build | Effort |
|---|---------|----------------|--------|
| **B1** | **Two-party swap session protocol** — one process holds all 4 secrets; two real parties can't run it. | New `pkg/swapsession`: state machine `Proposed→Accepted→PreSigned→Locked→Claimed→Swept→Done`/`Refunded`. New P2P message types `msgSwapInit/Accept/PreSig/Claimed` in `pkg/p2p/p2p.go`. Refactor `doAtomicSwap` (`cmd/obscura-swap/main.go:116`) into a **`MakerRun`/`TakerRun` pair** where each party holds only its own scalars (`sA,a` vs `sB,b`) and exchanges public points + proofs-of-possession + the adaptor pre-signature over the wire. **Move it out of `cmd/` into `pkg/swap`** so node and wallet can call it. | **L** |
| **B2** | **Offers carry no dialable maker contact** — `Offer.Maker` is a pubkey, not a reachable endpoint; a taker who finds an offer can't talk to the maker. | Add a signed `Contact`/`Endpoint` field to `swapbook.Offer` (include it in `Core()`/serialization so it's signed). Implement a **P2P rendezvous path** so takers reach makers without a public IP (route session messages through the existing gossip mesh by maker pubkey). | **M** |
| **B3** | **Executor must run per-party legs on the REAL chain, not a devnet, and not require a manual XNO paste.** | In `pkg/swapd/executor.go`: `ExecuteSwap(offer, side, dest)` drives the session against the **running node's `chain.Chain` + the user's real wallet** (reuse `rpc.Client`), submits real claim/fund/refund txs to the live mempool, and **funds/detects the XNO lock automatically** (user-signed send built+signed+`process`'d locally like `Sweep`, not a printed address). Wire `s.nano` into a new `POST /swap` handler so a take request actually executes. | **L** |
| **B4** | **Confirmation gating** — `waitForReceivable` settles on first *unconfirmed* receivable and picks the *largest* pending block. Theft/wrong-amount vector. | In the executor wait loop: after a receivable, poll `nano.Confirmed(blockHash)` until cemented; **match on exact agreed amount + expected sender + specific block hash** (never "largest"). `Confirmed()` already exists; this is a one-loop change but mandatory before real funds. | **S** |
| **B5** | **Automated refund / recovery** — no watcher drives the OBX timelock refund or the XNO sweep-back; secrets die on `log.Fatal`. Permanent loss is reachable by an RPC blip. | **Persisted encrypted journal** (`pkg/swapd/journal.go`, AES-GCM under a wallet-derived key) written *before* any funds move, recording `{swapID,sA,sB,jointAddr,lockID,xnoDest,unlockHeight,stage}`. Background **watcher goroutine**: auto-builds `BuildSwapSpend(isRefund=true)` once `height≥UnlockHeight`, and auto-sweeps XNO on abort. Replace every post-lock `log.Fatal` with a transition to `recoverable` + auto-retry. Add `obscura-swap recover --swap-id` + auto-resume on startup. | **M** |
| **B6** | **128-bit XNO amounts** — uint64 caps at ~1.8e-11 XNO; no real amount is representable. | Widen to `*big.Int`/decimal raw string end-to-end: `NanoClient.Lock/Balance` (`pkg/swapd/nano.go`, `nanorpc.go`), `swapbook.Offer.GiveAmount/GetAmount` (+ serialization in `Core()`), the offer CLI, and the amount-match in B4. Type-surgery but unavoidable. | **M** |
| **B7** | **Hide BTC/XMR until live** — offered in UI, mock-only backend. | **Today:** hide BTC/XMR from `website/wallet.html` and default-`XMR` in `obscura-wallet offer`; label "coming soon." Restrict the book/offer validation to assets with a live backend (XNO/OBX). **Later:** real `BitcoinClient` (P2WSH HTLC via Electrum) — independent track. | **S** (hide) / **L** (build) |
| **B8** | **Idempotent/resumable Sweep + RPC failover** — 2-block sweep is non-atomic; a crash between receive and send strands funds; single hardcoded preset with no failover. | Make `Sweep` read live `account_info` frontier on every entry, skip already-applied receive, treat balance-0 as done, and classify Nano `Old`/`Fork`/`Gap previous` as "already done." Add multi-endpoint client: on 429/5xx/transport error fail over across process-capable presets with backoff; check `StatusCode`, wrap body in `io.LimitReader`. | **M** |

**Critical path:** B7(hide) → B6 → B1 → B2 → B3 → B4 + B5 + B8 → then the one-click UI (§4). B4/B6/B8 are the cheapest fund-safety wins and can land first, in parallel with the B1 build.

---

## 3. AUTOMATION PLAN

**Goal A — hands-off one-click take.** After B1–B5, a taker's single action triggers: `ExecuteSwap` calls `Book.Best(give,get,amount)` → opens a session to the maker (B2 rendezvous) → runs the adaptor handshake (B1) → funds its leg on the real chain (B3) → the watcher (B5) confirms (B4), claims, extracts the secret, sweeps, and journals every stage. **No address pasting, no log reading, no scalars.** On any stall the watcher auto-refunds (OBX timelock) or auto-sweeps (XNO) and surfaces one plain-language message. Surfaces: `POST /swap` RPC, `GET /swap/{id}` status, `obscura-wallet take --offer <id> --to <addr>`, and the web "Take" button.

**Goal B — one-command "run a maker."** Build `obscura-swap maker` (long-running daemon, the headline liquidity experience):
1. **Setup wizard** (`obscura-swap setup`): pick a Nano RPC **preset** (the friendly workaround for the unavoidable RPC requirement — see below), paste a Nano seed, auto-detect `work_generate` capability via `Version`, write a config file. Maker never touches flags again.
2. **Preflight/inventory binding** (Major 4): verify OBX balance and Nano funding balance; **only post offers up to fundable inventory**, auto-shrinking as inventory is consumed.
3. **Auto-post + auto-refresh** (Major 5): re-post offers before the 1h expiry (configurable `--offer-ttl` up to the 6h cap, replacing the hardcoded 1h at `cmd/obscura-wallet/main.go:826`); **persist the maker's own offers to disk** so a restart re-posts them.
4. **Auto-settle**: watch the book for takers + the joint address for incoming locks; run `MakerRun` automatically against the maker's **real** funded wallets; idempotent retried claim/sweep via the watcher (B5/B8).
5. **One-time Nano live-gate self-validation** before any real onboarding: `obscura-swap validate-nano` signs+publishes a dust round-trip against the `rainstorm` preset; the daemon refuses real swaps until green.

**Reward automation (XNO=2× BTC) — must be built (B-reward):** create `pkg/chain/incentives.go` (does not exist). Pay makers from `incentivePool` on **settlement-proven** swaps (the on-chain OBX claim is the proof), with `rewardWeight(XNO)=2×rewardWeight(BTC)` per equivalent notional. **Settlement-gated + rate-limited** — XNO is feeless, so red-team wash-trading before enabling. Auto-credit in the maker daemon (no separate claim step); surface realized spread + reward in `maker --status`.

**Where it can't be fully non-technical:** the XNO leg needs a Nano RPC endpoint. **Workaround = 3 presets** (rainstorm/somenano/nano.to) selected in the wizard, with **health-check + failover** (B8) so a rate-limited preset doesn't strand a swap. For the *taker*, RPC config lives **entirely behind the node operator** — the swapping user's flow never mentions presets or work URLs (their wallet talks only to its OBX node, which holds the operator's Nano backend). Only the *maker* daemon operator ever sees the preset choice, and the wizard reduces it to one dropdown.

---

## 4. UX SPEC (friendly end-user flow)

**Primary surface: web wallet** (`website/wallet.html` + WASM — already non-technical: browser keys, tabs, no terminal). **Secondary: `obscura-wallet` CLI** for power users. The standalone `obscura-swap` is repositioned as the **maker daemon + dev/CI harness**, not the end-user product.

**Taker flow (each step maps to a blocker):**
1. **Pick** — "I have ___, I want ___" + a **human amount** ("0.5 XNO", not raw). Asset dropdown shows only live-backed pairs (B7). *(fixes amounts/B6, B7)*
2. **Quote** — calls `Book.Best`; shows **effective rate, total received, maker, and an expiry countdown** in plain units ("You send 3 OBX → you receive 0.5 XNO"). Re-validates freshness at confirm. *(B4 amount-match, M4)*
3. **Confirm** — single restated summary, **no jargon** ("shared escrow address" not "joint account"; "secured" not "adaptor"; "anti-spam check" not "PoW-stamped"). One "Confirm swap" button.
4. **Execute (automated)** — wallet drives both legs: handshake with maker, fund OBX on the user's real node, fund/detect XNO **from the connected wallet automatically** (or QR + copy button if the user insists on an external Nano wallet). No address-pasting, no log-watching. *(B1, B2, B3)*
5. **Watch** — a live **status card** with plain stages: **Matched → Locking your funds → Confirming → Settling → Done.** Persisted (B5) so closing the tab or a crash resumes. A **"My swaps" panel** lists in-flight + completed swaps with stage indicators. *(status `GET /swap/{id}`)*
6. **Done / Recover** — clear success ("Done — 0.5 XNO received") with a balance link. On any stall, **one button "Reclaim my funds"** drives the OBX timelock refund / XNO sweep from saved state. **Never expose raw scalars.**

**Progress/error/recovery handling:**
- **Progress:** machine-readable state from the journal (B5) → status card; never `log.Printf` lines.
- **Errors:** plain, actionable ("Your swap couldn't reach the other trader — your funds are safe and will be returned automatically"). RPC hiccups ride through via failover/retry (B8), never fatal.
- **Recovery:** automatic by default (watcher); the only manual fallback is a **single copy-paste command** (`obscura-wallet recover --swap-id <id>`), never "reconstruct sA+sB."
- **Maker dashboard:** `maker --status` panel shows live offers, inventory on both legs, completed swaps, realized spread + reward.

**Can't be fully non-technical:** running a **maker** inherently means committing capital + an RPC backend. The wizard (§3) + presets reduce it to "paste a seed, pick a dropdown, leave it running."

---

## 5. FUND-SAFETY (automated guarantees the user must have)

Every enumerated failure must map to **swap completes, XNO auto-returned, or OBX auto-refunded** — with one clear message and at most one copy-paste command.

1. **Journal-before-funds (B5):** secrets + stage persisted (encrypted) *before* the user is ever asked to commit value. This alone converts "permanent loss" into "always recoverable." Reorder so the send instruction appears only **after** journaling + RPC health check + exact-amount fix.
2. **Auto-refund on OBX timelock (B5/M1):** watcher polls height, auto-broadcasts `BuildSwapSpend(isRefund=true)` at `UnlockHeight`, survives restarts. Message: "Swap timed out — your OBX was automatically refunded at block N."
3. **Auto-sweep-back of XNO on abort (B5):** if the OBX leg never completes, the watcher sweeps the joint account back to the user's dest with the journaled secret.
4. **Confirmation + amount gate (B4):** OBX is never released against an unconfirmed, wrong-amount, or wrong-sender XNO send.
5. **Idempotent/resumable sweep (B8):** a crash mid-sweep re-reads the live frontier and resumes; balance-0 = done; Nano `Old`/`Fork` handled as "already done."
6. **RPC failover + retry (B8):** a rate-limited/dead preset fails over instead of stranding funds.
7. **Unilateral refund for the XNO sender (Gap 11):** enforce ordering so XNO is locked **only after** the OBX claim path is set up, and give the XNO sender a pre-signed unilateral sweep path so a stall can't strand them asymmetrically.
8. **Maker inventory binding (Major 4):** offers only posted up to fundable inventory, so takers don't start swaps that can't settle.

---

## 6. SEQUENCED ROADMAP

**Phase 0 — Stop the bleeding (days):**
- [ ] **B7:** Hide BTC/XMR from web + CLI; restrict book validation to XNO/OBX. Re-label `obscura-swap live` as a demo; make it refuse to solicit real XNO until B3.
- [ ] **B4:** Add `Confirmed()` + exact amount/sender/hash match in the wait loop.

**Phase 1 — Fund-safety primitives (1–2 weeks, parallelizable):**
- [ ] **B6:** Widen amounts to `*big.Int`/decimal-string end-to-end (Nano interface + Offer + CLI).
- [ ] **B8:** Idempotent/resumable `Sweep` + multi-preset failover with backoff + status-code/body-limit handling in `call()`.
- [ ] **B5 (journal):** Encrypted journal + `recover` command + auto-resume; remove all post-lock `log.Fatal`.
- [ ] One-time `validate-nano` to close the live gate against rainstorm with dust.

**Phase 2 — The real swap (multi-week, the spine):**
- [ ] **B1:** `pkg/swapsession` + P2P `msgSwap*` + `MakerRun`/`TakerRun` split (PoP verification in the handshake to close the rogue-key gap).
- [ ] **B2:** Signed `Contact`/`Endpoint` on `Offer` + P2P rendezvous.
- [ ] **B3:** `pkg/swapd/executor.go` on the real chain + user's real wallet; wire `s.nano` into `POST /swap`; self-funded user-signed XNO send.
- [ ] **B5 (watcher):** Background auto-refund/auto-sweep goroutine reading the journal.

**Phase 3 — One-click + liquidity (1–2 weeks):**
- [ ] Web wallet flow steps 1–6 (§4) + "My swaps" panel + `GET /swap/{id}`.
- [ ] CLI `obscura-wallet take --offer`.
- [ ] **Maker daemon:** `obscura-swap setup` wizard, preflight/inventory binding, auto-post/refresh (`--offer-ttl`, disk-persisted offers), auto-settle, `maker --status`.

**Phase 4 — Rewards + extra legs (after live & stable):**
- [ ] **B-reward:** Build `pkg/chain/incentives.go` — settlement-gated, rate-limited `incentivePool` payout with **`rewardWeight(XNO)=2×rewardWeight(BTC)`**; auto-credit in the maker daemon; red-team wash-trading (feeless XNO) before enabling on mainnet.
- [ ] **B7 (build):** real `BitcoinClient` (Electrum P2WSH HTLC) → re-enable BTC in UI, which is when the 2× XNO reward weighting becomes meaningful.
- [ ] Polish: `Book.Best` big.Int cross-multiply + deterministic tie-break; plain-language strings; human-readable IDs.

**Definition of "live and working":** Phases 0–3 complete — two non-technical users complete an XNO↔OBX swap from the web wallet with one click each, every failure mode auto-recovers funds, and one command (`obscura-swap maker`) runs a self-funding, auto-refreshing liquidity provider. The XNO=2× reward (Phase 4) is what then pulls XNO liquidity ahead of BTC once BTC ships.

Key files: `cmd/obscura-swap/main.go` (executor to split + relabel), `pkg/swapd/nanorpc.go` (amounts, failover, idempotent sweep, confirm gate), `pkg/swapd/nano.go` (uint64 interface), `pkg/swapbook/swapbook.go` (Offer contact field + amounts + float64 matcher), `pkg/p2p/p2p.go:443-461` (add session messages), `pkg/rpc/server.go:46,66,69` (`s.nano` wired but unused — needs `POST /swap`), `pkg/wallet/wallet.go:1026/1127` + `pkg/chain/validate.go:441-471` (OBX leg — solid), `pkg/chain/incentives.go` (does not exist — build for rewards), `website/wallet.html` (hide BTC/XMR now; one-click flow later).