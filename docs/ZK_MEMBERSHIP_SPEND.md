# ZK accumulator-membership spend (Track A endgame / 100B)

## Migration designs (next, NOT yet wired — sound primitives + circuits exist)

### #1 — 256-bit commitment nodes (fix the 2³² collision weakness) — ✅ DONE
The commitment tree now uses 4-element (256-bit) nodes → ~2¹²⁸ collision resistance,
wired end-to-end and adversarially tested:
- **Hash:** `poseidon_wide.go` — width-8 Poseidon; `WideHash2(L,R)=perm8(L‖R)[0:4]`
  (truncated-permutation 2-to-1 compression, AIR-friendly; `JiveCompress` is also
  available as the safer-margin production option). Bijective/sensitive/collision-
  free over samples.
- **Tree:** `imt256.go` — `PoseidonIMT256` with `Node256` (4 Felt) leaves/frontier/
  root, `MerklePath256`, 32-B node encoding, snapshot state.
- **Circuits:** `spend256_air.go` / `mint256_air.go` — width-8 AIR (13 cols), the
  same membership/injection/booleanity structure as the width-3 version. Adversarial
  tests pass (honest, wrong-nullifier, forged-amount, non-member, tampered, binding).
- **Chain + wallet:** `cmTree` is `*PoseidonIMT256`; `block.Header.CMRoot`,
  `tx.ZKInput.Anchor`, `tx.ZKOutput.Leaf`, `ZKCoin.Leaf` are 32 B; validate/apply/
  template/snapshot/reorg all on `Node256`; wallet uses `ProveMint256`/`ProveSpend256`.
  End-to-end consensus tests pass (mint→spend→receive, inflation/double-spend/unknown-
  anchor/tampered rejected, ZK→ZK stealth transfer). Live on mainnet.

CAVEATS: `WideHash2` is truncated-permutation (collision-resistant at 2¹²⁸, but Jive's
feed-forward is the safer production choice — circuitising Jive needs a per-block
input-carry); and `R_F/R_P=8/22` for t=8 still need the official security-bound check.

### #3 — recipient-secret nullifier (full sender↔spend unlinkability)
Today the stealth sender knows the coin secret, hence the nullifier, so can detect
when their sent coin is spent. The fix is the Zcash-Sapling TWO-KEY structure, all
Poseidon (no ECC, STARK-friendly):
- Recipient keys: a **nullifier key** `nk` (private) and a **diversified address**
  `pk_d = H(ivk, d)` the sender uses to pay. `nk` and `pk_d` are independent — the
  sender knows `pk_d`, only the recipient knows `nk`.
