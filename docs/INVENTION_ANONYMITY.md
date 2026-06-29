# Invention Log — Block 2: ZK Sender Anonymity

Produced with the invention methodology + Methodology-Tree brainstorming engine.

## 1. Challenge
Make the headline feature real & SOUND: a spend that hides WHICH output is being
spent among the whole coin set, with no trusted setup, no decoy ring, and no
theft/inflation/double-spend. (The earlier accumulator-witness ZK was unsound:
witness publicly recomputable, nullifier unbound.)

## 2. Baseline best (researched)
- Monero ring signatures: sound but **O(N)** and a *bounded* decoy ring.
- Zcash SNARKs: log-size but **trusted setup**.
- Our accumulator+ZK: constant-size but **unsound as implemented**.

## 3. Brainstormed "better-than" (engine output) + ranking
Engine ranked, condensed:
- **Primitive (a_primitive):** #1 **Groth-Kohlweiss 1-of-N (Lelantus)** — prove one
  Pedersen commitment among N opens to 0; **log-size**, pure Sigma on
  edwards25519, **no trusted setup**. #2 Bootle et al. (same asymptotics). #3
  Zerocoin double-DL Sigma (linear). #4 GK+Bulletproofs unified (coupling-risky).
  #5 Merkle+ZK (nonstandard hash-to-curve). → **ADOPT GK.**
- **Tag (b_tag):** **DLEQ-bound key image** `T = x·H` where coin key `P = x·G`;
  prove `DLEQ(G,P,H,T)` for the same `x` AND 1-of-N that `P ∈ {P_i}`. **Critical:
  T must be in the Fiat-Shamir hash** or it is malleable → double-spends. → ADOPT.

Rejected (off-limits): trusted setup, PoS, decoy rings.

## 4. Decision: Triptych-style linkable one-out-of-many
Combine GK (#1) + DLEQ key image (#2) = a **log-size linkable ring signature**
(Triptych, Noether 2020) over the FULL coin set. Coins are keys `C_i = x_i·G`
(commitments to 0). A spend proves, for a hidden index `l`:
- **membership/ownership:** `C_l = x_l·G` (knowledge of the opening), hiding `l`;
- **linking tag:** `T = x_l·U` (U a third NUMS generator), unique per coin and
  unlinkable, used as the double-spend nullifier.

**Key coupling trick (clean, implementable, tested):** GK's final response
`z_d = x_l·xᵐ − Σ ρ_k·xᵏ` and randomizers `ρ_k` are *reused* against `U`. Adding
tag commitments `cdt_k = ρ_k·U`, the verifier's extra check
`xᵐ·T − Σ xᵏ·cdt_k == z_d·U` holds **iff `T = x_l·U` for the same hidden `l`** —
binding the key image to the proven coin with no extra responses. `T` is folded
into the Fiat-Shamir challenge (per the engine's malleability warning).

Properties (test-verified): completeness; **index-hiding** (proofs for different
`l` are indistinguishable); **soundness** (false membership rejected);
**linkability** (same key → same `T`; forged/mismatched `T` rejected); log-size
`O(log N)`.

Implemented in `pkg/commit/oneofmany.go`; tests in
`tests/critical/anonymity/`. This is the cryptographic core that makes Obscura's
sender-anonymity claim true; full consensus tx-format integration (coin keys +
tag set) is the follow-on wiring step.
