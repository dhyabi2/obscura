# Obscura — Internal Security Audit-Prep Pass

*Status: internal review to ready the code for an INDEPENDENT external audit. This
is NOT a substitute for external review — it is a pre-pass that fixes issues found
in-house and documents the threat model so external auditors start ahead.*

## Method

Three parallel adversarial reviewers (independent contexts) covered the
highest-risk surfaces, each instructed to find real exploitable bugs with
file:line, severity, an attack sketch, and a minimal fix — not rubber-stamp:

1. **Crypto soundness** — `pkg/commit` (Pedersen, range proofs, conservation,
   stealth/subaddress/view-only, ownership/value-equality, one-of-many + key
   image, adaptor sigs, DLEQ, payment proofs).
2. **Consensus soundness** — `pkg/chain` + `pkg/tx` + `pkg/block` + `pkg/consensus`
   (inflation, double-spend, theft, validation completeness, non-determinism,
   reorg, overflow).
3. **Serialization & DoS** — every deserializer and P2P/RPC/mempool entry point.

Every finding below was triaged (real vs. known tradeoff), and every accepted
finding was **fixed with an adversarial regression test**. Full suite stays green
and race-clean after the fixes.

## Threat model (summary)

- **Assets:** coin supply integrity (no inflation), output ownership (no theft),
  no double-spend; transaction privacy (sender / recipient / amount); node
  liveness.
- **Adversaries:** a malicious peer (arbitrary P2P bytes), a malicious miner
  (crafted blocks, fee/timestamp/seed manipulation), a malicious counterparty
  (swaps, payment proofs), a malicious/compromised node a wallet connects to,
  and a passive network observer.
- **Trust roots:** the hardcoded genesis id (height 0 skips PoW/tx validation by
  design); the group of unknown order (class-group = no setup; RSA-2048 = NUMS
  modulus); the honest-majority most-work header chain (SPV).
- **Explicitly out of scope / experimental:** the accumulator ZK-membership layer
  (`pkg/accumulator` zkmem) is NOT on the consensus path and is documented as
  not-yet-sound. Canonical RandomX is the default backend; the insecure prototype VM is opt-in (`-tags protopow`).

## Findings and resolutions

| # | Severity | Component | Finding | Resolution |
|---|----------|-----------|---------|------------|
| 1 | **High** | `commit/txproof` | Payment-proof amount was **unauthenticated** — `encAmount` is XOR with a keystream from the *public* shared point `D`, and nothing bound it; a verifier accepted any re-encrypted amount. | The encrypted amount is now bound into the DLEQ Fiat-Shamir challenge (`ProveDLEQ`/`VerifyDLEQ` take authenticated data; `ProveReceipt`/`ProveSpend`/`VerifyPayment` pass `encAmount`). Regression: `txproof.TestForgedAmountRejected`. |
| 2 | Medium | `commit/spendproofs`,`conservation` | No domain separation in the shared Schnorr-DLog proof — ownership / value-equality / conservation proofs were cross-acceptable (not weaponizable today since all are pure-`G` statements, but fragile). | Per-proof-type domain label prefixed into the challenge (`domCtx` with `domOwnership`/`domValueEq`/`domConservation`). |
| 3 | Medium | `commit/adaptor` | Adaptor point `T = identity` was accepted, collapsing a pre-signature into a plain signature → swap atomicity defeated (`Extract` yields 0). | `PreSign`/`PreVerify` reject identity `T`. Regression: `swap.TestAdaptorRejectsIdentityPoint`. |
| 4 | Medium | `chain/validate` | **Within-block duplicate one-time-key** bypass: dedup was on the *prime* (and a non-canonical `PrimeNonce` lets one key yield many primes) + only the *confirmed* UTXO set, so two outputs in one block could share a key, collapsing UTXO/coin state at apply (value burn + map/slice inconsistency). | `checkOutput` now dedups the **one-time key itself, block-wide** (namespaced in the shared block map) in addition to prime + confirmed-UTXO checks. |
| 5 | Low-Med | `chain/validate` | Duplicate **swap-output key across two txs in one block** (the per-tx `seenSwapKey` map missed it) — the second overwrites the first at apply, burning the first's locked funds. | Swap-output keys deduped **block-wide** via the shared `seenSpent` map (`"swapout:"` namespace), mirroring swap inputs. |
| 6 | Medium | `p2p/dandelion` | The `fluffed` txid set grew **without bound** over the node's lifetime → memory exhaustion. | Bounded with generation rotation at a cap (`rememberFluffedLocked`, `maxFluffedSet`). |
| 7 | Low-Med | `rpc/client` | The wallet/CLI read node HTTP responses with **no size limit** → a malicious node could OOM it. | All response reads bounded with `io.LimitReader(maxRespBytes)`. |
| 8 | Low | `p2p` | A panic in per-connection parsing/dispatch would **crash the whole node** (no recover). | `recover()` in `handle()` — a malformed message drops only that peer. |
| 9 | Low | `chain/query` | `poolID * PoolSize` could overflow on an attacker-supplied id (traced to a safe rejection, but unguarded). | Explicit overflow guard in `poolMembersLocked`. |

