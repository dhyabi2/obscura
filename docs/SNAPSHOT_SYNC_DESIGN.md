# Verified Snapshot Sync — Design

**Status (2026-06-27): verification CORE shipped + tested; full state IMPORT proven to need a
precursor.** `pkg/chain/snapshotsync.go` ships `VerifySnapshotAuthenticity` — it verifies the
part that IS soundly checkable against PoW (genesis binds to our network, every header carries
real cumulative PoW under correct LWMA difficulty + seed, and the four header-committed roots
match the restored accumulators). `TestSnapshotAuthenticity` (pkg/chain) proves it accepts an
authentic snapshot and REJECTS substituted state, fake PoW, a tampered genesis, and a malformed
chain — and that it is read-only. It performs NO state import.

**UPDATE (2026-06-28): blocker 1 CLOSED — header StateRoot landed.** `pkg/block` now carries a
`Header.StateRoot` and `pkg/chain/stateroot.go` computes a PRE-STATE commitment over ALL
consensus state not already bound by an existing root: emitted, incentivePool, the disk-set
commitments (spent/tags/outPrimes — see below), and every in-RAM map INCLUDING pqUtxo amounts
(closes audit PQACC-1). It is a pre-state root (a block commits its PARENT's state), so template
and validate compute it identically with NO mirror-apply/prediction — the fork-prone failure
mode is structurally impossible. The classical key-image double-spend set, output-uniqueness
set, and spent set — previously committed by NO header root — are now committed via an
order-independent incremental multiset hash on the disk-backed sets (`pkg/chain/diskset.go`,
reorg/restart-safe via a per-count commit bucket). Enforced in validation; bound into PoW.
Tests: `TestDiskSetCommit`, `TestStateRootConsensus` (binding + tamper-reject + restart
determinism), full reorg/partition/ZK-e2e subset green; genesis reset via NetworkSeed `-sr1`.
A snapshot at height H is now verifiable against the PoW-bound `header[H+1].StateRoot`.

**UPDATE (2026-06-28 b): verified IMPORT shipped (Stages 1–3).** `pkg/chain/snapshotsync.go` now
has `VerifyAndImportSnapshot(data) (height, error)` and `ExportTransferSnapshot()`, plus a
`transferSnapshot` wire format. What it does, all PoW-bound before any mutation:
- decode (bounded: `maxSnapshotHeaders`, `maxSnapshotMembers`);
- genesis binding + full `verifyHeaderChain` (PoW/LWMA/seed) over headers `0..tip`;
- the four POST-state accumulator roots vs `header[H]` (`AccValue/NullRoot/PQAccRoot/CMRoot`);
- the residual PRE-state root (emitted, incentivePool, the three disk-set commitments, and ALL
  in-RAM maps incl. pqUtxo amounts) recomputed via the SHARED `stateRootOf` and checked vs
  `header[H+1].StateRoot` (so the transfer must carry headers to ≥ H+1; the producer serves a
  saved snapshot at H ≤ tip-1);
- the disk-set MEMBERS (spent/tags/outPrimes) are transferred and verified because their multiset
  commitments feed that residual root (tampered/dropped/added member ⇒ commitment mismatch ⇒
  reject). On commit, members are written to bolt (`diskSet.importMembers`) and the fork-choice
  node tree is rebuilt (`indexActiveChain`) so the imported tip accepts the next block.

`stateroot.go` was refactored to a pure `stateRootOf(residualState)` shared by the live chain and
the importer (ONE commitment code path — no apply-vs-import divergence). Exact hash preserved
(consensus unchanged; existing `TestStateRootConsensus` still green).

Tests (pkg/chain, all green): `TestSnapshotImportPositive` (fresh node imports a real snapshot,
lands at H, and validates + APPLIES the next real block on top), `TestSnapshotImportRejects`
(tampered emitted, tampered/dropped disk-set member, fake-PoW header, foreign genesis, missing
H+1 header — all rejected with NO state residue; a clean import after the rejects still works).

**Still open:**
- **Coin/anonymity set transfer (documented gap).** `CoinInfo` doesn't persist the `PrimeNonce`
  needed to re-derive each coin's accumulator prime, so a transferred coin set can't be verified
  against `AccValue` (a peer could inject phantom spendable coins). So an imported node can detect
  double-spends and apply blocks that don't spend pre-snapshot coins (e.g. coinbase blocks), but
  not validate a spend of a pre-snapshot coin until the coin set is transferable+verifiable. Fix:
  persist `PrimeNonce` in `CoinInfo` (or add a coin-set commitment), then transfer + re-accumulate
  the coins and check `g^∏primes == header[H].AccValue`. This is the last precursor.
- **Stage 4 P2P transport** (`msgGetSnapshot`/`msgSnapshot`, chunked + bounded reassembly +
  rate-limited serving + bootstrap auto-trigger). NOT shipped here: it is a large, stateful
  networking change whose auto-trigger interacts with the live sync loop, and shipping it
  under-tested risks the green p2p suite. The chain-side API it needs is ready
  (`ExportTransferSnapshot` to serve, `VerifyAndImportSnapshot` to receive). Wire-in spec:
  serve `ExportTransferSnapshot()` bytes chunked as `msgSnapshot{seq,total,chunk}` (the snapshot
  exceeds `maxMsgBytes`), reassemble under a bounded buffer + timeout, and in the sync loop:
  when a peer tip is far ahead AND `msgGetBlk` for an old height returns NOT-FOUND (pruned),
  request a snapshot, `VerifyAndImportSnapshot`, then resume `msgGetBlk` from `H+1`; fall back to
  genesis sync below `PoRWindow`; cross-check tips from ≥2 peers.

**Why import is NOT shipped (two structural blockers proven by attempting the build):**
1. **Uncommitted state.** The snapshot carries `Emitted`, `IncentivePool`, `AccValues`
   (anchors), and the `Referral`/swap/vault/PQ/ZK maps — none bound by a header root (audit
   PQACC-1 generalizes). `Emitted`/`IncentivePool` are deterministically recomputable for a
   classical-only chain, but the anchors and per-feature maps are not, in general. A complete
   field-by-field verifier cannot be *proven complete* (a missed field = a hole).
2. **Structurally omitted disk-backed sets.** The classical double-spend set `tags`
   (validate.go key-image/tag reuse checks), `outPrimes`, and the coin set are disk-backed; the
   snapshot stores only their COUNT, not their members (the format is built for LOCAL
   crash-resume, which trusts the intact local bolt). A fresh node importing a snapshot has an
   EMPTY bolt → no double-spend set, no coin set → it cannot validate any post-snapshot
   transaction. Importing would produce a BROKEN node, strictly worse than the availability gap.

**The proven-correct fix is a precursor, not a localized patch:** (a) add a single header
**state-root** committing ALL consensus state (emitted, pool, anchors, counts, every map), so
one check verifies everything; and (b) extend the snapshot/transfer to carry the full
disk-backed member sets (coins, tags, outPrimes), not just counts. Then import = recompute the
state-root from the received snapshot and compare to the PoW-verified tip header. That is a
multi-part consensus + networking change; on a live mainnet it must ship as a backward-compatible
upgrade with a migration path, so it needs careful design review, below.

---

# (original design — the target end state)

**Note:** the failure mode of getting import wrong is **fake-state injection** (a malicious
peer convincing a new node of a false ledger), strictly worse than the current availability
gap. The gap is also **not yet triggered**: the live chain is far below `PoRWindow = 10,000`
blocks. Build the import path with review of the verification logic.

## The problem

A fresh node bootstraps by downloading full blocks from genesis: `msgGetTip` →
`msgGetBlk(height)` → `msgBlock`. But body pruning is intrinsic (`pkg/chain/snapshot.go`):
every node, miners included, prunes bodies below `tip - PoRWindow`. So once the chain
exceeds `PoRWindow`, NO peer can serve the old bodies, and a from-genesis sync stalls at
the pruning boundary. There is currently no header-only sync and no state/snapshot
transfer over P2P (`pkg/p2p/p2p.go` message set: hello/tip/getblk/block/tx/addr/stem/swap).

## What already exists (reuse)

- `Chain.encodeSnapshotLocked() []byte` — serializes the full consensus state, **including
  the entire header chain** (`Headers: c.headers`), plus `acc/nullAcc/pqAcc/cmTree`
  MarshalState, the nullifier/tag/coin sets, swaps, vaults, etc. (`pkg/chain/snapshot.go:69`).
- `Chain.restoreSnapshotLocked(data)` — restores state from those bytes (used today by the
  trusted LOCAL crash-resume path; it does NOT verify, because local disk is trusted).
- Per-block PoW verification already exists in block validation; factor out a header-only
  variant.

## The design

### 1. New P2P messages
- `msgGetSnapshot` (request): empty, or a max-height hint.
- `msgSnapshot` (response): the `encodeSnapshotLocked()` bytes for the serving node's
  newest snapshot height.

**Size / chunking:** a snapshot at large height is bigger than `maxMsgBytes`
(`config.MaxBlockBytes + 4096` = ~4MB). Either (a) raise the cap for this message type, or
(b) chunk: `msgSnapshot{seq, total, chunk}` reassembled by the receiver with a bounded
buffer and a timeout. Chunking is preferred (no single 50MB+ frame). Rate-limit serving
(it is expensive) under the existing token bucket.

### 2. Verified import — THE SAFETY-CRITICAL CORE
Add `Chain.VerifyAndImportSnapshot(data []byte) error`. It must, IN THIS ORDER, BEFORE
mutating any live state:
1. **Decode** the snapshot into a scratch struct (gob). Bound the decoded sizes (header
   count, set counts) against a sane max to prevent a decode-bomb (mirror
   `pkg/pqaccum` RestoreState hardening).
2. **Verify the header chain**: for every header genesis→tip, check (a) it links to the
   previous (`PrevHash`), (b) its PoW satisfies the difficulty target it claims, (c) the
   difficulty itself is correct under LWMA from the prior headers, (d) timestamps/MTP are
   sane, (e) the epoch PoW seed is correct. This is the SAME work `validateBlockLocked`
   does minus the body; factor a `verifyHeaderChain(headers)` helper so there is ONE
   source of truth. A fake chain that does not carry real cumulative PoW is rejected here.
   ALSO verify the chain's cumulative work is competitive (do not import a low-work chain).
3. **Verify state roots against the verified tip header**: rebuild the accumulators from
   the snapshot's MarshalState bytes and assert `acc.Root() == tip.AccValue`,
   `nullAcc.Root() == tip.NullRoot`, `cmTree.Root() == tip.CMRoot`,
   `pqAcc.Root() == tip.PQAccRoot`, and that `PoRRoot`/`NumTxs`/`AccSize` are consistent.
   Because the tip header is PoW-bound and now verified, a tampered state (wrong balances,
   missing nullifier, phantom coin) produces a root mismatch and is REJECTED. This is the
   property that turns "trust the peer" into "trust the PoW."
4. **Only if all of the above pass**, commit: replace the live chain state with the
   restored state under `c.mu`, set the tip, and persist a local snapshot.

A subtle but critical point: step 3 must verify EVERY consensus root the header commits,
because anything not bound by a verified header is attacker-controlled (cf. the audit
finding that snapshot `pqUtxo` amounts were not root-bound — that gap must NOT exist here).

### 3. Bootstrap integration
In `syncLoop`/the bootstrap path: when a peer's tip is far ahead (e.g. `> PoRWindow`) AND
block-by-height download hits a pruned/unavailable old body, request a snapshot, run
`VerifyAndImportSnapshot`, then resume normal `msgGetBlk` sync from `snapshotHeight+1`
(those recent bodies are retained by peers). Fall back to from-genesis sync when below
`PoRWindow`. Prefer snapshots from multiple peers and cross-check the tip hash.

### 4. MANDATORY adversarial tests (the build's safety gate)
- **valid**: a real snapshot from a synced node imports; the fresh node's roots/height
  match and it continues syncing to the tip.
- **tampered state REJECTED**: flip one balance / drop one nullifier / add a phantom coin
  in the snapshot bytes → `VerifyAndImportSnapshot` returns an error and mutates nothing.
- **fake PoW chain REJECTED**: headers with insufficient PoW or wrong difficulty/linkage →
  rejected at step 2.
- **low-work chain REJECTED**: a valid-but-shorter/lower-work chain does not replace a
  heavier local chain.
- **decode bomb REJECTED**: oversized counts error instead of OOM.

If any of the REJECT tests cannot be made to pass, the verification is unsound — do NOT
ship. The whole value of this feature is that the reject tests hold.

## Why this is deferred (not built unattended)
A verification bug here is **fake-state injection** — a deceived node would validate/mine
on a false ledger (e.g. accept a double-spend its peers reject), forking itself off and,
for a merchant node, potentially accepting an invalid payment. The current issue is
availability-only and not yet triggered (chain < `PoRWindow`). Shipping a subtly-wrong
verifier converts an availability gap into a safety hole. The standard verify-PoW-then-
verify-roots pattern above is sound, but the implementation must be reviewed, not landed
blind. Estimated effort: header-sync/verify + chunked transfer + verified import + the five
adversarial tests ≈ a focused multi-session build.
