# Obscura DEX — Comprehensive Manual + Automated Testing Plan

This plan exercises every testable layer of the Obscura DEX (P2P order book + atomic-swap
session protocol + swap network coordinator + on-chain swap output) and ties each scenario
to (a) the exact `go test -run` invocation and/or `obscura-swap`/`obscura-node` command, and
(b) a manual exercise using the **local test wallet**.

Everything is grounded in real code (`file:symbol`). Where a route/test does not exist, it is
called out as a **GAP** so you do not look for something that is not there.

---

## 0. Layer map (what each test touches)

| Layer | Package / file | Consensus? | Key symbols |
|---|---|---|---|
| A. Order book (P2P RFQ board) | `pkg/swapbook/swapbook.go`, `match.go`, `autoliquidity.go` | No (off-chain) | `Offer.Sign`/`Offer.Verify`/`Book.Add`/`Book.Best`/`Book.Quote`/`Book.Depth`/`Book.Cancel`/`Book.PruneExpired`/`BuildSignedOffer`/`Book.MakerOffers` |
| B. Swap session protocol | `pkg/swapsession/session.go`, `flow_test.go`, `nonce_test.go` | No | 2-of-2 adaptor OBX leg + scriptless XNO leg state machine |
| C. Swap network coordinator | `pkg/swapnet/coordinator.go` | No | `Coordinator.Take`/`Deliver`/`driveTaker`/`driveMaker`/`makerSweep`/`sweepFromChain`/`makerRefund`/`register`/`recv`/`abort` |
| D. Legacy executor + live XNO leg | `cmd/obscura-swap/main.go` | No | `runSelfTest`/`runLive`/`doAtomicSwap`/`refundOnFail` |
| E. On-chain swap output | `pkg/swap/swap.go`, `pkg/wallet/wallet.go`, `pkg/chain` | **Yes** | `SwapOutput.VerifyClaim`/`VerifyRefund`/`AggregateKey`/`CoSignClaim`/`Adapt`/`Extract`; `Wallet.FundSwap`/`Wallet.BuildSwapSpend`; `FindSwapSpend`, swap-nonce uniqueness set |
| F. Alternate settlement backends | `pkg/swapd` | No | `MockNano`/`NanoRPC`/`MockBtc` |
| UI | `pkg/rpc/server.go`, `pkg/rpc/explorer.go`, `website/explorer.html`, `website/wallet.html`, `website/api/explorer.js` | No | `handleOffers`/`handleOffersJSON`/`handlePostOffer`; `bestPrices`/`PairPrice`; `priceCards`/`humanPrice`/`depthBars` |

**Config knobs under test** (`pkg/config/params.go`): `SettleableAssets={OBX,XNO}` / `IsSettleableAsset` (266),
`SwapReorgMargin=100` (382), `SwapTimelockWindow=200` (393), `SwapMinClaimWindow=50` (426),
`SwapMaxSessions=256` (435), `SwapMaxSessionsPerPeer=8` (442). Composition rule: a claim window
must satisfy `window >= SwapReorgMargin + SwapMinClaimWindow`.

---

## 1. SETUP (do this once)

All commands assume repo root `/Users/mac/XMR_alternative` and the Go module `obscura`.

### 1.1 Build the binaries

```bash
cd /Users/mac/XMR_alternative
go build -o ./bin/obscura-node      ./cmd/obscura-node
go build -o ./bin/obscura-swap      ./cmd/obscura-swap
go build -o ./bin/obscura-wallet    ./cmd/obscura-wallet
go build -o ./bin/obscura-testwallet ./cmd/obscura-testwallet
```

### 1.2 The test wallet (already generated)

`/Users/mac/XMR_alternative/.testwallet/testwallet.json` was produced by
`cmd/obscura-testwallet/main.go`:

```json
{
  "xno_address":    "nano_13kekgtubt9zywkyzys8fredu54tf333j1d9trfxjit86x9eyq1nafedtaw5",
  "xno_secret_hex": "<redacted-xno-secret>",
  "obx_seed_hex":   "<redacted-obx-seed>"
}
```

- **`xno_address` is a REAL Nano mainnet account.** Any scenario marked **[REAL XNO]** below
  spends from / receives to this account. **Hard cap: 0.00001 XNO per send.** Keep
  `xno_secret_hex` — sweeps and refunds need it.