## Confirmed sound (high-value, by the reviewers)

- **No inflation on spends:** conservation residual must equal `z·G` (Schnorr knowledge of dlog); `H` is a NUMS generator with unknown dlog vs `G`, so a nonzero value component can't masquerade as `z·G`. Range proofs gate every output incl. coinbase; values are `uint64` confined to `[0,2^64)`; generalized conservation correctly accounts for swap public legs.
- **Coinbase:** `Minted == ExpectedCoinbaseMinted` exactly, overflow-guarded, `≤ MoneySupplyCap`; referral bonus is 0 by config and never minted.
- **Double-spend / theft:** transparent inputs removed from UTXO (rejected if absent); anon key-images checked vs the spent set and block-wide; key image `T=x·U` deterministic per coin; ownership/value/anon/swap proofs all bound to `t.CoreHash()` (computed on a copy, excluding proof/sig fields) → no cross-tx replay.
- **Range proof / one-of-many:** per-bit Schnorr-OR sound; one-of-many uses domain-separated challenges binding ctx, ring, tag, and first-move commitments, with a shared selector forcing one hidden index across the ownership and value rings.
- **Determinism / reorg:** MTP and LWMA fully deterministic (big.Int, sorted copies); no map iteration feeds a hash; merkle is domain-separated with duplicate-txid rejection (CVE-2012-2459); reorg snapshots/restores all economic+state sets and replays from genesis (no double-credit). PoW epoch-seed lag ≥ reorg depth.
- **Parsing hardening:** length-prefixed fields double-bounded (cap + remaining bytes) across tx/block/commit/accumulator serializers; P2P magic+version handshake, `maxMsgBytes`, peer/IP caps, ban scoring; RPC POST bodies use `MaxBytesReader`; mempool/addrbook/orderbook capped.

## Residual / documented items (not changed here)

- **Accumulator class-group `Unmarshal`** accepts oversized `A,B` (64 KiB) before the reduced-form check, making `reduce()` costly — but it is **not reachable from consensus or network input** (the ZK layer is off-path). Tighten the field cap to a small multiple of the marshalled size *before* wiring that layer into consensus.
- **Incentive-pool accounting:** `emitted += base` while the coinbase mints `base − pool`; the pool portion has **no payout path yet**. Harmless as a counter, but any future payout MUST mint from this counter, not in addition. Overflow handling is asymmetric (emitted saturates; pool drops) — make consistent before enabling payouts.
- **No `Version` gating** on headers/txs — add a soft/hard-fork gate before mainnet.
- **Formal soundness** of the Triptych-style one-of-many extractor was validated by construction + runtime forgery attempts, but not formally re-derived — flagged for the external audit.

---

# Round 2 — Deep hunt + "enumerate-and-break" sweep

Method: 5 deep-audit reviewers (wallet, mempool/fees/rpc, p2p/reorg, consensus
arithmetic, crypto/swap) + 1 post-quantum auditor, then a second methodology pass
of 5 "enumerate every validation, break each one individually" hunters. Each found
issues *beyond* the first 9; each fix below has a regression test where practical.

**Honest tally of NEW issues (not the "100 critical" claimed): 1 Critical, ~4
High, ~12 Medium, ~12 Low.** The bulk are DoS-hardening and swap-layer (the swap
stack is not deployable yet — mock Monero) and low-severity polish.

## Round-2 FIXED (this pass, with tests)

| Severity | Area | Issue | Fix |
|---|---|---|---|
| **CRITICAL** | consensus | **Transparent⇄anonymous double-spend** — the UTXO spent-set (`c.utxo`) and the key-image set (`c.tags`) were unlinked, so a coin could be spent once each way for 2× value (reproduced into a mined block, both directions). | Transparent inputs now carry the coin's **key-image** `T=x·U` + a DLEQ proving it shares `x` with `P=x·G`; validation checks/records it in the **same** `c.tags` set as anonymous spends. `tests/critical/doublespend/`. |
| **High** | wallet | View-only wallet **crashes** (nil `OneTime`) on every state save → DoS on a primary use case. | `MarshalState`/`RestoreState` handle nil one-time secret (32 zero bytes ↔ nil). |
| Medium | mempool | Swap-claim/refund spends weren't in `spendKeys` → RBF bypass + invalid templates. | Added `"swap:"` keys to `spendKeys`. |
| Medium | mempool | Byte-cap eviction was a single `if` → 32 MiB cap not enforced. | Eviction is now a loop until the tx fits. |
| Medium | wallet | `--fee auto` double-reserved inputs → "insufficient funds". | `ReleaseReservation` on the throwaway sizing build. |
| Low-Med | crypto | `VerifyOneOfMany`/`VerifyAnonSpend`/`VerifyFull` accepted identity points; `checkOutput` accepted identity one-time key (degenerate nullifier). | Reject identity points in all four. |
| Low | p2p | Dandelion stem-relay guard ignored the rotated `fluffedOld` generation. | Use `fluffedSeenLocked`. |

