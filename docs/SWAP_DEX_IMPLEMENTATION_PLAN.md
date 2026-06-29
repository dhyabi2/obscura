# Obscura XNO↔OBX Cross-Chain DEX — Master Implementation Plan

**STATUS: PENDING IMPLEMENTATION** — 2026-06-26

> Local planning document for Obscura **mainnet**. Not for publication.

## Executive summary

Today the swap works **as a single-process demo** and scores ~**32.5/100** as a real DEX (8 factors, avg from `docs/DEX_TO_100_PLAN.md`). It can post PoW-stamped offers into a gossiped order book, render them, auto-post OBX→XNO liquidity from mining rewards, and complete the adaptor/HTLC cryptography — but it cannot be *taken* between two distrusting nodes, settle against real BTC/XMR, recover from a stall, or quote accurate prices. Four **structural roots** explain almost every one of the 105 audited issues in `docs/SWAP_ISSUES_105.md`: (1) **the executor plays both sides** — `newSecrets()` mints sA+sB+a+b in one process and `doAtomicSwap` is funder *and* claimer (`cmd/obscura-swap/main.go:133,150,244,313`); (2) **quotes/amounts are not bound to the book** — the live leg locks a hardcoded `obxAmt := 3*config.AtomicPerCoin` (`main.go:244,374`) ignoring the matched `Offer`, and `--xno-amount-raw` is display-only; (3) **the refund path is unwired** — `wallet.BuildSwapSpend(isRefund=true)` is never built/broadcast/watched (only the `false` caller exists at `main.go:180`), and `unlock := c.Height()+200` (`main.go:156`) is a magic constant with no cross-leg ordering; (4) **the secret is revealed before XNO is cemented** — `waitForReceivable` returns on first sighting and never calls `nano.Confirmed` (`main.go:583-586`). This plan sequences the fixes for all 105 issues into 10 work-streams and 7 phases, building **on** the already-shipped order-book viz, explorer hashrate/price cards, and default-on auto-liquidity loop (`pkg/swapbook/autoliquidity.go`, `cmd/obscura-node/main.go:306`, `/auto-liquidity`).

---

## 1. Phase list, effort, and DEX factors advanced

| Phase | Goal | Closes | DEX factors advanced | Effort |
|---|---|---|---|---|
| **P0 — Money-safety criticals** | No value can be lost on the happy path; correctness foundations land first | The 28 criticals + amount/scale/cementation correctness | F1, F2, F8 | **L** |
| **P1 — Real two-party P2P backbone** | A genuine maker/taker session over P2P; neither party holds both halves; persisted state | take/accept protocol, split secrets, swap-state store | F2, F5, F6, F8 | **XL** |
| **P2 — Settlement safety: timelocks, funding order, refund & recovery** | Cross-leg timelock ordering enforced; refund executable + watched; stranded-XNO recovery | refund/abort/recovery + reservation state | F2, F4, F5, F8 | **XL** |
| **P3 — Real chains & pluggable backends** | Production BitcoinClient; ChainAdapter + registry; XMR resolved | BTC leg, asset registry, multi-chain | F2, F3, F4, F8 | **XL** |
| **P4 — Market quality, matching & manipulation resistance** | Exact pricing, Quote/depth/partial-fill, hardened book, quorum RPC | order-book matching + price-discovery + book DoS | F1, F5, F6, F8 | **XL** |
| **P5 — Observability, UX & onboarding** | Browser take/execute, swap tracker, metrics, proof links, encrypted wallet | UI/UX + observability | F6, F7, F3 | **L** |
| **P6 — Live + adversarial validation** | Two-machine + regtest proof; honest party never loses funds under attack | external-audit-gated acceptance | F2, F4, F5, F7, F8 | **L** |

Effort legend: **S** small · **M** medium · **L** large · **XL** extra-large (aggregate per phase).

---

## 2. Traceability matrix (all 105 issues)

Every issue from `docs/SWAP_ISSUES_105.md` is mapped to a work-stream (WS1–WS10, §4) and the phase that closes it. Severity from the audit.

