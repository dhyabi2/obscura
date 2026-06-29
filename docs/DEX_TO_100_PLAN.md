# Obscura Cross-Chain DEX — Roadmap to 100/100

**STATUS: PENDING IMPLEMENTATION** — {{DATE}}

This document is the consolidated plan for taking the Obscura cross-chain atomic-swap DEX from its current per-factor scores to 100/100 across all seven evaluated factors. Each factor below carries its current score, the concrete gaps blocking 100, a numbered step-by-step plan (with per-step effort and the files each step touches), and the deliverables that close it. The end of the document sequences the work across factors and defines what "done" means.

Effort legend: **S** = small, **M** = medium, **L** = large, **XL** = extra-large.

---

## Scorecard

| Factor | Score/100 | Effort-to-100 |
|---|---|---|
| Price discovery & rate accuracy (cross-chain atomic-swap order book) | 22 | XL |
| Trustless atomic settlement (cross-chain DEX) | 52 | XL |
| Asset & chain coverage | 38 | XL |
| Refund & failure recovery (DEX / cross-chain atomic swaps) | 24 | XL |
| Two-party P2P protocol completeness | 12 | XL |
| UX/UI & onboarding ease (DEX / cross-chain atomic swap) | 28 | XL |
| Observability & transparency | 42 | XL |
| Security & MEV/front-running resistance (DEX / cross-chain atomic swaps) | 42 | XL |

**Average score: 32.5 / 100** (8 factors).

**Overall maturity verdict:** A cryptographically sound prototype whose atomic-swap guarantees are proven only in single-process self-tests — the DEX can post offers and (in one process) complete the adaptor/HTLC math, but it cannot yet *take* an offer between two distrusting nodes, settle against real BTC/XMR chains, recover from failures, or present accurate prices; it is **not production-ready** and every factor needs XL work, gated on building a genuine two-party P2P swap session as the shared backbone.

> Note: the scorecard lists 8 rows; the headline factor count quoted in the brief was "all factors" — the average above is computed over all 8 scored rows.

---

## Factor 1 — Price discovery & rate accuracy (cross-chain atomic-swap order book)

**Current score: 22/100** — **Effort to 100: XL**

### Gaps

- OBX decimal scale is split-brained: `wallet.html:303 DEC.OBX=8`, `explorer.html:242 DEC.OBX=8`, and `config.AutoLiquidityDecimals OBX:8 (params.go:214)` are mutually consistent BUT do not match on-chain `AtomicPerCoin=1e12 (params.go:53)`; the OBX leg of a swap is funded in 1e12 atomic, so any path bridging an offer amount to a settled OBX amount (`offerOBXAtomic` in `cmd/obscura-node/main.go:414` multiplies by 10^(12-8)) silently rescales by 1e4. No single shared source of truth.
- Rate is display-only at settlement: `doAtomicSwap`/`runLive` in `cmd/obscura-swap/main.go` price the OBX leg with a hardcoded `obxAmt=3*AtomicPerCoin (main.go:374)` and read `--xno-amount-raw` (flagSet, `main.go:613`) but NEVER read `Book.Best` or any `Offer`; there is no take/accept that locks a maker's get/give into the swap amounts.
- `Offer.GiveAmount/GetAmount` are uint64 (`swapbook.go:60-61`); 1 XNO = 1e30 raw (`nanorpc.go`), so whole-XNO amounts overflow by ~11 orders of magnitude — XNO offers are forced into a coarse 1e30-scaled human unit (`AutoLiquidityDecimals XNO:30`) and truncated.
- Best ratio is float64 (`swapbook.go:296-305`); `handleOffersJSON` emits `fmt.Sprintf("%g", float64/float64)` (`server.go:474`) and sorts by `ParseFloat` (`server.go:488`); `bestPrices` uses float64 ratio (`explorer.go:302-308`). Float on near-uint64 atomic amounts loses precision and makes ordering non-deterministic.
- No mid/spread, no depth-weighted or multi-offer aggregated fill price, no slippage tolerance, no min/partial fill. `Book.Best` returns exactly one offer (`swapbook.go:293`); `depthBars (wallet.html:316)` is a cosmetic cumulative-give bar, not a fillable curve.
- No stale-price fallback: offers vanish at Expiry via `pruneLocked (swapbook.go:269)`; empty book yields literal `'no market yet' (wallet.html:350, explorer.html priceCards)` with no last-trade/TWAP reference.
- Weak book-level manipulation resistance: `OfferPoWBits=12 (swapbook.go:25)` is trivially grindable, no per-maker minimum size / reputation / fill-rate gating, so the displayed best is spoofable by flooding fake-cheap offers.
- No cross-pair / triangular sanity check across OBX/XNO, OBX/BTC, XNO/BTC to flag mispriced or manipulative offers.

### Plan to 100

1. **Single source of truth for asset decimals; fix OBX scale** — *Effort: M*
   Add an exported map `AssetDecimals` (or a typed Asset registry) in `pkg/config/params.go` as the ONE authority: OBX must equal `log10(AtomicPerCoin)=12` (NOT 8), XNO:30, BTC:8. Repoint `config.AutoLiquidityDecimals` to read from it (drop the standalone OBX:8 at `params.go:214`) and update `offerOBXAtomic (cmd/obscura-node/main.go:414)` so `12-DEC.OBX==0` (no rescale). Serve the map to the frontend via a new RPC field on `/summary` (`explorer.go`) or a tiny `/assets/json` handler so `wallet.html:303` and `explorer.html:242` stop hardcoding `DEC={OBX:8,...}` and instead fetch it. Add a Go unit test asserting `AssetDecimals["OBX"]==log10(AtomicPerCoin)` so the scales can never drift again.
   *Files:* `pkg/config/params.go:52`, `pkg/config/params.go:214`, `cmd/obscura-node/main.go:409`, `cmd/obscura-node/main.go:414`, `pkg/rpc/explorer.go`, `pkg/rpc/server.go`, `website/wallet.html:303`, `website/explorer.html:242`

2. **Switch Offer amounts to arbitrary-precision decimal strings** — *Effort: L*
   Change `Offer.GiveAmount/GetAmount` from uint64 to `*big.Int (swapbook.go:60-61)`, with canonical decimal-string serialization. Update `Core()/Serialize()/ParseOffer (swapbook.go:75-239)` to length-prefix the decimal string of each amount (keeps the injective signed encoding the audit added; sign the canonical minimal-form string, reject leading zeros/empty/non-digit). Update Verify `amount!=0` checks, `BuildSignedOffer (autoliquidity.go:25)`, the wasm builder `obxBuildOffer (cmd/obscura-wasm/main.go:218)` to accept big decimal strings, and `OfferJSON` (give_amount/get_amount already strings on the wire — keep). This lets a whole-XNO offer carry full 1e30 raw precision and removes the coarse human-unit workaround.
   *Files:* `pkg/swapbook/swapbook.go:56`, `pkg/swapbook/swapbook.go:75`, `pkg/swapbook/swapbook.go:131`, `pkg/swapbook/swapbook.go:158`, `pkg/swapbook/swapbook.go:183`, `pkg/swapbook/autoliquidity.go:25`, `cmd/obscura-wasm/main.go:218`, `pkg/rpc/server.go:480`

3. **Exact integer rate comparison and rendering (kill float64)** — *Effort: M*
   Rewrite `Book.Best (swapbook.go:293-309)` to compare offers by cross-multiplication of big.Ints (`offerA.GetAmount*offerB.GiveAmount` vs `offerB.GetAmount*offerA.GiveAmount`) with a deterministic tiebreak on ID — no float. Change `handleOffersJSON (server.go:471-491)` to emit Rate as the exact reduced fraction string `"get/give"` (gcd-reduced) and sort by big.Int cross-multiply instead of ParseFloat. Change `bestPrices (explorer.go:289-320)` to track best via cross-multiply and already emit the `"get/give"` fraction (good). Float conversion happens ONLY in the browser at the final display step, using the now-correct DEC.
   *Files:* `pkg/swapbook/swapbook.go:293`, `pkg/rpc/server.go:461`, `pkg/rpc/explorer.go:284`

4. **Add a real take/accept flow that locks a maker's rate into settlement** — *Effort: XL*
   Add `Book.Quote(takerGives,takerWants,wantAmount)` returning the offers that fill it and the exact aggregated get/give (walking offers best-first until wantAmount is met — this also yields depth-weighted price). Add an executor mode to `cmd/obscura-swap`: `'take --pair OBX/XNO --amount N [--max-rate R]'` that fetches `/offers/json`, runs the quote, and derives `obxAmt` and the XNO raw amount FROM the chosen offer(s) instead of the hardcoded `obxAmt=3*AtomicPerCoin (main.go:374)` and `--xno-amount-raw (main.go:613)`. Persist the locked maker id+rate so `doAtomicSwap (main.go:150)` prices both legs from the quote. This is the structural fix that turns price discovery from a display number into settlement input.
   *Files:* `pkg/swapbook/swapbook.go:293`, `cmd/obscura-swap/main.go:150`, `cmd/obscura-swap/main.go:282`, `cmd/obscura-swap/main.go:374`, `cmd/obscura-swap/main.go:596`

5. **Depth-weighted aggregated price, mid/spread, slippage + partial/min fill** — *Effort: L*
   Build on `Book.Quote`: expose a `/depth/json` handler (`pkg/rpc/server.go`) returning the fillable curve (cumulative give vs marginal get/give) for a pair, plus best-bid/best-ask (both directions of the pair), mid = geometric mean of the two best, and spread. Add MinFill/PartialFill semantics to Offer (a maker-set minimum take size, signed into Core) and a taker `--max-rate / --max-slippage` guard in the take flow (step 4) that aborts if the aggregated quote crosses the bound. Replace the cosmetic `depthBars (wallet.html:316)` with a render of the real `/depth` curve and show mid/spread in the market banner (`wallet.html:374`, renderSwap).
   *Files:* `pkg/swapbook/swapbook.go:56`, `pkg/rpc/server.go:461`, `cmd/obscura-swap/main.go:150`, `website/wallet.html:316`, `website/wallet.html:374`

6. **Stale-price fallback: last-trade + TWAP reference** — *Effort: M*
   Add a small in-process TradeLog to `pkg/swapbook` (ring buffer of completed takes: pair, exact get/give fraction, timestamp, size) appended when the take flow (step 4) settles a swap. Compute a size-weighted TWAP over a window and expose last-trade + TWAP per pair via the new `/depth/json`. When the live book is empty, the wallet/explorer banner (`wallet.html:350`, `explorer.html priceCards`) shows `'last trade / TWAP (indicative, no live offers)'` instead of bare `'no market'`. Mark clearly as indicative, never used to auto-price a take.
   *Files:* `pkg/swapbook/swapbook.go:241`, `pkg/rpc/server.go:461`, `pkg/rpc/explorer.go:284`, `website/wallet.html:350`, `website/explorer.html`

7. **Book-level manipulation resistance: cost + per-maker gating** — *Effort: L*
   Raise `OfferPoWBits (swapbook.go:25)` to a difficulty that costs meaningful CPU per offer and/or scale required PoW bits with how far an offer's rate deviates from the current mid (cheap to post near-market, expensive to post an outlier that moves the best). Add per-maker controls in `Book.Add (swapbook.go:251)`: a minimum GiveAmount per asset (reject dust offers that only exist to skew best), a cap on simultaneous live offers per Maker pubkey (reuse `MakerOffers`, `autoliquidity.go:43`), and an optional fill-rate/reputation weight (makers with completed trades from the TradeLog rank ahead of unproven ones in Best/Quote ordering). Make Best/Quote ignore offers below min-size so the displayed best can't be spoofed by a fake-cheap dust offer.
   *Files:* `pkg/swapbook/swapbook.go:22`, `pkg/swapbook/swapbook.go:251`, `pkg/swapbook/swapbook.go:293`, `pkg/swapbook/autoliquidity.go:43`

8. **Cross-pair / triangular consistency check** — *Effort: M*
   Add a sanity pass over the three pairs (OBX/XNO, OBX/BTC, XNO/BTC) computing the implied triangular rate (`OBX/XNO * XNO/BTC` vs `OBX/BTC`) using exact big.Int fractions and the corrected decimals. Flag offers whose implied price deviates beyond a configurable band as `'suspect'` in `/offers/json` (a boolean field) and de-prioritize/exclude them from Best/Quote; surface the flag in the wallet offer table (`wallet.html:396` row render). This catches both honest mispricing and manipulation that only shows up across pairs.
   *Files:* `pkg/rpc/explorer.go:284`, `pkg/swapbook/swapbook.go:293`, `pkg/rpc/server.go:461`, `website/wallet.html:396`

9. **End-to-end tests for accuracy and ordering** — *Effort: M*
   Extend `swapbook_test.go`: big.Int amount round-trip (sign/serialize/parse of a 1e30 XNO amount), exact cross-multiply ordering vs the old float (construct two offers that float would mis-order), Quote depth aggregation correctness, min-size/PoW spoof rejection, and triangular-flag detection. Add a Go test asserting the take flow in `cmd/obscura-swap` derives obxAmt from a quoted offer (selftest path) rather than the hardcoded constant. Add a tiny JS assertion harness or doc note verifying DEC is fetched, not hardcoded, in `wallet.html/explorer.html`.
   *Files:* `pkg/swapbook/swapbook_test.go`, `cmd/obscura-swap/main.go:219`, `pkg/rpc/explorer.go`

### Deliverables

