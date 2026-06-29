# DEX 60-Factor Rating — Chart · Simulation · Order Book

**STATUS: rated; implementation IN PROGRESS (dependency order).** Live mainnet.
Full per-factor justifications + gap lists are in the three assessment transcripts; this is the scorecard + build order.

## Averages
| Area | Avg /100 |
|---|---|
| Price Chart (20 factors) | **10.6** |
| Market Simulation (20 factors) | **9.3** |
| Order Book (20 factors) | **26.0** |

## Price Chart (20) — scores
1 types 8 · 2 timeframes 0 · 3 zoom/pan 2 · 4 realtime 22 · 5 volume 0 · 6 MAs 0 · 7 oscillators 0 · 8 crosshair 0 · 9 Y-axis 18 · 10 X-axis 12 · 11 polish 35 · 12 drawing 0 · 13 OHLC backend 5 · 14 trade-driven price 8 · 15 history/persist 5 · 16 multi-pair 3 · 17 responsive 25 · 18 perf 45 · 19 a11y 5 · 20 stat-header 18

## Market Simulation (20) — scores
1 agent-types 5 · 2 heterogeneity 8 · 3 arrivals 6 · 4 size-dist 12 · 5 price-impact 4 · 6 slippage 6 · 7 price-process 18 · 8 vol-clustering 0 · 9 fat-tails 2 · 10 real-fills 8 · 11 MM-inventory 0 · 12 spread-dyn 10 · 13 adverse-sel 0 · 14 events 0 · 15 whales 0 · 16 regimes 0 · 17 reproducibility 55 · 18 configurability 35 · 19 metrics 15 · 20 stylized-facts 2

## Order Book (20) — scores
1 limit 55 · 2 market 45 · 3 stop 0 · 4 iceberg 0 · 5 TIF 20 · 6 match/priority 35 · 7 partial-fills 15 · 8 trade-tape 25 · 9 L2 40 · 10 L3 30 · 11 streaming 5 · 12 cancel 80 · 13 modify 10 · 14 bulk-ops 30 · 15 mkt-stats 10 · 16 anti-spoof 35 · 17 STP 15 · 18 fairness 30 · 19 persist/cancel-gossip 15 · 20 throughput/fees 30

## Build order (dependency-driven)
**P1 — KEYSTONE: matching/fill engine + trade tape** (`pkg/swapbook` + `pkg/rpc`). Binding offers (Remaining/Status), atomic reserve-on-take walking multiple rungs, partial fills, an executed-`Trade` ring buffer joined to on-chain `SwapEvent` by SwapKey, `lastPrice`/`/trades`, market/IOC/FOK/post-only order types, self-trade prevention, min order size. → unblocks OB factors 1,2,5,7,8,16,17,18 + Sim factor 10 + Chart factors 13,14.
**P2 — Market data + chart**: OHLC candle aggregation from the tape + volume + 24h stats (`/candles`,`/stats`); candlestick/line/area chart with timeframes, crosshair+tooltip, zoom/pan, MA/EMA + RSI/MACD/Bollinger/VWAP, Y/X axes + gridlines, persistence, stat header (vanilla canvas or Lightweight-Charts).
**P3 — Simulator**: `Agent` interface + MarketMaker(Avellaneda-Stoikov)/Informed/Noise/Momentum/MeanRev/Arbitrageur/Whale; Poisson/Hawkes arrivals; LogNormal/Pareto sizes; GBM/OU/jump price + GARCH vol + Student-t tails; Kyle-λ impact; real fills against the matcher; events/shocks/regimes; metrics + stylized-facts validation harness.
**P4 — Breadth/hardening**: per-pair indexed sides + cached ID + arrival-seq time priority; L2 aggregation; SSE/WS streaming; cancel-gossip (`msgSwapCancel`) + book persistence; stop/iceberg/modify/bulk-ops; fee/rebate; rate limits.