- Note/leaf commits to `pk_d`, `amount`, `rho` (a per-note nonce), `blind`. Sender
  builds the leaf from PUBLIC `pk_d` (can't compute the nullifier).
- Nullifier `nf = H(nk, rho)` — only the recipient (with `nk`) can compute it, so the
  sender cannot link the spend. The spend circuit additionally proves spend authority
  (knowledge of the `ak`/`nsk` behind `pk_d`,`nk`) so a thief who only knows `pk_d`
  cannot spend.
- This needs a small key-derivation tree + ~2 more hash blocks + an authority check
  in the spend circuit, and diversified-address support in the wallet. Contained but
  subtle — must be adversarially tested (forge-nullifier, steal-with-pk_d-only).

### #3b — confidential amounts
Replace the public `amount` with a Pedersen/commitment value bound into the leaf,
and prove `Σ in = Σ out + fee` inside the STARK (a range + balance sub-circuit).
This is the largest remaining privacy build and the biggest add to audit surface;
do it last, after the base is reviewed.

## ✅ SHIPPED on mainnet (full pivot)
The fully-anonymous ZK spend is **live end-to-end on mainnet**. The canonical anonymity
accumulator is a **Poseidon incremental Merkle commitment tree** (`pkg/stark/imt.go`),
committed in each block header (`block.Header.CMRoot`). The lifecycle:

- **Mint (shield):** `tx.ZKOutput{Leaf, Amount, MintProof}` moves public value into a
  commitment `L = Hash2(Hash2(serial,amount),blind)`. A STARK **mint proof**
  (`stark.ProveMint`/`VerifyMint`) binds `L` to the public `Amount` while hiding
  `serial`/`blind`, so a creator cannot mint a leaf worth more than declared
  (anti-inflation). Minting creates NO transparent UTXO, so there is no cross-system
  double-spend.
- **Spend:** `tx.ZKInput{Serial, Amount, Anchor, Proof}` reveals the nullifier
  `serial` + public `amount` and proves, with a transparent STARK
  (`stark.ProveSpend`/`VerifySpend`), membership of `L` against a recent header root
  (`Anchor`) — hiding which coin. Verified with NO ring/coin set.
- **Soundness guards (all consensus-tested, `pkg/chain/zk_e2e_test.go`):**
  double-spend (serial reuse) rejected; inflation (forged mint/spend amount)
  rejected; unknown anchor rejected; tampered proof rejected; proof bound to its tx
  (`bind = CoreHash`) so it can't be lifted into another tx.
- **Value:** amounts are PUBLIC (a trivially-sound public sum via the existing
  generalized conservation proof — mint = publicOut, spend = publicIn). Amount
  privacy is the one remaining trade-off (a confidential-value leg can replace it).
- **State safety:** the tree frontier + anchor set + nullifier set are
  snapshot/restore/verify and reorg/replay-safe (mirrors the PQ accumulator;
  header `CMRoot` is the trustless checkpoint).
- **Wallet:** `wallet.CreateZKMint` / `CreateZKSpend`; chain accessors
  `ZKRoot`/`ZKPath`/`ZKFindLeaf`/`ZKDepth`.

**Hardening done:**
- **FRI grinding** (`friGrindBits=16`): a Fiat-Shamir proof-of-work nonce is bound
  before query positions are drawn, so a prover can't cheaply re-roll the transcript
  to dodge queries. With `ZKQueries=48` (2 bits each) soundness is now **~112-bit**.
- **Bounded anchor window**: the spend-anchor set is a rolling window of the most
  recent `config.MaxAnchorWindow` DISTINCT commitment roots (older anchors expire;
  snapshot/reorg-safe). NOTE the nullifier set is intentionally UNBOUNDED — a
  nullifier can never be forgotten without enabling double-spend; bounding it needs a
  non-membership accumulator (Track C).
- **Stealth ZK→ZK transfer** (`wallet.CreateZKMintTo` + `ScanZKCoins`): a coin can be
  minted payable to another party — its (serial, blind) derive from a stealth shared
  secret only the recipient can reconstruct (via `ScanZKCoins`), enabling private
  shielded payments end-to-end (tested: alice→bob→carol, unrelated parties can't
  discover). CAVEAT: the sender knows the shared secret, hence the nullifier, so the
  sender can detect when that specific coin is spent (sender↔spend linkability). Full
  unlinkability needs the nullifier to derive from the recipient's secret key INSIDE
  the circuit (Zcash-style) — a circuit redesign, the remaining privacy step.

**No-trust-debt lane (done):**
- **Proof size −30%** (668 KB → 466 KB at depth 4): all W trace columns are committed
  under ONE row-Merkle tree (leaf = hash of the whole row), so each opened position
  carries a single column authentication path instead of W. No soundness change
  (same values committed, adversarial tamper tests still reject). `pkg/stark/merkle.go`
  `RowMerkleTree`. FRI ±-pairing is the next size lever (not yet done).
- **Canonical Poseidon round constants** (`pkg/stark/poseidon_grain.go`): the ad-hoc
  blake2b constants are replaced by the reference **Grain LFSR** generator (80-bit
  LFSR seeded from field/sbox/n/t/R_F/R_P, 160-bit warm-up, self-shrinking output +
  rejection sampling). The MDS is the reference Cauchy matrix (x_i=i, y_j=t+j). The
  constants are now standard-derived + auditable rather than hand-rolled. CAVEAT:
  this follows the published algorithm but has NOT been cross-checked against the
  reference sage script's known-answer output — do that before production.

### ⚠️ Security finding surfaced during the params work (must fix for real value)
**The commitment-tree hash has only ~2³² collision resistance.** `PoseidonHash2`
outputs ONE Goldilocks element (~64 bits), so a Merkle node is 64-bit → birthday
collision in ~2³² work. An attacker could find two coin openings hashing to the same
leaf and break commitment binding (mint cheap, spend rich → inflation). The round
constants were never the real weakness: the OUTPUT WIDTH is. To close this the
commitment hash needs ≥256-bit output (≥4 Goldilocks elements per node, i.e. a wider
sponge / multi-element nodes) or a larger field, which also changes the AIR state
width and the Merkle-path encoding. This remains an open hardening item, a hard
blocker to close alongside external review and a reference-param cross-check.

Constants: `stark.ZKDepth=16` (65,536-coin anonymity set; raise for production),
`stark.ZKQueries=48` (~112-bit with grinding). **Still experimental:** Poseidon parameters
(blake2b-derived constants + Cauchy MDS) must be regenerated with the official
generator + security analysis before any non-test use; raising depth grows proof
size/time. The class-group accumulator + rings remain for the legacy transparent/
ring spend paths but are no longer the ZK-spend mechanism.

---


This is the research track that makes a full node **constant-size at any scale**
(the genuine 100B answer) by retiring the anonymity-ring set entirely: spends prove
membership against the O(1) accumulator value in zero knowledge, so the node no
longer stores `coins`/`coinList` (rings) at all. It also converges with the
post-quantum zk-STARK membership effort ([[POST_QUANTUM_ROADMAP]]) — same circuit.

**Status: design only. NOT implemented in consensus.** The crypto soundness is
existential (an unsound membership proof = forgeable membership = inflation; an
unsound nullifier = double-spend or deanonymization), so this must be designed,
adversarially tested, and ideally externally reviewed before it replaces the
current ring spend path.

## Where we are
`pkg/accumulator/zkmem.go` already provides `ZKMembership` — a **witness-hiding**
proof that "some accumulated prime p (with witness w, wᵖ=acc) is in acc", hiding
*which* element (blinded witness C = w·hˢ + a multi-exponent PoKE). It is an
explicit PROTOTYPE; its own comment lists the two gaps that make it unsound for a
spend:

1. **No double-spend nullifier binding.** Nothing ties the hidden element p to a
   unique, revealed nullifier — so the same coin could be spent repeatedly with
   independent proofs.
2. **No prime/range proof.** PoKE soundness in a group of unknown order requires
   the exponent to be a proper large prime in range (adaptive-root assumption).
   Without proving p is such a prime, membership can be FORGED (e.g. small or
   composite exponents, shared factors) → inflation.

## What a sound spend must prove (all in ZK, revealing only the nullifier)
For a coin committed as a prime p = HashToPrime(one-time key) accumulated in `acc`,
a spend must prove knowledge of (p, witness w, secrets) such that:
- **Membership:** wᵖ = acc (against a header-committed checkpoint) — have it
  (`ZKMembership`), modulo the prime proof below.
- **Validity of p (gap 2):** p is the canonical HashToPrime image of a real
  output key the prover owns, i.e. an odd prime in the correct range produced by
  the agreed hash-to-prime map. This is the hard part.
- **Nullifier (gap 1):** a deterministic, unlinkable nullifier N is revealed, with
  a ZK proof that N is derived from the SAME coin/secret as the membership proof
  (so one coin → one N, but N reveals nothing about p). Double-spends collide on N
  (checked against the disk-backed nullifier set — already O(1) RAM).
- **Ownership + value:** knowledge of the spend key, and that the spend's
  pseudo-commitment equals the coin's committed amount (reuse the existing
  ownership / value-equality proofs, bound to the same coin).