- **`obx_seed_hex` is a test-chain seed** with NO real value. The OBX wallet is derived via
  `wallet.FromSeed(seed)` and self-funded by mining. Any scenario marked **[FREE OBX]** costs
  nothing — the test chain is value-less; genesis resets and consensus changes are free.

Convenience env (use in the shells below):

```bash
export TW=/Users/mac/XMR_alternative/.testwallet/testwallet.json
export XNO_ADDR=$(python3 -c "import json;print(json.load(open('$TW'))['xno_address'])")
export XNO_SK=$(python3 -c "import json;print(json.load(open('$TW'))['xno_secret_hex'])")
export OBX_SEED=$(python3 -c "import json;print(json.load(open('$TW'))['obx_seed_hex'])")
```

### 1.3 Start a local test node with auto-liquidity

The node refuses to start on the prototype PoW backend unless overridden
(`cmd/obscura-node/main.go:535`). Auto-liquidity is ON by default when mining to the node's own
wallet (`cmd/obscura-node/main.go:180-188`); it is disabled by `--no-auto-liquidity`.

```bash
# Terminal A — local single node, mining, auto-liquidity ON, XNO execution disabled (no --nano-rpc).
OBX_ALLOW_PROTOTYPE_POW=1 ./bin/obscura-node \
  --datadir /private/tmp/obx-testnode \
  --rpc 127.0.0.1:18181 \
  --p2p 127.0.0.1:18888 \
  --mine
```

Expected log lines:
- `OBX_ALLOW_PROTOTYPE_POW=1 set — continuing on INSECURE prototype PoW backend`
- `auto-liquidity ENABLED: auto-posting OBX→XNO offers from mining rewards (seed rate … XNO/OBX, every …s)`

Sanity checks (Terminal B):

```bash
export RPC=http://127.0.0.1:18181
curl -s $RPC/status            | python3 -m json.tool   # height climbing
curl -s $RPC/offers/json       | python3 -m json.tool   # auto-liquidity offers appear after first mature reward
curl -s $RPC/auto-liquidity    | python3 -m json.tool   # loop status counters (handleAutoLiquidityStatus)
```

> **What needs REAL XNO:** only Scenarios **1a (XNO→OBX)** and **9 (live XNO leg)**, which spend
> from `XNO_ADDR` (≤ 0.00001). Everything else is **[FREE OBX]** or pure in-process tests with
> no network and no funds.

### 1.4 Full automated suite (run first, before any manual work)

```bash
cd /Users/mac/XMR_alternative
OBX_ALLOW_PROTOTYPE_POW=1 go test \
  ./pkg/swapbook/... \
  ./pkg/swapnet/... \
  ./pkg/swapsession/... \
  ./cmd/obscura-swap/... \
  ./tests/critical/swap/... \
  ./tests/critical/swapchain/... \
  ./tests/critical/swapd/...
```

The consensus/chain tests need `OBX_ALLOW_PROTOTYPE_POW=1` to keep class-group mining fast. All
of these should pass before you do manual exercises; manual steps then confirm the same code
paths against a live node/network.

---

## 2. SCENARIOS

Each scenario states: funds class, automated test, manual steps, expected result.

---

### Scenario 1 — Swap BOTH directions at 0.00001

The atomic swap is symmetric: OBX leg = 2-of-2 adaptor output, XNO leg = scriptless.
`runSelfTest` (`cmd/obscura-swap/main.go:275`) drives **both** directions XNO→OBX→XNO in-process
against `MockNano`, no funds, no network. `runLive` drives one real XNO leg.

#### 1a. XNO → OBX  **[REAL XNO — send ≤ 0.00001 from `XNO_ADDR`]**

This is the only scenario where you lock real XNO and receive OBX. It is the `runLive` path
(`cmd/obscura-swap/main.go:338`): the tool shows a joint Nano account, you send XNO to it, it
settles the OBX leg locally and sweeps the XNO back to `--xno-dest`.

```bash
OBX_ALLOW_PROTOTYPE_POW=1 ./bin/obscura-swap live \
  --nano-rpc rainstorm \
  --xno-dest $XNO_ADDR \
  --xno-amount-raw 10000000000000000000000 \
  --obx-amount 3
```

