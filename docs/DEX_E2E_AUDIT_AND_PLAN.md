# DEX E2E Audit + Build Plan (buy/sell · miner XNO · real counterparty)

**STATUS: audited (4 dimensions); build IN PROGRESS.** Mainnet.
Consolidates the fund-flow, miner-XNO-wallet, UI/UX, and real-counterparty audits.

## The core finding
The swap **crypto + state machine + fund-safety guards (F-1/F-B/F1/F2/F3) are sound**. Everything *around* the XNO leg for a REAL buy/sell is prototype:
- **uint64 can't hold XNO.** `0.00001 XNO = 1e25 raw > uint64 max (1.84e19)`. The `NanoClient` interface's `amount uint64` **saturates** even at the test scale; the maker's amount check "passes" only by coincident saturation. (CLI dodges it with `big.Int`+strings; the in-node path doesn't.) **FOUNDATIONAL.**
- **Offer XNO unit ≠ raw.** Offers use `AutoLiquidityDecimals[XNO]=12`; the in-node take feeds that `1e12` straight into `nano.Lock` as raw (= 1e-18 XNO). Missing `×10^18`.
- **Miner sweep dest is a placeholder string** `"obx-node-xno-sweep-dest"` (`swapwire.go:223`) — fake under MockNano, `DecodeNanoAddress`-rejected under real Nano. No recoverable miner XNO wallet exists. Withdraw unwired in both modes.
- **One node, both roles, one Nano wallet** — can't serve a counterparty it doesn't custody for. Real topology = two role-split nodes (seller=maker-only sweep; buyer=taker-only with own funded `--nano-wallet`).
- **No counterparty discovery** — offers gossiped without source peer; `/swaps/take` hits `PeerAddrs()[0]`.
- **Browser has no XNO send** — a real buyer can't pay XNO from the website; realistic taker = a buyer-run CLI/node.
- **Settlement reconciles taker-side only** — maker never decrements its book / records a trade; `Trade.SwapKey` = session id, not on-chain key.
- **In-node SwapState not persisted** (`StateDir:""`) → crash mid-real-swap can freeze XNO with no logged recovery keys.
- **UI honesty** — must not present MockNano (fake XNO) as real.

## Phased build (dependency order)
- **P1 — Matching/fill engine + trade tape** — ✅ DONE (`pkg/swapbook/fillstate.go`).
- **PA — FOUNDATIONAL XNO correctness** (🔧 next): widen `NanoClient` amounts (`Lock/LockInfo/Sweep/Balance/Send`) + `swapsession.XNOAmount` to `*big.Int`/string (128-bit); add offer-unit(1e12)→raw(1e30) `×10^18` conversion in the in-node take; derive the miner XNO account from `miner.seed` (`Obscura/xno-proceeds/v1`, distinct from `maker/v1`) → real `SweepDest()` + `--xno-sweep-dest` override + startup validation.
- **PB — Miner XNO wallet** (after PA): `GET /xno/account` (addr/balance/receivable, no secret), operator-gated `POST /xno/withdraw` (sign in-process); `wallet.html` XNO panel — show address, reveal seed once + forced "I saved it", explain why, balance, withdraw; real-vs-test honesty banner.
- **PC — Real two-party E2E**: role-split maker/taker Nano caps; offer→peer provenance + correct take routing; `AcceptInit` offer-reservation + in-band fee; maker-side `CommitTrade` + on-chain `SwapKey` join; persist in-node `SwapState`.
- **PD — Chart** (uses P1 tape + `/candles`/`/stats`): candlestick/OHLC + volume + MA/EMA/RSI/MACD/Bollinger/VWAP + crosshair/tooltip + zoom/pan + timeframes + persistence + stat header.
- **PE — Simulator** (uses P1 matcher for real fills): `Agent` interface (MM/informed/noise/momentum/meanrev/arb/whale), Poisson/Hawkes arrivals, LogNormal/Pareto sizes, GBM/OU/jump + GARCH + fat tails, Kyle-λ impact, events/regimes, metrics + stylized-facts validation.
- **PF — Breadth/hardening**: multi-rung settlement fan-out, streaming market data, cancel-gossip, tape persistence + indexing, stops/iceberg/modify, per-pair indexing + time priority, rate limits.

See `docs/DEX_60_FACTOR_RATING.md` for the chart/sim/orderbook factor scores.
