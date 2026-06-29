# Obscura (OBX) — Deep Security Audit, 2026-06-28

20 parallel auditors, one per subsystem, each reading the real code and probing for
mint-from-nothing / theft / deanonymization / chain-halt, followed by a synthesis pass.
Full per-subsystem findings JSON: the workflow run output (`deep-security-audit-20`,
run `wf_289859fa-402`).

**Totals:** 0 critical, **6 high**, 9 medium, 17 low, 15 info (20/20 subsystems audited).

> This audit is a code review by AI auditors. It does NOT replace the external
> cryptography audit that gates value-bearing mainnet. Several load-bearing soundness
> questions (below) can only be settled by a human specialist.

## Overall posture

Not safe to hold real value today, but the monetary substrate is genuinely strong:
the class-group accumulator uses a no-trusted-setup nothing-up-my-sleeve discriminant,
the Goldilocks field + F(p^2) extension check out arithmetically, Pedersen commitments
and 64-bit range proofs conserve value soundly, and **supply conservation is airtight
across every tx kind** (transparent, anon, ZK, CZK, swap, vault, PQ). Double-spend,
replay, cross-fork and cross-epoch protections are sound and live. The scariest
theoretical issue (mint-from-nothing via PoKE forgery) is NOT consensus-wired today.

The single biggest blocker is a remotely-triggerable consensus DoS (below). The
second-order blocker is that the ZK value endgame rests on FRI soundness the code
itself documents as ~65 provable bits vs the ~112-bit target.

## Top findings (ranked)

| # | Sev | Subsystem | Issue | Location |
|---|-----|-----------|-------|----------|
| 1 | HIGH | FRI/AIR | **Consensus-halt DoS**: unbounded prover-chosen `pf.Degree` -> `RootOfUnity` panic / div-by-zero in verify; one cheap crafted ZK-spend tx crashes every validating node. | `air.go:290-299` |
| 2 | HIGH | PoKE / ZKMembership | `VerifyZKMembership` proves nothing about *which* element is accumulated; trivially forged (`C=acc,p=1,y=0`). Latent mint-from-nothing if ever wired as a spend authorizer (not wired today). | `zkmem.go:55` |
| 3 | HIGH | Commitment tree | ZK leaves are never persisted in snapshots; after restart or fast-sync all pre-snapshot shielded coins become witness-unavailable = unspendable (coin freeze). | `imt256.go:133-179` |
| 4 | MED | FRI / field | Deployed FRI ~65 provable bits vs ~112 conjectured; contradictory in-code comments; gates the go-live endgame. | `chainparams.go`, `fri.go:25-34` |
| 5 | MED | Reorg | Deep partition-recovery reorg (up to PoWSeedLag=512) can't heal on a pruned tall chain: snapshot retention (2) only covers MaxReorgDepth=100 -> permanent minority split. | `snapshot.go:118` |
| 6 | MED | Poseidon | Width-8 round counts RF=8/RP=22 copied from width-3, never validated for t=8; this hash IS live in ZK spend/mint. | `poseidon_wide.go:16-20` |
| 7 | MED | Poseidon | No domain separation across Merkle nodes / note commitments / nullifiers / addresses (all bare `WideHash2`); soundness leans entirely on circuit geometry. | `poseidon_wide.go:105-140` |
| 8 | MED | PoKE / nullifier | `EqualExp` nullifier binds exponents only mod ell; binding/extractability of the ad-hoc shared-R construction is unproven. | `nullifier.go:66` |

## Cross-cutting themes

- **FRI proximity-gap conjecture dependence** (recurs in FRI, field, emission). Three
  auditors independently land on the ~65-provable-vs-~112-conjectured-bit gap. This is
  the load-bearing soundness assumption for the whole ZK money path; code review cannot
  resolve it.
- **"Sound only because of the circuit, not the primitive."** Poseidon domain
  separation, Fiat-Shamir public-input binding, and hash-to-prime nonces share a
  pattern: the primitive is permissive and safety rests on AIR constraint geometry. Each
  is one refactor away from a forgery.
- **NUMS / unknown-discrete-log assumptions stated but not provable from code** — notably
  the non-RSA PoKE fallback generators use a *publicly known* discrete log
  (`g^H(seed)`), breaking the independence the blinding/nullifier arguments assume on the
  production class-group backend.
- **Conservation is the bright spot** — robust and end-to-end across all subsystems.

## Needs a specialist (code audit cannot settle)

- FRI proximity-gap / list-decoding conjecture at (rate 1/4, 48 queries, 16 grind,
  coset, ext-field alpha, blowup 4): rely on the conjecture, or raise query count to the
  provable bound?
- Poseidon width-8 round counts (RF=8/RP=22, alpha=7, t=8, Goldilocks) vs the
  Groebner/interpolation bound (run the official parameter + security scripts).
- `EqualExp` nullifier extractability for equal integer exponents.
- Class-group general-case Dirichlet composition (e>1 branch) correctness.
- In-circuit ZK/CZK amount-binding AIR constraints (leaf-value = declared amount,
  a_in = a_out + fee).
- Torsion handling in range-proof bit commitments (OR proof vs explicit subgroup check).
- NUMS unknown-dlog of the Pedersen H generator.

## Fix/review these 5 before anything else

1. **FRI/AIR verifier** (`air.go`, `serialize.go`) — hard degree cap + `recover()` around
   consensus proof verification. Live, cheap, network-wide halt. Non-negotiable first.
2. **FRI/field soundness params** (`chainparams.go`, `fri.go`) — external sign-off on the
   proximity-gap conjecture or raise `ZKQueries`; reconcile contradictory comments.
3. **Poseidon width-8** (`poseidon_wide.go`, `poseidon_grain.go`) — validate RF/RP for
   t=8, add Grain KAT vectors. Live in consensus ZK; a break is direct money-printing.
4. **Commitment-tree leaf persistence** (`imt256.go`, `imt256_epoch.go`, `chain.go`) —
   persist/reconstruct ZK leaves across snapshot/restart/fast-sync before any shielded
   value is held.
5. **PoKE ZKMembership + EqualExp + non-RSA generators** (`zkmem.go`, `nullifier.go`) —
   gate the forgeable `VerifyZKMembership`, replace `EqualExp` with a standard equal-DL
   PoKE2, implement true hash-to-class-group generators; add a layering test forbidding
   consensus packages from importing these verifiers.

## Note on the just-shipped ZK CLI

Finding #1 (the `Degree` panic) lives in the ZK-spend verification path that the new
`zkmint`/`zkspend` CLI exposes. On the value-less test chain this is acceptable, but the
degree cap + recover must land before that path can be reached by untrusted input on any
network that matters.