- `--xno-amount-raw 10000000000000000000000` = 0.00001 XNO in raw (1 XNO = 1e30 raw; 0.00001 XNO
  = 1e25 raw = `10000000000000000000000000`). **Double-check the raw value before sending** so you
  never exceed the 0.00001 cap. (If unsure, omit `--xno-amount-raw`; the panel then accepts "any
  amount (no minimum enforced — demo)" — but you still manually send only ≤ 0.00001.)
- The STATUS PANEL prints the **JOINT account** to send to and logs recovery half-keys
  `sA`/`sB` (`main.go:372`). **Send ≤ 0.00001 XNO from `XNO_ADDR` to the joint account.**
- Expected: `XNO lock … cemented. Settling OBX leg and sweeping…` then
  `LIVE SWAP COMPLETE ✓  Your XNO was swept back to <addr>`. Your XNO returns to `XNO_ADDR`
  (Nano is feeless), and the OBX leg settles 3 OBX locally.
- **Recovery:** if it crashes after you send, the joint XNO is recoverable from the logged
  `sA`+`sB`; keep the log. The dest secret is also logged if you let it generate a dest.

> Note the in-panel warning (`main.go` STATUS PANEL): this demo *plays both sides* and the XNO
> refund path is not wired in the legacy executor, which is why the cap is 0.00001 and why the
> joint half-keys are logged for manual recovery.

#### 1b. OBX → XNO  **[FREE — in-process MockNano, no funds]**

Covered both directions by the self-test. No real XNO needed because the XNO leg runs against
`swapd.MockNano`.

```bash
OBX_ALLOW_PROTOTYPE_POW=1 ./bin/obscura-swap selftest
```

- Expected: a full **XNO→OBX→XNO round trip** completes (both legs settle, secret revealed,
  sweep succeeds) and the command exits 0. This is the no-network proof of the orchestration.

#### 1c. Both directions, automated (consensus on-chain leg)

```bash
OBX_ALLOW_PROTOTYPE_POW=1 go test ./tests/critical/swap/...      -run 'TestEndToEndAtomicSwap|TestSwapRefundPath'
OBX_ALLOW_PROTOTYPE_POW=1 go test ./tests/critical/swapchain/... -run 'TestOnChainSwapClaimRevealsSecret|TestOnChainSwapRefund'
OBX_ALLOW_PROTOTYPE_POW=1 go test ./tests/critical/swapd/...     -run 'TestNanoSwapHappyPath'
```

Expected: all pass — `TestOnChainSwapClaimRevealsSecret` proves claiming the OBX output reveals
the secret that completes the XNO leg (atomicity).

---

### Scenario 2 — Liquidity ADD and REMOVE  **[FREE OBX]**

ADD = post offers (manually or via mining auto-liquidity). REMOVE = cancel offers.

#### 2a. ADD — post an offer over RPC

Build + sign an OBX→XNO offer under the OBX test seed, POST the hex to `/offer`
(`handlePostOffer`, `pkg/rpc/server.go:499`), confirm it is admitted.

Use a tiny harness (the wallet/CLI does not yet have a `post-offer` subcommand — **GAP**, see §3):

```bash
cat > /private/tmp/postoffer_test.go <<'EOF'
package main_harness
// run with: go test -run TestPostOffer ./... after placing in a throwaway pkg,
// OR inline via `go run`. Shown here for the exact symbols to call.
EOF
```

Recommended path — drive it directly through the library in a throwaway `_test.go` placed under
`pkg/swapbook` (so it links the package), calling:
`swapbook.BuildSignedOffer(...)` (`autoliquidity.go`) → serialize with `Offer.Serialize` →
`hex.EncodeToString` → `POST /offer {"offer":"<hex>"}`.

```bash
# After you have a hex-encoded signed offer in $OFFER_HEX:
curl -s -X POST $RPC/offer -d "{\"offer\":\"$OFFER_HEX\"}"
# expect: {"offer_id":"<hex>"}
curl -s $RPC/offers/json | python3 -m json.tool   # the offer now appears
```

- Automated equivalent: `go test ./pkg/swapbook -run 'TestSignVerifyRoundTrip|TestBuildSignedOfferAdmitted'`
- Expected: POST returns `{"offer_id":...}`; the offer shows in `/offers/json` (give OBX / get XNO,
  rate, expiry within 6h `MaxOfferTTL`).

#### 2b. ADD — auto-liquidity from mining

With the node from §1.3 running `--mine` (no `--no-auto-liquidity`):

```bash
watch -n2 "curl -s $RPC/offers/json | python3 -c 'import sys,json;print(len(json.load(sys.stdin)))'"
curl -s $RPC/auto-liquidity | python3 -m json.tool   # posted count, cap, last tick
```

- Automated: `go test ./pkg/swapbook -run 'TestBuildSignedOfferAdmitted|TestBuildSignedOfferTTLClamp'`
  (the building block `BuildSignedOffer` and its TTL clamp).
- Expected: after the first mature mining reward, OBX→XNO offers appear and are capped at
  `config.AutoLiquidityMaxOffers`; offers re-post when their (≤30-min) TTL nears expiry.
- **GAP:** the loop itself (`cmd/obscura-node/main.go:autoLiquidityLoop`, line ~306 — tick cadence,
  spendable-balance read, un-offered-balance computation, cap enforcement, re-post-on-expiry,
  human→atomic rate) has **no automated test**; only `BuildSignedOffer` is unit-tested. This
  manual watch is currently the only end-to-end coverage. Recommend adding a loop-level test.

#### 2c. REMOVE — cancel an offer

Cancel is maker-signed (`Book.SignCancel` / `Book.Cancel`, `match.go`) with a domain-separated
`CancelMessage` (`cancelDomain`), distinct from the offer encoding.

- Automated: `go test ./pkg/swapbook -run 'TestCancelValid|TestCancelForged|TestCancelMessageDistinctFromOffer'`
- Manual (library harness, since there is no RPC route): call `book.SignCancel(offerID)` under the
  maker secret, then `book.Cancel(msg)`; assert `book.List()` no longer contains the offer. Then
  flip one byte of the signature and assert `Cancel` returns an error (forged rejected).
- Expected: valid signed cancel removes the offer; forged cancel rejected; a cancel message is
  never accepted as an offer and vice-versa.
- **GAP:** there is **no `/cancel` HTTP route** — `pkg/rpc/server.go` exposes only `/offers`,
  `/offers/json`, `/offer`. A maker cannot cancel over RPC. Cancellation is also best-effort
  local-only: an offer already gossiped lives in peers' books until it expires (no global
  revocation). Flag both. The practical "remove" over the network is **expiry** (Scenario 3g).