## Design sketch
- **Nullifier binding (gap 1, tractable):** give each coin a serial s chosen at
  creation (committed alongside the amount). The output's prime is p =
  HashToPrime(P) where P is the one-time key; bind N = H("OBX/null", s). The spend
  proves, in ZK, knowledge of (s, opening) consistent with the membership witness's
  hidden coin — a Schnorr-style equality of discrete logs across the commitment and
  the membership relation. Revealed: N. Hidden: which coin. Detect double-spend by
  N ∈ nullifier set.
- **Prime/range proof (gap 2, hard):** two viable routes:
  1. **Algebraic (CL-style):** a Camenisch-Lysyanskaya proof that p lies in a
     range and is of the correct form; classic but heavy and assumption-laden.
  2. **Transparent STARK (preferred, converges with PQ):** prove the statement
     "N = H(s) AND p = HashToPrime(P) AND wᵖ = acc AND I own P" inside a single
     transparent zk-STARK over the hash/accumulator circuit. No trusted setup
     (keeps Obscura's ethos), post-quantum, and it subsumes BOTH gaps in one proof.
     This is the substantial build — the same circuit the PQ roadmap needs.

## Phased plan
1. **Nullifier binding** (gap 1) — **DONE** (`pkg/accumulator/nullifier.go`,
   tested in `nullifier_test.go`). `ProveMembershipNullifier` ties a deterministic
   nullifier `N = U^p` (U an independent generator) to the membership proof's
   existing commitment `Z1 = g^p` via a shared-exponent proof (`EqualExp`), so one
   coin ⇒ one nullifier, `N` leaks nothing about `p`, and the
   double-spend-with-fresh-nullifier attack is rejected (adversarial tests pass:
   honest-verifies, deterministic-per-coin, tampered-nullifier-rejected,
   tampered-binding-rejected, equal-exp-rejects-mismatch). Algebraic — no STARK.
   **Still open: gap 2** (prime/range proof) — until then this is an experimental
   building block, not a sound stand-alone spend.
2. **Prime/range proof** (gap 2) — the STARK route over the hash-to-prime +
   membership circuit (co-designed with PQ). The biggest piece. **The transparent
   STARK ENGINE is now built and adversarially tested** (`pkg/stark/`); the
   membership/nullifier AIR on top is what remains — see "STARK engine status".
3. **New anon-spend tx version** carrying {ZKMembership-or-STARK, nullifier,
   ownership, value}, behind a version byte. Node verifies with NO ring/coin set →
   `coins`/`coinList` can finally be dropped (disk O(1)).
4. **Adversarial test battery:** forge-membership attempts (composite/small/oob
   exponents), double-spend (nullifier replay), linkability (statistical), value
   inflation. Must all fail.
5. **External review** before it becomes the default spend; until then it ships
   gated/experimental alongside the sound ring spend.

## STARK engine status (gap-2 phase 2, in progress)
A from-scratch, pure-Go **transparent zk-STARK engine** now lives in `pkg/stark/`.
No trusted setup, post-quantum (security rests only on blake2b collision
resistance + Reed-Solomon proximity). Built bottom-up, each layer adversarially
tested against ground truth:

- **`field.go`** — Goldilocks field `p = 2^64−2^32+1` (fast 64-bit reduction,
  2-adicity 32). Add/Sub/Mul/Inv/Exp cross-checked against `math/big` over 200k
  random + boundary inputs (`field_test.go`).
- **`ntt.go`** — number-theoretic transform over the field (Cooley-Tukey); root
  order, INTT∘NTT round-trip, and NTT-as-multipoint-eval all verified.
- **`merkle.go`** — blake2b Merkle vector commitment (the transparent, PQ vector
  commitment FRI binds layers with).
- **`transcript.go`** — Fiat-Shamir transcript (challenges derived only from
  already-committed data).
- **`fri.go`** — **FRI low-degree test** (the soundness core). Honest low-degree
  proofs verify; random/degree-exactly-d functions are REJECTED; tampered
  values/roots/final-constant, shrunken degree claims, and forged query positions
  all rejected (`fri_test.go`). ~40 queries ⇒ ~80-bit soundness at rate 1/4.
- **`poly.go`** — dense polynomial arithmetic + synthetic division (quotients).
- **`stark.go`** — a **complete DEEP-ALI STARK** for a concrete AIR (the
  "square-step" `a[i+1]=a[i]²+K` with boundary constraints), proving knowledge of a
  full valid trace while revealing only public inputs. Soundness = three
  transcript-bound checks: (1) FRI proves the DEEP polynomial `g` is low-degree;
  (2) at an out-of-domain `z`, the algebraic relation `CP(z)=Σαᵢ·qᵢ(z)` ties the
  trace's constraint satisfaction to the committed composition; (3) at each FRI
  query point, `g` is cross-checked to equal the DEEP combination of the committed
  trace `f` and composition `CP` — binding the abstract low-degree object to the
  real commitments. Adversarial tests reject invalid traces (at prove time, via a
  nonzero division remainder), forged public outputs, forged out-of-domain values,
  tampered `CP(z)`, tampered openings, wrong `K`, and a 64-case fuzz-tamper sweep
  (`stark_test.go`). Race-clean.

### Generalized to a real AIR + the hash, and a ZK hash-preimage proof
Built on the engine above (all in `pkg/stark/`, adversarially tested, race-clean):

- **`air.go` + `arith.go`** — a **general multi-column AIR STARK** (the square-step
  construction generalized to W trace columns, K transition constraints, and public
  periodic columns for round constants / selectors). A circuit writes its
  constraints ONCE over a generic field environment (`cenv[T]`); the prover
  instantiates it with polynomials, the verifier with scalars — one source of truth.
  Validated on a 2-column coupled recurrence (`air_test.go`): honest verifies; bad
  trace rejected at prove (non-exact quotient); forged public output and tampered
  column openings rejected.
- **`poseidon.go`** — **Poseidon over Goldilocks** (x⁷ S-box, Cauchy MDS,
  8 full + 22 partial rounds), the STARK-friendly hash. Tested: deterministic,
  bijective permutation (no collisions in 20k), sensitive, collision-free Hash2 over
  50k samples. *Parameters are EXPERIMENTAL* (blake2b-derived constants, Cauchy MDS)
  — real use needs the official parameter generator + security analysis.
- **`poseidon_merkle.go`** — **Poseidon Merkle tree + nullifier** (the membership
  structure + `N = H(serial)`), in the CLEAR; tested (honest path verifies, wrong
  leaf / corrupted sibling rejected, nullifier deterministic + collision-free).
- **`poseidon_air.go`** — a **zero-knowledge Poseidon preimage STARK**: proves "I
  know `s` such that `H1(s) = y`" revealing only `y`. The whole round function
  (add-constants → x⁷ → MDS) is one uniform transition constraint with periodic
  columns supplying per-round constants + full/partial selector + an active selector
  for padding rows. **This is the nullifier sub-proof of the spend, proven in ZK.**
  Tested (`poseidon_air_test.go`): honest verifies; wrong output rejected; a trace
  whose output is forged without a real preimage is rejected at prove; tampered
  proof rejected; the preimage never appears in any revealed/out-of-domain value.

**What remains for the full spend:** the **Merkle-membership path circuit** —
a multi-instance of the proven preimage circuit (D chained Poseidon compressions)
with injection/swap constraints (path-bit selectors choosing left/right at each
level) and the root as a public boundary, then **binding** it to the same coin as
the nullifier + ownership/value legs. This is engineering on the now-validated
engine (chaining + selectors), not new cryptography. It converges with
[[POST_QUANTUM_ROADMAP]]: the PQ Merkle accumulator IS the STARK-friendly
membership structure (RSA/class-group ops are STARK-hostile, so the accumulator
becomes a Poseidon Merkle tree). Until that circuit exists, is adversarially
tested, and the parameters are hardened, none of this is wired into consensus.

## Payoff
With this + the disk-backed nullifier set (done) + non-membership pruning of the
nullifier set (Track C), a full node holds only: header chain (or a compressed
proof), the O(1) accumulator value(s), and a rolling window of recent blocks —
**constant size at 100B and beyond, and post-quantum.** That is the endgame.

---

## Scaling + confidential amounts (Phases A/B/C)

### Phase A — UNLIMITED coins at constant proof cost — ✅ DONE
A single growing Merkle tree makes proofs more expensive as the coin set grows
(cost ~depth = log capacity), and higher arity doesn't help (total hash work
≈ depth·arity·node-size is minimized near binary). So we DON'T grow the tree:
- **`EpochIMT` (`pkg/stark/imt256_epoch.go`):** fixed-depth (2^ZKDepth coins per
  epoch) trees; when one fills, roll to a fresh one. Total coins UNBOUNDED; per-proof
  depth (hence size/time) CONSTANT. Every epoch root (current + recent finalized) is a
  valid spend anchor. The spend circuit is unchanged — it already proves against a
  `(root, path, depth)`.