- `pkg/config/params.go`: exported `AssetDecimals` authority with OBX=12 (==log10(AtomicPerCoin)), XNO=30, BTC=8; AutoLiquidityDecimals derived from it; a drift-guard unit test
- `Offer.GiveAmount/GetAmount` as `*big.Int` with canonical length-prefixed decimal-string signing/serialization across `swapbook.go`, `autoliquidity.go`, `cmd/obscura-wasm/main.go`
- Exact integer (cross-multiplied, gcd-reduced fraction) rate in `Book.Best`, `handleOffersJSON`, and `bestPrices` — no float64 in pricing/ordering paths
- `Book.Quote` + a take/accept executor mode (`cmd/obscura-swap 'take'`) that reads the live book and prices BOTH swap legs from the chosen offer(s), replacing the hardcoded `obxAmt` and `--xno-amount-raw`
- `/depth/json` handler exposing fillable depth curve, best-bid/ask, mid, spread; real depth render replacing cosmetic depthBars; taker `--max-rate/--max-slippage` guard; maker min/partial-fill
- swapbook TradeLog with last-trade + size-weighted TWAP, surfaced as clearly-indicative fallback when the book is empty
- Hardened `Book.Add`: higher/deviation-scaled PoW, per-maker min-size + offer-count caps, fill-rate/reputation ranking; dust/spoof offers excluded from Best/Quote
- Triangular cross-pair consistency check flagging suspect offers in `/offers/json` and the wallet UI
- Frontend (`wallet.html`, `explorer.html`) fetching decimals from the node instead of hardcoding DEC; market banner showing mid/spread and indicative-when-empty
- Expanded `swapbook_test.go` + executor test covering big.Int round-trip, exact ordering, quote aggregation, spoof rejection, triangular flagging, and quote-driven settlement

---

## Factor 2 — Trustless atomic settlement (cross-chain DEX)

**Current score: 52/100** — **Effort to 100: XL**

### Gaps

- Settlement is single-process: `cmd/obscura-swap/main.go newSecrets()` generates sA,sB,a,b in ONE actor and doAtomicSwap plays funder AND claimer locally (logs sB for self-recovery) - true half-secret two-party atomicity is never exercised.
- No P2P take/accept/settle protocol: `pkg/p2p` only gossips offers (`msgSwapOffer/msgGetOffers`, `p2p.go:36-37,536-554`); no message type to take an offer, exchange adaptor points/nonces (R,T), proofs-of-possession, or coordinate funding.
- BTC leg is mock-only: `BitcoinClient (swapd/bitcoin.go:19)` is implemented solely by MockBitcoin; no bitcoind/Electrum P2WSH funding/redeem/refund against a real chain (only a comment at `bitcoin.go:17` references a production build).
- Cross-chain timelock ordering unenforced: OBX UnlockHeight is a hand-picked constant (`main.go:156 c.Height()+200`) and BTC CLTV is hand-picked in tests; the BTC-CLTV > OBX-UnlockHeight relationship and claim-the-slow-chain-first ordering are validated nowhere.
- uint64 XNO amount cap: `NanoClient.Lock` takes uint64 (`nano.go:26`, `nanorpc.go:151-155`) but XNO raw is 128-bit; mainnet-sized amounts overflow the interface.
- No confirmation-depth / reorg gating in the executor before settling OBX: `waitForReceivable (main.go:583)` returns on first receivable seen; Confirmed (`nano.go:29`) and Confirmed (`bitcoin.go:26`) exist on the interfaces but are never called in doAtomicSwap.
- No monitoring/abort/punishment for the live counterparty race: if a counterparty claims at the last block before refund there is no race handling - doAtomicSwap is a straight-line happy path.

### Plan to 100

1. **Define a two-party swap session state machine (no merged secrets)** — *Effort: L*
   Create `pkg/swapsession` with Maker and Taker roles that each hold ONLY their own half-secrets. Maker holds (sB_share or sA depending on direction, b, rb); Taker holds (a, ra) and never sees the counterparty scalar. Refactor the body of `doAtomicSwap (main.go:150-215)` into role-scoped methods: `Maker.BuildFundOBX` (wraps `funder.FundSwap` with `K=AggregateKey(pt(a),pt(b))` using ONLY the peer's point `pt(a)` received over the wire, not the scalar), `Taker.BuildClaim` (calls `swap.CoSignClaim` + `commit.Adapt` with Taker's own a and the bridge secret), and `Extractor.Recover` (`commit.Extract` then Add the local half). Sessions must compile such that no single struct ever holds both sA and sB. Add a swapsession unit test that runs Maker and Taker in two goroutines passing ONLY wire messages through a channel, proving atomic completion with split secrets - the missing real-trustlessness proof.
   *Files:* `pkg/swapsession/session.go`, `pkg/swapsession/session_test.go`, `pkg/swap/swap.go` (reuse AggregateKeyVerified, CoSignClaim, ProvePossession), `pkg/commit/adaptor.go` (PreSign/PreVerify/Adapt/Extract)

2. **Add P2P take/accept/settle message types and handlers** — *Effort: L*
   Extend `pkg/p2p/p2p.go` message-type block (currently ends at `msgGetOffers=11`, `p2p.go:36-37`) with `msgSwapTake=12, msgSwapAccept=13, msgSwapNonce=14, msgSwapFunded=15, msgSwapAbort=16`. Each carries a swapsession wire message (offer ID, taker pubkey, adaptor point `T=pt(sA).Bytes()`, aggregate nonce `R=Ra+Rb` shares, proofs-of-possession popA/popB via `swap.ProvePossession`). Add handler cases in `handleMessage` (the switch at `p2p.go:536-554`) that route into a per-offer swapsession kept in a new `Node.sessions` map (guarded by `Node.mu` like `n.bans/n.peers`). Reuse the existing `penalize()` path for malformed session messages. These are point-to-point (use `n.send`, not `n.broadcast`) so secrets/nonces are not gossiped. PoW on the take message (reuse swapbook `leadingZeroBits`) to keep sybil resistance on takes as on offers.
   *Files:* `pkg/p2p/p2p.go` (msg consts, handleMessage switch, Node struct sessions map), `pkg/swapsession/wire.go` (serialize/parse take/accept/nonce/abort), `pkg/p2p/p2p_test.go`

3. **Wire the executor to drive the real two-party flow over P2P** — *Effort: L*
   Replace doAtomicSwap's single-process funder+claimer call (`main.go:150-215`, called at 250/260/376) with two entry points: `runMaker` (post offer via existing `node.PostOffer`, then on msgSwapTake run Maker side of swapsession) and `runTaker` (pick offer via `book.Best` at `swapbook.go:293`, send msgSwapTake, run Taker side). Keep selftest as a localhost two-NODE test (spin up two `p2p.Node` instances on 127.0.0.1 ephemeral ports instead of one process holding all secrets) so the selftest itself proves the split-secret path end to end. Remove the sB logging (`main.go:317-318`) and self-recovery framing in the live path; replace with per-party recovery files holding only the local half.
   *Files:* `cmd/obscura-swap/main.go` (doAtomicSwap, runSelfTest, runLive, newSecrets split into maker/taker secret gen), `pkg/p2p/p2p.go` (PostOffer, new TakeOffer method)

4. **Implement a real Bitcoin BitcoinClient adapter** — *Effort: XL*
   Add `pkg/swapd/bitcoinrpc.go` implementing the existing `BitcoinClient` interface (`bitcoin.go:19`) against a bitcoind/Electrum JSON-RPC, mirroring the no-hardcoded-endpoint pattern of `NewNanoRPC (nanorpc.go:50-60` - require operator-supplied URL, error if empty). FundHTLC: pay the P2WSH witness program from `BtcWitnessProgram(BtcHTLCScript(...))` (`bitcoin.go:62,88`) and bech32-encode tb1/bc1; Confirmed: gate on a configurable confirmation depth via gettxout/getrawtransaction confirmations; Redeem: build+sign the witness spend on the OP_IF hashlock branch revealing preimage=t; Refund: spend the OP_ELSE CLTV branch with nLockTime>=locktime. Cross-check the constructed script and witness against MockBitcoin's rule enforcement in a new bitcoinrpc_test.go (regtest), keeping MockBitcoin for offline tests. Add a btc preset registry analogous to nanopresets.go.
   *Files:* `pkg/swapd/bitcoinrpc.go`, `pkg/swapd/bitcoinpresets.go`, `pkg/swapd/bitcoin.go` (BtcHTLCScript, BtcWitnessProgram - reuse), `tests/critical/swapd/btcswap_test.go` (extend to regtest path)

5. **Enforce cross-chain timelock ordering in a validator** — *Effort: M*
   Add `swapsession.ValidateTimelocks(slowChainLocktime, fastChainUnlockHeight, params)` that rejects any swap where the chain the TAKER must claim second has a timelock that is NOT safely greater than the chain claimed first (the classic claim-slow-chain-first invariant). Concretely: derive a minimum delta from confirmation depths and target block times (`config.TargetBlockTime` for OBX; a BTC block-time constant) so BTC CLTV must exceed the wall-clock of the OBX UnlockHeight by a safety margin. Call it before any Fund step in both Maker and Taker session paths, replacing the hand-picked `c.Height()+200 (main.go:156)` and the hardcoded CLTV in `btcswap_test.go`. A misordered swap must return an error and never fund.
   *Files:* `pkg/swapsession/timelock.go`, `pkg/swapsession/timelock_test.go`, `cmd/obscura-swap/main.go` (remove c.Height()+200 constant), `pkg/config` (BTC block-time + safety-margin constants)

6. **Widen the XNO amount path to 128-bit and gate confirmations** — *Effort: M*
   Change `NanoClient.Lock` signature (`nano.go:26`) and `MockNano.Lock (nano.go:72)` and `NanoRPC.Lock (nanorpc.go:155)` to take `*big.Int` (raw) instead of uint64, removing the overflow note at `nanorpc.go:151-154`; the publishState path already uses big.Int (`nanorpc.go:321,412`) so only the interface boundary widens. Then make doAtomicSwap/sessions call Confirmed (`nano.go:29 / bitcoin.go:26`) and require a confirmation/cement depth BEFORE settling the OBX leg - currently `waitForReceivable (main.go:583-594)` returns on first sight with no depth check. Add a configurable min-confirmations and poll Confirmed until satisfied or abort.
   *Files:* `pkg/swapd/nano.go` (Lock signature), `pkg/swapd/nanorpc.go` (Lock, drop uint64 note), `cmd/obscura-swap/main.go` (waitForReceivable -> confirm-depth gate, doAtomicSwap Confirmed calls), `pkg/swapsession/session.go` (require Confirmed before claim)

7. **Add abort/refund-race monitoring and a punishment-free safe-exit branch** — *Effort: L*
   In swapsession add a watcher loop for the funded-but-not-yet-claimed window: the funder monitors OBX height vs UnlockHeight (the `VerifyClaim height<UnlockHeight / VerifyRefund height>=UnlockHeight` rules at `swap.go:166-183`, `validate.go:493-503`) and the BTC CLTV, and if the counterparty has not claimed by a safety buffer before UnlockHeight, it stops waiting and broadcasts the refund (`BuildSwapSpend` refund path / Bitcoin Refund). Handle the last-block race: if a claim and a refund could both be valid in the same window, the watcher must prefer detecting the on-chain claim (which reveals t and lets the funder still sweep the other leg) before issuing a refund. Add a `session_abort_test.go` that drives a counterparty who claims at `UnlockHeight-1` and asserts no double-spend/strand.
   *Files:* `pkg/swapsession/watcher.go`, `pkg/swapsession/session_abort_test.go`, `pkg/swap/swap.go` (VerifyClaim/VerifyRefund - reuse), `pkg/swapd/bitcoin.go` (Refund/RevealedPreimage - reuse)

8. **Live two-party + real-BTC integration validation** — *Effort: M*
   Run a genuine two-node, two-machine swap on the existing testnet (134.122.71.149 droplet as one party, local as the other) for the OBX<->XNO direction proving split secrets over P2P, and a BTC regtest (or signet) run for the OBX<->BTC direction exercising `bitcoinrpc.go` FundHTLC/Confirmed/Redeem/Refund and the timelock validator. Capture that neither party ever holds both halves and that an aborted swap refunds cleanly. This converts the cryptographic+unit-test proof into a deployed-protocol proof - the actual blocker to 100.
   *Files:* `tests/critical/swapd/twoparty_e2e_test.go`, `cmd/obscura-swap/main.go` (selftest two-node mode)

### Deliverables

- `pkg/swapsession`: two-party state machine where Maker and Taker each hold ONLY their own half-secrets (sA/sB/a/b never co-located), with split-secret unit test proving atomic completion via wire messages
- P2P take/accept/settle protocol: msgSwapTake/Accept/Nonce/Funded/Abort added to `pkg/p2p` with point-to-point (non-gossiped) adaptor-point/nonce/PoP exchange and per-offer session map
- `cmd/obscura-swap` driving a real two-NODE flow (two `p2p.Node` instances), sB logging and single-process self-recovery removed
- `pkg/swapd/bitcoinrpc.go`: a real bitcoind/Electrum BitcoinClient (P2WSH FundHTLC, confirmation-gated Confirmed, hashlock Redeem revealing t, CLTV Refund) with no hardcoded endpoint, regtest-tested
- `swapsession.ValidateTimelocks` enforcing BTC-CLTV > OBX-UnlockHeight + safety margin and claim-slow-chain-first, replacing hand-picked `main.go:156` / test constants
- 128-bit XNO amounts: `NanoClient.Lock` widened to `*big.Int` across nano.go/nanorpc.go/MockNano, overflow note removed
- Confirmation-depth gating in the executor (Confirmed polled before OBX settlement) replacing first-sight waitForReceivable
- Abort/refund watcher with last-block-race handling and a `session_abort_test.go` proving no strand/double-spend
- Live validation: two-machine OBX<->XNO split-secret swap on the testnet droplet + OBX<->BTC regtest swap, documented as the deployed-protocol proof

---

## Factor 3 — Asset & chain coverage

**Current score: 38/100** — **Effort to 100: XL**

### Gaps