---

### Scenario 3 — Orders: types, post, view, cancel, partial fills, depth, caps, expiry  **[FREE OBX]**

The only order type that exists is the limit-style **give/get `Offer`** (`swapbook.go`). There is
**no native market-order type**; "market" taking is emulated by `Book.Quote` walking the book
best-rate-first with partial fill. There is no maker-side partial state; partials are computed
taker-side in `Quote`.

#### 3a. Post + view

```bash
curl -s $RPC/offers      | head -c 200    # handleOffers — hex (length-prefixed injective encoding)
curl -s $RPC/offers/json | python3 -m json.tool   # handleOffersJSON — decoded view
```
- Automated: `go test ./pkg/swapbook -run 'TestSerializeRoundTrip|TestCoreInjective'`
- Expected: every posted offer round-trips byte-identically; `/offers/json` lists give/get assets,
  amounts, rate, maker, expiry, id.

#### 3b. Limit give/get + validity gate

- Automated: `go test ./pkg/swapbook -run 'TestSignVerifyRoundTrip|TestVerifyRejectsBadAssets|TestValidAsset'`
- Manual: POST an offer whose `Expiry` is in the past → `400 rejected: …` (Verify enforces
  `Expiry ∈ (now, now+MaxOfferTTL=6h]`); POST an offer with a non-settleable asset → rejected
  (`asset not settleable`).

#### 3c. Market-style taking via Quote (full fill)

`Book.Quote(give, get, size)` (`match.go`) returns `filled, getOut, vwap, offersUsed, full`.