| # | Workstream | Phase | Sev |
|---|---|---|---|
| 1 | WS1 | P0 | critical |
| 2 | WS1 | P0/P1 | critical |
| 3 | WS3 | P0 | critical |
| 4 | WS3 | P0/P1 | critical |
| 5 | WS1 | P0 | critical |
| 6 | WS8 | P0 | critical |
| 7 | WS4 | P2 | critical |
| 8 | WS4 | P2 | critical |
| 9 | WS1 | P2 | critical |
| 10 | WS1 | P2 | high |
| 11 | WS1 | P2 | high |
| 12 | WS1 | P2 | medium |
| 13 | WS1 | P1 | high |
| 14 | WS10 | P4 | medium |
| 15 | WS1 | P4 | low |
| 16 | WS2 | P1 | critical |
| 17 | WS2 | P1 | critical |
| 18 | WS2 | P1 | critical |
| 19 | WS2 | P1 | high |
| 20 | WS2 | P1 | medium |
| 21 | WS2 | P2 | high |
| 22 | WS2/WS4 | P2 | high |
| 23 | WS6 | P4 | medium |
| 24 | WS6 | P4 | low |
| 25 | WS4 | P0/P1 | critical |
| 26 | WS4 | P1 | high |
| 27 | WS4 | P0 | high |
| 28 | WS4 | P0 | critical |
| 29 | WS4 | P2 | high |
| 30 | WS4 | P2 | high |
| 31 | WS4 | P2 | medium |
| 32 | WS7/WS4 | P3 | high |
| 33 | WS7/WS4 | P3 | medium |
| 34 | WS4 | P0 | high |
| 35 | WS4 | P0 | high |
| 36 | WS4 | P0 | high |
| 37 | WS4 | P0 | high |
| 38 | WS4 | P2 | medium |
| 39 | WS4 | P2 | high |
| 40 | WS4 | P2 | medium |
| 41 | WS4 | P2 | medium |
| 42 | WS3 | P0 | high |
| 43 | WS3 | P0 | high |
| 44 | WS4 | P2 | low |
| 45 | WS4 | P2 | medium |
| 46 | WS4 | P2 | medium |
| 47 | WS10 | P0 | critical |
| 48 | WS10 | P0 | high |
| 49 | WS10 | P4 | high |
| 50 | WS10 | P4 | high |
| 51 | WS3 | P0 | medium |
| 52 | WS8 | P2 | medium |
| 53 | WS8 | P2 | medium |
| 54 | WS7 | P3 | high |
| 55 | WS10 | P3 | high |
| 56 | WS4/WS7 | P3 | medium |
| 57 | WS7 | P3 | medium |
| 58 | WS6 | P0 | critical |
| 59 | WS6 | P4 | high |
| 60 | WS6 | P4 | medium |
| 61 | WS6 | P4 | high |
| 62 | WS6 | P4 | medium |
| 63 | WS6 | P4 | medium |
| 64 | WS6 | P4 | medium |
| 65 | WS6 | P4 | medium |
| 66 | WS5/WS6 | P4 | medium |
| 67 | WS5/WS6 | P4 | high |
| 68 | WS5 | P4 | medium |
| 69 | WS6 | P4 | low |
| 70 | WS6 | P4 | low |
| 71 | WS6 | P4 | medium |
| 72 | WS5/WS6 | P4 | medium |
| 73 | WS8 | P0 | critical |
| 74 | WS8 | P5 | high |
| 75 | WS8 | P5 | high |
| 76 | WS8 | P5 | high |
| 77 | WS8 | P5 | medium |
| 78 | WS8 | P5 | high |
| 79 | WS8 | P5 | medium |
| 80 | WS8 | P5 | high |
| 81 | WS8 | P5 | high |
| 82 | WS8 | P5 | high |
| 83 | WS8 | P5 | medium |
| 84 | WS8 | P5 | medium |
| 85 | WS8 | P1/P5 | critical |
| 86 | WS8 | P5 | high |
| 87 | WS8 | P5 | medium |
| 88 | WS8 | P5 | medium |
| 89 | WS8 | P5 | medium |
| 90 | WS8 | P5 | medium |
| 91 | WS6/WS8 | P4 | low |
| 92 | WS8 | P5 | medium |
| 93 | WS8 | P5 | medium |
| 94 | WS9 | P5 | high |
| 95 | WS9 | P5 | high |
| 96 | WS9 | P5 | high |
| 97 | WS9 | P5 | medium |
| 98 | WS9 | P5 | medium |
| 99 | WS9 | P5 | medium |
| 100 | WS9 | P5 | medium |
| 101 | WS9 | P5 | low |
| 102 | WS9 | P5 | low |
| 103 | WS9 | P5 | low |
| 104 | WS9 | P5 | low |
| 105 | WS9 | P5 | low |

**Mapped: 105 / 105** (28 critical · 44 high · 30 medium · 3 low).

### Per-workstream issue index (for cross-checking completeness)
- **WS1 Atomic-swap safety & secret/timelock:** 1, 2, 5, 9, 10, 11, 12, 13, 15 (9)
- **WS2 Two-party P2P protocol:** 16, 17, 18, 19, 20, 21, 22 (7)
- **WS3 Quote/amount/price binding:** 3, 4, 42, 43, 51 (5)
- **WS4 Refund & failure recovery / sweep correctness:** 7, 8, 22, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 44, 45, 46, 56 (24)
- **WS5 Liquidity & market-making:** 66, 67, 68, 72 (4)
- **WS6 Order book & matching engine:** 23, 24, 58, 59, 60, 61, 62, 63, 64, 65, 66, 67, 69, 70, 71, 72, 91 (17)
- **WS7 Asset/chain coverage & pluggable backends:** 32, 33, 54, 56, 57 (5)
- **WS8 UX/UI & onboarding:** 6, 52, 53, 73, 74, 75, 76, 77, 78, 79, 80, 81, 82, 83, 84, 85, 86, 87, 88, 89, 90, 91, 92, 93 (24)
- **WS9 Observability & transparency:** 94, 95, 96, 97, 98, 99, 100, 101, 102, 103, 104, 105 (12)
- **WS10 Security & MEV/front-running:** 14, 47, 48, 49, 50, 55 (6)

(Issues 22, 32, 33, 56, 66, 67, 72, 91 are listed under two streams because the fix spans both; each is counted once in the §2 matrix toward the 105 total.)

---

## 3. Dependency-sequenced phases

### Phase 0 — Money-safety criticals (no protocol change, immediate score lift) — **Effort L**
**Goal:** make the *existing* happy path incapable of losing value, and land the cheap correctness fixes everything else depends on.

