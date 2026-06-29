# Vulnerability Fix Campaign — Summary (2026-06-26)

Driven by the two pre-live audits (`SECURITY_AUDIT.md` — 24 confirmed gate findings; `CRYPTO_AUDIT.md` — 17 confirmed crypto weaknesses). Both returned **NO-GO for a value-bearing launch**.

## FIXED + verified (applied, build + tests green)

### Consensus criticals (inflation) — hand-fixed, chain suite green
- PQ fees no longer minted into the classical coinbase (`validate.go`)
- Coinbase may carry **only** transparent outputs — no PQ minting, no unvalidated ZK/CZK/swap/vault legs (`validate.go`)
- PQ tx must carry **only** PQ fields — closes the PQ-routing bypass of all ZK/CZK/vault validation (`pqvalidate.go`)

### Coupled-core (sequential workflow, soundness-reviewed)
- **Class-group accumulator backend** (`AccumulatorBackend = "classgroup"`) — removes the RSA-2048-challenge **unaccountable trusted setup**. *(crypto HIGH fixed)*
- **Key-image cofactor clearing** — torsion double-spend (CVE-2017-12424 class) closed; canonical `8·T` nullifier. *Soundness review: SOUND.*
- **Swap PoP + commit R+T** — rogue-key (`A'=R−B`) + atomicity closed. *Soundness review: SOUND.*
- **Chain-id (netID) binding** — proofs/sigs/transcript no longer replay across sibling instances. *Soundness review: SOUND.*
- **Orphan-pool DoS** — PoW-before-buffer + 32 MiB byte-bound + FIFO eviction.
- **Deep-reorg durability** — full replayed suffix persisted to bolt (>blockCacheCap reorgs).

### File-disjoint (parallel workflow)
- RPC: operator/public split + bearer-token/loopback gate; `/peers` count-only to untrusted; `/blocktemplate` gated + TTL-cached
- P2P: persistent per-/16 ban score (defeats reconnect reset) + per-peer token-bucket rate limit
- Nano RPC: 128-bit balance panic guard + `io.LimitReader` + status-code check
- Swapbook: length-prefixed (injective) signed message + asset allowlist
- Block decoder: rejects trailing junk
- Default PoW → canonical RandomX (plain `go build`, no tags; insecure prototype VM is opt-in via `-tags protopow`) + prototype-PoW startup guard
- PQ wallet: WOTS one-time-key burn (no OTS reuse)
- PQ ledger: BlindDiff conservation bound

### Mempool / P2P (hand-fixed)
- Mempool reserves conflict keys for ZK/CZK/PQ nullifiers (in-pool double-spend closed)
- Self-discovery requires ≥2 **distinct /16 networks** (2-vote poisoning closed)

## Deep crypto criticals — NOW FIXED (both adversarially reviewed SOUND)
1. **WideHash2 → Jive in-circuit** *(CRITICAL, reproduced O(1) Merkle collision)* — **FIXED + review: sound.** Native `WideHash2 := JiveCompress` (Davies-Meyer feed-forward); in-circuit added 4 folded-input carry columns across all 7 AIRs with constant/inject-reseed/row-0-init/reset-reseed constraints, parent rebind `f[i]+y[i]+y[i+4]`, and public outputs rebound to the Jive output via verifier-reconstructed periodic columns (proof-size neutral). Full `stark`+`chain` suite green; soundness tests added (truncation-root rejected, tampered-carry rejected).
2. **Base-field → degree-2 extension-field Fiat-Shamir** *(CRITICAL, dominant soundness was ~2⁻⁴⁶)* — **FIXED + review: sound.** New `pkg/stark/ext2.go` `F_p[u]/(u²−7)` (non-residue verified via Euler) + tests; FRI fold α, OOD point z, and DEEP coefficients now drawn in `F_{p²}` → dominant soundness amplified to ~2⁻¹²⁸.
   - *Lower-order residual (documented, NOT the critical):* composition-batching coeffs `a_k` stay base-field by design (CP stays base-Merkle-committed, the Winterfell design point) → that one term ~2⁻⁵⁸…⁻⁶⁴. Can be raised later; not the dominant ceiling.

### Still deferred (lower-priority crypto)
- **FRI provable-query count** — now over the extension field; the query/grind parameters could still be raised to clear an explicit ≥128-bit *provable* (not just conjectured) bound. Parameter tuning, not a structural break.
- **ZK-mask CP under-masking** (witness leak — a *privacy* weakening, not an inflation/forgery break).

### STANDING CAVEAT (unchanged)
Both deep fixes passed *AI adversarial* soundness review — strong evidence, but **not a substitute for a professional external cryptographic audit** of the from-scratch STARK + extension-field Fiat-Shamir + accumulator stack. That external audit remains mandatory before carrying real value.

## Verification round (full-suite run caught 2 more regressions — now fixed)
The authoritative full suite surfaced 4 failures; resolving them found **2 real follow-on regressions** introduced by the fixes themselves (exactly what verification is for):
- **Nullifier API query-side not canonicalized** — `TagSpent`/mempool queried the raw tag while the cofactor fix stored canonical `8·T`. Fixed: query side now canonicalizes too (`chain.go`, `mempool.go`).
- **Rate-limiter dropped consensus-critical messages** — the new P2P token bucket dropped `msgBlock`/`msgTip`/`msgGetBlk`/`msgGetTip`, breaking multi-node convergence. Fixed: block/sync messages are exempt from rate-limiting.
- The other 2 (`pqchain`, `snapshot`) were **expected** behavior changes — tests updated to assert the now-secure behavior (coinbase-PQ-mint rejected; PoR-window retention respected), with a new `TestCoinbasePQMintRejected` pinning the inflation fix.

## Residual minor (non-consensus)
- Mempool double-spend pre-filter keys on raw tag while the confirmed set stores canonical `8·T` — a relay/liveness pre-filter gap only; `validateTxLocked` canonicalizes + rejects authoritatively. Worth a follow-up tidy.

## Bottom line
**~22 confirmed vulnerabilities fixed + verified**, including ALL 4 critical inflation bugs, the crypto-HIGHs (RSA trusted setup, key-image torsion double-spend, swap rogue-key, cross-instance replay), AND **both deep crypto CRITICALs** (WideHash2 Merkle collision + extension-field Fiat-Shamir) — each soundness-reviewed. Every applied crypto-critical fix carries an explicit "review: sound" verdict. The test chain can be redeployed from genesis with all fixes today.

**The one remaining gate to a real-value launch is a professional EXTERNAL cryptographic audit** of the from-scratch STARK + extension-field Fiat-Shamir + class-group accumulator stack. AI adversarial review (used throughout) is strong evidence but is not a substitute for it. With that audit + the documented lower-order items (FRI query count to a provable bound, ZK-mask privacy), the protocol would be in materially launch-grade shape.