- Automated: `go test ./pkg/swapbook -run 'TestQuoteFullFill|TestQuoteBestRateFirst|TestQuoteNoMatch'`
- Manual (library harness): post a ladder of offers, call `book.Quote` for a size that the book
  can fully cover; assert `full==true`, `vwap` matches best-rate-first consumption, `getOut`
  correct.
- **GAP:** `Quote`/`Depth` are **library-only — no `/quote` or `/depth` RPC route**. A taker
  hitting RPC gets only `bestPrices` (top-of-book) from the explorer summary; the wallet UI
  reconstructs depth client-side (`website/wallet.html:depthBars`, line ~318) from `/offers/json`.
  Cover Quote/Depth via in-package harness; recommend adding `/quote`+`/depth` routes.

#### 3d. Partial fills + floor rounding

- Automated: `go test ./pkg/swapbook -run 'TestQuotePartialFill|TestQuoteFloorRounding'`
- Manual: call `Quote` for a size larger than total book depth; assert `full==false` and the
  returned `getOut` uses floor rounding (`mulDivFloor`/`mul64`/`div128by64`) — never rounds up in
  the taker's favor.
- Expected: partial fill reports the achievable `filled`/`getOut` with `full=false`.

#### 3e. Depth ladder

- Automated: `go test ./pkg/swapbook -run TestDepthLadder`
- Manual: `book.Depth(give,get)` returns a cumulative `DepthLevel` ladder; assert cumulative sums
  are monotonic and rate-sorted. Cross-check visually in `wallet.html` depth bars.

#### 3f. Per-maker cap + global book cap

- Automated: `go test ./pkg/swapbook -run 'TestMakerCapEnforced|TestMaxBookSizeEnforced|TestDuplicateIDRejected'`
- Manual: from one maker key, POST 65 distinct offers; the **65th is rejected** (`MaxOffersPerMaker=64`).
  Re-POST an already-admitted offer id → rejected (dedup). `MaxBookSize=50000` is the global cap.
- Expected: 64 admitted, 65th `400`; duplicate id `400`.

#### 3g. Expiry / prune (the real "remove over the network")

- Automated: `go test ./pkg/swapbook -run 'TestExpiryExclusion|TestExpiryExcludedFromQuote'`
- Manual: POST an offer with a short TTL (e.g. Expiry = now+60s). Poll `/offers/json`; after expiry
  + a `Book.PruneExpired` tick it disappears, and `Quote`/`Depth` exclude it even before pruning.
- Expected: expired offers vanish from the book and are never matched.

---

### Scenario 4 — Two-party P2P swap over `pkg/swapnet`  **[FREE — in-process P2P]**

`Coordinator.Take` mints an unguessable `SwapID` (`commit.RandomScalar`); `Deliver` routes inbound
(`Init`→`startMaker`); `driveTaker`/`driveMaker` run the legs; `makerSweep`/`sweepFromChain` do the
independent chain-scrape sweep; `register`/`recv` enforce caps and per-phase timeouts.

#### 4a. Happy path taker→maker over the wire

```bash
OBX_ALLOW_PROTOTYPE_POW=1 go test ./pkg/swapnet/... -run TestTwoNodeSwapOverP2P -v
```
- Expected: swap completes; the test asserts XNO lands at the maker dest, the taker's OBX grows,
  the two parties' secret shares stay isolated, and the P2P message counts match the protocol.

#### 4b. Reverse direction (OBX→XNO, roles flipped)

- In-process both directions: `./bin/obscura-swap selftest` (Scenario 1b) runs XNO→OBX→XNO.
- **GAP:** the **wire-level reverse direction is not separately tested** — `TestTwoNodeSwapOverP2P`
  drives a single taker→maker direction; the reverse is the symmetric run of the same coordinators
  but has no dedicated wire test. Recommend a second `swapnet` test for the flipped direction.

#### 4c. Session caps + deny-by-default funding (DoS)

```bash
OBX_ALLOW_PROTOTYPE_POW=1 go test ./pkg/swapnet/... -run 'TestSessionCapRejected|TestAcceptInitGatesMakerFunding' -v
```
- Expected: per-peer `Init` flood past `SwapMaxSessionsPerPeer=8` (and global `SwapMaxSessions=256`)
  is rejected; an `Init` to a maker with a nil `AcceptInit` predicate funds **nothing** (deny by
  default — F-A).
