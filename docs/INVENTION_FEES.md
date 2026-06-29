# Invention Log — Block 20: Dynamic Fee Estimation

Produced with the invention methodology. (The Methodology-Tree brainstorming API
timed out on this query — deep multi-round reasoning exceeded the curl window —
so the design below was reached by the methodology's own steps: survey the best
existing solutions, table their features, and choose/improve critically.)

## The challenge
A flat minimum fee (`MinFeePerByte`) is simple but wrong under load: when blocks
fill, a flat-fee tx can stall indefinitely, and users have no signal for how much
to pay. We need a wallet to answer **"what fee-per-byte gets me confirmed within
`target` blocks?"** — for a *pure-PoW privacy coin*, so: no PoS/stake, no trusted
oracle, computable by a light client from recent blocks, resistant to miner fee
manipulation, and graceful when blocks are empty.

## Best existing solutions (surveyed)
| Solution | Statistic | Inputs | Weakness for us |
|---|---|---|---|
| Bitcoin `estimatesmartfee` | tracks which fee-rates confirmed within N blocks | long history of confirmations + live mempool | needs persistent confirmation-tracking state; mempool view miner-stuffable |
| Monero dynamic fee | fee floor scales with median block weight over a long window | block weights | robust but coarse — a network *minimum*, not a per-target *estimate* |
| Mempool-backlog (many wallets) | sum of sizes above each rate ÷ block size = blocks of backlog | full live mempool | not light-client friendly; a miner/attacker can stuff the mempool |

## Chosen design — median-of-per-block-percentiles, congestion-gated
`pkg/fee/Estimate(blocks []BlockFees, target int, floor uint64) uint64`:

1. **Per block, find the fee-rate that was *necessary* to be included.**
   - If the block was **under-full** (`Fullness < CongestionThreshold = 0.80`),
     even the minimum fee got in → the necessary rate is the **floor**. This is
     what makes the estimator collapse to the floor on quiet/empty chains
     (graceful degradation) instead of parroting back whatever fees people
     happened to overpay.
   - If the block was **congested**, sort its included fee-rates and take a
     **target-dependent percentile**: urgent target (1 block) → 80th pct (pay to
     sit safely above the inclusion cutoff); patient target (≥6) → 25th pct (near
     the cutoff). Monotonic: more urgent ⇒ higher suggestion.
2. **Combine blocks by the MEDIAN of their per-block samples** (window = 30
   blocks). This is the manipulation-resistance lever: a miner who stuffs *one*
   block with sky-high self-paying fees moves at most one sample, and the median
   of 30 is unmoved. No identity or stake required.
3. **Floor and cap.** Result is floored at the network minimum and capped at
   `MaxFeeMultiplier (=1000) × floor`, so a suggestion is always sane and bounded.

All inputs (`Rates` = per-tx fee-per-byte; `Fullness` = bytes/`MaxBlockBytes`) are
derivable from the recent blocks alone, so a **light client can reproduce the
estimate** by fetching the last 30 blocks — no global mempool, no node trust.

## Manipulation analysis
- **One-block fee stuffing** → defeated by the cross-block median (test
  `TestEstimateManipulationResistance`: an attacker block with 100000× fees does
  not move the suggestion).
- **Persistent majority manipulation** → a miner controlling >50% of blocks for
  the whole window could bias the median, but that is the same threshold at which
  they already control consensus; fee bias is the least of the problems.
- **Empty-block lying** → can only push the estimate *down toward the floor*,
  which is safe (you simply pay the floor; if real congestion exists, congested
  blocks in the window pull it back up).
- **Unbounded blow-up** → prevented by the `MaxFeeMultiplier` cap.

## What is built
- `pkg/fee/fee.go` — pure, dependency-free `Estimate` + `BlockFees`,
  `CongestionThreshold`, `MaxFeeMultiplier`.
- `chain.RecentFeeSamples(n)` — builds `[]fee.BlockFees` from the last n blocks
  (excludes coinbase; per-tx fee÷size; fullness vs `MaxBlockBytes`).
- RPC `GET /feerate?target=N` → `{target_blocks, fee_per_byte, floor_per_byte,
  window_blocks}`; `rpc.Client.FeeRate(target)`.
- Wallet CLI: `obscura-wallet feerate` (prints suggestions for targets 1/2/6) and
  `obscura-wallet send --fee auto` (queries the node, measures the tx's serialized
  size — fixed-width fee field ⇒ size is independent of the fee value — and sets
  `fee = fee_per_byte × size`, floored).
- Tests `tests/critical/feeest/`: empty/quiet → floor; congested → above floor &
  monotonic in urgency; one-block-stuffing resistance; bounded; `/feerate` RPC.