- No production BitcoinClient: only MockBitcoin; BtcHTLCScript builds correct P2WSH bytes but no bitcoind/electrum adapter funds/redeems/refunds a real HTLC, and BitcoinClient is never wired into any executor or RPC server (`pkg/swapd/bitcoin.go`, `cmd/obscura-node/main.go`).
- No production MoneroClient: only MockMonero; XMR dropped from wallet UI (`website/wallet.html` oGive/oGet selects list OBX/XNO/BTC-mock).
- Only XNO is a real counterparty chain (`pkg/swapd/nanorpc.go NanoRPC`); 3-chain coverage is really 1 live + 2 mocks.
- `NanoClient.Lock/Balance` use uint64, which cannot represent full 128-bit XNO raw (1 XNO = 1e30 raw); self-acknowledged precision gap in nanorpc.go Lock/Balance comments.
- Even the XNO leg's two-party flow is unproven: `cmd/obscura-swap doAtomicSwap` plays BOTH funder and claimer locally; no maker/taker take-accept handshake drives the clients.
- No generic Chain/adapter abstraction or registry: NanoClient (scriptless Sweep) vs BitcoinClient (HTLC Redeem/Refund) are separate hand-written interfaces; adding a 4th chain is bespoke.
- Multi-asset pairs are free-form string tickers (`swapbook.validAsset` allowlist) with no binding to a real settlement backend; nothing prevents an OBX<->DOGE offer that can never settle.
- Per-asset precision/decimals hardcoded client-side (`website/wallet.html DEC={OBX:8,XNO:30,BTC:8}`); a new asset needs client code changes, no asset registry.
- BTC/XMR clients have no integration tests beyond mock unit tests (`tests/critical/swapd/btcswap_test.go`); HTLC redeem-script-to-real-witness path untested against any node.

### Plan to 100

1. **Introduce a settlement-backend registry that binds asset tickers to real backends** — *Effort: M*
   Create `pkg/swapd/registry.go` defining an AssetID type and a SettlementBackend registry: a `map[string]struct{Kind (scriptless|htlc), Decimals uint8, Live bool, Lock/Sweep|Redeem handles}`. Register OBX (native), XNO (scriptless, decimals 30, live), BTC (htlc, decimals 8), XMR (scriptless, decimals 12). Expose `Registry.Backend(ticker)` and `Registry.Live(ticker)`. Then make swapbook reject offers whose assets are not BOTH registered: add a registry-backed validator hook so `swapbook.Offer.Verify` rejects pairs with no settlement backend (replace the pure-syntactic validAsset gate with `validAsset(s) && registry.Known(s)` via an injected allowlist set, keeping swapbook free of an import cycle by passing the known-set in). This closes the 'OBX<->DOGE offer that can never settle' gap and makes decimals registry-derived, not hardcoded.
   *Files:* `pkg/swapd/registry.go` (new), `pkg/swapbook/swapbook.go:validAsset`, `pkg/swapbook/swapbook.go:Offer.Verify`

2. **Widen NanoClient/MoneroClient amount types to 128-bit before any mainnet-sized settlement** — *Effort: M*
   Change `NanoClient.Lock(amount uint64,...)` and `Balance() ...uint64` in `pkg/swapd/nano.go` to accept/return a decimal raw string (or `*big.Int`) so full 1e30-raw XNO is representable; `NanoRPC.Lock` already formats via `fmt.Sprintf` and Receivable/account_info already parse big.Int, so the RPC impl change is small. Update MockNano (`nano.go`) and the doAtomicSwap caller (`cmd/obscura-swap/main.go` xnoRaw uint64 + Balance asserts) and `tests/critical/swapd/nanoswap_test.go`. Do the same for MoneroClient (`pkg/swapd/monero.go`, decimals 12). This removes the self-acknowledged uint64 cap that blocks real-value XNO.
   *Files:* `pkg/swapd/nano.go:NanoClient`, `pkg/swapd/nanorpc.go:Lock,Balance`, `pkg/swapd/monero.go:MoneroClient`, `cmd/obscura-swap/main.go:doAtomicSwap,runSelfTest`, `tests/critical/swapd/nanoswap_test.go`

3. **Build a production BitcoinClient against bitcoind/Electrum and wire it everywhere XNO is wired** — *Effort: XL*
   Add `pkg/swapd/bitcoinrpc.go` implementing BitcoinClient over a bitcoind JSON-RPC + Electrum endpoint, mirroring NanoRPC's operator-config pattern (`BitcoinRPCConfig{URL,AuthHeader,WalletName,...}`, `NewBitcoinRPC` requiring a non-empty URL, no hardcoded endpoint, retry/LimitReader plumbing copied from `nanorpc.go call/callOnce`). FundHTLC: pay the P2WSH from `BtcWitnessProgram(BtcHTLCScript(...))` (already correct in bitcoin.go) via fundrawtransaction/sendtoaddress, bech32-encode the witness program (tb1/bc1). Redeem: build the witness stack `[sig,preimage,1,script]` and broadcast; Refund: `[sig,0,script]` at/after locktime with nLockTime+nSequence set; RevealedPreimage: scan the spending tx witness for the 32-byte preimage. Add a `swapd.BitcoinPresets` list (regtest/testnet) like nanopresets.go. Then wire it: add `Server.SetBitcoinBackend(c BitcoinClient)` + btc field + `BitcoinEnabled()` in `pkg/rpc/server.go` (clone of SetNanoBackend at line 99-104), call `srv.SetBitcoinBackend(btc)` in `cmd/obscura-node/main.go` next to SetNanoBackend (line 138), and add a `--btc-rpc` path + HTLC settle branch in `cmd/obscura-swap`. This turns BTC from design-only into a second live counterparty chain.
   *Files:* `pkg/swapd/bitcoinrpc.go` (new), `pkg/swapd/bitcoinpresets.go` (new), `pkg/swapd/bitcoin.go:BtcHTLCScript,BtcWitnessProgram`, `pkg/rpc/server.go:SetNanoBackend(clone)`, `cmd/obscura-node/main.go:138`, `cmd/obscura-swap/main.go`

4. **Implement a real two-party maker/taker take-accept handshake driving the clients** — *Effort: XL*
   Add `pkg/swapd/session.go` with a swap-session state machine and two RPC endpoints in `pkg/rpc/server.go` (register in registerRoutes near line 121 alongside /offer): POST `/swap/take` (taker selects an offer id from the book, sends its OBX claim pubkey b and Nano/BTC dest) and POST `/swap/accept` (maker responds with a,T,R commitments). Refactor `cmd/obscura-swap/main.go doAtomicSwap` so the funder and claimer halves run as two distinct roles communicating over these endpoints instead of one process holding both sA+sB and a,b: maker holds (sA,a), taker holds (sB,b), neither sees the other's half until the adaptor reveals it on-chain. Drive XNO first (NanoClient), then BTC (BitcoinClient HTLC). This is the gap that the order book and clients exist but are never exercised by a real counterparty flow.
   *Files:* `pkg/swapd/session.go` (new), `pkg/rpc/server.go:registerRoutes,handlePostOffer(sibling handlers)`, `cmd/obscura-swap/main.go:doAtomicSwap,runLive`

5. **Unify scriptless vs HTLC chains behind one Chain adapter interface so adding a 4th chain is non-bespoke** — *Effort: L*
   Define `pkg/swapd/chainadapter.go` with a single `ChainAdapter` interface that both legs satisfy: `Lock(amount, jointPub)`, `Confirmed(id)`, `Settle(id, secretOrPreimage, dest)`, `Refund(id, dest)`, `Decimals()`, `Kind()`. Provide two embeddable scaffolds: scriptlessAdapter (wraps NanoClient/MoneroClient: Settle==Sweep with the joint scalar, Refund==on-OBX-timelock no-op) and htlcAdapter (wraps BitcoinClient: Settle==Redeem with preimage, Refund==Refund at locktime). Make the registry from step 1 return a ChainAdapter so the session machine (step 4) and doAtomicSwap call ONE uniform API regardless of chain. This is the shared scaffolding whose absence makes each new chain bespoke; with it, a 4th chain is ~5 methods plus a registry entry.
   *Files:* `pkg/swapd/chainadapter.go` (new), `pkg/swapd/registry.go`, `pkg/swapd/nano.go`, `pkg/swapd/bitcoin.go`, `cmd/obscura-swap/main.go:doAtomicSwap`

6. **Either ship a production MoneroClient or remove XMR from the codebase to make coverage claims honest** — *Effort: L*
   XMR is effectively abandoned (MockMonero only, dropped from `website/wallet.html`). Pick one: (a) implement `pkg/swapd/monerorpc.go` against monero-wallet-rpc (transfer to the joint spend pub, sweep_all proving knowledge of s_a+s_b, get_transfers confirms) and re-add XMR to the wallet selects and registry as live; or (b) delete monero.go + MockMonero + tests and drop the '3-chain'/XMR language everywhere (docs, wallet meta description at `website/wallet.html line 7`). Recommended: (a) so the '3 real chains' claim becomes true (OBX+XNO+BTC+XMR), reusing the scriptless scaffold from step 5 since XMR is the same ed25519 scriptless construction as XNO.
   *Files:* `pkg/swapd/monerorpc.go` (new) OR delete `pkg/swapd/monero.go`, `website/wallet.html:7,140-141`, `pkg/swapd/registry.go`

7. **Drive per-asset decimals from the registry instead of the hardcoded wallet DEC map; expose an /assets endpoint** — *Effort: M*
   Add GET `/assets` to `pkg/rpc/server.go` returning the registry's registered tickers with `{decimals, live, kind}`. Replace the hardcoded `DEC={OBX:8,XNO:30,BTC:8}` in `website/wallet.html (line 303)` with a fetch of `/assets` at load so a new asset needs zero client code changes, and populate the oGive/oGet selects (lines 140-141) from the same response. Fix the XNO decimals mismatch too: the wallet hardcodes XNO:30 but BTC:8 while the precision note in nanorpc.go is about raw 1e30 — make the registry the single source of truth.
   *Files:* `pkg/rpc/server.go:registerRoutes(new handleAssets)`, `website/wallet.html:140-141,303`

8. **Add integration tests for the real BTC HTLC witness path and the two-party session** — *Effort: L*
   Add `tests/critical/swapd/btcrpc_test.go` that, gated behind an env flag (`OBX_BTC_REGTEST_URL`) so CI without a node skips, funds-redeems-refunds a real HTLC against bitcoind regtest: verify the witness stack from step 3 actually spends BtcHTLCScript and that RevealedPreimage recovers t. Add `tests/critical/swapd/session_test.go` exercising the maker/taker handshake (step 4) end-to-end over MockNano + MockBitcoin with two separate key custodians, asserting neither side can settle without the on-chain adaptor reveal. This closes the 'HTLC redeem-script-to-real-witness path untested' gap.
   *Files:* `tests/critical/swapd/btcrpc_test.go` (new), `tests/critical/swapd/session_test.go` (new), `tests/critical/swapd/btcswap_test.go`

### Deliverables

- `pkg/swapd/registry.go`: asset->settlement-backend registry binding tickers to real backends with decimals/kind/live, consumed by `swapbook.Offer.Verify` so unsettleable pairs are rejected
- NanoClient/MoneroClient amount types widened from uint64 to 128-bit (string/big.Int) across nano.go, nanorpc.go, monero.go, cmd/obscura-swap, and nanoswap_test.go
- `pkg/swapd/bitcoinrpc.go` + `bitcoinpresets.go`: production BitcoinClient over bitcoind/Electrum (FundHTLC P2WSH pay, Redeem/Refund witness construction, RevealedPreimage), wired via `Server.SetBitcoinBackend` in cmd/obscura-node and a `--btc-rpc` path in cmd/obscura-swap
- `pkg/swapd/session.go` + `/swap/take` and `/swap/accept` RPC endpoints: real maker/taker handshake where maker holds (sA,a) and taker holds (sB,b), replacing doAtomicSwap's single-process both-sides play
- `pkg/swapd/chainadapter.go`: one ChainAdapter interface with scriptless and HTLC scaffolds so the session machine calls a uniform API and a 4th chain is ~5 methods + a registry entry
- Resolved XMR: either `pkg/swapd/monerorpc.go` (monero-wallet-rpc, re-added to wallet+registry as live) or full removal of monero.go and all XMR/3-chain language
- GET `/assets` endpoint + `website/wallet.html` fetching decimals and the offer-asset selects from the registry instead of the hardcoded DEC map
- `tests/critical/swapd/btcrpc_test.go` (regtest-gated real HTLC witness) and `session_test.go` (two-custodian handshake) integration tests

---

## Factor 4 — Refund & failure recovery (DEX / cross-chain atomic swaps)

**Current score: 24/100** — **Effort to 100: XL**

### Gaps

- No timeout/stall watcher that auto-builds+broadcasts the OBX refund once height>=UnlockHeight - `BuildSwapSpend(isRefund=true)` is never called outside a unit test.
- Stranded XNO first-send recovery is a manual copy of sA/sB hex from stdout; no tool reconstructs the joint secret and sweeps it.
- No persisted swap-state machine: an executor crash loses sA/sB and cannot resume; swapd has no save/load.
- swapbook holds no in-flight/take/accept/reservation state, so it cannot drive or recover a stalled swap.
- Real Bitcoin client absent: BTC HTLC CLTV refund exists only in MockBitcoin.
- No user-reachable refund action in the web wallet or any CLI subcommand.
- Both-secrets/abort safety only proven in the single-process self-test where the executor plays both sides; no adversarial 2-party stall test.
- No OBX-chain height monitor wired to fire the refund automatically.

### Plan to 100

1. **Persisted swap-state machine in swapd (the foundation everything else needs)** — *Effort: L*
   Add `pkg/swapd/swapstate.go` defining a SwapState struct that captures EVERYTHING needed to resume or refund a half-done swap: SwapKey, Direction, all four secrets (sA,sB,a,b as hex), the OBX FundSwap txid + UnlockHeight + ClaimR/ClaimT, the XNO jointAddr + lockID + xnoDest, the counterparty role, and a Phase enum (Created, XnoLocked, ObxFunded, ObxClaimed, Swept, RefundedOBX, RefundedXNO, Aborted). Add a SwapStore with Save(s)/Load(key)/List()/Delete that gob- or json-encodes one file per swap under `<datadir>/swaps/<swapKey>.json`, fsync'd, written BEFORE each irreversible action (write-ahead: persist Phase=ObxFunded before broadcasting FundSwap, etc.). Mirror the existing snapshot.go discipline (append-then-rename for atomicity). This is the missing 'swapd has no save/load' - it must exist before any watcher or recovery can resume.
   *Files:* `pkg/swapd/swapstate.go` (new), `pkg/swapd/swapstate_test.go` (new)