- **Chain-wired:** `cmTree` is now `*EpochIMT`; `ZKWitnessFor(leaf)` returns a coin's
  EPOCH root + path (a coin is a member of its own epoch). Snapshot/reorg-safe. Tested
  on-chain (`TestZKEpochRolloverOnChain`): minted past several epoch boundaries, spent
  an old-epoch coin against its finalized root at fixed depth.
- `ZKDepth` is now a var (so deployments raise the anonymity set: 20 → ~1M, 22 → ~4M;
  the tradeoff is anonymity set = one epoch, the standard sound choice).

### Phase C — confidential amounts — CORE ✅ DONE, integration designed
Hiding amounts requires checking `Σ in = Σ out + fee` over HIDDEN amounts. Binding a
hidden amount to an ed25519 Pedersen commitment would need ed25519 scalar-mult inside
a Goldilocks STARK (infeasible). The innovation: **do value conservation entirely
in-field**, no cross-system binding.
- **`range_air.go` (`ProveRange`):** in-circuit range proof via bit-peeling — proves a
  value ∈ [0,2^n). Crucially rejects the field-WRAPAROUND case (a "negative"/huge
  amount aliasing to a small one), the anti-inflation guard.
- **`value_air.go` (`ProveValueBalance`):** proves `a_in = a_out + fee` with `a_in`,
  `a_out` HIDDEN (only fee public) and both range-checked. Tested incl. the wrap-
  inflation attack (a_out = P−1, fee = 2, a_in = 1: balance holds mod P but a_out is
  out of range ⇒ rejected).