## Round-2 CATALOGUED (real, fix recommended — queued, mostly DoS/swap/polish)

| Severity | Area | Issue | Recommended fix |
|---|---|---|---|
| High | swap | **Rogue-key** on `AggregateKey` (`K=A+B`, no proof-of-possession) → taker steals OBX, keeps XMR. (Swap layer is not deployable — mock Monero.) | MuSig2 key aggregation, or require+verify a Schnorr PoP for each key share before funding. Same on the XMR side (`XMRSpendPub`). |
| High | p2p | **Addrbook eclipse**: one IP fills the 4096-entry book via port variation; no per-IP-group buckets / stale eviction; `msgAddr` unthrottled. | Bucket the address book by IP /24, cap per group, evict untried/stale, rate-limit PEX. |
| Med-High | p2p | `msgGetBlk` (and other serve messages) is an unthrottled 2 MB-reply / 9-byte-request **amplification** vector. | Per-peer rate-limit on serving messages. |
| Medium | p2p | Data **race** on `peer.listen` (handshake write unlocked vs discovery read locked). | Set `peer.listen` under `n.mu`. |
| Medium | chain | `c.nodes` block-tree grows unbounded (side-branch blocks never pruned); failed reorgs aren't cached (repeat full-genesis replay). | Reject/prune side branches > `MaxReorgDepth` behind the tip; cache rejected blocks. |
| Medium | wallet | `BumpFee` doesn't bind dest/amount to the original tx (a buggy caller could redirect a payment). | Derive dest/amount from `prev` or assert they match. |
| Medium | swapd | `XMRSpendPub`/`MockMonero` accept an identity spend key (sweepable with scalar 0). | Reject identity/low-order; require PoP. |
| Medium | swap | Offers lack a network/chain-id binding → cross-network replay; 12-bit PoW is trivial. | Bind genesis/network id into `Offer.Core()`; raise PoW. |
| Low | p2p | Orphan buffer admits zero-PoW blocks (bounded ~512 MB); no self-connection nonce. | Bound orphan bytes; add a node nonce for self-connect detection. |
| Low | mempool | RBF conflict eviction happens before the capacity check can fail (self-inflicted loss). | Defer eviction until after capacity succeeds. |
| Low | wallet | `sweep` records `Balance()` not the actual paid amount; `parseOBX` can overflow; `FundSwap` lacks the `addCheck` overflow guard. | Record actual amount; guard the multiply; use `addCheck`. |
| Low | consensus | `ReferrerTag` length unbounded in coinbase; per-context domain tags missing on the swap-signature layer; keystore `maxMem` cap generous (4 GiB). | Bound the tag; add domain labels; tighten the cap. |
| Doc | consensus | `emitted` uint64 saturates ~78k blocks past the cap (accounting only, guarded — no wrap); referral decay is inert (gated off by `ReferralMaxBps=0`); merkle SPV proof path is only safe vs the dedup-validated chain; timestamps may move backwards (standard LWMA). | Track separately / lower cap; gate referral before enabling; document SPV merkle scope. |

## Bottom line (round 2)

The one genuinely **critical** bug — a transparent⇄anonymous double-spend — is
**fixed and regression-tested**, along with the High-severity view-only crash and
several Mediums. The remaining items are real but lower-priority (network-DoS
hardening, the not-yet-deployable swap layer, and low-severity polish), each
catalogued above with a recommended fix for the next pass / external audit. There
is **no evidence of "100 critical bugs"** — the codebase is far more sound than that
claim implies, and the actual critical issue was found and closed in-house.

---

## Bottom line

Nine issues found and fixed (1 High, 4 Medium, 4 Low), each with a regression
test; the core value-conservation, double-spend, theft, determinism, and parsing
defenses were independently confirmed sound. The High (forgeable payment-proof
amount) and the consensus state-corruption bugs (within-block duplicate keys) are
the most important fixes. Remaining items are documented for the external audit,
which is still required before mainnet.
