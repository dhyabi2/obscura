I have a comprehensive, cross-checked audit already in front of me. No additional code exploration is needed — the findings are confirmed against specific file/line locations. Here is the verdict.

---

# OBSCURA — FINAL CRYPTOGRAPHIC SOUNDNESS VERDICT

## 1. CRYPTO VERDICT — DO NOT LAUNCH WITH REAL VALUE TONIGHT

**Blunt answer: No. Launching real value tonight is cryptographically irresponsible.**

This is a from-scratch, never-externally-audited STARK + accumulator + adaptor-signature + key-image stack. That alone is a stop sign: every production privacy chain (Zcash, Monero, StarkNet) had its core primitives audited by specialists *before* carrying value, and several still shipped with bugs. Obscura proposes to skip that step entirely.

Worse, this is not a "no break found, just unaudited" situation. The review surfaced **two confirmed, reproduced critical breaks and seven confirmed high-severity weaknesses**, including a tree-compression collision exploit demonstrated in O(1) and a key-image torsion double-spend reproduced as a working exploit against live consensus code. The "112-bit soundness" claim in chainparams is objectively false — the real Fiat-Shamir ceiling is ~2^-46 to 2^-50 because every soundness-critical challenge is drawn from the 64-bit base field with no extension.

The single mitigating fact (per project memory: the chain is test-only with free genesis resets) is exactly why this is recoverable — but it is also the reason there is *no excuse* to rush real value onto it tonight. Fixing consensus is free right now. It will not be free after value is on-chain.

## 2. CONFIRMED BREAKS (exploitable, reproduced)

**[CRITICAL] WideHash2 Merkle compression is collision-broken in O(1)** (`pkg/stark/poseidon_wide.go:134`)
`WideHash2(L,R) = perm8(L‖R)[0:4]` is a truncated invertible permutation — no capacity, no feed-forward. The x^7 S-box (gcd(7,P-1)=1) and Cauchy MDS are both invertible, so the whole permutation inverts. Reproduced: (a) two distinct child pairs colliding to one parent, and (b) a forged membership path accepted by `VerifyPath256` against an honest root the attacker owns no leaf in. This is the in-AIR fold used by spend256/nfspend/cnfspend/cspend_full and is wired into consensus at `pkg/chain/zkspend.go:207`. The *only* residual barrier to outright inflation is an unintended structural accident (forced zero cells force a ~2^128 MITM at the splice node) — not a designed margin.
**Fix:** replace WideHash2 with `JiveCompress` (already in the same file, line 105 — Davies-Meyer feed-forward) for ALL tree nodes AND inside every AIR's `wideMerkleConstraints`.

**[CRITICAL] Base-field-only Fiat-Shamir caps soundness at ~2^-46..2^-50, not 112 bits** (`pkg/stark/transcript.go:53-56`, `fri.go:127`, `air.go:206-233`, `stark.go:49-58`)
No field extension exists anywhere in `pkg/stark`. Every soundness-critical challenge — FRI fold α, OOD point z, composition coefficients a_k, DEEP coefficients — is a single ~64-bit Goldilocks element. The DEEP and per-round FRI algebraic error terms are floored by |F|=2^64 and cannot be lowered by queries or grinding. Forgery work ≈ 2^46, enabling a false-statement proof (spend with no valid witness → inflation/double-spend). The `chainparams.go:15` "~112-bit" claim is false.
**Fix:** sample α, z, a_k, and DEEP coefficients from a degree-2/3 Goldilocks extension (≥2^128), as Plonky2/Winterfell/RISC Zero/ethSTARK do; keep trace/commitments in the base field.

**[HIGH] Key-image torsion double-spend** (`pkg/commit/anonspend.go:155`)
`VerifyAnonSpend` rejects only the identity point; no subgroup check / cofactor clearing on the tag T or ring members. Tags are byte-compared in the nullifier set (`validate.go:414`, `apply.go:150`). A working from-scratch exploit was reproduced: shifting T→T+torsion and injecting matching torsion into `cdt[0]` yields 3+ distinct valid nullifier byte-strings for one coin (up to 8 with full order-8 torsion) — the Monero CVE-2017-12424 class. Live in consensus; same defect in `oneofmany.go:289` and `VerifyKeyImage`.
**Fix:** store/compare cofactor-cleared (`MultByCofactor`) tags in the nullifier set; reject low-order T; cofactor-clear ring members on `SetBytes`.

