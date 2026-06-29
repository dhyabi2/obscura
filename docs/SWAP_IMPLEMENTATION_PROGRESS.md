# Swap/DEX Implementation — Live Progress Log

**Mode:** autonomous execution of `docs/SWAP_DEX_IMPLEMENTATION_PLAN.md` (105 issues, 7 phases), implement → review → verify per phase. Live mainnet; source kept private (no publish).

Legend: ✅ done+verified · 🔧 in progress · 🔍 under review · ⏭ deferred (needs network layer / external audit) · ⬜ not started

---

## P0 — Money-safety criticals  ✅ (6 fixed, build+selftest+tests green)
All in `cmd/obscura-swap/main.go`.
- ✅ **#1** poll `nano.Confirmed()` before revealing `sA` (no settle on un-cemented send)
- ✅ **#3** validate received XNO ≥ agreed (`--xno-amount-raw` enforced in `waitForReceivable`)
- ✅ **#4** `--obx-amount` flag + `obxAtomic()` — no hardcoded 3 OBX
- ✅ **#5** prove OBX fundable (≥ amt+fee) before advertising joint addr (`setupOBXLeg(required)`)
- ✅ **#6** removed false "reclaimable" claim → honest EXPERIMENTAL warning
- ✅ **#12** verify adaptor extract round-trips BEFORE mining the claim (abort, don't strand)

## P2 — Refund & timelocks  ✅ (#7/#8 done) · 🔧 (#9/#11 pending)
- ✅ **#7/#8** `refundOBX` auto-fires on any pre-claim failure → waits to UnlockHeight → builds refund spend `commit.Sign(sec.b,…)` accepted by `SwapOutput.VerifyRefund`. Test `TestSwapRefundOnPreClaimFailure` PASSES (16s).
- ✅ **#10 (partial)** timelock window is now `config.SwapTimelockWindow` (default 200), not magic `height+200`.
- ✅ **#11** reorg grace margin: claim valid iff `height+M ≤ U`, refund iff `height ≥ U`, dead-zone `[U−M, U)`, `M=config.SwapReorgMargin=100`; matched in `swap.go` + `validate.go`; test `TestSwapReorgMarginDeadZone`. Closes the claim-vs-refund cross-reorg race.
- ⬜ **#9** enforce cross-leg timelock ordering `L_obx < L_btc` (only relevant once the BTC leg is wired — P3)

## P1 — Two-party protocol backbone  ✅ SOUND + 🆕 P2P TRANSPORT BUILT (🔍 transport under review)
_Reviewed adversarially; 3 fund-loss bugs + 1 nonce bug found and ALL fixed + tested (each attack-rejection test verified to fail without its fix). Build+vet+all swap tests green._