## Block 21 — Fee-aware block selection (the other half of a fee market)
Estimation is meaningless if miners don't actually prioritize by fee. Before this
block, `mempool.Select` returned transactions in Go map order — fees were ignored
entirely, so paying more bought nothing. Now `Select`:

- Sorts pending txs by **fee-rate (atomic/byte) descending**, with a fully
  deterministic tie-break (then absolute fee desc, then txid asc) so independent
  miners build **identical templates from identical mempools** — no template
  divergence, no covert ordering channel.
- Greedily fills up to the **block byte budget** (`MaxBlockBytes -
  CoinbaseReserveBytes`), skipping a tx that doesn't fit and continuing to scan
  for a smaller one (so a single huge low-fee tx can't starve the block).

Note on CPFP: child-pays-for-parent was considered and is **not applicable here**
— transaction inputs resolve only against the *confirmed* UTXO set, so a tx
spending an unconfirmed output is rejected at mempool admission. There are no
in-mempool parent→child packages for CPFP to act on. The user-facing way to
un-stick a low-fee tx is therefore replace-by-fee (a future block), not CPFP.

Tests `tests/critical/feeselect/`: fee-rate ordering, determinism across calls
(including a fee tie), and byte-budget bound.

## Block 22 — Replace-by-fee (RBF): un-sticking a low-fee tx
Fee estimation + fee-aware selection mean a *too-low* fee leaves a tx stuck. With
no CPFP possible here, the recourse is RBF: re-broadcast a higher-fee tx that
re-spends the **same inputs**, superseding the original in the mempool.

**Mempool policy** (`pkg/mempool`, adapted from Bitcoin BIP125). Conflict tracking
moved from a boolean `spent` set to a `spentBy` map (spend-key → owning txid) so a
new tx can find exactly which pending txs it conflicts with. A conflicting tx is
accepted **only if** it qualifies as a replacement (`checkReplacementLocked`):
1. it displaces at most `MaxReplacementConflicts` (=100) txs — bounds attacker work;
2. its fee-**rate** is strictly higher than the best tx it replaces — so miners and
   relays genuinely prefer it;
3. its absolute fee covers **all replaced fees plus its own relay bandwidth** at
   the minimum rate — the network is never worse off, and churning the mempool
   costs the attacker real money each round.
A spend already **confirmed on-chain** is never replaceable (fatal). On success
the replaced txs are evicted and the new one reserves the spend-keys.

**Wallet** (`wallet.BumpFee`). `CreateTransaction` was refactored to delegate to an
internal `buildSpend(selected, …)`; `BumpFee(prev, dest, amount, newFee)` finds the
owned outputs referenced by `prev`'s inputs and rebuilds with **exactly those
inputs** at a higher fee (the extra fee comes out of the change). Reusing the same
inputs is what guarantees the result *replaces* `prev` rather than becoming a
second, independent payment. It refuses a fee ≤ the original and refuses anon
(non-transparent-input) txs.

**Privacy.** A replacement shares input references with the original, so the two
are publicly linkable — inherent to any re-spend; bump only a genuinely stuck tx.

Tests `tests/critical/rbf/`: a stuck tx is replaced (size→1, original gone); an
insufficient bump (higher rate but under fees+relay) is rejected and the original
survives; `BumpFee` rejects a non-increasing fee.

## Block 23 — Sent-transaction persistence + CLI `bump`/`history`
RBF was library-only because the wallet didn't remember what it had sent. Now it
does. `wallet.SentTx{TxID, Dest, Amount, Fee, Raw, Height, Replaced}` records every
outgoing payment; `RecordSent` appends one, `SentHistory`/`FindSent` read them.

- **Confirmation tracking.** `ScanBlock` marks a recorded send `Height` when its
  txid appears in a block, so history shows pending vs confirmed.
- **Persistence.** The history is appended to `MarshalState` after the outputs.
  `RestoreState` reads it *if present* — a state file written before this section
  existed (ending right after the outputs) still restores, with an empty history
  (`tests/critical/senthistory/TestBackwardCompatNoHistorySection`).
- **CLI.** `send` now records the payment (and re-saves state) after submitting.
  `obscura-wallet history` lists outgoing payments with status
  (PENDING / confirmed @ H / replaced). `obscura-wallet bump --txid T --fee OBX`
  looks up the stored payment, rebuilds the replacement with `BumpFee` (same
  inputs, higher fee), submits it, marks the original `Replaced`, and records the
  new one — refusing to bump an already-confirmed or already-replaced payment.

Tests `tests/critical/senthistory/`: record + state round-trip (incl. raw-tx
re-deserialization), confirmation-by-scan, bump updates history (old replaced +
new appended), and old-format backward compatibility.

## Future
- Track *confirmation outcomes* (did a fee-rate actually confirm within target?)
  for Bitcoin-grade accuracy once the chain has real fee pressure history.