Closes (issues): **1, 2 (partial), 3, 4 (partial), 5, 6, 25 (persist), 27, 28, 34, 35, 36, 37, 42, 43, 47, 48, 51, 58, 73**.
Items:
- Asset-decimals single source of truth; **OBX=12** not 8 (WS8/#73; fixes the silent 1e4 rescale in `offerOBXAtomic`, `cmd/obscura-node/main.go:414`).
- Fix `BTC (mock)` option `value` so offers aren't silently rejected (WS6/#58).
- Confirmation-depth gate: `waitForReceivable` must poll `nano.Confirmed` before returning; gate `doAtomicSwap` entry (WS1/#1, WS10/#47,#48).
- Validate received XNO amount and bind OBX to the matched offer (WS3/#3,#4,#51) — at minimum reject mismatches; full binding completes in P1/P4.
- Widen XNO amount to `*big.Int` / decimal-raw-string across `NanoClient` and callers (WS3/#42); pick the receivable by expected amount (WS3/#43).
- Make `Sweep` idempotent/resumable and verify completion/cementation (WS4/#28,#34,#35,#36,#37).
- Persist per-swap secrets/state to a `0600` file before advertising the joint address (WS4/#25 partial,#27).
- Replace the false "funds reclaimable" panel with an honest experimental warning (WS8/#6).

**DEX factors advanced:** F1 (price accuracy via decimals + amounts), F2 (cementation gating, 128-bit), F8 (cementation hole, single-RPC trust first cut).

### Phase 1 — Real two-party P2P backbone (the universal unblocker) — **Effort XL**
**Goal:** a genuine maker/taker swap session over P2P where neither party ever co-locates both halves; persisted, resumable.

Closes: **2 (full), 13, 16, 17, 18, 19, 20, 25 (full), 26, 85 (relabel→drive)**.
Items: `pkg/swapsession` split-secret state machine; `HalfPresig`/`CombineHalves`/`VerifyHalfPresig`; P2P `msgSwapTake/Accept/Nonce/Funded/Abort`; deterministic adaptor nonces (#13); session authentication scaffolding (#20); rewrite selftest to two nodes (#19); idempotent resume (#26).

**DEX factors advanced:** F2, F5, F6, F8.

### Phase 2 — Settlement safety: timelocks, funding order, refund & recovery — **Effort XL**
**Goal:** cross-leg timelock ordering enforced; refund executable and watched; stranded XNO recoverable; sweep robust.

Closes: **7, 8, 9, 10, 11, 12, 21, 22, 29, 30, 31, 38, 39, 40, 41, 44, 45, 46, 52, 53**.
Items: timelock-ordering validator (#9,#10,#11); funding-order protocol with on-chain leg verification (#21); OBX refund builder + height watcher (#7,#8); claim-race / secret-extraction watcher (#22); stranded-XNO recovery + CLI refund/recover/resume; sweep correctness battery (#29,#38,#39,#40,#41,#44,#45,#46); MockNano failure-injection tests (#30); cleanup/finalize-before-fatal (#31); mining-serialization + funding-loop diagnostics (#52,#53).

**DEX factors advanced:** F2, F4, F5, F8.

### Phase 3 — Real chains & pluggable backends — **Effort XL**
**Goal:** a production BitcoinClient, a ChainAdapter + asset registry, XMR resolved; RPC failover.

Closes: **32, 33, 54, 55, 56, 57**.
Items: `pkg/swapd/bitcoinrpc.go` (P2WSH FundHTLC, confirmation-gated `Confirmed`, hashlock `Redeem` revealing `t`, CLTV `Refund`); `bitcoinpresets.go`; ChainAdapter + settlement-backend registry; resolve XMR (ship `monerorpc.go` or remove); RPC failover + multi-work endpoints (#32,#33); BTC mempool/front-running notes wired into orchestration (#55); unify BTC/XNO refund semantics (#56); harden `MockBitcoin.FundHTLC` (#57).

**DEX factors advanced:** F2, F3, F4, F8.

### Phase 4 — Market quality, matching & manipulation resistance — **Effort XL**
**Goal:** exact pricing/ordering, real matching (Quote/depth/partial-fill/cancel), hardened book, quorum RPC, nonce-reuse consensus guard.

Closes: **14, 15, 23, 24, 49, 50, 59, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69, 70, 71, 72, 91**.
Items: exact integer rate comparison killing float64 (#59,#60,#61,#62,#63); `Book.Quote` + partial/min fill + reservation/cancel (WS6 §4); offer↔SwapOut/funds binding + bonds (#23,#24); netID + settlement-address binding + expiry↔timelock coupling (#64,#65); book hardening (PoW/per-maker caps/min size/rate limit) (#66,#67,#69,#70,#71); persistence + rebroadcast (#72); liquidity sybil/wash defenses + reward kill-switch (#68); canonical pair key (#91); consensus ClaimR nullifier (#14); fold economic terms into signed message (#15); QuorumNano + TLS/MITM hardening (#49,#50).

**DEX factors advanced:** F1, F5, F6, F8.

### Phase 5 — Observability, UX & onboarding — **Effort L**
**Goal:** browser take/execute swap, swap tracker + metrics + proof links, encrypted wallet, honest explorer.

Closes: **74, 75, 76, 77, 78, 79, 80, 81, 82, 83, 84, 86, 87, 88, 89, 90, 92, 93, 94, 95, 96, 97, 98, 99, 100, 101, 102, 103, 104, 105** (+ completes 85).
Items: WS8 wallet UI battery (human decimals, confirm modal, take/execute stepper, in-flight tracking, encryption, XSS/escape, depth/Σ/rate fixes on BigInt); WS9 explorer + tracker + metrics + slog + proof links.

**DEX factors advanced:** F6, F7, F3.

### Phase 6 — Live + adversarial validation — **Effort L**
**Goal:** prove it on real infrastructure and under attack.
Items: two-machine OBX↔XNO split-secret swap on the testnet droplet (134.122.71.149); OBX↔BTC regtest/signet swap; adversarial suite (stall, late-claim, griefing, rogue-key, lying-RPC member, front-runner, sweep-withholder, pre-cementation reorg) asserting the honest party never loses funds. **Gate: external audit before any value** (see §7).

**DEX factors advanced:** F2, F4, F5, F7, F8.

---

## 4. Work-streams (concrete steps, file:symbol)

Steps pull the fixes already written per-issue in `docs/SWAP_ISSUES_105.md`, deduped and ordered. The shared dependency for WS1–WS5 is the **two-party session** (WS2); build it once.

### WS1 — Atomic-swap safety & secret/timelock correctness (issues 1,5,9,10,11,12,13,15)
1. **Don't reveal sA until XNO is cemented (#1,#5).** In `cmd/obscura-swap/main.go:583 waitForReceivable`, after `nano.Receivable(account)` returns a hash, loop `nano.Confirmed(hash)` (`pkg/swapd/nanorpc.go:184`) until cemented or timeout; re-verify the credited amount; only then return `lockID`. Gate `doAtomicSwap` (`main.go:150`) entry on a final `Confirmed`. Assert `Balance() >= obxAmt+fee` (dry-run `FundSwap`) before advertising the joint address (`runLive` STATUS panel, `main.go:368`). Fix the "Detected + confirmed" log.
2. **Cross-leg timelock ordering with safety gap (#9,#10,#11).** Replace `unlock := c.Height() + 200` (`main.go:156`) with a derived window in a new `swapsession.ValidateTimelocks(slowLocktime, fastUnlockHeight, params)`: `refundHeight = currentHeight + ceil(nanoCementSeconds / config.TargetBlockTime) + safetyMargin`; require BTC CLTV > OBX UnlockHeight by a margin; require refund only at `UnlockHeight + reorgDepth` and claim a margin earlier (`pkg/swap/swap.go VerifyClaim/VerifyRefund`, `pkg/chain/validate.go:493`). Add BTC block-time + margin consts to `pkg/config/params.go`. Reject misordered setups before any Fund.
3. **Extract round-trip before committing the claim (#12).** In `doAtomicSwap` step 3, verify `commit.Extract` against `PreVerify/Adapt` BEFORE `mineWith(claim)`; on mismatch log both candidate scalars and still attempt the sweep / hand to recovery rather than aborting after the claim is committed.
4. **Deterministic adaptor nonces (#13).** Derive `ra,rb,R` RFC6979-style over `secretShare || coreHash` in `newSecrets`/session so a retry over a different `coreHash` cannot reuse `(ra,rb)`; treat `(a,b,ra,rb)` as single-use per swap id.
5. **Fold economic terms into the signed message (#15).** Extend `tx.CoreHash` (SwapInputs path) to cover the resolved `(Amount, Claim/RefundKey, UnlockHeight, ClaimR, ClaimT)` as defense-in-depth.

### WS2 — Real two-party P2P protocol (issues 16,17,18,19,20,21,22)
1. **Session state machine, split secrets (#17,#18,#19).** Create `pkg/swapsession/session.go` with Maker/Taker roles each holding ONLY their own half (Maker: sB-or-sA + b + rb; Taker: a + ra) — must compile such that no struct co-locates sA AND sB. Add `swap.HalfPresig(x_i, r_i, R, T, K, m)→s_i`, `swap.VerifyHalfPresig(s_i, X_i, R, T, K, m)`, `swap.CombineHalves(...)`; refactor `swap.CoSignClaim` (`pkg/swap/swap.go`) on top so the old call site still works but the protocol uses halves. Add a DLEQ binding sA↔T (#17).
2. **P2P take/accept/settle messages (#16).** Add `msgSwapTake=12, msgSwapAccept=13, msgSwapNonce=14, msgSwapFunded=15, msgSwapAbort=16` to `pkg/p2p/p2p.go:36` and dispatch cases routing into a per-offer session map on `Node` (guarded by `Node.mu`). Point-to-point via `n.send` (NOT gossip). PoW on the take message (reuse `swapbook.leadingZeroBits`).
3. **Drive the executor over P2P; rewrite selftest (#19).** Replace the single-process `doAtomicSwap` call (`main.go:244,250,260,376`) with `runMaker`/`runTaker`; selftest spins up two `p2p.Node` instances on 127.0.0.1 ephemeral ports. Remove sB logging / self-recovery framing.
4. **Funding order + on-chain leg verification (#21).** Longer-timelock party funds first; second party funds only after verifying the first leg funded AND `Confirmed` to a depth; re-derive K via `swap.AggregateKeyVerified` and check `ClaimR/ClaimT` before locking the XNO leg.
5. **Counterparty claim-extraction monitor (#22).** Per-session watcher stores the pre-sig, watches for the swap-key spend, runs/verifies `commit.Extract`, sweeps the cross-chain leg (moves logic out of the in-memory `pre` var at `main.go:196-204`).
6. **Session authentication scaffolding (#20).** Bind each message to a fresh session ID + nonces; sign public shares; prove control of `Offer.Maker`. (Encrypted Noise/X25519 channel may defer to external-audit phase.)

### WS3 — Quote/amount/price binding (issues 3,4,42,43,51)
1. **Validate received XNO vs agreed amount (#3,#51).** Thread the expected `*big.Int` into `waitForReceivable`; keep polling/reject until `amt >= expected` AND confirmed.
2. **Bind OBX leg to the matched offer (#4).** Thread the selected `Offer` (or explicit `--obx/--xno`) into `doAtomicSwap`; derive `obxAmt` from the agreed rate; refuse if expired/mismatched. Removes `obxAmt := 3*config.AtomicPerCoin` (`main.go:244,374`). Full take/Quote binding completes in WS6/P4.
3. **128-bit XNO amounts (#42).** Widen `NanoClient.Lock`/`Balance` and `nanorpc.Lock` from uint64 to `*big.Int`/decimal-raw-string (`pkg/swapd/nano.go`, `nanorpc.go:151-155,231`); `publishState` already uses big.Int.
4. **Receive by expected amount (#43).** `Receivable` selects by expected amount (tolerance) and ideally source/send-block hash; receive ALL pending so nothing strands.

### WS4 — Refund & failure recovery / sweep correctness (issues 7,8,22,25,26,27,28,29,30,31,32,33,34,35,36,37,38,39,40,41,44,45,46,56)
1. **Persisted swap-state machine (#25,#27,#29).** `pkg/swapd/swapstate.go`: `SwapState` (SwapKey, Direction, half-secrets, FundSwap txid, UnlockHeight, ClaimR/ClaimT, jointAddr, lockID, xnoDest, role, Phase enum) + `SwapStore` write-ahead (append-then-rename, fsync, `0600`) under `<datadir>/swaps/<key>.json`, written BEFORE each irreversible action.
2. **Resumable phase-driven executor (#26).** Restructure `doAtomicSwap` to take a `*SwapStore`/`*SwapState`, checkpoint before `mineWith(fund)`/`mineWith(claim)`/`nano.Sweep`, and skip completed phases on resume.
3. **OBX refund builder (#7).** `buildRefund` actually calls `wallet.BuildSwapSpend(swapKey, amount, isRefund=true, fee, sign)` (the path only a unit test hits today; only the `false` caller exists at `main.go:180`); sign under the funder RefundKey (`commit.Sign(sec.b, coreHash)`).
4. **Height watcher auto-fires refund (#8).** `pkg/swapd/watcher.go`: for each `Phase==ObxFunded`, poll `c.Height()` and `c.Swap(swapKey)`; at `Height()>=UnlockHeight` build+mine the refund; if `c.Swap` returns `!ok` (claimed) extract sA and sweep instead.
5. **Stranded-XNO recovery (#5 recovery, #36,#37).** `recoverXNO(state, store, nano)`: case A (aborted before claim) reconstruct `accountSecret = sA+sB` and `NanoRPC.Sweep`; case B (after claim) `commit.Extract` sA. Replaces printed-hex at `main.go:317,377`. For generated-dest: publish the open/receive block or refuse to generate a dest.
6. **CLI subcommands.** `obscura-swap refund|recover-xno|resume <swapKey>` in the `main.go` switch.
7. **Sweep correctness battery (#28,#34,#35,#38,#39,#40,#41,#44,#45,#46).** Idempotent/resumable `Sweep` (skip receive if lock gone; send-all on live frontier); poll `block_info` until cemented before chaining the send and before reporting completion; enumerate ALL receivables and assert exactly the locked amount; propagate `account_info` errors; retry only idempotent reads, treat post-timeout old-block/fork as success via `block_info`; fetch `active_difficulty` and escalate work; fix `callOnce` double-marshal; operator-configurable representative.
8. **RPC failover (#32,#33).** Carry the full preset list; fail over to the next `CanProcess/CanWork`; support local work; persist `recvHash` so retry resumes at the send.
9. **Reservation state (#22 driving).** swapbook `Take/Accept/Abort/Reservation` (see WS6) persisted via SwapStore so a restart reloads in-flight swaps.
10. **MockNano failure injection (#30) + finalize-before-fatal (#31).** Add return-to-sender / timelock / withheld-claim mocks; gate the PASSED banner on them; write the recovery bundle before any `Fatalf`.
11. **Unified BTC/XNO abort semantics (#56).** Enforce cross-leg ordering and per-leg abort the live flow actually invokes on timeout (lands with WS7 BTC).

### WS5 — Liquidity & market-making *(builds on shipped auto-liquidity)* (issues 66,67,68,72)

**Current-state assessment (MISSING factor — written here):** Liquidity provision exists *only* as the default-on auto-liquidity loop (`cmd/obscura-node/main.go:306 autoLiquidityLoop`, `pkg/swapbook/autoliquidity.go BuildSignedOffer`, `/auto-liquidity` status). A mining node posts OBX→XNO offers from spendable rewards: budget-capped (`AutoLiquidityMaxFraction=0.5`), chunked (`AutoLiquidityChunkOBX=5`), capped count (`AutoLiquidityMaxOffers=8`), priced at the book-best-or-seed rate (`bookOrSeedRate`, seed `AutoLiquiditySeedRateXNO=1.0`), 30-min TTL. **Gaps making this <100:** (a) all liquidity is one-directional (OBX→XNO only) and single-maker, so depth is shallow and trivially self-dealt (#67); (b) the loop posts *intent* only — there is no escrow/bond, so advertised depth is not backed by locked funds (#66, and WS6/#23); (c) the `XNO=2x` liquidity-reward subsidy makes the cheapest, feeless-XNO leg the most profitable to wash, with burn/anchor defenses unshipped (#68, `docs/LIQUIDITY_REWARDS.md`); (d) offers are in-memory only — a maker's auto-offers vanish on restart and are never rebroadcast (#72); (e) the decimals bug (#73) makes every posted rate 1e4-wrong until P0 lands. **Plan to 100:** 
1. Raise/asset-tier offer PoW + per-maker live-offer cap so the single auto-maker cannot dominate the book (#66,#67); reuse `MakerOffers` (`autoliquidity.go:43`).
2. LP incentives under a budget cap with kill-switch: keep `XNO=2x` strictly distributional, do NOT enable any reward until burn+anchor+sybil-mesh regression test land (#68).
3. Two-sided & reputation-weighted market-making: extend the loop to post both directions once `Book.Quote` exists; rank makers with completed `TradeLog` trades ahead (P4 hook).
4. Book persistence + rebroadcast (#72): persist locally-originated live offers, rebroadcast on startup and on a timer until Expiry; snapshot the book to disk (`pkg/swapbook`).
5. Distinct-maker depth annotation feeds the UI (WS8/#67) so fabricated single-maker depth is visible.

### WS6 — Order book & matching engine (issues 23,24,58,59,60,61,62,63,64,65,66,67,69,70,71,72,91)

**Current-state assessment (MISSING factor — written here):** The "matching engine" today is `Book.Best(takerGives, takerWants)` (`pkg/swapbook/swapbook.go:293`) — a single-offer best-price selector over an in-memory map, plus `/offers` (hex, uncapped) and `/offers/json` (decoded, `%g` float rate, sorted ascending, capped 2000) in `pkg/rpc/server.go:436,463`. **There is no matching, no partial fill, no cancel, no taken/reserved state, no persistence.** The order book is a "billboard" (#16): makers gossip PoW-stamped signed offers; takers read them; nothing binds an offer to funds or drives a swap. **Concrete defects:** `Best` ranks on un-normalized `float64(Give)/float64(Get)` across assets, NaN/Inf on zero amounts, non-deterministic ties (#59,#60,#61); `handleOffersJSON` rate is a lossy inverted `%g` (#62) with non-deterministic sort (#63); offers carry no netID/settlement binding (#65), no proof-of-funds (#23,#24), maker wall-clock expiry unbound to timelocks (#64); `OfferPoWBits=12` (~4096 hashes) + `MaxBookSize=50000` + no per-maker cap enables sybil flooding (#66,#67) and relay amplification (#69); `pruneLocked` is O(n) under the global lock on every Add/List (#70); `msgGetOffers` returns an arbitrary 256-entry map-order subset so peers can't reconcile (#71); the book is purely in-memory and lost on restart (#72); pair keys aren't canonicalized (#91). **Plan to 100:**
1. **Fix the BTC asset value (#58, P0).** `<option value="BTC">BTC (mock)</option>` in `website/wallet.html:140-141`; map labels→canonical tickers before `obxBuildOffer`; validate client-side.
2. **Exact integer ordering (#59,#60,#61,#62,#63, P4).** Rewrite `Book.Best` to cross-multiply `*big.Int` with a deterministic ID tiebreak (no float); make `handleOffersJSON` emit a gcd-reduced `get/give` fraction and `SliceStable` by cross-multiply with an ID secondary key; align the one canonical taker-receives-per-pays convention with `wallet.html offerRate` so UI-best == `Best()`.
3. **Real matching: `Book.Quote(takerGives, takerWants, wantAmount)` (P4).** Walk offers best-first until filled, returning the filling offers + exact aggregated price (depth-weighted). Add MinFill/PartialFill to `Offer` (signed into Core). Expose `/depth/json` (fillable curve, best-bid/ask, mid, spread).
4. **Reservation / take / cancel / persistence (#23,#24,#72).** Add `Swap`+`Reservations` to `Book`: `Take(offerID, takerPub)` reserves with a deadline, `Accept/Settle/Abort` transitions, stale-reservation sweep, `ReservedOffers()/InFlight()`. Add signed cancellation `msgSwapCancel`. Bind offers to a pre-funded SwapOut / refundable bond verifiable before committing capital. Persist locally-originated offers + reservations; rebroadcast on startup/timer.
5. **Binding & replay (#64,#65).** Mix `config.NetID()` into `Core()`; add a settlement-binding field (maker Nano account / BTC refund pubkey); require remaining TTL > worst-case swap completion; re-validate Expiry against a confirmed chain reference at lock time.
6. **DoS hardening (#66,#67,#69,#70,#71).** Raise/deviation-scale `OfferPoWBits`; per-maker live-offer cap + min economic amount per asset; per-peer token-bucket on `msgSwapOffer`; expiry-ordered index (min-heap / time buckets) or background prune ticker replacing the O(n) scan; order `msgGetOffers` (SortedByID) + cursor pagination or have/want inventory.
7. **Canonical pair key (#91).** Sorted-pair key rendering bid/ask together; normalize/uppercase tickers.

### WS7 — Asset/chain coverage & pluggable backends (issues 32,33,54,56,57)
1. **Production BitcoinClient (#54).** `pkg/swapd/bitcoinrpc.go` over bitcoind/Electrum (operator-supplied URL, no hardcoded endpoint): `FundHTLC` pays the P2WSH from `BtcWitnessProgram(BtcHTLCScript(...))` (`bitcoin.go:62,88`, bech32 tb1/bc1); `Confirmed` gates on configurable depth; `Redeem` spends the hashlock branch revealing `t`; `Refund` spends the CLTV branch at/after locktime; `RevealedPreimage` parses the witness. `bitcoinpresets.go` (regtest/testnet). Wire `Server.SetBitcoinBackend` (`pkg/rpc/server.go`, clone of `SetNanoBackend`) in `cmd/obscura-node/main.go`; add a `--btc-rpc` HTLC settle branch in `cmd/obscura-swap`. Set the BTC hashlock = SHA256(t) of the OBX-committed adaptor secret; verify the funded HTLC matches agreed terms before OBX funds; until shipped, **disable BTC offers**.
2. **ChainAdapter + registry (#32,#33 plumbing).** `pkg/swapd/chainadapter.go` (`Lock/Confirmed/Settle/Refund/Decimals/Kind`) with scriptless (Nano/Monero) and HTLC (Bitcoin) scaffolds; `pkg/swapd/registry.go` binds tickers→backends (decimals/kind/live) and feeds `swapbook.Offer.Verify` so unsettleable pairs (OBX↔DOGE) are rejected. RPC failover lives here (carry the full preset list — see WS4 step 8).
3. **Unified abort semantics (#56).** Enforce secret-revealer-gets-shorter-window across legs; per-leg abort the live flow invokes on timeout (with WS4).
4. **Harden MockBitcoin (#57).** Require 33-byte pubkeys; compute the script via `BtcHTLCScript`; store/compare `BtcWitnessProgram` as the lock identity so the mock is a faithful atomicity oracle.
5. **Resolve XMR.** Ship `pkg/swapd/monerorpc.go` (reuse scriptless scaffold) OR delete `monero.go`+MockMonero and drop 3-chain language.

### WS8 — UX/UI & onboarding (issues 6,52,53,73,74,75,76,77,78,79,80,81,82,83,84,85,86,87,88,89,90,91,92,93)
1. **P0 honesty + scale.** Honest experimental warning replacing "funds reclaimable" (#6); `DEC.OBX=12` derived from `AtomicPerCoin` with a drift assertion (#73); funding-loop timeout/diagnostics + serialize block production (#52,#53).
2. **Amounts & decimals (#75,#76,#77,#78,#80,#90,#93).** Human-decimal inputs with live preview; format give/get through the (fixed) DEC map; aggregate Σ/depth/rate as BigInt, divide only at display; restrict depth to a single directed pair with bid/ask stacks and human axes; never mix human/raw ordering.
3. **Verification & success truth (#74,#82).** WASM `Verify` path filtering expired/invalid offers; re-query `/offers` and confirm the id is present before "Posted ✓"; poll txids for sends.
4. **Take/execute UI (#85,#89).** Live swap-execution stepper (Matched→XNO locked→OBX funded→OBX claimed→XNO swept) with per-step hash/height/spinner, refund-timelock countdown, "Your in-flight swaps" (localStorage) + Recover/Refund button. (Depends on WS2/WS9.)
5. **Safety & ergonomics (#81,#83,#84,#86,#87,#88,#92).** Confirm modal before grinding PoW; "My offers" lifecycle + expiry countdown; disable button + Web-Worker PoW; encrypt the mnemonic (WebCrypto PBKDF2/AES-GCM, require unlock); `textContent`/escape in `msg()`+tables (XSS); copy-toast + fallback + a11y; expiry/maker columns.
6. **Pair/market polish (#91, #79).** Canonical pair rendering; call `/offers/json` (capped) and cap rendered rows.

### WS9 — Observability & transparency (issues 94,95,96,97,98,99,100,101,102,103,104,105)
1. **Swap tracker + RPC (#94 surfaced).** `pkg/swapd/tracker.go` per-swap both-leg state; `pkg/rpc/swaps.go` `/swaps` + `/swap?id=`; explorer order-book panel polling `/offers/json` (#94); cross-chain proof links on atomic-swap txs.
2. **Explorer correctness (#95,#96,#97,#98,#101,#102,#103,#105).** Add per-block `difficulty` to `ExplorerBlockSummary` and compute Σ(per-block difficulty)/elapsed (#95,#98,#101); include `VaultOutputs/ZKOutputs` in `NumOutputs` + zk/vault kind cases (#96); set `PoWBackend` (#97); average two middle elements for median fee (#102); diff genuinely-new heights, skip initial animation (#103); search input validation + inline error (#105).
3. **Failure surfacing (#100,#104).** Mark mempool section stale on failure (#100); include proxy JSON error/detail in thrown messages (`api/explorer.js`) (#104).
4. **Vaults widget (#99).** Per-vault list with maturity progress from `/explorer/vaults`.
5. **Metrics + slog.** `pkg/metrics` `/metrics` (expvar) + JSON slog across swap/liquidity paths; dashboard swap panel; node-reported hashrate.

### WS10 — Security & MEV/front-running (issues 14,47,48,49,50,55)
1. **Kill single-RPC trust (#47,#48,#49, P0+P4).** P0: require `Confirmed` + treat unreachable RPC as a hard preflight failure (or `--force`). P4: `pkg/swapd/quorumnano.go` — M-of-N agreement on `Confirmed/Receivable/Balance`, broadcast-to-all `Sweep`, repeatable `--nano-rpc`.
2. **Channel integrity (#50).** Enforce https for non-loopback; min TLS version; cert/SPKI pinning in `nanorpc.callOnce`.
3. **Consensus nonce-reuse guard (#14).** Treat `(ClaimKey, ClaimR)` as a uniqueness nullifier in `pkg/chain/validate.go`; reject a SwapOut whose ClaimR collides with any live/historical swap; persistent seen-R index in `chain.go`; validation test.
4. **BTC MEV (#55).** Confirmation-gated, deadline-aware redeem/refund with asymmetric timelocks (secret-revealing leg redeemed second with margin), RBF fee-bumping, sweep-before-reveal ordering, optional direct-to-miner claim submission. (With WS7 BTC.)

---

## 5. Phase 0 / do-first checklist — the 28 criticals (condensed)

| # | One-line | File:symbol |
|---|---|---|
| 1 | Poll `Confirmed` before revealing sA | `cmd/obscura-swap/main.go:583 waitForReceivable` |
| 2 | Split joint key; fund+confirm OBX before XNO prompt | `runLive`/`doAtomicSwap`, `newSecrets` (`main.go:133,150`) |
| 3 | Validate received XNO vs agreed amount | `waitForReceivable`/`doAtomicSwap` |
| 4 | Bind OBX leg to matched offer (drop hardcoded 3 OBX) | `main.go:244,374 obxAmt` |
| 5 | Assert OBX fundable before advertising joint addr | `setupOBXLeg`/`FundSwap` |
| 6 | Replace false "funds reclaimable" panel | `runLive` STATUS panel (`main.go:349`) |
| 7 | Build/broadcast/watch the refund spend | `wallet.BuildSwapSpend(isRefund=true)` (only `false` caller, `main.go:180`) |
| 8 | Refund/abort state machine + deadlines | `doAtomicSwap` (`main.go:156`) |
| 9 | Compute & enforce L_obx<L_btc with gap | `doAtomicSwap` + `bitcoin.FundHTLC` |
| 16 | take/accept handshake (book is a billboard) | `p2p` msgSwap*, `rpc` handleOffers |
| 17 | Exchange per-swap public shares + DLEQ | `newSecrets`/`doAtomicSwap` |
| 18 | Split CoSignClaim into partial pre-signatures | `swap.CoSignClaim` |
| 25 | Persist per-swap secrets before advertising addr | `runLive` in-memory `swapSecrets` |
| 28 | Idempotent/resumable Sweep | `nanorpc.Sweep` |
| 47 | Stop trusting one RPC's "receivable"; confirm+cross-check | `waitForReceivable`/`Receivable`/`Confirmed` |
| 58 | Fix `BTC (mock)` option value (offers DOA) | `wallet.html:140-141` → `swapbook.validAsset` |
| 73 | `DEC.OBX=12` (prices 1e4 wrong) | `wallet.html:303`, `config` decimals |
| 85 | Take/execute/refund UI (or relabel) | `wallet.html #t-swap` |
| 42 | Widen XNO amount to big.Int (≥1 XNO truncates) | `nano.go`/`nanorpc.go Lock/Balance` |

*(The remaining criticals are sub-items of the rows above: #2 stranded-send and #25 persistence are one fix; #16/#17/#18 are the one two-party protocol; #47 covers the RPC-trust criticals #5-class; #4 ties #3. All 28 critical issues from the audit — 1,2,3,4,5,6,7,8,9,16,17,18,19(→P1),25,28,47,58,73,85,42, plus the criticals folded into these rows — are scheduled no later than P1, and the money-safety subset lands in P0.)*

**P0 exit criterion:** the existing self-test path cannot lose value — amounts are 128-bit and offer-bound, sA is only revealed after cementation, the sweep is idempotent, and per-swap state is persisted to disk before the joint address is shown.

---

## 6. Definition of Done for a value-bearing DEX

Global gates (must hold across the whole DEX):
- **No co-located halves:** no code path ever holds both sA AND sB (or a AND b) in one struct/process — enforced by a compile-/test-level split-secret check.
- **128-bit safe + one decimals authority:** no uint64 truncation of XNO raw; every scale derives from one authority with OBX=12, guarded by a drift test.
- **Exact pricing:** every pricing/ordering decision uses cross-multiplied integer arithmetic; float appears only at final browser display.
- **Cementation gate:** no irreversible OBX settlement before the counterparty leg is confirmed to the configured depth.
- **Executable refund/recovery:** the OBX refund branch is built, broadcast, and watched; stranded XNO is recoverable from persisted state; refund/recover/resume are user-reachable (CLI + web).
- **Bound quotes:** every settled leg's amount derives from a taken `Offer`/`Quote`, never a constant.
- **Real two chains:** a two-node, two-machine swap completes on the live testnet for OBX↔XNO and on regtest/signet for OBX↔BTC; an aborted swap refunds/recovers with no party losing funds.
- **Adversarial pass:** stall, late-claim, griefing, rogue-key, lying-RPC member, front-runner, sweep-withholder, and pre-cementation-reorg tests all pass with the honest party never losing funds.
- **Observable:** every swap is queryable (`/swaps`), both-leg tx-linked, and shows explicit Stuck/Refunding/Aborted states.

---

## 7. Honest risk note — what still needs an external audit

This plan makes the swap *structurally* sound and test-covered, but **value must not flow until an external audit** clears the following, which are beyond in-house assurance:
- **Adaptor-signature & half-presig protocol.** `swap.HalfPresig/CombineHalves/VerifyHalfPresig`, the DLEQ binding sA↔T, deterministic-nonce derivation, and the consensus ClaimR nullifier (#13,#14,#17,#18) are exactly the class of crypto where a subtle nonce/aggregation flaw silently leaks keys. The 2026-06 internal crypto audit was a NO-GO and flagged "deep STARK rewrites need external audit before value"; the same standard applies to the swap cosigning.
- **Cross-chain timelock ordering under reorg.** The PoW chain reorgs freely (#11); the safety-margin derivation (`ValidateTimelocks`) and the claim/refund boundary must be reviewed against realistic XNO cementation and BTC reorg depths, not just unit tests.
- **Bitcoin witness/script construction.** `bitcoinrpc.go` builds real consensus-critical Bitcoin scripts and witnesses; a malformed CLTV/hashlock branch is unrecoverable on mainnet. Regtest/signet passing is necessary, not sufficient.
- **MEV / front-running residual.** The public-mempool secret reveal (#55) and OBX claim front-running have only *mitigations* (ordering, direct submission, fee-bump), not a proof of no-steal; the residual race needs adversarial review.
- **RPC trust & MITM.** QuorumNano (#49) and TLS pinning (#50) reduce but do not eliminate reliance on third-party Nano infrastructure; the trust model must be documented and reviewed.

This ships on **mainnet**: it is new software, so review it yourself, and **the chain source must stay local / never publish** per project policy.

---

*Local planning document for Obscura mainnet. Not for publication.*