2. **Refactor doAtomicSwap into a resumable, phase-driven executor that persists at every step** — *Effort: L*
   Restructure `cmd/obscura-swap/main.go doAtomicSwap` so it (a) takes a `*swapd.SwapStore` and a `*swapd.SwapState`, (b) checkpoints state.Phase to disk before each on-chain action (before mineWith(fund), before mineWith(claim), before nano.Sweep), and (c) on entry switches on the loaded Phase to skip already-completed steps. Replace the `runLive log.Fatalf('recover it with sA+sB - keep these logs')` at `main.go:377` with a call to a recovery routine that persists the state and either fires the refund or surfaces a resumable handle. Capture the current `unlock=c.Height()+200 (main.go:156)` and ClaimR/ClaimT into the state so the refund builder later has them.
   *Files:* `cmd/obscura-swap/main.go` (doAtomicSwap, runLive, newSecrets→store)

3. **OBX-leg refund builder + signer (invoke the primitive nobody invokes)** — *Effort: M*
   Add a BuildRefund helper that actually calls `wallet.BuildSwapSpend(swapKey, amount, isRefund=true, fee, sign)` — the path exercised ONLY by a unit test today. The sign closure produces a plain Schnorr signature under the funder's RefundKey (the funder holds scalar b; RefundKey was set to pt(sec.b) at `main.go:167-168`, so the refund signer is `commit.Sign(sec.b, coreHash)`). Wire it so the funder side of a swap can reclaim its locked OBX. Add it to `cmd/obscura-swap` as an exported function used by both the watcher (step 4) and the CLI subcommand (step 6). Cross-check against the existing TestOnChainSwapRefund/TestSwapRefundPath so the consensus path (`validate.go:493-499`) accepts it.
   *Files:* `cmd/obscura-swap/main.go` (new buildRefund/broadcastRefund), `pkg/wallet/wallet.go` (BuildSwapSpend — caller only, no change unless fee edge)

4. **OBX-chain height watcher that auto-fires the refund at UnlockHeight** — *Effort: M*
   Add a RefundWatcher goroutine (in `cmd/obscura-swap` or a new `pkg/swapd/watcher.go`) that, for every persisted SwapState in Phase ObxFunded (claim never observed), polls `c.Height()` (and `c.Swap(swapKey)` via `chain.Chain.Swap` at `chain.go:491` to confirm the swap is still live/unclaimed) and, the moment `c.Height() >= state.UnlockHeight`, builds the OBX refund (step 3), mines/broadcasts it via `mineWith(...,node)`, and advances Phase to RefundedOBX. Also detect the counterparty-claimed case: if `c.Swap` returns `!ok` the swap was claimed, so instead extract sA from the claim and proceed to sweep (recovery, not loss). This is the missing 'timeout/stall watcher' and 'monitor OBX chain to auto-fire refund'.
   *Files:* `cmd/obscura-swap/main.go`, `pkg/swapd/watcher.go` (new), `pkg/chain/chain.go` (reuse Swap accessor)

5. **Stranded-XNO recovery: reconstruct joint secret and sweep without manual hex** — *Effort: M*
   The XNO is locked into the sA+sB joint account which the OBX timelock does NOT govern (`nano.go:14-18`). Two recovery cases, both now automatable because the state machine persists sA,sB: (case A) the swap aborted BEFORE the OBX claim — we still hold both sA and sB locally, so reconstruct `accountSecret=sA+sB` and call `NanoRPC.Sweep(lockID, accountSecret, xnoDest) (nanorpc.go:261`, already works for any scalar) to return the user's own XNO. (case B) abort AFTER OBX claim — sA is recoverable via `commit.Extract` from the published claim sig (already done at `main.go:196-204`); persist it. Implement `recoverXNO(state, store, nano)` that picks the case from Phase and sweeps. This replaces the printed-hex workaround at `main.go:317-318` and `377`.
   *Files:* `cmd/obscura-swap/main.go` (recoverXNO), `pkg/swapd/nanorpc.go` (Sweep — caller only)

6. **CLI recovery subcommands: obscura-swap refund / recover / resume** — *Effort: M*
   Add three subcommands in main.go's switch (`main.go:47-54`): `obscura-swap refund <swapKey>` (load state, run the OBX refund builder+broadcast from step 3), `obscura-swap recover-xno <swapKey>` (run recoverXNO from step 5 to sweep stranded XNO with the persisted half-keys), and `obscura-swap resume <swapKey>` (reload state and re-enter the phase-driven executor from step 2 to finish an interrupted swap). Each reads from the SwapStore datadir. This makes refund user-reachable instead of a copy-from-stdout operation.
   *Files:* `cmd/obscura-swap/main.go` (main switch, usage text)

7. **In-flight take/accept/reservation state in swapbook (currently stateless offers-only)** — *Effort: L*
   `swapbook.Book` holds only offers (`swapbook.go:242-248`) with no concept of a swap being taken, so failure recovery is structurally impossible there. Add a Swap struct + Reservations map keyed by offer ID: Take(offerID, takerPub) reserves an offer (marks it in-flight with a deadline), Accept/Settle/Abort transitions, and a sweep that expires stale reservations back to open. Persist reservations alongside offers via the new SwapStore so a daemon restart re-loads in-flight swaps. Add ReservedOffers()/InFlight() accessors. Wire Take into the RPC (`server.go handlePostOffer` neighbors at `server.go:498`) so a real taker can claim an offer. This gives the order book the state needed to DRIVE and RECOVER a stalled swap.
   *Files:* `pkg/swapbook/swapbook.go` (Take/Accept/Abort/Reservation), `pkg/swapbook/reservation_test.go` (new), `pkg/rpc/server.go` (handleTakeOffer)

8. **Real Bitcoin client implementing BitcoinClient against bitcoind/Electrum (testnet)** — *Effort: XL*
   BTC HTLC CLTV refund (`bitcoin.go:210 Refund`) exists only in MockBitcoin, never validated on a real chain. Add `pkg/swapd/bitcoinrpc.go` implementing the BitcoinClient interface (`bitcoin.go:19-38`) against bitcoind RPC / Electrum on signet or testnet3: FundHTLC pays the P2WSH program from `BtcWitnessProgram(BtcHTLCScript(...))` (`bitcoin.go:62-91`, already correct), Redeem spends the hashlock path revealing the preimage, and Refund spends the CLTV timelock path after locktime. Reuse the existing script builder verbatim. Gate it behind a preset selector like the Nano one (nanopresets.go) and add a BTC live-gate test on signet mirroring nanorpc_test.go. This validates the BTC refund against a real network.
   *Files:* `pkg/swapd/bitcoinrpc.go` (new), `pkg/swapd/bitcoinpresets.go` (new), `pkg/swapd/bitcoinrpc_test.go` (new), `pkg/swapd/bitcoin.go` (BtcHTLCScript reuse)

9. **Adversarial 2-party stall test (no single process playing both sides)** — *Effort: L*
   Today both-secrets/abort safety is only proven in selftest where one process is funder AND claimer (`main.go:250,260`). Add a test harness that runs funder and claimer as separate executors/goroutines with independent state stores and a MockNano/Mock chain, then inject a malicious-stall: claimer NEVER claims after XNO is locked. Assert the funder's RefundWatcher (step 4) fires the OBX refund at UnlockHeight AND the XNO-locking party's recoverXNO (step 5) sweeps its stranded XNO back — i.e. no party loses funds when the counterparty stalls. Also assert the symmetric case where claimer claims late but still reveals sA, and the funder's watcher detects the claim and sweeps instead of refunding. This closes the 'no adversarial 2-party path' gap.
   *Files:* `cmd/obscura-swap/adversarial_test.go` (new), `pkg/swapd/watcher_test.go` (new)

10. **User-reachable refund/recover action in the web wallet + explorer surfacing** — *Effort: M*
    Add a Refund/Recover control to `website/wallet.html`'s swap tab (the swap section at `wallet.html:127-134`) that, for a swap the user initiated, calls a new RPC (`pkg/rpc/server.go`) to trigger the OBX refund (step 3) or stranded-XNO sweep (step 5) and shows status (pending refund / refunded / swept). Add a swap-status RPC reading the SwapStore so the wallet/explorer can show in-flight/refundable swaps. Update `explorer.go` (around `explorer.go:219` where SwapInputs are already decoded) to label refund vs claim spends. Keep it honest: mark BTC as mock until step 8 lands.
    *Files:* `website/wallet.html` (swap tab refund button + status poll), `pkg/rpc/server.go` (handleSwapStatus, handleRefund), `pkg/rpc/explorer.go` (refund/claim labeling)

### Deliverables

- `pkg/swapd/swapstate.go` + SwapStore: write-ahead persisted, per-swap state machine with Phase enum, fsync atomic writes, full unit coverage (resume across simulated crash)
- Refactored `cmd/obscura-swap doAtomicSwap` into a resumable phase-driven executor that checkpoints before every irreversible on-chain action and skips completed phases on resume
- `buildRefund/broadcastRefund` in cmd/obscura-swap that actually calls `wallet.BuildSwapSpend(isRefund=true)` and signs under the funder RefundKey (scalar b) — the consensus refund path finally invoked by a real actor
- RefundWatcher goroutine (`pkg/swapd/watcher.go`) polling `c.Height()` + `c.Swap(swapKey)` that auto-fires the OBX refund at UnlockHeight, and auto-sweeps via extracted sA when the counterparty claims instead
- `recoverXNO` in cmd/obscura-swap that reconstructs sA+sB (or extracts sA) from persisted state and sweeps the stranded joint-account XNO via `NanoRPC.Sweep` — replacing the printed-hex manual workaround
- Three new CLI subcommands: `obscura-swap refund <swapKey>`, `recover-xno <swapKey>`, `resume <swapKey>`
- swapbook Take/Accept/Abort/Reservation in-flight state with expiry + persistence, plus an RPC take-offer endpoint, giving the order book the state to drive/recover a stalled swap
- `pkg/swapd/bitcoinrpc.go`: real BitcoinClient against bitcoind/Electrum on signet/testnet, reusing BtcHTLCScript, with a live-gate test proving the CLTV refund on a real chain
- Adversarial 2-party stall test (separate funder/claimer state stores) asserting no party loses funds on a malicious counterparty stall, in both refund-fires and late-claim-detected directions
- Web-wallet Refund/Recover button + swap-status RPC reading the SwapStore, plus explorer refund-vs-claim labeling, making refund user-reachable
- Updated `docs/INVENTION_CROSSCHAIN_SWAPS.md` and wallet.html honesty copy describing the recovery machinery and remaining BTC caveats

---

## Factor 5 — Two-party P2P protocol completeness

**Current score: 12/100** — **Effort to 100: XL**

### Gaps

- Take/accept handshake binding a specific `Book.Best` offer to a live swap session between two distinct nodes (currently no msgSwapTake/Accept; RPC stops at /offers,/offer in `pkg/rpc/server.go:121-123`; p2p msg types stop at `msgGetOffers=11` in `pkg/p2p/p2p.go:36-37`).
- Split cosigning: each party computes only its own `(r_i+e*x_i)` half and exchanges PoP+half-presig over the wire (today `swap.CoSignClaim` is called with BOTH a,b,ra,rb at `cmd/obscura-swap/main.go:181`).
- Per-counterparty swap session state machine with persisted state, replacing the linear in-process doAtomicSwap happy path (`cmd/obscura-swap/main.go:150`).
- Who-funds-when ordering: maker funds OBX SwapOutput first, taker verifies it on-chain before locking the XNO leg, both verify the other's leg before revealing.
- Timeout/abort coordination: monitor unlock deadline and trigger `swap.SwapOutput.VerifyRefund` / BTC HTLC Refund (`pkg/swapd/bitcoin.go:210`); none is monitored today.
- Claim-race / front-running handling: claimer watches OBX claim publication to extract sA in time on the XNO leg; funder races refund as the timelock nears.
- Griefing window guard: refund-after-secret-reveal must be blocked by deadline separation between the two legs.
- Two mutually-distrusting trust domains actually exercised in code paths and tests (selftest+live both run executor on both sides: `main.go:250,260,324-325`).

### Plan to 100

1. **Define the two-party swap message schema (new pkg/swap/session message types)** — *Effort: M*
   Create `pkg/swap/proto.go` with wire structs + Serialize/Parse for the per-swap handshake: `SwapTake{offerID [32]byte, takerPub, takerR (taker's Rb), takerKeyShare B, popB, takerNonceCommit}`; `SwapAccept{makerKeyShare A, popA, makerR (Ra), makerT (adaptor point T=sA*G), claimR=Ra+Rb, unlockHeight, xnoJointPub, fundTxid placeholder}`; `SwapHalfPresig{partialS = r_i+e*x_i, signerIsMaker bool}`; `SwapReveal/SwapAbort`. Each carries a `sessionID = blake2b(offerID||takerPub||makerR||takerR)`. Mirror `swapbook.Offer.Core()` length-prefixed injective encoding (`swapbook.go:75`) so signed/hashed bytes are unambiguous. These structs are the missing R/T/PoP/half-presig envelope that today only live as locals ra,rb,popA,popB inside doAtomicSwap (`main.go:161-164`).
   *Files:* `pkg/swap/proto.go` (new), `pkg/swapbook/swapbook.go:75` (encoding pattern to mirror)

2. **Split CoSignClaim into maker-half and taker-half so neither party holds both keys** — *Effort: M*
   Add `swap.HalfPresig(x_i, r_i, R_total, T, K, m) []byte` returning only `s_i = r_i + e*x_i` (where `e = commit.AdaptorChallenge(R+T,K,m)`), and `swap.CombineHalves(sMaker,sTaker, R, T) *commit.AdaptorSig`. Refactor `swap.CoSignClaim (swap.go:128)` to be implemented as `CombineHalves(HalfPresig(a..),HalfPresig(b..))` so the existing call site still works but the protocol path uses the halves. Add `swap.VerifyHalfPresig(s_i, X_i, R_total, T, K, m)` so the receiver checks the counterparty's half (`s_i*G == R_i + e*X_i`) before combining — this is the on-the-wire integrity check that does not exist today. Each node now computes ONLY its own half from its own secret; the aggregate is assembled from two messages, defeating the single-process ownership at `main.go:181`.
   *Files:* `pkg/swap/swap.go:117-141` (CoSignClaim), `pkg/swap/swap.go:45-86` (reuse ProvePossession/VerifyPossession)