- **GAP:** the production `AcceptInit` must bind an `Init` to a **live published offer**
  (offer-book binding). Tests use a stand-in `acceptOffer` predicate matching only amounts, so full
  offer-binding is untested. Also fee is not negotiated in-band (`Config.Fee` is fixed out-of-band;
  see `coordinator.go` comment) — no fee-mismatch test beyond amount matching.

#### 4d. Counterparty binding (third-peer injection)

```bash
OBX_ALLOW_PROTOTYPE_POW=1 go test ./pkg/swapnet/... -run TestDeliverDropsNonCounterpartyEnvelope -v
```
- Expected (F-C): a `KindAbort` injected by a third peer into a live `SwapID` is dropped; the swap
  between the two real counterparties still completes.

---

### Scenario 5 — Failure paths  **[FREE — in-process]**

#### 5a. Refund / abort / timeout

```bash
OBX_ALLOW_PROTOTYPE_POW=1 go test ./pkg/swapsession/... -run TestAbortMakerRefunds -v
OBX_ALLOW_PROTOTYPE_POW=1 go test ./pkg/swapnet/...     -run TestAbortMakerRefunds -v
OBX_ALLOW_PROTOTYPE_POW=1 go test ./cmd/obscura-swap/... -run TestSwapRefundOnPreClaimFailure -v
```
- Expected: when the counterparty aborts (or fails before XNO is locked), the maker reaches the
  refunded phase and reclaims OBX after the timelock; the legacy executor's `refundOnFail`
  reclaims with sentinel `"refunded to the funder"` (pre-mine extract check #12).
- Manual: in `obscura-swap selftest`, the abort branches are exercised in-process. (A live abort
  test would need the §1.3 node + a killed counterparty before `XNOLocked`.)

#### 5b. F-1 unclaimable-window rejection

A swap whose unlock window cannot be safely claimed must be rejected before funding:
`UnlockHeight` must leave room for `SwapReorgMargin` + a claim window of at least `SwapMinClaimWindow`
(window `>= margin + minWindow`).

```bash
OBX_ALLOW_PROTOTYPE_POW=1 go test ./pkg/swapsession/...    -run TestRejectUnclaimableUnlockWindow -v
OBX_ALLOW_PROTOTYPE_POW=1 go test ./tests/critical/swapchain/... -run TestOnChainSwapRefund -v
OBX_ALLOW_PROTOTYPE_POW=1 go test ./tests/critical/swap/...      -run TestSwapReorgMarginDeadZone -v
```
- Expected: an `Init`/`FundSwap` with `unlock = height+1` (too tight) is **rejected** — the taker
  refuses to lock XNO against a swap it could never claim; the on-chain consensus fund check also
  enforces `UnlockHeight >= fundHeight + SwapReorgMargin`. `TestSwapReorgMarginDeadZone` proves the
  claim and refund spend regions are disjoint across the `[U-M, U)` dead zone.

#### 5c. F-B maker-independent extraction (taker withholds)

The maker must be able to sweep even if the taker withholds or corrupts the `ClaimDone` message,
by scraping the chain and extracting `sA = S_full - ŝa - ŝb` independently.

```bash
OBX_ALLOW_PROTOTYPE_POW=1 go test ./pkg/swapnet/...     -run 'TestMakerSweepsViaChainScrape|TestMakerSweepsWithoutAnyClaimDone' -v
OBX_ALLOW_PROTOTYPE_POW=1 go test ./pkg/swapsession/... -run TestMakerExtractsSAIndependently -v
```
- Expected: the taker claims the OBX (revealing the secret on-chain) then withholds/corrupts
  `ClaimDone`; the maker still detects the claim via `sweepFromChain`, extracts the secret, and
  sweeps the XNO. No counterparty cooperation required at the end (griefing-resistant).

#### 5d. Nonce-reuse rejection (co-sign safety + on-chain ClaimR uniqueness)

```bash
# Off-chain: maker refuses a second distinct co-sign / forged taker half; nonce guard survives reload.
OBX_ALLOW_PROTOTYPE_POW=1 go test ./pkg/swapsession/... \
  -run 'TestCoSignRejectsForgedTakerHalf|TestMakerRefusesSecondDistinctCoSign|TestNonceGuardSurvivesReload' -v
# On-chain consensus: swap-nonce (ClaimR) uniqueness across blocks, in-block, and across reorg.
OBX_ALLOW_PROTOTYPE_POW=1 go test ./tests/critical/swapchain/... \
  -run 'TestNonceUniqFreshValidatesReuseRejected|TestNonceUniqReuseInSameBlockRejected|TestNonceUniqReorgSafe' -v
```
- Expected: a forged taker half is rejected; the maker refuses to co-sign a **second distinct**
  message under the same nonce (the classic nonce-reuse key-leak guard), and that guard survives a
  process reload; on-chain, a reused swap nonce is rejected cross-block and in-block, and the
  uniqueness set is correctly restored across a reorg.

#### 5e. Leg-ordering + amount/account rejections

```bash
OBX_ALLOW_PROTOTYPE_POW=1 go test ./pkg/swapsession/... \
  -run 'TestMakerFundsFirstOrderingEnforced|TestRejectWrongXNOAccount|TestRejectWrongXNOAmount|TestRejectUnderfundedOBX' -v
```
- Expected: maker-funds-first ordering is enforced; wrong XNO destination account, wrong XNO amount,
  and an underfunded OBX leg are all rejected before value moves.

---

### Scenario 5b — Confirm BTC is REJECTED (disabled)  **[FREE]**

`config.SettleableAssets={OBX,XNO}`; `Offer.Verify` rejects any BTC side, and `Quote`/`Depth`
exclude BTC. The BTC HTLC code is kept and unit-tested but disabled at the offer layer.

```bash
OBX_ALLOW_PROTOTYPE_POW=1 go test ./pkg/swapbook/...      -run 'TestBTCOfferRejected|TestBTCExcludedFromQuoteAndDepth' -v
OBX_ALLOW_PROTOTYPE_POW=1 go test ./tests/critical/swapd/... -run 'TestBtcSwapHappyPath|TestBtcSwapRefund|TestBtcHTLCRules|TestBtcHTLCScript' -v
```
- Manual: build + sign a BTC-side offer and POST to `/offer` → `400 rejected: asset not settleable`.
  Confirm it never appears in `/offers/json`, `Quote`, or `Depth`.
- Expected: BTC offers rejected at the order book; BTC HTLC primitive tests still pass (code kept,
  offer path closed).

---

### Scenario 6 — Explorer: price chart, orders table, history table  **[FREE OBX]**

The explorer summary surfaces `bestPrices` (`pkg/rpc/explorer.go:bestPrices`, line ~284) →
`PairPrice` per pair, rendered as PRICE cards in `website/explorer.html`
(`priceCards`/`humanPrice`, lines ~243-299). The wallet renders client-side depth bars
(`website/wallet.html:depthBars`, line ~318). The Vercel proxy `website/api/explorer.js` forwards
`/offers` + `/offers/json`.

#### 6a. Price cards update from the live book

1. With the §1.3 node running, POST a few OBX→XNO offers (Scenario 2a / auto-liquidity).
2. Fetch the explorer summary and confirm prices:
   ```bash
   curl -s $RPC/explorer/summary | python3 -c "import sys,json;d=json.load(sys.stdin);print(d.get('prices'))"
   ```
3. Open `website/explorer.html` pointed at the node (or via the Vercel proxy). Confirm a PRICE
   card renders per pair with `humanPrice(rate, give, get)` conversion and the offer count.
- Expected: each pair with offers shows a price card; price tracks top-of-book (`Book.Best`). Post a
  better-rate offer → the card's price moves to the new best.

#### 6b. Orders table (offers list)

- The orders/offers view reads `/offers/json`. Post + cancel/expire offers (Scenarios 2–3) and
  confirm rows appear and drop. In `wallet.html`, select a pair and confirm `depthBars` renders the
  cumulative-give ladder (`#swapDepth`).
- Expected: orders table reflects `/offers/json` 1:1; depth bars match the `Book.Depth` ladder.

#### 6c. History table

- **GAP / clarification:** the order book is an off-chain RFQ board with no persistent fill history;
  the explorer's "history" is the **anonymity-set / supply history sparkline** (`explorer.html`
  ~line 277), not a swap-fill log. Completed *on-chain* swap spends are observable via
  `chain.FindSwapSpend` and block inspection (`/explorer/block`), not via a DEX trade-history table.