**🆕 P2P TRANSPORT (`pkg/swapnet`, 2026-06-26):** carries the six `swapsession` messages between TWO separate nodes over `pkg/p2p` (new directed `msgSwapSession`, routed by SwapID — NOT gossip). `Coordinator` runs maker/taker state machines from network events; timeout→`Maker.Refund`; per-session `SwapState` persistence/resume. **Key design: the transport is UNTRUSTED** — swapsession messages self-authenticate, so a hostile transport/counterparty can only DoS, not steal (so P1's hardening carries over). **Two-node test `TestTwoNodeSwapOverP2P` PASSES** — full swap completes over a real p2p socket, asserts share-isolation + wire-traversal; `TestAbortMakerRefunds` passes. **Adversarial review DONE — NO fund-LOSS** (untrusted-transport property holds; 9 verified-safe items: `SweepXNO` re-verifies sA cryptographically, F1/F2/F3/F-1 hold over the wire, state machine serialized per-session, replies point-to-point, SwapID bound into nonce derivation). Found 3 griefing/liveness items (NOT loss), **fix in progress:** **F-A** unauthenticated `Init` auto-funds maker OBX → capital-lockup DoS (fix: session caps + offer-binding/AcceptInit gate); **F-B** absent/corrupt `ClaimDone` freezes maker XNO sweep — recoverable since sA is public on-chain (fix: chain-scrape fallback); **F-C** inbound routed by SwapID only → abort-injection/buffer-flood (fix: bind `fromPeer==s.peer` + unpredictable SwapID). Still NOT covered: live multi-machine/NAT, counterparty peer discovery, fee negotiation.
- ✅ Hardening F-A (session caps `SwapMaxSessions`/`SwapMaxSessionsPerPeer` + deny-by-default `AcceptInit` gate), F-C (`fromPeer==s.peer` binding + internally-minted unpredictable SwapID) — done, tested.
- **🔒 F-B revealed a PROTOCOL gap (maker-loss griefing), fix in progress:** the maker recovered `sA` only from the taker-relayed aggregate pre-sig `Ŝ`; the on-chain claim gives `S_full=Ŝ+sA` (2 unknowns) and the maker holds only its own half `ŝ_b` → a taker can claim OBX, withhold `ŝ_a`, and freeze the maker's XNO sweep (robbing the funded OBX; taker burns its own XNO). Earlier in-process reviews missed it (harness always relayed the pre-sig cooperatively). FIX (standard trustless 2-party adaptor) — ✅ DONE + verified: taker reveals `ŝ_a` (`ClaimRequest.TakerHalf`), maker verifies `ŝ_a·G==Ra+e·A` before co-signing (`verifyTakerHalf`) + stores it (`SwapState.PeerClaimHalf`), then `Maker.SweepXNOIndependent` extracts `sA=S_onchain−ŝ_a−ŝ_b` from the chain-scraped claim — **ZERO taker cooperation in the maker's recovery path** (the `ClaimDone` relay is now a latency hint only). Tests: `TestMakerExtractsSAIndependently`, `TestCoSignRejectsForgedTakerHalf`, `TestMakerSweepsWithoutAnyClaimDone` (taker withholds ALL relay → maker still sweeps; fail-before/pass-after confirmed). Math matched to the code's `S=ŝ_a+ŝ_b+sA` convention, cross-checked vs `commit.Extract`.
New `pkg/swapsession` (in-process; P2P/RPC transport deferred). `Maker` mints only `b,sB,rb`; `Taker` only `a,sA,ra` — **neither party holds both shares** (`assertShareIsolation` test). Flow: Init→MakerCommit→**maker funds OBX first**→Funded→taker verifies on-chain SwapOut (ClaimKey==K, amount, UnlockHeight, ClaimR==R, ClaimT==T, RefundKey==B) then locks XNO→ClaimRequest→maker co-signs half→taker adapts with sA + publishes claim→maker extracts sA, sweeps XNO. #13 deterministic nonces from per-party term hash. `SwapState` JSON persistence for resume/refund. Messages serialized + validated (PoP re-verify, identity-point reject, T==Sa).
- **🔒 REVIEW BUG CAUGHT + FIXED (me):** `CoSignClaim` didn't stop a malicious taker from requesting a SECOND distinct core-hash co-sign under the same committed nonce `rb` → `b=(sb1−sb2)/(e1−e2)` leak → full claim key → claim OBX without revealing sA (maker loses OBX, XNO frozen). Added a one-core-hash guard (benign same-hash retry still allowed) + test `TestMakerRefusesSecondDistinctCoSign`. All swapsession tests pass.
- 🔍 **adversarial review DONE — found 3 fund-loss bugs (P1 NOT yet sound), fixes in progress:**
  - **F1 (HIGH):** `ConfirmXNOLock` doesn't verify the XNO lock pays the joint account `(sA+sB)·G` or agreed amount → maker can be robbed. Fix: add `LockInfo(lockID)→(amount,accountPub)` to the XNO interface; check both before co-signing.
  - **F2 (HIGH):** `checkSwapOut` doesn't verify the on-chain OBX amount (`swap.SwapOutput` lacks `Amount`) → taker can be robbed. Fix: thread `Amount` into `SwapOutput`/`FindSwapOut`/checkSwapOut.
  - **F3 (MED):** co-sign nonce guard is in-memory only → maker crash+resume re-opens the `b` leak. Fix: persist `coSignedCoreHash` in `SwapState`.
  - F4/F5 (LOW): Refund callable post-cosign (in-proc only; consensus margin covers it); test global-config hygiene.
  - Review CONFIRMED safe: rogue-key/PoP, adaptor binding + forced sA reveal, OBX leg ordering, replay/SwapID, #13 nonces, share isolation, in-memory nonce guard.
- ⏭ deferred: P2P/RPC transport, timeout-armed refund watcher, matching/amount-rate binding, live confirmation polling.

## P3 — Real chains & pluggable backends  ⏭ DEFERRED (blocked on a dependency decision)
Production `BitcoinClient` (HTLC), `ChainAdapter` registry, RPC failover. **Blocked:** trustless BTC redeem/refund signing needs a pure-Go **secp256k1** dependency — adding an external dep is the maintainer's call (per project memory). The HTLC script + mock are already built/tested; what remains is the real RPC client + signer, which can't be certified without the dep + a live/regtest node. Recommend a separate decision + focused effort. (#54/#58.)

- **Bitcoin: DISABLED** — BTC is gated OFF across the swap surface via a settleable-asset allowlist (`config.SettleableAssets = {"OBX","XNO"}`, helper `config.IsSettleableAsset`). `Offer.Verify` (and `Book.Add`) REJECT any offer whose give/get asset is not settleable, so BTC offers cannot be posted, taken, gossiped, or surfaced by `Quote`/`Depth`/`autoliquidity` (all read from the book). The web wallet (`website/wallet.html`) no longer offers a "BTC (mock)" option. The BTC code (`pkg/swapd/bitcoin.go` HTLC/MockBitcoin) is left intact, only gated. **Re-enable:** add `"BTC"` to `config.SettleableAssets` once the BTC leg + secp256k1 dep land (and restore the wallet `<option>BTC (mock)</option>` entries).

## P4 — Market quality, matching & manipulation resistance  🔧 (matching engine ✅; rest ⬜)
- ✅ **Matching engine** (`pkg/swapbook/match.go`, reviewed): `Book.Quote` (depth-aware VWAP, 128-bit-safe floor math validated vs math/big, partial-fill flag), `Book.Depth` ladder, maker-signed `Book.Cancel` (domain-separated Schnorr, forged/replay rejected), `PruneExpired`, `MaxOffersPerMaker=64` cap. 19 tests pass; vet clean. Honest limits noted (P2P cancel is best-effort until TTL; sybil can post 64/key — needs scarce identity).
- ✅ **#14 consensus `ClaimR` uniqueness** (`pkg/chain` `swapNonces` set): rejects any swap reusing an adaptor nonce R; reorg-safe via the exact `zkNull` snapshot/restore lifecycle (init/reset/snapshot/restore/apply). 3 tests incl. reorg-safety. Defense-in-depth backstop to the P1 protocol guard.
- ⬜ exact pricing/decimals binding, QuorumNano (multi-RPC confirmation), thin-book manipulation resistance.

## P5 — Observability, UX & onboarding  ⏭ DEFERRED (premature — needs the network-wired protocol)
Browser take/execute, swap tracker + proof links, encrypted wallet. The take/accept UX should be built against the REAL two-party message transport (P1's deferred network layer), not the in-process core — building it now would be thrown away. Already shipped this session and serving as the baseline: order-book viz (id/rate/depth/liquidity), explorer hashrate + price cards, swap-tab market-rate banner, auto-liquidity. Remaining UX waits on transport.

## P6 — Live + adversarial validation  🔧 (capstone review run; live two-machine deferred)
- ✅ Per-phase adversarial reviews done (P1 review found+fixed 3 fund-loss bugs); ✅ capstone integration review.
- **🔒 CAPSTONE FOUND F-1 (HIGH) — FIXED + tested.** The `SwapTimelockWindow > SwapReorgMargin` invariant (a swap's `UnlockHeight` must leave an open claim window) was enforced NOWHERE → a maker could fund with `UnlockHeight` inside the reorg margin, taker accepted (only matched announced value) + locked XNO into an unclaimable swap → XNO frozen, maker refunds risk-free. FIXED at 3 layers: taker `checkSwapOut` (primary), `Maker.Fund` (honest-misconfig), consensus fund-time (defense-in-depth), via new `config.SwapMinClaimWindow=50` (invariant `UnlockHeight ≥ H + SwapReorgMargin + SwapMinClaimWindow`; defaults compose 200≥150). Regression test `TestRejectUnclaimableUnlockWindow` (verified fail-without-fix) + `TestHappyPathNonTrivialClaimWindow`. Capstone confirmed #11/#14/F1/F2/F3 otherwise compose correctly (byte-identical consensus/helper checks, Amount authoritative, nonce set reorg-safe, no config-leak).
- ✅ **In-process swap core is now SOUND** — all found bugs fixed+tested, capstone-clean. Full security battery (7 tests) green; build+vet green.
- ⏭ Two-machine / regtest live proof + the on-network adversarial battery wait on the P2P transport (P1-deferred) and, for BTC, P3.

---

## Deferred summary (honest)
The **in-process, single-chain (OBX-side) atomic-swap core is now hardened + reviewed**. What remains for a value-bearing, multi-party, multi-chain DEX is gated on two external things, neither a coding gap I can close unilaterally:
1. **A P2P/RPC transport** for the two-party messages (P1 serializes them; moving them over the network + timeout-armed watchtowers is the next build — large but unblocked).
2. **A secp256k1 dependency decision** for the real Bitcoin leg (P3).
Plus a **professional external audit** before any real value — the swap crypto is intricate (adaptor 2-of-2, reorg margins, nonce uniqueness) and self-review, however adversarial, is not a substitute.

---
_Updated as each phase lands. See `SWAP_ISSUES_105.md` for issue detail and `SWAP_DEX_IMPLEMENTATION_PLAN.md` for the full plan._