3. **Build the swap session state machine (new pkg/swap/session.go)** — *Effort: L*
   Add `type Session` with explicit states: Proposed->Accepted->MakerFunded->TakerLocked->Claimed->Swept (claim path) and abort branches ->Aborting->Refunded. Session holds ONLY this party's role (Maker|Taker), its own key half (a XOR b, never both), its own sA-or-nothing (only the XNO-funder/maker holds sA=T's secret), the agreed R, T, K, unlockHeight, offerID, counterparty pub, and chain/nano handles. Methods OnTake/OnAccept/OnHalfPresig/OnReveal drive transitions and emit the next message. Persist state to disk (JSON under `datadir/swaps/<sessionID>.json`) on each transition so a crash mid-swap resumes into refund/claim monitoring rather than losing funds — replacing the all-in-RAM swapSecrets (`main.go:128`). This is the structural replacement for doAtomicSwap (`main.go:150`).
   *Files:* `pkg/swap/session.go` (new), `cmd/obscura-swap/main.go:128-215` (logic to decompose into Maker/Taker halves)

4. **Add P2P transport + RPC endpoint for the take/accept handshake** — *Effort: L*
   Add `msgSwapTake=12, msgSwapAccept=13, msgSwapHalfPresig=14, msgSwapReveal=15, msgSwapAbort=16` to the const block (`p2p.go:36`) and a dispatch case for each (`p2p.go:536` area) that routes the payload to a registered swap-session manager via a new `Node.OnSwapMsg` callback (mirroring the existing `n.OnBlock` hook at `p2p.go:125/504`). Add `Node.SendSwapMsg(peerPubOrAddr, typ, payload)` using the existing `n.send` (`p2p.go:744`). Because swap messages are direct (not gossip), route by an established session rather than broadcast; rate-limit them (they are NOT in rateExempt, `p2p.go:373`, which is correct). Add RPC POST `/swap/take` (taker initiates against an offerID) and GET `/swap/status/<sessionID>` to `pkg/rpc/server.go` Handler (`server.go:112`) wired through a new SwapProvider interface like the existing OfferProvider (`server.go:34`). This is the missing link from `Book.Best (swapbook.go:293)` to an executed swap.
   *Files:* `pkg/p2p/p2p.go:36-37` (msg consts), `pkg/p2p/p2p.go:458-559` (dispatch), `pkg/p2p/p2p.go:744` (send), `pkg/rpc/server.go:112-139` (Handler+routes), `pkg/rpc/server.go:34-37` (provider iface)

5. **Implement funding-order protocol with on-chain verification of the counterparty leg** — *Effort: L*
   In the Maker session path: after SwapAccept, Maker calls `wallet.FundSwap (wallet.go:1031)` to lock OBX into the SwapOutput, broadcasts via `node.BroadcastTx`, then sends the fund txid in SwapAccept/a follow-up and WAITS observing chain for confirmation (scan via `chain.BlockByHeight` loop like scanAll, `main.go:118`). Taker MUST verify the on-chain SwapOutput exists with the agreed K,R,T,unlock (re-derive K via `swap.AggregateKeyVerified`, `swap.go:73`; check ClaimR/ClaimT match) BEFORE locking the XNO leg via `nano.Lock (nano.go:26)`. Only after Taker confirms the XNO lock (Confirmed, `nano.go:29`) does Maker proceed to claim. Encode the ordering rule explicitly: OBX-funder-funds-first, XNO-locker-second, claimer-third — enforced by state transitions that refuse to advance until the prior leg is observed on its own chain. None of this verification exists in the linear path (`main.go:166-174` funds and immediately claims with no counterparty check).
   *Files:* `pkg/swap/session.go` (new), `pkg/wallet/wallet.go:1031` (FundSwap), `pkg/swapd/nano.go:23-35` (Lock/Confirmed), `pkg/swap/swap.go:73` (AggregateKeyVerified),162-174 (VerifyClaim binding)

6. **Add deadline monitoring + abort/refund branch** — *Effort: L*
   Add a `Session.monitor` goroutine that tracks the OBX unlockHeight (set today at `main.go:156`, c.Height()+200) and the XNO/BTC deadlines. If the counterparty stalls before the swap is bound, send SwapAbort and stop. If Maker has funded but Taker never locked, at unlockHeight Maker builds the refund spend via `wallet.BuildSwapSpend(...,isRefund=true) (wallet.go:1133)` validated by `swap.SwapOutput.VerifyRefund (swap.go:178)` and broadcasts it to reclaim OBX. For the BTC leg, wire `swapd.BitcoinClient.Refund (bitcoin.go:210)`. Critically, derive the two legs' deadlines with a SAFETY GAP (OBX claim window must close strictly after the XNO/BTC reveal window) so the griefing window (party A revealed secret, party B can still refund) is closed by construction — pick unlockHeight gaps in session.go and reject a take whose offer/leg timelocks don't leave the gap. Today there is zero deadline monitoring and zero abort branch.
   *Files:* `pkg/swap/session.go` (new), `pkg/wallet/wallet.go:1133` (BuildSwapSpend isRefund), `pkg/swap/swap.go:176-183` (VerifyRefund), `pkg/swapd/bitcoin.go:210` (BTC Refund)

7. **Add claim-race / secret-extraction watcher** — *Effort: M*
   After Maker publishes the adapted full claim sig (`commit.Adapt`, `main.go:183`) the Taker (XNO side) must independently watch OBX for the claim tx, parse its sig (`commit.ParseFullSig`, `main.go:196`), and run `commit.Extract(pre, fullSig) (main.go:200)` to recover sA, then form sA+sB and sweep XNO BEFORE the OBX/XNO timelock lets Maker refund. Implement a Session watcher (poll `chain.BlockByHeight` from last-scanned height) that fires extraction the moment the claim lands and races the sweep, with retry. Conversely the Maker monitors that, if it refunds, it does so only after the claim window proves it did NOT claim. This makes the adaptor-secret-reveal atomicity (`swap.ClaimBindingOK`, `swap.go:95`) actually enforced across two independent watchers instead of one process that already holds sA (`main.go:204`).
   *Files:* `pkg/swap/session.go` (new), `cmd/obscura-swap/main.go:195-212` (extraction+sweep logic to move into Taker watcher), `pkg/commit` (Extract/ParseFullSig)

8. **Wire offer-book matching to session creation and add a real swapd daemon command** — *Effort: XL*
   Add SwapManager in `pkg/swapd` that owns active Sessions, registered as `Node.OnSwapMsg` and as the RPC SwapProvider. On POST `/swap/take` it resolves the offer (Book offer by ID; reuse `Book.SortedByID/List`, `swapbook.go:279/319`), creates a Taker Session, and sends msgSwapTake to the maker peer. The maker's node, on msgSwapTake, looks up its own posted offer (`Node.MakerOffers`, `p2p.go:167`) and spins a Maker Session. Add a long-running `obscura-swap daemon` mode (and integrate into cmd/obscura-node) so a node can act as maker OR taker with NO shared process — replacing selftest/live which generate both wallets in-process (`main.go:232-233,324-325`). Keep `selftest` but rewrite it to run TWO SwapManagers over an in-memory pipe so the two sides are genuinely separate trust domains exchanging wire messages.
   *Files:* `pkg/swapd/manager.go` (new), `pkg/p2p/p2p.go:153-167` (PostOffer/MakerOffers), `pkg/swapbook/swapbook.go:279-319` (List/SortedByID), `cmd/obscura-swap/main.go` (new daemon subcommand; rewrite selftest), `cmd/obscura-node` (register SwapManager)

9. **Two-distinct-node tests + wallet/explorer wiring** — *Effort: L*
   Add `pkg/swapd/session_test.go` that runs Maker and Taker SwapManagers in separate goroutines connected only by the p2p transport (or an in-mem `net.Pipe`), each holding ONLY its half, asserting: (a) happy path completes via wire messages; (b) Taker-stalls -> Maker refunds via VerifyRefund; (c) Maker-claims-late -> Taker still extracts+sweeps; (d) griefing attempt (refund after reveal) is rejected by the deadline gap; (e) rogue key share is rejected by AggregateKeyVerified/VerifyHalfPresig. Update `website/wallet.html:130` to drive POST `/swap/take` and poll `/swap/status` (remove the 'orchestration demo' disclaimer once the two-party path is real), and add an in-flight-swaps view to `website/explorer.html`. This is the adversarial coverage that has NEVER executed (no mutually-distrusting path today).
   *Files:* `pkg/swapd/session_test.go` (new), `website/wallet.html:130`, `website/explorer.html`

### Deliverables

- `pkg/swap/proto.go`: wire message schema (SwapTake/Accept/HalfPresig/Reveal/Abort) with injective length-prefixed encoding and sessionID derivation
- `pkg/swap/swap.go`: HalfPresig + CombineHalves + VerifyHalfPresig so each party computes/verifies only its own (r_i+e*x_i) half (CoSignClaim refactored on top)
- `pkg/swap/session.go`: persisted per-counterparty state machine (Proposed->...->Swept + Abort/Refund), role-scoped secrets (never both halves in one party), deadline monitor, claim-extraction watcher, funding-order enforcement with on-chain leg verification
- `pkg/p2p/p2p.go`: msgSwapTake/Accept/HalfPresig/Reveal/Abort (12-16), dispatch routing, Node.OnSwapMsg hook, Node.SendSwapMsg directed send
- `pkg/rpc/server.go`: SwapProvider interface + POST `/swap/take` and GET `/swap/status/<id>` endpoints
- `pkg/swapd/manager.go`: SwapManager linking Book.Best/offer-by-ID to live Maker/Taker sessions; registered on the node as OnSwapMsg + RPC SwapProvider
- `cmd/obscura-swap`: `daemon` subcommand (maker OR taker, single trust domain) and a selftest rewritten to two separate SwapManagers exchanging wire messages
- `pkg/swapd/session_test.go`: two-distinct-node tests covering happy path, taker-stall refund, late-claim extraction, griefing-rejection, rogue-key rejection
- `website/wallet.html` + `explorer.html`: take-offer flow, live swap status, and removal of the 'orchestration demo' disclaimer (`wallet.html:130`)

---

## Factor 6 — UX/UI & onboarding ease (DEX / cross-chain atomic swap)

**Current score: 28/100** — **Effort to 100: XL**

### Gaps

- No taker/execute path exists anywhere in the system: `pkg/rpc/server.go` exposes only `/offers`, `/offers/json`, `/offer` (handlePostOffer:521); the OfferProvider interface (`server.go:35`) is Offers()+PostOffer() only. A user can post into the book but nothing can take.
- `cmd/obscura-swap/main.go:doAtomicSwap` (line 150) is single-process and holds ALL four secrets sA,sB,a,b for BOTH parties; wallets are hardcoded `wallet.FromSeed` (lines 324-325). There is no two-party handshake to surface in any UI.
- `swapbook.Offer (swapbook.go:56)` has a Maker pubkey but NO contact/dial address, so even with a take button a taker cannot reach the maker. P2P has no swap-session message type.
- `cmd/obscura-wasm/main.go` exposes only obxBuildOffer/obxParseOffer (lines 205,231); the browser cannot cosign an adaptor (`swap.CoSignClaim`), build a FundSwap, or sweep XNO. All settlement crypto is CLI/Go-only.
- Offer form takes RAW ATOMIC amounts (`wallet.html:144-145` placeholders 5000000000000/50000000) unlike the polished obxToAtomic OBX/vault forms (`wallet.html:173`).
- Fund-safety for live swaps is copy sA/sB out of stdout (`main.go:317,377`); no managed recovery, no UI surfacing of the joint account / refund timelock (unlock = `c.Height()+200`, `main.go:156`).
- `wallet.html` offers a BTC (mock) asset (lines 140-141) that has no real backend wired (`pkg/swapd/bitcoin.go` is MockBitcoin only), misleading the user.
- No in-flight swap tracking, status, or refund/timelock visibility in the UI; only static grinding/Posted toasts (postOffer `wallet.html:402`).
- Mnemonic stored plaintext in localStorage (`wallet.html:207`), acknowledged unsafe in footer (line 156); no PIN/encryption gate even for an experimental DEX handling real XNO.
- No swap onboarding/tooltips; the tab is labeled experimental and admits BTC is a demo (`wallet.html:130`).

### Plan to 100

1. **Add a real two-party swap session: P2P take/accept handshake + session store** — *Effort: XL*
   This is the unblocker; everything else is UI on top. Create `pkg/swapsession` with a SwapSession struct `{OfferID, Role (maker/taker), Pair, Amounts, our half-secrets only (sA OR sB, a OR b), JointXNOPub, OBXSwapKey, UnlockHeight, State enum: Proposed->Locked->OBXFunded->Claimed->Swept->Refunded->Aborted, plus counterparty pubkey/RR/T/PoP exchanged}`. Split `cmd/obscura-swap/main.go:doAtomicSwap` (currently one process holding sA,sB,a,b) into maker-side and taker-side halves that each hold only their own scalar and exchange points (T=sA*G, R=Ra+Rb, PoP via `swap.ProvePossession`) per the protocol the code already implements. Add a 'take' message to p2p (mirror msgSwapOffer): taker -> maker `{offerID, takerXNOdest, takerHalfPub, takerRb, takerPoP}`; maker replies with FundSwap details so the OBX leg lands on-chain via the existing mineWith/BroadcastBlock path. Persist sessions to disk so a crash is recoverable. Add a `swapbook.Offer.MakerContact` field (P2P node id/addr) so a taker can dial the maker — without this no take is possible.
   *Files:* `pkg/swapsession/session.go` (new), `pkg/swapbook/swapbook.go:Offer` (add MakerContact, bump Core/Serialize/ParseOffer), `cmd/obscura-swap/main.go:doAtomicSwap` (split into makerSide/takerSide), `pkg/p2p` (new msgSwapTake/msgSwapAccept handlers), `pkg/swapd/nano.go:NanoClient` (reuse Lock/Confirmed/Sweep)

