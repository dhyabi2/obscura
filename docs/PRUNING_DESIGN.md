# In-Protocol Pruning — design brainstorm

## ✅ Implemented in this pass (the recommended "now" foundation)

Consensus-level, gated by the existing version byte, all tests green (default
52/52, `-tags pq` 52, race-clean), regression test in `tests/critical/pruning`:

- **Nullifier-set header commitment** (`block.Header.NullRoot`): every header now
  commits a Merkle root over the spent key-image set (an add-only `pqaccum`
  accumulator, `nullAcc`), mirroring the audited `PQAccRoot` pattern — predicted
  in `BlockTemplate`, verified in `validateBlockLocked`, rolled back on reorg
  (snapshot/reset/restore), rebuilt on replay. This makes the **spent set part of
  the trustlessly-verifiable state**, the prerequisite for trustless spent-set
  pruning / snapshot-sync. (The output set was already committed via `AccValue`.)
- **Bounded `accValues`** (#4): `accValues` is write-only (not consumed by active
  consensus — superseded by the header `AccValue` chain), so it is capped to
  `config.MaxAnchorWindow`. `pqRoots` is intentionally NOT cardinality-capped (it
  IS consumed as the PQ anchor set, so a hard cap would be a liveness wall);
  bounding it correctly needs a **height-gated rolling window** (accept anchors
  within N blocks of the tip, evict older) — the precise remaining TODO for the
  PQ path. (Self-audited: the NullRoot commitment is order-matched, reorg-safe,
  replay-deterministic, PoW-bound, and unspoofable.)

So both the **output set** and the **spent set** are now O(1) header-committed
accumulators, and the unbounded in-memory caches are bounded — the foundation
the rest of this design builds on.

## ✅ RAM-bounding pass (2026-06-24) — "survive 2 GB"

The live 2 GB node was being **OOM-killed** (SIGKILL) once the chain grew (~7.5k
blocks): the dominant RAM cost was **every full block body retained in RAM**
(`c.blocks` map + `chainNode.block`), each carrying its range/conservation/
ownership proofs. Fixed:

- **Tier 0 — block-body RAM eviction (the actual fix).** `c.blocks` is now a
  bounded LRU (`blockCacheCap=300 > MaxReorgDepth`); bodies are durable in bolt and
  loaded on demand (`bodyAtHeight`/`bodyForNode`). Fork-tree nodes keep only cheap
  metadata (hash/prev/work) and their bodies are freed past the reorg window
  (`pruneNodeBodiesLocked`); reorg replay sources the shared prefix from bolt and
  the divergent suffix from RAM. `persistActiveChainLocked` now overwrites changed
  heights + trims stale ones instead of rewriting the whole bucket. Node RAM for
  blocks is now **O(window), not O(chain)**. `GOMEMLIMIT=1500MiB` added to the
  systemd unit so the GC reclaims before the kernel OOM-kills. Verified: forkchoice
  / swapchain / chain / pruning / vault suites green.
- **Tier 1a — streaming nullifier accumulator.** `pqaccum` gained a `NewStreaming`
  mode that keeps only the O(log n) perfect-subtree **peaks** instead of all
  leaves, with **byte-identical RFC 6962 roots** (proven by `streaming_test.go`
  over many sizes + RootAfter + Clone). `nullAcc` (which grows with every spend and
  is only ever root-committed, never proved) now uses it — O(n) → O(log n) RAM, no
  consensus change (roots unchanged). `pqAcc` keeps leaf mode (it serves `Prove`).

Net: the node's RAM is now bounded to a working set independent of chain length
for millions of txs, so it survives 2 GB. The remaining O(n) terms below are
disk/scale concerns, NOT the 2 GB RAM ceiling.

## ⏳ Deferred (by recommendation — value-vs-risk)

- **Tier 1b — disk-back the add-only sets** (`coins`/`coinList`, `tags`,
  `outPrimes`, `utxo`) via bolt + small LRU. These are the next O(n) RAM terms (tens
  of MB at small scale; GBs only at full mainnet scale). Deferred because reorg rollback
  currently deep-copies these maps in-memory; disk-backing needs transactional
  rollback — a sizeable, consensus-adjacent refactor best done supervised. Not
  required for the 2 GB goal at current scale.