- Expected: confirm the explorer history sparkline updates with height; confirm settled swaps are
  visible as on-chain spends in block detail. (If a DEX trade-history table is desired, it does not
  exist yet — record as a feature gap.)

---

## 3. KNOWN GAPS (do not hunt for these — they are confirmed missing)

1. **No `/quote` or `/depth` RPC route.** `Book.Quote`/`Book.Depth` (`pkg/swapbook/match.go`) are
   library-only. RPC serves only `bestPrices` (top-of-book); the wallet reconstructs depth
   client-side (`website/wallet.html:depthBars`). A taker cannot get a depth-aware quote over RPC.
   → Cover via in-package harness (Scenarios 3c-3e); recommend adding the routes.
2. **No `/cancel` RPC route.** `Book.Cancel`/`SignCancel` are tested in-package but
   `pkg/rpc/server.go` exposes only `/offers`, `/offers/json`, `/offer`. Cancellation is library-
   only and best-effort local (gossiped offers persist on peers until expiry — no global
   revocation).
3. **`autoLiquidityLoop` (`cmd/obscura-node/main.go:~306`) has no automated test** — only its block
   `BuildSignedOffer` is unit-tested. Loop cadence, balance read, un-offered-balance computation,
   `AutoLiquidityMaxOffers` cap, re-post-on-expiry, human→atomic rate are untested end-to-end.
