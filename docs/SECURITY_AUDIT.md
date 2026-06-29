This is a synthesis task. All the analysis is already done and provided. Let me write the executive report directly.

NO-GO. Multiple confirmed critical inflation vectors and high-severity consensus/fund-loss bugs exist on the live consensus path.

---

# OBSCURA — FINAL SECURITY GO/NO-GO REPORT
**Reviewer:** Lead Security Review · **Date:** 2026-06-23 · **Decision context:** launch with real value TONIGHT

## 1. VERDICT: **NO-GO**

Do not go live tonight. The codebase contains **four independently-confirmed CRITICAL inflation/forged-coin bugs** on reachable, consensus-accepted paths, plus multiple confirmed HIGH issues including a consensus-state-corrupting reorg bug, vault fund-destruction, and a void PoW memory-hardness model. Any one of the four criticals alone — PQ-fee leakage into the classical coinbase, uncapped coinbase PQ minting, the PQ-routing bypass of ALL ZK/CZK/vault validation, and unvalidated coinbase ZK/swap legs — lets an attacker (in several cases any user or any miner) mint unbounded real spendable OBX that every honest node accepts, breaking the money-supply cap silently and irreversibly. These are not theoretical: each was traced end-to-end through validate.go → apply.go to a real cash-out. Launching with real value in this state would near-certainly result in catastrophic, unrecoverable inflation and theft. Separately, the entire from-scratch zk-STARK + class-group accumulator stack has had **no external cryptographic audit**, which on its own is disqualifying for a value-bearing launch.

## 2. MUST-FIX-BEFORE-LIVE (confirmed CRITICAL/HIGH, by severity)

**CRITICAL**
1. **PQ fees credited to classical coinbase (supply inflation)** — `validate.go:139`. In the fee loop add `if hasPQ(t) { continue }` so PQ-value-space fees are never minted as classical OBX.
2. **Coinbase PQ outputs minted with no aggregate cap** — `validate.go:117`. Reject coinbase PQOutputs entirely (`len(cb.PQOutputs)!=0` → error) until a real PQ emission schedule exists; combine with fix #1.
3. **PQ-routed tx bypasses ALL ZK/CZK/vault validation → unlimited inflation** — `pqvalidate.go:100-103`, `validate.go:328-329`. Extend the forbidden-field guard so a PQ tx may carry ONLY PQ fields (reject ZKInputs/ZKOutputs/CZKSpends/VaultInputs/VaultOutputs), making the apply-side assumption true by construction.
4. **Coinbase can carry unvalidated ZK/CZK/swap/vault value legs** — `validate.go:104-197`, `apply.go:97-99,176-185`. Reject any coinbase carrying non-Output/PQOutput value fields; defense-in-depth: skip `t.IsCoinbase` in `cmLeavesFromTxs` and the SwapOutputs apply loop.

**HIGH**
5. **Uncapped coinbase PQ minting (supply-integrity break)** — `validate.go:117-121`. Same fix as #2 (accumulate + cap, or forbid); listed separately as the standalone PQ-supply break.
6. **Deep partition-recovery reorg (>300 deep) leaves spliced/stale bolt bodies → restart corruption** — `forkchoice.go:226,458-494`. Persist the full replayed suffix (`applyBlock(blk, true)` on adoption, or re-serialize heights forkHeight+1..tip from the fork tree).
7. **Vault claim/deposit auth bypassed on PQ-routed txs (fund destruction + pool drain)** — `pqvalidate.go:100-103`. Closed by fix #3's field-exclusion guard.
8. **Orphan pool fillable with zero-PoW high-height blocks; no eviction → memory-DoS / sync stall** — `forkchoice.go:160-176`. Require verifiable PoW before buffering, bound orphan bytes, and add FIFO/age eviction + penalize far-future orphans.
9. **Default build ships weak prototype "vm-randomx-style" PoW (near-zero memory-hardness)** — `Makefile:13`, `build.sh:37`. Resolved: the default build (plain `go build`, no tags) now ships KAT-verified canonical RandomX (`randomx-canonical`); the weak prototype VM is opt-in only via `-tags protopow` and a node on it refuses to start without `OBX_ALLOW_PROTOTYPE_POW=1`.
10. **STARK ALI/DEEP binding on a single base-field OOD point → soundness ~2^-54** — `air.go:307-331`, `transcript.go:53-56`. Sample z and batching coeffs in a degree-2/3 Goldilocks extension (or interim: multiple independent OOD points).
11. **Mempool spendKeys omits ZK/CZK/PQ nullifiers → in-pool double-spend admitted, block-production DoS** — `mempool.go:57-77`. Add `zk:`/`pq:` nullifier keys to spendKeys and mirror the confirmed-set recheck under lock.
12. **Self-discovery adopts advertised address by vote count, not distinct peers (2-vote poisoning / eclipse)** — `discovery.go:78-82`. Dedupe votes by real remote /16, require ≥2 distinct outbound groups, validate routability, expire votes.