- **Tier 2 — block-body PRUNING (disk) + snapshot-based reorg/restart.** Tier 0
  bounded blocks in RAM but bolt still keeps every body (reorg + restart replay
  from genesis need them). To prune bodies from DISK too, reorg/restart must replay
  from a persisted finalized **state snapshot** (capturing all consensus maps +
  accumulators) instead of genesis. That snapshot serialize/restore is the larger
  change; deferred as consensus-critical (must be verified, not shipped blind).
  Disk is cheap, so this is lower urgency than the RAM fix.

- **Block-body pruning + state-snapshot sync** (#2 network side): the actual disk
  win. Needs a snapshot serialize/restore + a P2P snapshot-fetch protocol, each
  verified against the now-committed header roots. Build when there is a real
  network (recommended); the header commitments above are the enabling piece.
- **Accumulator-membership anon-spend (retire ring lists, #1)** and
  **nullifier non-membership proofs (#3)**: soundness-critical ZK — co-design with
  the PQ zk-STARK membership effort (same machinery), not hand-rolled.

---

# Design brainstorm (full)

How Obscura bounds node storage **by consensus design**, not by an external
prune script. The thesis: Obscura is *accumulator-first*, so it can shrink its
authoritative state to ~O(1) commitments + a rolling window of recent blocks —
something a UTXO chain (Bitcoin) or a per-output-nullifier chain (Zcash) cannot
do. The work is to make **every** growing structure accumulator-native and to
push witness maintenance to wallets.

## What actually grows (today)

From `pkg/chain/chain.go`, the unbounded state is:

| Structure | Role | Growth |
|---|---|---|
| `acc` (RSA/class-group) | global anonymity-set commitment | **O(1)** ✅ |
| `coins` / `coinList` | per-coin records, used to rebuild anon **rings** (`poolMembersLocked`) | add-only, forever ❌ |
| `tags` | key-image nullifiers (double-spend set) | add-only, forever ❌ |
| `accValues` | historical accumulator checkpoints = spend **anchors** | add-only ❌ |
| `outPrimes` | output-prime uniqueness cache | add-only ❌ |
| `utxo` | unspent outputs (transparent spend model) | net live set (prunable) |
| `blocks` / `headers` | full block bodies + headers on disk | forever ❌ (bodies); headers tiny |
| PQ: `pqAcc` leaves, `pqNull`, `pqRoots` | PQ set / nullifiers / anchors | add-only ❌ |

The accumulator *value* is already constant-size and committed in every header
(`AccValue`, `AccSize`, `PQAccRoot`). Everything else is the pruning target.

## Ranked in-design mechanisms

### 1. Witness-carried accumulator membership → delete the coin list (biggest win, most native)
**Removes:** `coins` + `coinList` (the forever-growing per-coin records).
**Change:** the anonymous spend (`AnonInput`) today rebuilds a *ring* from
`c.coinList` (`poolMembersLocked`). Replace that with the accumulator's own
**witness-hiding ZK membership proof** (NI-PoKE2 / PoKE already implemented in
`pkg/accumulator`): the spender proves "my coin ∈ accumulator" against a
header-committed checkpoint, carrying its own witness in the tx. The node never
needs the coin set — only the O(1) accumulator value.
**Keeps:** the accumulator value + the set of valid checkpoint anchors (see #4).
**Tradeoff:** witness maintenance moves to wallets. Because Obscura already
anchors proofs to a *historical* checkpoint (the `accValues` set), a coin can
prove membership against the accumulator value *as of when it was added* — so in
the add-only model **no witness updates are needed** (the design's existing
"witnesses valid forever" property). Privacy is equal-or-better: the anonymity
set is the *entire* accumulator, not a fixed-size ring. Safety unchanged
(accumulator soundness). This is THE change that makes the anonymity layer
prunable, and it converges with the PQ roadmap (Merkle-accumulator + zk-STARK
membership is the post-quantum version of exactly this).

### 2. Header-committed state roots + snapshot bootstrap → prune block bodies
**Removes:** block **bodies** older than the finality window (`MaxReorgDepth`) —
the bulk of disk.
**Change:** headers already commit `AccValue`/`AccSize`/`PQAccRoot`; also commit a
**UTXO-set root** and **nullifier-set root** (Merkle/accumulator) per header. A
new node then bootstraps from a recent *finalized state snapshot* (UTXO set +
accumulator value + nullifier root at height H), verified against header H's
committed roots, plus the PoW header chain — **without replaying history**. Old
bodies are dropped; archive/explorer nodes keep them.
**Keeps:** full header chain (tiny, ~hundreds of bytes/block), recent bodies
(reorg + serve-sync window), committed state.
**Tradeoff:** consensus change (new header fields → hard fork) + a snapshot-sync
protocol. Trust model stays trustless: snapshot is checked against
PoW-header-committed roots (cf. Bitcoin `assumeutxo`, Ethereum snap-sync). High
value, well-trodden.

### 3. Nullifier accumulator with non-membership → bound the spent set
**Removes:** the unbounded `tags` (and PQ `pqNull`) maps.
**Change:** keep nullifiers in an **accumulator** (the package already has
non-membership proofs). At spend, prove the key-image is *not yet* accumulated
(non-membership) and add it. The node stores only the O(1) nullifier-accumulator
value.
**Keeps:** O(1) nullifier accumulator value.
**Tradeoff:** the hardest one — non-membership proofs are larger/costlier per
spend, and the prover must track the current nullifier set (or a service serves
witnesses). Spent-set membership *must* be perfect forever (a missed nullifier =
double-spend), so this trades simple O(n) storage for proof complexity. Rank
below #1/#2; viable but the most delicate.

### 4. Anchor window + drop the prime cache → bound checkpoints & uniqueness
**Removes/bounds:** `accValues` → a **rolling window** of the last N accumulator
checkpoints; `outPrimes` → dropped (hash-to-prime is deterministic and the
accumulator's add-only insert already rejects duplicates; the prime map is a perf
cache, not a soundness requirement).
**Change:** spends must reference an anchor within the window. Dormant coins must
be **re-anchored** (cheap RSA witness-update with the elements added since)
before their anchor ages out.
**Tradeoff:** introduces a holder-liveness requirement (re-anchor or re-derive a
witness from an archive node before the window passes) — the classic
bounded-anchor-set tradeoff (Zcash keeps all anchors and pays unbounded growth;
Obscura would bound state at the cost of periodic witness refresh). Privacy and
safety unaffected. Apply identically to PQ `pqRoots` (already flagged as a TODO).

### 5. Compress the header chain itself (long-horizon)
Even after #1–#4, the **header chain** grows (PoW security). Bound it with PoW
**super-block / FlyClient-style sampling** or a recursive zk-PoW proof, so a
light/pruned node verifies chain work from a logarithmic sample instead of every
header. Lowest priority (headers are tiny), but it's the last unbounded piece and
fits the accumulator/zk direction.

## Recommended combined design

A full Obscura node persists only:
- the header chain (or a compressed proof of it, #5),
- the accumulator value(s) — global set + nullifier set — **O(1)** (#1, #3),
- a rolling window of recent **anchors** (#4) and recent **block bodies** (#2),
- the live UTXO set for the transparent model (snapshot-restorable, #2).

History and the full coin/nullifier sets live only on opt-in archive nodes;
wallets hold their own membership witnesses (refreshed per the anchor window).
Net result: **consensus state ≈ O(1) + O(window)**, independent of total chain
age — the property a UTXO/per-nullifier chain structurally can't achieve, which
Obscura's accumulator makes natural.

## Migration / staging

All of these are consensus changes (hard fork) and should land behind a version
byte / activation height, in order of value-per-risk:
1. **#2 snapshot-sync + body pruning** — biggest disk win, least cryptographic
   risk, standard technique. Add the UTXO/nullifier header roots first.
2. **#4 anchor window + drop prime cache** — small, bounds two growers.
3. **#1 accumulator membership for anon spends** (retire ring lists) — the
   structural anonymity-layer prune; co-design with the PQ STARK-membership.
4. **#3 nullifier accumulator** — last, most delicate; needs the membership/
   non-membership proving infra to be mature.

Note: the accumulator already gives O(1) *verification* state today — the reason
nodes still hold everything is the **ring-based anon spend** (needs the coin
list) and **set-membership conveniences** (tags/anchors as plain maps). Pruning
is therefore mostly about *moving each of those onto the accumulator* rather than
inventing new cryptography — most primitives (`pkg/accumulator`: PoKE2,
non-membership; `pkg/pqaccum`) already exist.