4. **No explicit bad-PoW / bad-signature / wrong-Schnorr negative test for `Offer.Verify`.** It is
   exercised only indirectly via valid round-trips and bad-asset cases. → Add a test mutating
   `Nonce`/`Sig` and asserting `Book.Add` fails.
5. **`runLive` (`obscura-swap live`) has no automated test** — it is a manual LIVE GATE needing a
   real Nano node (Scenario 1a / 9). The real-network XNO sweep is only exercised by hand.
6. **Reverse-direction wire-level two-party swap is untested** — only `runSelfTest` covers reverse
   in-process; `TestTwoNodeSwapOverP2P` drives a single taker→maker direction over the wire.
7. **`AcceptInit` offer-book binding is not implemented in tests** — tests use a stand-in
   amount-matching predicate; full Init↔published-offer binding is untested. Fee is also not
   negotiated in-band (no fee-mismatch test).
8. **No DEX trade-history table** — the order book keeps no persistent fill history; explorer
   "history" is the anon-set/supply sparkline. Settled swaps are visible only as on-chain spends
   (`chain.FindSwapSpend`).
9. **No multi-node book-convergence integration test** — cross-peer offer gossip and per-node book
   divergence (a cancelled/expired offer still live on a peer) is an inherent limitation with no
   integration test.

---

## 4. Funds summary (what costs real money)

| Scenario | Funds | Cap / notes |
|---|---|---|
| 1a XNO→OBX (`obscura-swap live`) | **REAL XNO** | send ≤ **0.00001** from `XNO_ADDR`; recover via logged `sA`+`sB` |
| 9 / live XNO leg (same path, real Nano node) | **REAL XNO** | ≤ **0.00001**; LIVE GATE for the from-scratch Nano signer |
| 1b selftest, 1c, 2, 3, 4, 5, 5b, 6 | **FREE** | in-process MockNano or free test-chain OBX (value-less) |

Everything that is not Scenario **1a** runs with **no real value** — the OBX test chain is
test-only, so genesis resets and consensus changes are free. Only run a single 1a/9 send and
confirm the sweep returns the 0.00001 XNO before repeating.

---

## 5. One-shot regression command

```bash
cd /Users/mac/XMR_alternative
OBX_ALLOW_PROTOTYPE_POW=1 go test \
  ./pkg/swapbook/... ./pkg/swapnet/... ./pkg/swapsession/... \
  ./cmd/obscura-swap/... \
  ./tests/critical/swap/... ./tests/critical/swapchain/... ./tests/critical/swapd/... \
  && OBX_ALLOW_PROTOTYPE_POW=1 ./bin/obscura-swap selftest \
  && echo "DEX regression GREEN (manual REAL-XNO scenario 1a still requires a hand-sent 0.00001 XNO)"
```
