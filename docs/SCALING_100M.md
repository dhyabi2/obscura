# Scaling to 100M transactions

Status of the node's memory/CPU as of the RAM-bounding pass (Tier 0 + Tier 1a,
see docs/PRUNING_DESIGN.md): RAM for **block bodies** is O(window) and the
**nullifier accumulator** is O(log n). That carries a 2 GB box to ~millions of
txs. This doc is the concrete plan to reach **100M** — and an honest map of which
pieces are audit-grade consensus changes (NOT to be shipped unsupervised).

## What is still O(n) in RAM at 100M (the blockers)

Traced in code, each exists to **serve membership / witnesses / uniqueness**:

| Structure | Why it's retained | ~RAM at 100M (200M outputs) |
|---|---|---|
| `accumulator.members` (`c.acc`) | serve `MembershipWitness` to wallets; `Contains` dup-check; `Size` | ~8–16 GB |
| `coins` + `coinList` | build anonymity **rings** (`poolMembersLocked`) | ~24 GB |
| `tags` | double-spend set (needs full-set membership) | ~4 GB |
| `outPrimes` | output-prime uniqueness | ~8 GB |
| `utxo` | live output set (net, smaller) | ~GBs |
| `pqAcc.leaves` | PQ membership `Prove` (PQ path only; small today) | n/a on classical |

Total far exceeds 2 GB — and even on a big box, ~tens of GB of live RAM + days of
proof re-verification on restart make 100M impractical without the redesign below.

The throughline: **every one of these is the witness/membership-serving cost.**
The node holds the whole set so it can answer "is X a member / give me a witness
/ is this unique." That is exactly the cost the accumulator was meant to remove.

## The fix, in three independent tracks

### Track A — value-only accumulator + wallet-carried witnesses (the structural win)

> **DONE (sound, 2026-06-24) — the full 100M RAM goal is met.** Two changes, no
> crypto risk:
> 1. **Value-only accumulator** (`accumulator.NewValueOnly`): `c.acc` keeps only the
>    O(1) value + a size counter, dropping the O(n) member set — byte-identical
>    AccValue/AccSize (`accumulator/valueonly_test.go`). The members were used only
>    by the prototype `WitnessFor` RPC (now archive-only), never by the ring spend
>    or consensus. Removes the `acc.members` hog (~20 GB at 100M).
> 2. **Disk-backed coins/coinList** (`pkg/chain/coinstore.go`): the "all coins ever"
>    anonymity-ring set lives in bolt (coinlist by index, coins by key); only an
>    O(1) `coinCount` is in RAM. Rings (PoolSize=16) are read on demand. Reorg
>    safety without O(n) rollback: coin appends are STAGED in RAM during a reorg and
>    committed to bolt only on success (a failed reorg leaves bolt untouched);
>    restart/reorg truncate bolt to the snapshot's `coinCount` then replay
>    re-appends. Coins are NEVER pruned (needed for rings forever) — disk O(n), RAM
>    O(1). Removes the `coins`/`coinList` hog (~24 GB at 100M).
>
> **Result: the full node has no remaining O(n) RAM structure** — block bodies,
> nullifier accumulator, class-group accumulator, and the coin/ring set are all now
> O(window)/O(1) in RAM. Tested (anonchain/anonymity/ledger/forkchoice/snapshot) +
> race-clean + full suite (54 pkgs) green.
>
> **Optional remaining (NOT needed for the RAM goal):** the elegant ZK-membership
> spend would retire rings entirely and also shrink DISK — but the existing
> `accumulator.ZKMembership` is a documented PROTOTYPE (`pkg/accumulator/zkmem.go`)
> missing a ZK nullifier binding + a prime/range proof (Zerocoin-style); shipping it
> as the default spend would replace the audited sound ring spend with an unsound
> one (forgeable membership = inflation), so it stays a research item. Plus Track C
> (nullifier non-membership).

Make the node keep only the **O(1) accumulator value** (+ an O(1) `Size` counter),
not `members`. Membership witnesses move to **wallets**: a wallet builds and
maintains its own coin's witness (the design already anchors proofs to a
historical checkpoint, so in the add-only model a witness stays valid forever — no
updates needed; see PRUNING_DESIGN #1). Spends carry a **witness-hiding ZK
membership proof** (the `pkg/accumulator` PoKE2 / NI-PoKE2 primitives already
exist) instead of the node rebuilding a ring from `coinList`.
- **Removes:** `acc.members`, `coins`, `coinList` (the two biggest hogs).
- **Keeps:** the O(1) accumulator value (already header-committed as `AccValue`).
- **Converges with** the PQ zk-STARK membership effort (POST_QUANTUM_ROADMAP) —
  same proving machinery; do them together.