2. **Expose a node RPC for take/status/recovery and surface the order book maker contact** — *Effort: L*
   Extend the OfferProvider interface (`pkg/rpc/server.go:35`) and Server with: POST `/swap/take {offerId, takerXnoDest, takerSignedHalf} -> {sessionId, jointXnoAddress, obxUnlockHeight}`; GET `/swap/status?session=ID -> {state, jointAddr, refundHeight, txids}`; POST `/swap/recover {sessionId}` -> refund/sweep driver. Add these three paths to the Vercel allowlist in `website/api/explorer.js` (it currently whitelists only summary/mempool/vaults/height/feerate/offers/offersjson + submittx/offer). Keep the same same-origin proxy model so there is zero RPC-config friction, exactly like the existing send flow.
   *Files:* `pkg/rpc/server.go:OfferProvider` (add Take/SwapStatus/Recover), handleSwapTake/handleSwapStatus/handleSwapRecover + mux.HandleFunc, `pkg/rpc/client.go` (Offers/PostOffer pattern), `website/api/explorer.js` (add swaptake/swapstatus/swaprecover to get/post maps), `cmd/obscura-node/main.go` (wire node as provider)

3. **Move adaptor cosign + FundSwap + XNO sweep into WASM so the browser can actually settle** — *Effort: XL*
   Add to `cmd/obscura-wasm/main.go`: `obxSwapTakeInit(offerHex)->{takerHalfPub,takerRb,takerPoP,xnoDest}` (derive per-swap a/sA via commit.RandomScalar, T=sA*G), `obxSwapCosignClaim(coreHash, counterpartyHalves)->fullSigHex` (wraps `swap.CoSignClaim` + `commit.Adapt`, the same calls `cmd/obscura-swap/main.go:180` makes), `obxSwapBuildFund(...)` (wraps `wallet.FundSwap`), and `obxSwapSweepXNO(lockID, recoveredScalar, dest)` (wraps the Nano signer). Keys NEVER leave the browser, consistent with the wallet's non-custodial promise. This is the crypto half of making a swap completable from the UI.
   *Files:* `cmd/obscura-wasm/main.go` (new obxSwap* exports + js.Global().Set in main), `pkg/swap/swap.go:CoSignClaim/AggregateKey` (reused), `pkg/commit` (Adapt/Extract/Sign reused), `pkg/wallet:FundSwap/BuildSwapSpend` (reused)

4. **Build the Swap-tab take/execute UI with live status and refund visibility** — *Effort: L*
   In `website/wallet.html #t-swap`: make each offer row in renderSwap() (line 396) clickable to open a Take panel showing human amounts, market rate (reuse offerRate/marketFor at line 305/331), the JOINT XNO address with a QR + click-to-copy and an 'I have sent N XNO' confirm, then drive POST `/swap/take` and poll GET `/swap/status`, rendering a stepper: Locked XNO -> OBX funded@height -> Claimed -> Swept, plus the refund timelock countdown (unlock height from the session, cf. `cmd/obscura-swap/main.go:156` unlock=height+200). Add a 'Your in-flight swaps' card persisted in localStorage (like obx_vaults at `wallet.html:276`) with a Recover/Refund button calling `/swap/recover`. Replace the static 'grinding...'/'Posted' toasts with real per-step banners. This is the single change that flips 'can post into the void' to 'can complete a swap from the browser'.
   *Files:* `website/wallet.html:#t-swap section (lines 127-151)`, `website/wallet.html:renderSwap/loadOffers/postOffer (lines 341-413)`, `website/wallet.html (new takeSwap/pollSwap/renderInflight JS)`

5. **Fix the offer form to human decimals and remove the misleading BTC (mock) option** — *Effort: M*
   Change wallet.html 'Post an offer' Give/Get amount inputs (lines 144-145) from raw-atomic numeric to decimal inputmode and convert per-asset before calling obxBuildOffer (line 408) using the existing DEC map (`wallet.html:303`, OBX:8/XNO:30) — mirror obxToAtomic (line 173). Remove 'BTC (mock)' from both selects (lines 140-141) and the DEC/asset pickers until a real BTC backend is wired (`pkg/swapd/bitcoin.go` is MockBitcoin only); keep OBX<->XNO as the sole live pair so the picker stops promising pairs that cannot settle. Add a client-side PoW progress indicator: run swapbook Sign grinding (OfferPoWBits=12, `swapbook.go:25`) in a Web Worker and show a percent/spinner instead of the static 'grinding...' message.
   *Files:* `website/wallet.html:#t-swap Post-an-offer inputs (lines 140-148)`, `website/wallet.html:postOffer/offerHint (lines 402-433)`, `cmd/obscura-wasm/main.go:obxBuildOffer` (accept decimal, document units)

6. **Add wallet encryption (PIN/passphrase) before exposing real-value swaps** — *Effort: M*
   Encrypt the mnemonic at rest instead of plaintext localStorage (`wallet.html:207` setItem obx_mnemonic). Add a passphrase prompt on createWallet/restoreWallet, derive a key (WebCrypto PBKDF2/AES-GCM), store ciphertext, and require unlock on boot (currently auto-restores plaintext at `wallet.html:191`). A cross-chain DEX moves real XNO, so the footer's 'do not use for real value' caveat (line 156) is incompatible with a 100/100 onboarding score; this closes it.
   *Files:* `website/wallet.html:createWallet/restoreWallet/boot IIFE (lines 178-215)`, `website/wallet.html:footer caveat (line 156)`

7. **Guided onboarding, tooltips, and mobile affordances for the swap flow** — *Effort: M*
   Add a one-time explainer modal on first Swap-tab open describing maker/taker + adaptor/HTLC atomicity in plain language and the refund-timelock safety net, with dismiss-persisted in localStorage. Add inline tooltips on Pair/Give/Get and the joint-address step. Make the SVG depth chart (depthBars, `wallet.html:316`) responsive (viewBox + max-width) and stack the atomic/amount inputs vertically under a media query for small screens. Drop the blanket 'experimental' framing on the now-working OBX<->XNO path (`wallet.html:130`) while keeping an honest note that this is new software you should review yourself.
   *Files:* `website/wallet.html:#t-swap header copy (line 130)`, `website/wallet.html:depthBars (lines 316-328)`, `website/wallet.html:<style> (responsive rules, lines 8-46)`

8. **End-to-end browser swap test + executor parity check** — *Effort: M*
   Add a test that the new WASM obxSwap* path produces the SAME adaptor pre-sig/extract result as `cmd/obscura-swap/main.go:doAtomicSwap` (extend `pkg/swapd/crosscheck_test.go` style), and a maker/taker selftest in `cmd/obscura-swap` that runs the two split roles in separate goroutines exchanging only points (no shared secret object) to prove the new two-party flow settles against MockNano before going live on XNO mainnet (the existing runSelfTest at `main.go:219` currently proves only the single-process orchestration).
   *Files:* `cmd/obscura-swap/main.go:runSelfTest (line 219, add two-party variant)`, `pkg/swapsession/session_test.go` (new), `pkg/swapd/crosscheck_test.go` (extend for WASM parity)

### Deliverables

- `pkg/swapsession`: persisted two-party SwapSession state machine (each party holds only its own scalar) replacing the single-process `cmd/obscura-swap/main.go:doAtomicSwap` that holds all of sA,sB,a,b
- P2P take/accept messages + `swapbook.Offer.MakerContact` so a taker can actually reach a maker (today the Maker field is an unreachable pubkey)
- Node RPC `/swap/take`, `/swap/status`, `/swap/recover` wired through the existing same-origin Vercel proxy (`website/api/explorer.js` allowlist) for zero-config browser use
- WASM obxSwapTakeInit/obxSwapCosignClaim/obxSwapBuildFund/obxSwapSweepXNO so all swap crypto runs in-browser, keys never leaving the page
- A working Swap tab where clicking an offer completes an OBX<->XNO swap: joint-address QR, send-confirm, live stepper, refund-timelock countdown, in-flight tracking, and a Recover/Refund button
- Human-decimal offer inputs (no more raw 5000000000000), removal of the non-functional BTC (mock) option, and a Web-Worker PoW progress indicator
- Encrypted (PIN/passphrase, WebCrypto) mnemonic at rest replacing plaintext localStorage, making the real-value swap path safe
- First-run swap onboarding modal, tooltips, responsive depth chart and inputs, and honest non-experimental framing of the working path
- Tests: two-party maker/taker selftest against MockNano + WASM/executor adaptor parity check before any live XNO run

---

## Factor 7 — Observability & transparency

**Current score: 42/100** — **Effort to 100: XL**

### Gaps

- No swap-status/tracking API: `pkg/rpc/server.go` has no per-swap state route; progress lives only in `cmd/obscura-swap log.Printf` (terminal-only, single-process).
- No web swap-execution/progress UI: `website/wallet.html #t-swap` only lists/posts offers (renderSwap/loadOffers), no take/accept/stage-tracker/refund-timer.
- No both-leg monitoring: `swapd.NanoClient.Confirmed/Receivable` + `BitcoinClient.Confirmed/RevealedPreimage` are never surfaced to any UI/API; explorer is OBX-only.
- No cross-chain proof links: explorerTx emits a generic `kind=='atomic-swap'` tag; cannot link an OBX SwapInput/SwapOutput txid to its XNO send-block hash or BTC HTLC lockID/preimage.
- No structured logging/metrics/tracing: zero expvar/Prometheus/ `/metrics` in pkg/ or cmd/ (confirmed by grep).
- Shallow order-book transparency: `/offers` + bestPrices show live offers/prices but no fill/taken/expired/failed history; Book is in-memory (swapbook.Book, no persistence/audit trail).
- Auto-liquidity partly opaque: `cmd/obscura-node handleAutoLiquidityStatus` exposes posted/live/rate but logs no per-offer reason and the `swapbook.BuildSignedOffer` path emits no metrics.
- Weak stall/abort clarity: only `cmd/obscura-swap waitForReceivable` timeout signals a stall; wallet/explorer never show stuck/refunding/aborted.
- Hashrate is a client-side estimate (`explorer.html refreshSummary`), clamped not measured, not node-reported.

### Plan to 100

1. **Add a SwapTracker state machine in pkg/swapd that records every swap's both-leg state** — *Effort: M*
   Create `pkg/swapd/tracker.go` with a thread-safe Tracker holding a `map[swapID]*SwapState`. SwapState fields: ID, Direction (OBX->XNO / XNO->OBX / OBX->BTC), Role (maker/taker), Stage enum (Proposed, OBXFunded, OBXClaimed, SecretRevealed, XNOLocked, XNOSwept, BTCHTLCFunded, BTCRedeemed, Refunding, Refunded, Aborted, Done), OBXTxids (fund/claim), XNOLockHash, XNOSweepHash, BTCLockID, BTCPreimage, RefundUnlockHeight, UpdatedAt, LastErr. Methods: New(direction,role) returns id; Set(id,stage); Attach(id, key, value) for tx hashes; Snapshot()/Get(id). Mirror the existing `cmd/obscura-node autoLiquidityStatus` atomic-counter precedent but richer. doAtomicSwap currently threads sec/lockID/txids through locals — Tracker is where those become queryable.
   *Files:* `pkg/swapd/tracker.go` (new), `pkg/swapd/tracker_test.go` (new)

2. **Instrument the swap executor to drive the Tracker instead of only log.Printf** — *Effort: M*
   In `cmd/obscura-swap/main.go` thread a `*swapd.Tracker` into doAtomicSwap, runLive, waitForReceivable. At each existing log.Printf milestone call tracker.Set/Attach: after FundSwap+mineWith set OBXFunded + attach fund.HashHex(); after claim mineWith set OBXClaimed + attach claim.HashHex(); after commit.Extract set SecretRevealed; in waitForReceivable on Receivable() set XNOLocked + attach the returned block hash (lockID); after nano.Sweep set XNOSwept (capture the recv/send hashes by having Sweep return them — see step 4). On every log.Fatalf path set Aborted/Refunding with LastErr so a stall is an explicit state, not just a timed-out log line.
   *Files:* `cmd/obscura-swap/main.go`

3. **Expose swap state over RPC: /swaps and /swap?id= on the node and a public read whitelist entry** — *Effort: L*
   Add handleSwaps/handleSwap to `pkg/rpc` (new `pkg/rpc/swaps.go`) returning Tracker.Snapshot()/Get as JSON (SwapStatusJSON with stage, both-leg tx links, refund height/countdown, last_err). Wire via a new optional SwapProvider interface on Server (like OfferProvider) + SetSwapTracker, register routes in Handler() PUBLIC block next to /offers. Because cmd/obscura-swap is a separate process from the node, also register the same two handlers on cmd/obscura-swap's own tiny HTTP listener (it has none today — add a minimal http.Server in runLive bound to a `--swap-status-addr` flag) so a live swap is observable while it runs. Add both paths to `website/api/explorer.js` GET whitelist (swaps, swap).
   *Files:* `pkg/rpc/swaps.go` (new), `pkg/rpc/server.go`, `cmd/obscura-swap/main.go`, `website/api/explorer.js`