- **Full confidential SPEND — ✅ BUILT + tested (`cspend_full.go`, `cspendFullCircuit`):**
  the complete monolith — one proof that spends a member coin (hidden `a_in`, nullifier
  `serial` revealed) AND mints a fresh output coin `leaf_out` (hidden `a_out`), proving
  value conservation `a_in = a_out + fee` and range-binding both amounts, revealing only
  `{serial, root, leaf_out, fee}`. Built via a `reset` transition that seeds the output
  leaf input right after the membership root, a constant `ain` column transporting the
  hidden `a_in` to the balance row, and two bit groups for the ranges. Adversarial tests
  pass: honest spend verifies; **inflation rejected** (a_out > a_in−fee impossible);
  non-member rejected; wrong/mismatched `leaf_out` rejected; tampered rejected; wrong-fee
  rejected. **TWO integration requirements surfaced + documented:**
  1. **Consensus MUST range-check the public `fee` (`0 ≤ fee < 2^vbits`).** The circuit
     does field balance; a "negative" fee `P−k` makes `a_in = a_out − k` hold mod P (more
     out than in). The reviewer flagged this; `TestCSpendFullNegativeFeeNeedsConsensusGuard`
     proves the circuit alone accepts it, so the tx/consensus layer must reject out-of-range
     fees. (Same as the transparent path already does for amounts via `parseZKAmount`.)
  2. **ZK engine — ✅ DONE (`zk_mask.go` + coset LDE).** Originally the transparent
     FRI-STARK was not zero-knowledge (OOD + FRI-query openings leak the witness; the `ain`
     constant column's OOD eval = `a_in`). Now the LDE/FRI/DEEP pipeline runs on the coset
     `airCoset·⟨ω_{N0}⟩` (disjoint from the trace domain H, so no opened position is a raw
     trace row) and every trace column is masked `f'(x)=INTT(trace)+Z_H(x)·r(x)`, `Z_H=x^T−1`,
     `r` with `2·nQueries+4` random coefficients. `f'=f` on H (constraints/soundness intact);
     off H every revealed value is randomized → honest-verifier ZK. Verified by
     `TestCSpendFullAmountPrivacy` + `TestZKMaskRandomized`, full stark+chain suites, and an
     independent adversarial soundness review (no break). SECURITY_AUDIT FINDING 4 RESOLVED;
     residual = external cryptographer sign-off on the quantitative ZK bit-bound.
- **Confidential INPUT — ✅ BUILT + tested (`cspend_air.go`, `cspendInputCircuit`):**
  the width-8 membership/leaf circuit with the `amount` boundary REMOVED — amount is now
  the hidden state column `s4` at the leaf-input row — plus an in-circuit range proof
  (`vbits` boolean witness columns recomposed at row 0, gated by `sel0`). It proves
  *"I own a member coin whose amount ∈ [0,2^vbits), revealing only the nullifier serial
  and the root, not the amount."* `ProveCSpendInput`/`VerifyCSpendInput`. Adversarial
  tests pass (honest hides amount + no leak into OOD evals; out-of-range amount rejected;
  non-member rejected; tampered rejected). This is the novel, hardest half — membership
  with a hidden, range-bound amount, all in-field.
- **Remaining integration (precise design, ~1 supervised build):** a full confidential
  ZK→ZK spend still needs the OUTPUT half + the value binding. Two equivalent shapes:
  - **(a) Monolith** — extend `cspendInputCircuit` with output-leaf blocks appended
    after the membership root: a *reset* transition at the membership-root row (selector
    `reset=1`) seeds a fresh leaf input `[serial_out,0,0,0, amount_out,0,0,0]`, two more
    Poseidon blocks fold in `blind_out` and yield `leaf_out` (boundary at the last row,
    public). Add a constant column `ain` carrying the hidden `amount_in` (constancy
    `ain_{i+1}=ain_i`, link `sel0·(ain−s4)`), range-bind `amount_out` with a second bit
    group, and bind balance at the output row: `selOut·(ain − s4_out − fee)=0`. One proof.
  - **(b) Decomposed** — keep `cspendInput` but have it additionally expose a hiding
    commitment `Cin = WideHash2(amount_in, r_in)`; an output circuit (mint + range)
    exposes `Cout`; the existing `value_air` gadget, extended to open `Cin`/`Cout`,
    proves `amount_in = amount_out + fee`. Three smaller proofs, chained by `Cin`/`Cout`.
  Monolith = one proof / one new selector (reset); decomposed = simpler per-circuit
  threading but 3× proofs + cross-proof commitment binding. Either way tx/chain/wallet
  then carry `leaf_out` + the (hidden-amount) input instead of public amounts. This is
  the biggest add to consensus proof surface → build supervised + re-audit before wiring.

### Phase B — S-box degree reduction (~3× cheaper proofs) — designed
The width-8 round constraint is degree ≈`9(T−1)` (x⁷ × selectors), which blows up the
FRI domain. Decomposing the S-box into auxiliary witness columns — `sq=x²`, `qd=sq²`,
`pow7=qd·sq·x` (each degree ≤2-3 in trace columns) — drops the max constraint degree to
≈`4(T−1)`, shrinking the FRI domain ~2-3× → ~2-3× smaller/faster proofs, with the extra
columns nearly free under the combined-row commitment. Lower priority than A/C since
epoch sharding already makes per-proof cost CONSTANT (not growing with supply); it's a
constant-factor win. Soundness-critical circuit change → needs re-audit.