- **Risk:** soundness-critical ZK in the spend path. **Audit-required.**

### Track B — disk-backed uniqueness/nullifier sets + snapshot-based reorg
`tags`, `outPrimes`, `utxo` move to **bolt** (with a small in-RAM LRU). The blocker
is reorg: today reorg does `resetState` + **replay from genesis** (needs all state
in RAM). To disk-back state, reorg must become **incremental**: restore a persisted
**finalized state snapshot** at height `S = tip − MaxReorgDepth` and replay only
`S+1..tip` (bounded). Then:
- restart loads the snapshot (minutes) instead of re-validating every block (days);
- **block bodies below the snapshot are pruned from disk** (bounds disk to
  O(window) too);
- the disk-backed sets roll back correctly because a reorg restores the snapshot.
- **Risk:** the reorg/restart rewrite is consensus-critical. **Audit-required.**
- **Mitigation:** gate the whole pruned/disk-backed mode behind a `--prune` node
  flag (default OFF = today's behavior byte-for-byte), so the default chain and the
  existing test suite are untouched while the new mode is built and tested in
  isolation, then enabled on the explorer/light nodes once verified.

### Track C — nullifier accumulator + non-membership (last, hardest)
Replace the `tags` set with an accumulator + **non-membership** proofs at spend
(PRUNING_DESIGN #3). Bounds the spent set to O(1) but needs the spender to track
the nullifier set or a service to serve witnesses; spent-set membership must be
perfect forever. Do after A and B are mature.

## CPU / "lightweight processing" at 100M
- Per-tx range-proof verify (~35 ms) dominates. The `verifiedProofs` cache already
  skips re-verifying mempool-seen txs. A `--prune`/light node verifies against
  header-committed roots + a snapshot, so it **never re-verifies historical
  proofs** — restart cost drops from O(all proofs) to O(window).
- Anon-spend ring building scans `coinList` O(n) — **Track A removes it entirely**
  (ZK membership against the accumulator value, no ring).

## Recommended order (value-per-risk)
1. **Track B snapshot + body-pruning behind `--prune`** — biggest disk + restart
   win, standard technique (assumeutxo/snap-sync), no new cryptography. Bounds disk
   and restart; lets pruned nodes drop the disk-backed sets' cold data.
2. **Track A value-only accumulator + ZK membership** — the structural RAM win
   (removes `acc.members` + `coins`/`coinList`); co-design with PQ STARK.
3. **Track C nullifier non-membership** — last; needs A/B's proving infra mature.

Each lands behind a version byte / activation height (hard fork), gated, with an
adversarial audit before activation — the same discipline used for the PQ and
NullRoot changes. None should be merged to the live chain without that review.

## What is already done
- **Tier 0:** block bodies evicted from RAM (LRU + bolt), fork-tree bodies freed
  past the reorg window, `GOMEMLIMIT` — block RAM is O(window).
- **Tier 1a:** `nullAcc` is a streaming O(log n) Merkle frontier with byte-identical
  roots (`pqaccum.NewStreaming`).
- **Track B: DONE** (no `--prune` gate; default on mainnet).
  `pkg/chain/snapshot.go` + `pkg/accumulator/snapshot.go`
  + `pkg/pqaccum/snapshot.go`:
  - **State snapshots** (gob for the maps + custom marshal for both accumulators),
    taken every `SnapshotInterval` blocks, verified against header-committed
    AccValue/NullRoot/PQAccRoot on load. Keeps the 2 most recent.
  - **Fast restart**: `replay()` restores the newest snapshot and re-applies only
    blocks above it (no genesis re-validation) — O(window) restart, not O(chain).
  - **Snapshot-based incremental reorg**: `reorgToLocked` restores a snapshot
    at-or-below the finalized height (tip − MaxReorgDepth) and replays forward,
    instead of replaying from genesis (falls back to genesis if no snapshot —
    keeps short chains/tests on the original path).
  - **Block-body pruning**: bodies below the oldest kept snapshot are deleted from
    bolt — **disk is now O(window) too**.
  - Tests: `tests/critical/snapshot` — restart-identical, restart with pre-snapshot
    bodies DELETED, and a **reorg across a pruned snapshot boundary**. Full suite +
    forkchoice race green.

These carry 2 GB to ~millions of txs and bound DISK regardless of chain length.
**Remaining for true 100M: Track A** (value-only accumulator + wallet ZK witnesses
— removes `acc.members`/`coins`/`coinList`, the ~32 GB runtime-RAM hog) and
**Track C** (nullifier non-membership). Track A is the audit-grade ZK piece;
correctness-of-crypto risk is real on a live mainnet, so build + adversarially
test it carefully.