**[HIGH] Swap atomicity not consensus-enforced + rogue-key aggregation** (`pkg/swap/swap.go:24-26,70-75`)
`K = A + B` is plain additive aggregation with no proof-of-possession (the code's "PoP" mitigation is never implemented). A claimer choosing `A' = A_rogue − B` controls K alone, signs a fresh independent signature under K, and claims the OBX *without* adapting the pre-signature — so `Extract = s_full − s'` is garbage, the counterparty never recovers the secret, the cross-chain leg never unlocks. Direct value theft in a 2-of-2 swap. Consensus stores no R/T, so it cannot enforce `sig.R == R+T`.
**Fix:** commit R and T on-chain and require `sig.R == R+T` in the claim path; switch aggregation to MuSig2 (or enforce a verified Schnorr PoP on each submitted contribution + commit-reveal).

**[HIGH] ZK mask budget undercounts CP openings** (`pkg/stark/zk_mask.go:32-35`)
The composition polynomial CP is never independently masked, yet CP(p) is opened at every FRI query and depends on the un-opened shifted neighbor f'(gH·p). Empirically: 194 distinct revealed points per column vs. mask rank 100 → ~94 linear relations whose constants are witness-dependent. The explicit ZK claim ("revealed evaluations leak nothing") is provably false. Full witness recovery not demonstrated, but the joint-ZK property does not hold as stated — a real defect for a privacy coin.
**Fix:** independently randomize CP (add a Z_H·r_CP mask, ≥~98 openings), or enlarge per-column maskCoeffs to cover all CP/quotient neighbor functionals (~4·nQueries+O(1)).

**[HIGH] Provable FRI soundness is only ~49 bits** (`pkg/stark/fri.go:25-34`, `chainparams.go:15`)
48 queries × 0.678 bits/query (proven unique-decoding bound) + 16 grind ≈ 49 bits. The ~112 figure needs the list-decoding/proximity-gap conjecture *and* an extension field. Both are absent. Breakable for value.
**Fix:** move challenges to an extension field, then re-derive query count to clear an explicit target under the provable bound (≈160+ queries for ~128 bits) or formally adopt the conjecture with sign-off.

**[HIGH] RSA-2048 challenge modulus = unaccountable trusted setup** (`pkg/config/params.go:181`)
Live backend is `rsa2048`, instantiating the group of unknown order over the 1991 RSA Factoring Challenge modulus. Whoever generated/retained p,q can extract arbitrary roots and forge membership witnesses (mint/false membership). The project's central "no trusted setup" claim does not hold for the shipping config.
**Fix:** set `AccumulatorBackend = "classgroup"` (genuine nothing-up-my-sleeve, already implemented), or run + publish an audited multiparty modulus ceremony — otherwise retract the no-trusted-setup narrative.

**[HIGH] No chain-id bound into any proof/signature/transcript** (`pkg/tx/tx.go:789`, `pkg/chain/apply.go:15-46`)
CoreHash, all commit-layer domains, and the STARK transcript bind no network/genesis id. Genesis is deterministic and constant; the live RSA group is constant. A ZK anon-spend proof or swap claim/refund signature replays verbatim across any sibling instance (relaunch after the "free" genesis reset, testnet, fork) that re-minted the same coins.
**Fix:** bind a 32-byte netID (blake2b of NetworkSeed ‖ genesis header ‖ group modulus) into CoreHash, every Fiat-Shamir domain string, the transcript label, and the swap signed message.

**[HIGH] PQ value-conservation forgery** (`pkg/pqtx/ledger.go:136-164`) — off the default consensus path (behind `pq` build tag), but within that subsystem `checkConservation` accepts an unbounded attacker-supplied `BlindDiff`, reducing the "commitment to zero" balance check to a linear identity the prover controls → unlimited inflation via Gaussian elimination (no lattice hardness needed). Keep off any value path; add aggregate norm bound + range/opening proof before promotion.

## 3. ASSUMPTIONS LEDGER (load-bearing, and their status)

| Assumption / parameter | Where | Status |
|---|---|---|
| FRI/DEEP challenges in a field large enough for soundness | `pkg/stark` (no extension) | **FALSE** — base 2^64 only; caps soundness ~2^-46..2^-50 |
| FRI proximity-gap / Johnson-radius list-decoding conjecture (2 bits/query) | `fri.go:25-34` | **Conjectured, unsigned-off**; provable bound = ~49 bits |
| WideHash2 collision/2nd-preimage resistance | `poseidon_wide.go:134` | **FALSE** — invertible, broken in O(1) |
| Poseidon t=8 round counts R_F=8/R_P=22 + Cauchy MDS subspace safety | `poseidon_wide.go:14,34-39` | **Unverified** — reused from t=3; no official analysis, no subspace-trail test |
| Strong-RSA / adaptive-root in unknown-order group | `params.go:181`, `rsa.go` | **Unaccountable trusted setup** — RSA-2048 challenge modulus; factorization-holder forges |
| Key-image T in prime-order subgroup | `anonspend.go:155` | **Not enforced** — torsion double-spend reproduced |
| Adaptor aggregate-key rogue-key safety + atomic extraction | `swap.go:24-26,70-75` | **Not enforced** — plain addition, no PoP, R/T not committed |
| ZK joint mask covers all revealed openings | `zk_mask.go:32-35` | **FALSE** — CP openings unmasked; ~94 leaked relations |
| Schnorr nonce uniqueness | `adaptor.go:54,104` | **RNG-only**, no RFC6979/synthetic binding; caller-supplied nonces in CoSignClaim |
| Module-SIS binding (PQ commitments) | `ajtai.go:48` | **Broken at shipped params** (off-path) |
| WOTS+ EU-CMA (non-RFC-8391 keyed hash) | `wots.go:65` | **Non-standard, unproven**; no concrete break (hybrid-protected) |
| MultiPoKE 2-exponent extractor + prime/range binding (gap-2) | `zkmem.go:34-70`, `nullifier.go:21-25` | **Unproven extractor; gap-2 open** (experimental, off-path) |
| Network-id domain separation | system-wide | **Absent** — cross-instance replay |

## 4. EXTERNAL-AUDIT SCOPE (prioritized, must-clear before any real value)

1. **STARK soundness core** — extension-field migration for all Fiat-Shamir challenges; FRI query-count vs. (provable bound or accepted conjecture); DEEP/ALI accounting. *(Resolves both criticals' soundness side + the two FRI/HIGH items.)*
2. **Poseidon-wide hash + Merkle compression** — switch to JiveCompress everywhere incl. AIR; official t=8 round-count + MDS subspace-trail analysis; regenerate constants. *(Resolves the collision critical + two Poseidon MEDIUMs.)*
3. **Key-image / one-of-many / anon-spend** — subgroup/cofactor handling end-to-end; nullifier canonicalization.
4. **Accumulator backend** — class-group composition/reduction correctness (once switched off RSA-2048); MultiPoKE extractor + prime-range proof if ever spend-authorizing.
5. **Adaptor / swap protocol** — MuSig2 or PoP+commit-reveal, consensus-enforced nonce binding (R+T), synthetic nonces.
6. **ZK joint-ZK proof** — ethSTARK-style formal joint zero-knowledge over CP/DEEP openings + simulator/statistical test.
7. **Domain separation** — netID binding review (light, but must land).

## 5. CLOSING RECOMMENDATION

**Do not go live with real value tonight, full stop.** The honest path:

1. **Keep the chain test-only.** Genesis resets are free now — use that freedom.
2. **Fix the consensus-path breaks immediately** (free today, ordered): JiveCompress for all tree nodes + AIRs; extension-field challenges; key-image cofactor clearing; netID domain separation; swap nonce-binding + MuSig2/PoP. Switch `AccumulatorBackend` to `classgroup`. Correct the false `chainparams.go:15`/`:7` soundness comments now.
3. **Then** commission the prioritized external audit above. A from-scratch STARK + accumulator + adaptor + key-image stack carrying value without specialist sign-off is not defensible regardless of how the fixes go.
4. Consider a public bug-bounty on the test chain as an intermediate gate before any value-bearing launch.

No false comfort: even after every fix above lands, the system has had zero external cryptographic review, and the assumptions ledger has multiple unverified, security-critical entries. Real value should wait for items 1–6 of the audit scope to clear.