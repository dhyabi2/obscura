# Invention Log — Block 1: Fork Choice / Reorg

Produced with the invention methodology, using the Methodology-Tree brainstorming
engine (`POST /api/v1/run`) for every "better-than-baseline" idea.

## 1. Challenge
The node only accepted `tip+1`, so a same-height fork **permanently partitions**
the network. Needed: a fork-choice rule + reorg that converges all honest nodes.

## 2. Baseline best (researched)
Bitcoin/Monero **most-cumulative-work** chain selection with reorg. Features:
F1 cumulative-difficulty metric · F2 side-branch tracking by hash · F3 reorg
(rollback to fork, re-apply heavier) · F4 validate-before-apply · F5 reorg-depth
limit/checkpoints · F6 orphan buffering · F7 first-seen tie-break.

## 3. Brainstormed "better-than" ideas (engine output, condensed) + evaluation
Scoring: Impact / Soundness / Implementable-in-Go / beats-baseline.
Rejected up front: anything needing **stake/PoS** or **trusted parties/keys**
(violates pure-PoW, trustless premise).

| Sub-challenge | Engine's top ideas | Verdict |
|---|---|---|
| Metric (Q1) | work-weighted-by-inclusion-delay; stake-committed; orphan-exposure discount; uncle-cap; timestamp-committee slashing | **Keep baseline cumulative-difficulty.** All five need authenticated peer timing or stake → centralization/complexity. REJECT. |
| Tie-break (Q4) | **lowest-block-hash**; multi-peer XOR; forward-only delay; utxo-age; checkpoint snapshots | **ADOPT lowest-block-hash.** Unbiasable, eclipse-neutral, O(1), strictly beats first-seen. (XOR/delay add P2P complexity for marginal gain.) |
| Rollback (Q2) | **epoch-segmented snapshots + replay ≤N**; immutable delta chain; COW shadow states | **ADOPT snapshot+replay** (here: full in-memory state snapshot → reset → replay branch, restore on failure). Correct given the add-only accumulator can't remove elements. Engine's own practicalRating 35 flags the storage/replay cost → mitigated by bounded reorg depth. |
| Finality (Q3) | difficulty-weighted decentralized checkpoints; **work-budget/max-reorg-depth cap**; MTP gate; state-commitment bottleneck; reorg-penalty bonds | **ADOPT max-reorg-depth cap** (no trusted keys) + we already have the **MTP gate** + **accumulator/UTXO state-commitment** in the header. REJECT penalty-bonds (changes coin semantics). |
| Orphans (Q5) | forward-only shadow chains; **delta replay from common ancestor**; mesh flooding; periodic checkpoints; uncle rewards | **ADOPT block-tree + orphan pool**, reorg via replay from genesis/snapshot. REJECT uncle rewards (inflation/game theory). |

## 4. Decision (what gets coded)
A most-cumulative-work fork choice with **three concrete improvements over the
Bitcoin/Monero baseline**, each traceable to a brainstormed, ranked idea:

1. **Lowest-block-hash tie-break** on equal cumulative work (Q4#2) — removes the
   first-seen / network-position advantage that aids selfish mining and eclipse.
2. **Bounded reorg depth** (`MaxReorgDepth`) (Q3#2) — practical finality with **no
   trusted checkpoints and no PoS**; deep-reorg attacks are rejected outright.
3. **Block-tree + orphan pool + snapshot/replay reorg** (Q2+Q5) — convergence
   despite the add-only accumulator: snapshot live state, replay the heavier
   branch, restore on any validation failure (atomic, never half-applied).

Implemented in `pkg/chain/forkchoice.go` (+ `Accumulator.Clone`). Tested:
heavier branch wins, equal-work resolves by lowest hash, deep reorg rejected,
orphan connects when its parent arrives.