## 3. SHOULD-FIX-SOON (confirmed MEDIUM/LOW)

- **MED** Orphan buffer stores full bodies before PoW (~512 MB DoS) — `forkchoice.go:160-176`; PoW floor + byte cap + penalty.
- **MED** Ban score per-connection, resets on reconnect — `p2p.go:470-483`; persist per-IP score with decay.
- **MED** No per-peer message rate limiting (getblk amplification) — `p2p.go:366-461`; token-bucket per peer.
- **MED** Entire RPC surface unauthenticated; explorer deploy binds `0.0.0.0` — `rpc/server.go:72-95`, `explorer_node.sh:52`; split public/operator handlers, gate operator on loopback/token.
- **MED** `/peers` leaks full peer IP list on a privacy coin — `server.go:263-273`; return count only to non-loopback callers.
- **MED** Unauthenticated `/blocktemplate` is a lock-contending DoS amplifier — `server.go:175-205`; gate + rate-limit + short-TTL cache.
- **LOW** Unbounded RPC Nano balance panics swap daemon — `nanorpc.go:368`; bounds-check `BitLen()<=128` before pad.
- **LOW** Swap 2-of-2 claim key plain-summed (rogue-key), `PreVerify` never called — `swap.go:24`; MuSig2/PoP + identity-T guard + PreVerify before funding (latent; single-entity demo only today).
- **LOW** PQ wallet doesn't burn spent one-time key → WOTS+ OTS reuse — `pqwallet.go:115-156`; add used-key set, never re-sign a seed (Schnorr backstop limits to PQ-half loss).
- **LOW** ZK serial only ~64 bits → birthday collision bricks a coin (~2^32) — `zkspend.go:25-29`; widen nullifier to multi-felt.
- **LOW** Decoders accept trailing junk; relay re-broadcasts raw padded payload (1-hop amplification) — `tx.go:777`, `block.go:295`; enforce `r.Len()==0`, relay `t.Serialize()`.
- **LOW** Offer.Core NUL-delimited unvalidated assets → signed-message ambiguity — `swapbook.go:47`; length-prefix asset fields.

## 4. OPEN RISKS — HUMAN / CRYPTOGRAPHIC JUDGEMENT REQUIRED

- **STANDING CAVEAT (overriding):** This is a **from-scratch zk-STARK + class-group accumulator + WOTS+/hybrid-PQ stack that has had NO external cryptographic audit.** That is a fundamental, launch-blocking risk independent of every code-level finding above. Novel cryptographic consensus systems routinely contain soundness breaks invisible to code review; shipping one with real value before independent expert review is not defensible.
- **FRI soundness ~65 bits provable vs. ~112 advertised** — `fri.go:19-34`, `chainparams.go:16` (ZKQueries=48). The ~112-bit figure relies on the conjectured FRI proximity-gap at the Johnson radius (industry-standard to assume, but the code's OWN comment demands an external cryptographer sign off — no sign-off is encoded). The provable floor (~49 bits FRI, worse once composed with the single base-field ALI point in finding #10) is thin. **Requires a cryptographer's decision**, and `ZKQueries`/`friGrindBits` should likely be raised and challenges moved to an extension field before real value. This compounds directly with MUST-FIX #10 — treat them together.

## 5. CLOSING RECOMMENDATION

**Do not launch tonight.** Land all twelve MUST-FIX items (the four criticals are non-negotiable and several share the single `validatePQTxLocked` field-exclusion fix, so they are cheap to close together), add the regression tests called out for each, then re-validate. Before any real-value launch — not just tonight — commission an **independent cryptographic audit** of the STARK/accumulator/PQ stack and resolve the FRI/extension-field soundness decision. Per project memory this chain is currently test-only with no live value; keep it that way until the criticals are fixed and the crypto stack has had external review. The right next step is a focused fix-and-reverify cycle on consensus inflation, not a launch.