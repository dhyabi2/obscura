# Obscura 4-node load test — performance findings & the 10x plan

A 4-node Dockerized load harness (`docker-compose.yml`, `cmd/obscura-loadgen`,
`scripts/loadtest/`) was built to drive confidential transactions and chart
throughput/degradation. Driving it surfaced concrete performance issues and a
ranked plan to raise throughput ~10x. Brainstormed via the methodology API + two
parallel code-analysis agents.

## Baseline
~3-5 confirmed confidential tx/sec (one mining node + three validating followers,
8-logical-core host shared by 4 containers + the load generator).

## Root-cause: where the ceiling comes from
1. **Double validation.** Every tx is verified at least twice: once on mempool
   admission (`mempool.Add → ValidateStandaloneTx`) and again when the block is
   validated (`chain.validateBlockLocked` re-runs `validateTxLocked` on every tx,
   incl. `AddBlock`'s local mining path). Range-proof verify (`commit.VerifyRangeBytes`,
   ~256 scalar-mults) is ~35ms/tx and dominates — so a block of N txs costs ~N×35ms
   to validate, capping confirm throughput near **~28 tx/s on one core**.
2. **O(mempool) re-validation per block.** `mempool.Remove` re-ran
   `ValidateStandaloneTx` on *every* remaining mempool tx after each block — full
   proof re-verification of the whole pool, per block.
3. **PoW instability on a contended host.** Single-thread RandomX hashrate varies
   with host load, so LWMA oscillated (64→1200+) → multi-minute stalls.
4. **p2p relay deadlock / propagation lag under load.** Dandelion++ stem-relay
   spawns a goroutine per tx that writes under a per-peer write lock (`p.wmu`);
   under high submit rate these pile up behind a slow (CPU-starved) peer's blocked
   `conn.Write`, and `mineLoop`'s synchronous `BroadcastBlock` then blocked behind
   them → **mining halted**. With very fast (low-difficulty) blocks, followers also
   fall behind propagation and peers drop.

## Implemented (this pass — mainnet-safe unless noted)
- **`mempool.Remove` O(mempool) → O(conflict)** (`pkg/mempool/mempool.go`): only
  evict pending txs that share a spend-key with the block; no proof re-verification
  (a still-unspent tx's proofs remain valid). Removes the per-block full re-scan.
  *Mainnet-safe.*
- **Async block broadcast** (`cmd/obscura-node/main.go` mineLoop): `go
  BroadcastBlock(...)` so a slow/back-pressured peer can never stall block
  production. *Mainnet-safe.*
- **Devnet PoW knobs** (`pkg/config/params.go`, env-gated, mainnet defaults
  unchanged): `OBX_TARGET_BLOCK_TIME`, `OBX_GENESIS_DIFFICULTY`, and
  `OBX_FIXED_DIFFICULTY` (pegs difficulty so block production is steady on a
  contended host — eliminates the LWMA oscillation for load tests).
- Load generator: parallel multi-wallet building, fan-out output splitting,
  submit-to-miner, in-flight dedup, coinbase-maturity match.

Result: at a low pegged difficulty the miner produced ~5 blocks/s × ~2-3 tx =
**~12 tx/s (≈3x baseline already)** before the propagation/relay limits bit.

## The remaining 10x (ranked — node-side, scoped with file:line)
1. **Skip block re-validation of already-admitted mempool txs** — a validated-txid
   cache on the chain (invalidated on tip change), or a devnet `--trust-mempool`
   flag: in `validateBlockLocked` skip the expensive proof checks
   (`VerifyRangeBytes`, ownership/value/key-image, anon-spend, conservation) for
   txs already verified at admission, but KEEP the cheap in-block structural /
   double-spend / UTXO checks (`seenSpent`, `c.utxo`, `c.tags`, maturity, prime
   dedup). **~2x**, removes the dominant double-verify. (`pkg/chain/validate.go:120-132`,
   `pkg/chain/forkchoice.go:122`).
2. **Parallelize per-tx proof verification** across cores with `errgroup`
   (verification is pure/read-only; keep the stateful `seenSpent`/UTXO/tag updates
   serial). **~4-8x** on this 8-core host. Mainnet-safe.
   (`pkg/chain/validate.go`, `pkg/commit/rangeproof.go`).
3. **Bound/queue Dandelion stem relay** (per-peer single-writer send queue that
   drops on backpressure) so high tx rate can't pile goroutines on `p.wmu`.
   Robustness, not raw speed. (`pkg/p2p/dandelion.go`, `pkg/p2p/p2p.go:533-584`).
4. **Devnet `--fast-verify`** that skips proof verification entirely — >10x but
   stops measuring real crypto cost; use only to isolate the non-crypto pipeline.

#1 + #2 together (both keeping verification real) clear the 10x while staying
correct; #3 is the robustness fix for sustained high load. RSA-2048 accumulator
cost is **constant per output** (one fixed-size `Exp`), so it does NOT degrade as
the chain grows — keep load tests on the `rsa2048` backend, not the class group.

## How to run
`scripts/loadtest/run.sh [TARGET]` → builds the image, starts 4 nodes, runs the
load generator while sampling node + `docker stats` metrics, then renders
`scripts/loadtest/out/report.html` (self-contained SVG charts) + `analysis.txt`
(degradation analysis: TPS trend, mempool, per-node sync, accumulator size,
build/submit latency, container CPU/mem).