4. **Return real cross-chain tx hashes from the swapd backends so proof links are populated** — *Effort: M*
   Change `swapd.NanoClient.Sweep` and `BitcoinClient.Redeem` signatures (or add SweepTx/RedeemTx variants) to return the produced block hash / txid. NanoRPC.Sweep already computes recvHash and the final send hash via publishState — return the send hash instead of discarding it; update MockNano to return a synthetic id. BitcoinClient.Redeem already records preimage; have it also return the spend txid (MockBitcoin can fabricate one). Update the two callers (doAtomicSwap, tests/critical/swapd/*) so the Tracker gets XNOSweepHash/BTCRedeemTxid/BTCPreimage. This is what makes a swap's OBX leg linkable to its XNO/BTC counterparty leg.
   *Files:* `pkg/swapd/nano.go`, `pkg/swapd/nanorpc.go`, `pkg/swapd/bitcoin.go`, `cmd/obscura-swap/main.go`, `tests/critical/swapd/nanoswap_test.go`, `tests/critical/swapd/btcswap_test.go`

5. **Surface cross-chain proof links in the explorer's atomic-swap tx view** — *Effort: M*
   Extend `rpc.ExplorerTx` (`pkg/rpc/explorer.go`) with optional SwapLeg fields: SwapRole, SwapCounterChain (XNO/BTC), SwapCounterTx (hash from the Tracker, matched by OBX txid). In explorerTx(), when `kind=='atomic-swap'`, look up the Tracker by the tx's SwapInputs/SwapOutputs identifier and populate the counterparty hash + a deep-link URL template (e.g. nanocrawler/blockchair for XNO/BTC). In `website/explorer.html`, where k-atomic-swap is rendered, render these as clickable proof links and a 'secret revealed' badge when BTCPreimage/SecretRevealed is set.
   *Files:* `pkg/rpc/explorer.go`, `website/explorer.html`

6. **Build the swap-execution + progress UI in the wallet swap tab** — *Effort: L*
   In `website/wallet.html #t-swap`, add per-offer a 'Take' button (renderSwap currently only renders depth/market). On take, POST to a new node `/swap/take` that initiates the two-party flow OR, until real 2-party take exists, drives the local executor and returns a swap id; then poll `/swaps` and render a stage tracker: a horizontal stepper (Proposed -> OBX funded -> OBX claimed -> secret revealed -> XNO locked -> XNO swept -> done) with the both-leg tx-hash links, plus a live refund-timer countdown computed from RefundUnlockHeight minus current `/height`, and explicit Stuck/Refunding/Aborted states (red) sourced from SwapState.Stage/LastErr. Reuse the explorer's setLive/failure-backoff polling pattern.
   *Files:* `website/wallet.html`, `website/api/explorer.js`

7. **Add an expvar/Prometheus metrics endpoint across node and executor** — *Effort: M*
   Add `pkg/metrics` (new) exposing a `/metrics` handler (expvar-based to avoid a new dep, or x/prometheus if acceptable) with counters/gauges: obx_swaps_total{stage}, obx_swaps_active, obx_offers_live, obx_offers_posted_total, obx_offer_fills_total, obx_block_height, obx_mempool_size, obx_difficulty, obx_node_hashrate (node-measured, see step 9). Register on `cmd/obscura-node`'s wrapped mux (next to /auto-liquidity) and on `cmd/obscura-swap`'s status listener. Have the Tracker (step 1) and autoLiquidityLoop increment these counters so swap + liquidity activity is scrapeable, not just human-readable.
   *Files:* `pkg/metrics/metrics.go` (new), `cmd/obscura-node/main.go`, `cmd/obscura-swap/main.go`, `pkg/swapd/tracker.go`

8. **Add an order-book audit trail: fill/taken/expired/failed history with bounded persistence** — *Effort: L*
   Extend `swapbook.Book` to record terminal offer events: add an events ring buffer (OfferEvent{ID,Maker,Pair,Kind:posted|taken|expired|failed,At}). Emit 'expired' from pruneLocked, 'posted' from Add, and 'taken'/'failed' when the swap executor reports a take outcome to the book (new Book.RecordTake(id, ok)). Persist the ring to `<datadir>/swapbook-events.jsonl` so the book is no longer purely in-memory/ephemeral. Expose via a new `/offers/history` RPC route + explorer panel showing recent fills and expiries (today only live offers are visible).
   *Files:* `pkg/swapbook/swapbook.go`, `pkg/swapbook/events.go` (new), `pkg/rpc/server.go`, `website/explorer.html`

9. **Make auto-liquidity fully observable (per-offer reasons) and node-report hashrate** — *Effort: M*
   In `cmd/obscura-node autoLiquidityLoop`, log a structured line for each decision (posted offer id+rate, or skipped: budget-cap/max-offers/insufficient-spendable) and feed those into the step-7 metrics and the autoLiquidityStatus struct (add last_reason, skipped counters). Separately, compute node-reported hashrate server-side in `pkg/rpc handleExplorerSummary` (difficulty / measured avg block interval over the last N headers via `chain.HeaderByHeight`) and add a Hashrate field to ExplorerSummary, so explorer.html stops client-side-estimating (remove/keep-as-fallback the refreshSummary clamp).
   *Files:* `cmd/obscura-node/main.go`, `pkg/rpc/explorer.go`, `website/explorer.html`

10. **Add structured (leveled, fielded) logging to the swap + liquidity paths** — *Effort: M*
    Replace ad-hoc log.Printf in `cmd/obscura-swap/main.go` and `cmd/obscura-node autoLiquidityLoop` with Go log/slog (JSON handler, opt-in via env OBX_LOG_JSON) carrying stable fields: swap_id, stage, leg, obx_txid, xno_hash, btc_lockid, err. Keep human text default for the CLI panel but emit machine-parseable lines so swap progress is greppable/ingestable beyond a single terminal. Thread a *slog.Logger through doAtomicSwap alongside the Tracker.
    *Files:* `cmd/obscura-swap/main.go`, `cmd/obscura-node/main.go`

11. **End-to-end observability test + dashboard awareness** — *Effort: M*
    Add a test that runs obscura-swap selftest with the Tracker enabled and asserts `/swaps` reports the full stage progression and populated both-leg hashes (extends the selftest in cmd/obscura-swap). Add swap awareness to `cmd/obscura-dashboard`: it polls only `/status,/height` today — add a `/swaps` + `/metrics` poll and a swap panel (active swaps, stages, stalls) so the operator dashboard is no longer zero-swap-aware.
    *Files:* `cmd/obscura-swap/main_test.go` (new), `cmd/obscura-dashboard/main.go`, `cmd/obscura-dashboard/webui/*`

### Deliverables

- `pkg/swapd/tracker.go` — thread-safe per-swap both-leg state machine (stages, OBX/XNO/BTC tx hashes, refund height, last error) + tests
- Instrumented `cmd/obscura-swap` that drives the Tracker at every milestone and exposes a `--swap-status-addr` HTTP listener (`/swaps`,`/swap`,`/metrics`) so a live swap is observable, not terminal-only
- `pkg/rpc/swaps.go` + SwapProvider/SetSwapTracker wiring + public `/swaps` and `/swap?id=` routes whitelisted in `website/api/explorer.js`
- Cross-chain proof links: swapd backends (NanoRPC/MockNano Sweep, Bitcoin Redeem) return real block/tx hashes; ExplorerTx carries SwapCounterChain/SwapCounterTx + secret-revealed badge; explorer.html renders clickable XNO/BTC proof links on atomic-swap txs
- Wallet swap-execution UI: per-offer Take, a stage stepper, both-leg tx links, live refund-timer countdown, and explicit Stuck/Refunding/Aborted states in `website/wallet.html`
- `pkg/metrics` with an expvar/Prometheus `/metrics` endpoint on node + executor exporting swap/offer/chain gauges and counters
- Order-book audit trail: swapbook event ring (posted/taken/expired/failed) persisted to `swapbook-events.jsonl`, `/offers/history` route, explorer fills/expiries panel
- Fully transparent auto-liquidity (per-decision structured logs + skipped-reason counters) and node-reported hashrate field in `/explorer/summary` replacing the client-side estimate
- slog JSON structured logging (OBX_LOG_JSON) across swap + liquidity paths with stable swap_id/stage/leg/tx fields
- Swap-aware `cmd/obscura-dashboard` panel + an end-to-end selftest asserting `/swaps` reports full stage progression with populated both-leg hashes

---

## Factor 8 — Security & MEV/front-running resistance (DEX / cross-chain atomic swaps)

**Current score: 42/100** — **Effort to 100: XL**

### Gaps

- Single operator Nano RPC fully trusted (Confirmed/Receivable/Balance believed verbatim; no quorum/multi-source/SPV).
- waitForReceivable accepts a lock on first 'receivable' sighting and never calls Confirmed, so a non-cemented send can trigger irreversible OBX settlement.
- No real 2-party take/accept P2P flow: doAtomicSwap funds AND claims locally so the adversarial secret-reveal race is unexercised.
- MEV/front-running of the public OBX claim (sA extractable via commit.Extract from mempool) — no private relay, no fee-bumping, no claim confidentiality.
- Refund timelock is a single coarse fixed Height()+200 not derived from per-chain confirmation depth; cross-chain timelock asymmetry unmodeled.
- Operator supplies RPC AND generates work AND processes the sweep — can withhold/front-run the sweep.
- Offer-book DoS: 12-bit PoW trivial, per-offer Schnorr verify under 50 msg/s + 50000-entry book; no maker bond, cancellation, or reputation.
- No consensus guard against pre-signature nonce (R) reuse across two swaps under the same key (key-leak).
- BTC HTLC leg is MockBitcoin only — no bitcoind/witness/RBF/locktime validation against a live chain.
- Nano amount interface is uint64 (caps ~1.8e19 raw) while real XNO is 128-bit raw; mainnet amounts truncate.

### Plan to 100

1. **Close the non-cemented-lock hole: require Confirmed before settling OBX** — *Effort: S*
   In `cmd/obscura-swap/main.go waitForReceivable`, after seeing a receivable, DO NOT return the hash immediately. Loop calling `nano.Confirmed(hash)` until it returns true (or timeout), and only then return the lockID. The comment 'brief settle wait, then accept' and the bare `return hash` are the bug. Also gate doAtomicSwap entry on a final `Confirmed(lockID)` check so the OBX FundSwap/claim never fires against an uncemented send. Add a `NanoClient.ConfirmedAmount(lockID)` (hash->confirmed amount) so the executor can also assert the cemented amount matches the offer before settling.
   *Files:* `cmd/obscura-swap/main.go:waitForReceivable`, `cmd/obscura-swap/main.go:doAtomicSwap`, `cmd/obscura-swap/main.go:runLive`, `pkg/swapd/nano.go:NanoClient`

2. **Multi-RPC quorum NanoClient (kill single-operator RPC trust)** — *Effort: L*
   Add `pkg/swapd/quorumnano.go`: a QuorumNano that wraps N independently-configured NanoRPC endpoints and implements NanoClient. Confirmed returns true only if >=M of N agree the block is cemented; Receivable returns a (hash,amount) only if >=M agree on the same hash AND identical raw amount; Balance returns the median/agreed value and errors on disagreement beyond a tolerance. Sweep broadcasts the signed `process` to ALL endpoints (so no single operator can withhold). Wire a repeatable `--nano-rpc` flag in `cmd/obscura-swap flagSet/runLive` so operators pass several endpoints; require a minimum quorum size to enable live mode. This directly removes the 'one operator answer believed verbatim' trust and the withhold-the-sweep vector.
   *Files:* `pkg/swapd/quorumnano.go` (new), `pkg/swapd/nanorpc.go:Confirmed`, `pkg/swapd/nanorpc.go:Receivable`, `pkg/swapd/nanorpc.go:Balance`, `cmd/obscura-swap/main.go:flagSet`, `cmd/obscura-swap/main.go:runLive`

3. **Real 2-party take/accept P2P protocol (replace doAtomicSwap self-play)** — *Effort: XL*
   Add msgSwapTake / msgSwapAccept / msgSwapProof message types in `pkg/p2p/p2p.go handle()` alongside msgSwapOffer. Define a swap session in a new `pkg/swapsession`: taker sends a take referencing an Offer.ID() with its key share + nonce + PoP; maker replies accept with its share + R + T; both derive K=A+B and the joint XNO pub independently (never one process holding both a,b,sA,sB). Split doAtomicSwap into funder-only and claimer-only halves driven by received messages so funder and claimer are distinct peers. The taker funds XNO, the maker funds the OBX SwapOut, the taker claims (revealing sA), the maker extracts and sweeps — each side only ever holds its own secret. Add session timeout/abort that triggers the OBX refund path. This is the change that actually makes the adaptor/atomicity guarantees real instead of simulated.
   *Files:* `pkg/swapsession/session.go` (new), `pkg/p2p/p2p.go:handle`, `pkg/p2p/p2p.go:msgSwapOffer` (add msgSwapTake/Accept/Proof), `cmd/obscura-swap/main.go:doAtomicSwap` (split into fund/claim roles), `pkg/swap/swap.go:CoSignClaim` (drive from two parties' halves)

4. **Per-chain confirmation-depth timelocks + asymmetry safety margin** — *Effort: M*
   Replace the fixed `unlock := c.Height() + 200` in doAtomicSwap with a derived window: `refundHeight = currentHeight + ceil(nanoCementSeconds / OBX_TargetBlockTime) + safetyMargin`, where nanoCementSeconds is a configured worst-case XNO cementation bound. Enforce the invariant that the OBX claim window MUST outlast the time needed to (a) cement the XNO lock and (b) reveal+sweep, AND that the funder's refund cannot open until the counterparty provably can no longer claim — model the two-leg asymmetry explicitly. Add a config block (`config/params.go`) for swap timelock parameters so it is consensus-visible, not a magic constant. Add a test in `pkg/swap` that a race-with-refund (claim arriving one block before unlock vs refund at unlock) resolves deterministically.
   *Files:* `cmd/obscura-swap/main.go:doAtomicSwap` (unlock derivation), `pkg/config/params.go` (swap timelock params), `pkg/swap/swap.go:SwapOutput.VerifyClaim/VerifyRefund`, `pkg/swap/swap_test.go` (new race test)

5. **MEV / claim front-running mitigation on the OBX leg** — *Effort: L*
   The OBX claim tx reveals sA and goes through the public mempool; a watcher runs commit.Extract and races the XNO sweep. Mitigations specific to this codebase: (1) make the maker sweep XNO BEFORE the taker's OBX claim is broadcast where the protocol order allows, or bind a short exclusivity — i.e. have the claimer pre-commit so the sweeper (who already knows sB) needs no mempool observation; the real fix is ordering: the party who will sweep XNO should be the one who learns sA from a claim they themselves submit. (2) Add a private-submission path: a node-direct submit (skip Dandelion stem/fluff public relay) for the claim tx so it is mined without broad mempool exposure — extend mempool/p2p to accept a direct-to-miner claim. (3) Add fee-bumping/replacement for the OBX claim so an honest sweeper can outpace a front-runner. Document the residual race and prove via test (`pkg/swapsession`) that a watcher extracting sA cannot beat the legitimate sweeper given the timelock margin.
   *Files:* `pkg/swapsession/session.go` (sweep-before-reveal ordering), `pkg/p2p/p2p.go` (direct claim submission path), `pkg/mempool` (claim fee-bump/replace), `cmd/obscura-swap/main.go:doAtomicSwap` (sweep/claim ordering)

6. **Harden the offer book against DoS/griefing** — *Effort: M*
   Raise OfferPoWBits from 12 to a meaningful target (e.g. 20-24, tuned so an offer costs seconds not ~4096 hashes) in `pkg/swapbook/swapbook.go`, scaling difficulty with book pressure. Add a maker bond / on-chain stake reference so each offer is attributable to value at risk (a maker pubkey that has posted a refundable OBX bond), and a signed cancellation message (msgSwapCancel) so makers can withdraw offers and stale liquidity drains. Add per-maker offer caps and a lightweight reputation/decay so a flood of fresh maker keys is bounded. Move the per-offer Schnorr verify behind the PoW check (already first) and add a global offer-ingest rate cap distinct from the 50 msg/s peer bucket so book pollution can't ride normal gossip budget.
   *Files:* `pkg/swapbook/swapbook.go:OfferPoWBits`, `pkg/swapbook/swapbook.go:Offer` (add Bond/Cancel), `pkg/swapbook/swapbook.go:Book.Add` (per-maker cap), `pkg/p2p/p2p.go:msgSwapOffer` (add msgSwapCancel, ingest cap)

7. **Consensus guard against pre-signature nonce (R) reuse** — *Effort: M*
   Two distinct swaps reusing the same ClaimR under the same ClaimKey leak the key (two equations, same nonce). Add a per-key seen-R set in chain validation: when a SwapOut is funded, record (ClaimKey, ClaimR); reject funding a second swap that reuses a (ClaimKey,ClaimR) pair, mirroring the existing seenSpent swap dedup in validate.go. Also reject ClaimR == ClaimT-derived degenerate nonces. Add a validate_test.go case proving a second funding with a reused R is rejected at block validation.
   *Files:* `pkg/chain/validate.go` (swap output funding path, add seenSwapNonce), `pkg/chain/chain.go` (persistent seen-R index alongside c.swaps), `pkg/swap/swap.go` (document R-uniqueness requirement), `pkg/chain/validate_test.go` (new)

8. **Real Bitcoin HTLC leg (replace MockBitcoin)** — *Effort: XL*
   Implement `pkg/swapd/bitcoinrpc.go` as a BitcoinClient against bitcoind/Electrum: build the P2WSH from the existing BtcHTLCScript, fund via real tx, FundHTLC returns a real txid, Confirmed checks confirmation depth (reorg-safe N confirmations, not 'lock exists'), Redeem builds+signs the witness spending the hashlock branch (revealing the preimage = OBX adaptor secret on-chain), Refund spends the CLTV branch only at/after locktime with real nLockTime/sequence, RevealedPreimage parses the witness of the redeem tx from the chain. Add RBF/fee-bumping for redeem. Validate end-to-end on Bitcoin signet/testnet as the BTC live gate, exactly as the Nano signer was cross-checked.
   *Files:* `pkg/swapd/bitcoinrpc.go` (new), `pkg/swapd/bitcoin.go:BitcoinClient` (confirmation-depth semantics), `pkg/swapd/bitcoin_crosscheck_test.go` (new, like crosscheck_test.go)

9. **Widen Nano amount to 128-bit raw** — *Effort: M*
   Change `NanoClient.Lock(amount uint64,...)` and `Balance()` to a 128-bit-safe type (string decimal or *big.Int) throughout `pkg/swapd` (nano.go interface, nanorpc.go Lock/Balance, MockNano), so mainnet-sized XNO amounts no longer truncate at ~1.8e19 raw. Receivable already returns a string and Sweep uses big.Int, so the leak is purely the interface; thread the big amount into the offer/amount checks the executor does before settling.
   *Files:* `pkg/swapd/nano.go:NanoClient.Lock/Balance, MockNano`, `pkg/swapd/nanorpc.go:Lock/Balance`, `cmd/obscura-swap/main.go` (amount handling)

10. **Adversarial cross-chain test harness + live gates** — *Effort: L*
    Add an integration test that runs the full 2-party flow with a malicious counterparty/RPC: a lying QuorumNano member (faking Confirmed), a front-running watcher extracting sA, a maker that withholds the sweep, and a reorg of the XNO lock before cementation — asserting the honest party never loses funds in each. This converts the currently-unexercised guarantees into regression-tested ones and is the acceptance criterion that the factor is actually at 100.
    *Files:* `pkg/swapsession/adversarial_test.go` (new), `pkg/swapd/quorumnano_test.go` (new), `cmd/obscura-swap` (selftest extended to 2-process)

### Deliverables

- waitForReceivable + doAtomicSwap gated on real cementation (Confirmed) before any irreversible OBX settlement
- QuorumNano multi-RPC NanoClient with M-of-N agreement on Confirmed/Receivable/Balance and broadcast-to-all Sweep, wired to a repeatable `--nano-rpc` flag
- Real 2-party take/accept P2P protocol (msgSwapTake/Accept/Proof + pkg/swapsession) where funder and claimer are distinct peers each holding only their own secret
- Per-chain confirmation-depth-derived OBX timelocks replacing Height()+200, with consensus-visible swap timelock params and a race-with-refund test
- MEV mitigation: sweep-before-reveal ordering + direct (non-public-relay) claim submission + claim fee-bumping, with a tested no-steal guarantee under the timelock margin
- Hardened order book: higher/adaptive offer PoW, maker bond, signed cancellation (msgSwapCancel), per-maker caps, separate offer-ingest rate cap
- Consensus rejection of pre-signature nonce (R) reuse per ClaimKey, with a validation test
- Production BitcoinClient (bitcoinrpc.go) with real P2WSH/witness/CLTV/RBF and confirmation-depth Confirmed, cross-checked on signet/testnet
- 128-bit-safe Nano amount interface across pkg/swapd
- Adversarial integration tests (lying RPC member, front-runner, sweep-withholder, pre-cementation reorg) proving the honest party never loses funds

---

## Recommended implementation order

The factors share a single dominant dependency: **a genuine two-party P2P swap session** (`pkg/swapsession`/`pkg/swap/session.go`) plus a **real BitcoinClient** (`pkg/swapd/bitcoinrpc.go`). Almost every factor's path to 100 references one or both. The sequence below maximizes impact-per-unit-effort and respects those dependencies. Multiple factors are advanced by each phase because the same code lands in several plans.

### Phase 0 — Foundations & correctness (no protocol change, immediate score lift)
1. **Asset-decimals single source of truth + OBX=12 fix** (F1 step 1) — fixes a silent 1e4 rescale bug; unblocks every amount-correct path. *Cheap, high-value, no dependencies.*
2. **128-bit amount widening** across NanoClient/MoneroClient (F2 s6, F3 s2, F8 s9) — done once, satisfies four factors.
3. **Confirmation-depth gating before settlement** (F8 s1, F2 s6) — small change closing the non-cemented-lock hole.
4. **Exact-integer rate comparison / kill float64** (F1 s3) — deterministic ordering; prerequisite for honest price discovery.

### Phase 1 — The backbone: two-party swap session over P2P (the universal unblocker)
5. **Two-party session state machine with split secrets** (F2 s1, F5 s1-3, F6 s1, F8 s3) — the single most-referenced deliverable. Build `pkg/swapsession` / `pkg/swap/session.go` with HalfPresig/CombineHalves/VerifyHalfPresig and role-scoped secrets.
6. **P2P take/accept/settle message types + dispatch + session map** (F2 s2, F5 s4, F6 s1, F8 s3).
7. **Persisted swap-state machine (SwapStore, write-ahead)** (F4 s1) — the resume/refund foundation; fold into the session persistence.
8. **Wire the executor to drive the real two-party flow; rewrite selftest to two nodes** (F2 s3, F5 s8, F6 s8, F8 s10).

### Phase 2 — Settlement safety: timelocks, funding order, refund & recovery
9. **Per-chain confirmation-depth timelocks + asymmetry/ordering validator** (F2 s5, F5 s6, F8 s4).
10. **Funding-order protocol with on-chain leg verification** (F5 s5).
11. **OBX refund builder + height watcher; stranded-XNO recovery; CLI refund/recover/resume** (F4 s3-6).
12. **Claim-race / secret-extraction watcher + abort/refund-race handling** (F2 s7, F5 s7, F8 s5).
13. **In-flight take/accept/reservation state in swapbook** (F4 s7).

### Phase 3 — Real chains: Bitcoin (and Monero decision)
14. **Production BitcoinClient (bitcoinrpc.go) + presets, wired into node + executor** (F2 s4, F3 s3, F4 s8, F8 s8) — built once, satisfies four factors; regtest/signet gated.
15. **ChainAdapter unification + settlement-backend registry** (F3 s1, s5) — makes a 4th chain non-bespoke and binds tickers to real backends.
16. **Resolve XMR: ship monerorpc.go or remove XMR** (F3 s6).

### Phase 4 — Market quality & manipulation resistance
17. **Take/Quote flow that prices both legs from the book; depth/mid/spread/slippage/partial-fill** (F1 s4-5).
18. **TradeLog last-trade + TWAP fallback** (F1 s6).
19. **Book hardening: adaptive PoW, maker bond, signed cancel, per-maker caps, ingest rate cap** (F1 s7, F8 s6).
20. **Triangular cross-pair consistency check** (F1 s8).
21. **Multi-RPC QuorumNano** (F8 s2) and **consensus nonce-reuse guard** (F8 s7).

### Phase 5 — Observability, UX, and proof
22. **SwapTracker + /swaps RPC + metrics + structured slog + cross-chain proof links** (F7 s1-5,7-10).
23. **WASM swap crypto + Swap-tab take/execute UI + stage tracker + refund countdown** (F6 s1-4, F7 s6).
24. **Human-decimal offer inputs, remove BTC-mock, Web-Worker PoW, registry-driven /assets** (F6 s5, F3 s7).
25. **Wallet encryption, onboarding modal, tooltips, responsive UI** (F6 s6-7).
26. **Order-book audit trail + dashboard swap panel + node-reported hashrate** (F7 s8-9,11).

### Phase 6 — Live, adversarial validation (the actual blocker to 100)
27. **Two-machine OBX<->XNO split-secret swap on the testnet droplet; OBX<->BTC regtest swap** (F2 s8).
28. **Adversarial integration tests across all factors** — split-secret atomicity, taker-stall refund, late-claim extraction, griefing rejection, rogue-key rejection, lying-RPC member, front-runner, sweep-withholder, pre-cementation reorg (F2 s8, F4 s9, F5 s9, F6 s8, F7 s11, F8 s10).

---

## Definition of Done for 100/100

A factor is at 100 only when ALL of the following hold for it; the DEX is at 100/100 when every factor below is satisfied and the live + adversarial validation in Phase 6 passes.

**Global gates (must hold across the whole DEX):**
- No code path ever co-locates both swap halves (sA AND sB, or a AND b) in a single struct/process — proven by a compile-/test-level split-secret check.
- Every amount path is 128-bit-safe (no uint64 truncation of XNO raw) and every decimal scale derives from one authority (`AssetDecimals`, OBX=12), enforced by a drift-guard test.
- Every pricing/ordering decision uses exact integer (cross-multiplied) arithmetic; float appears only at final browser display.
- No irreversible OBX settlement occurs before the counterparty leg is confirmed to the configured depth.
- A genuine two-node, two-machine swap completes on the live testnet for OBX<->XNO and on regtest/signet for OBX<->BTC, and an aborted swap refunds/recovers cleanly with no party losing funds.
- Adversarial tests (stall, late-claim, griefing, rogue-key, lying-RPC, front-runner, sweep-withholder, reorg) all pass with the honest party never losing funds.

**Per-factor done:**
- **F1 Price discovery (22→100):** decimals authority + exact-fraction rates + Book.Quote-driven settlement + depth/mid/spread/slippage/partial-fill + TWAP fallback + manipulation-resistant book + triangular check, all test-covered; frontend fetches decimals and shows mid/spread.
- **F2 Trustless settlement (52→100):** split-secret two-party state machine over P2P + real BitcoinClient + timelock-ordering validator + 128-bit amounts + confirmation gating + abort/refund-race watcher, validated by a deployed two-machine + regtest run.
- **F3 Asset & chain coverage (38→100):** settlement-backend registry rejecting unsettleable pairs + 128-bit amounts + production BitcoinClient + maker/taker handshake + ChainAdapter unification + XMR resolved (live or removed) + registry-driven /assets + regtest/handshake integration tests.
- **F4 Refund & recovery (24→100):** write-ahead persisted SwapStore + resumable phase-driven executor + invoked OBX refund builder + auto-firing height watcher + automated stranded-XNO recovery + refund/recover/resume CLI + swapbook reservation state + real BTC CLTV refund + adversarial 2-party stall test + user-reachable web refund.
- **F5 Two-party P2P completeness (12→100):** proto schema + HalfPresig/CombineHalves/VerifyHalfPresig + persisted per-counterparty session machine + P2P transport/RPC + funding-order on-chain verification + deadline/abort + claim-extraction watcher + SwapManager + two-distinct-node tests + wallet/explorer wiring with the demo disclaimer removed.
- **F6 UX/onboarding (28→100):** working browser take/execute swap (WASM crypto, joint-address QR, stepper, refund countdown, in-flight tracking, recover/refund) + human-decimal inputs + BTC-mock removed + encrypted mnemonic + onboarding/tooltips/responsive UI + parity & two-party selftests.
- **F7 Observability (42→100):** SwapTracker + /swaps & /swap RPC + --swap-status-addr listener + metrics endpoint + cross-chain proof links + swap-execution UI + order-book audit trail + transparent auto-liquidity + node-reported hashrate + slog JSON + dashboard swap panel + end-to-end observability test.
- **F8 Security & MEV (42→100):** cementation-gated settlement + QuorumNano + real 2-party flow + confirmation-depth timelocks + MEV mitigations (sweep-before-reveal, direct submission, fee-bump) + hardened book (PoW/bond/cancel/caps) + consensus nonce-reuse guard + production BitcoinClient + 128-bit amounts + passing adversarial harness on the live gates.

---

*This is a local planning document for Obscura mainnet. It is not for publication.*
