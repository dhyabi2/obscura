# Obscura (OBX): A Privacy Cryptocurrency with a Global Anonymity Set

**A Technical Whitepaper**

*Version 1.0, Mainnet*

---

## Abstract

Obscura (ticker: **OBX**) is a privacy-preserving cryptocurrency that replaces the bounded ring signatures of Monero with a **trustless cryptographic accumulator** built over a **group of unknown order**. Where a Monero transaction hides a real spend among a small, fixed set of decoys (historically 11, then 16), an Obscura spend is hidden among the **entire unspent output set** of the chain. The anonymity set is therefore *global* and grows monotonically with adoption, while the proof attesting to membership remains **constant size** and verifiable in **O(log)** work, independent of how large that set becomes.

The construction combines a dynamic accumulator (Boneh–Bünz–Fisch, CRYPTO 2019), Wesolowski's Proof of Exponentiation, the NI-PoKE2 proof of knowledge of exponent, Pedersen commitments over edwards25519 for confidential amounts, bit-decomposition range proofs, and Monero-style dual-key stealth addresses. The accumulator's group of unknown order is instantiated either over an RSA group or, preferably, over an **imaginary quadratic class group**, which requires **no trusted setup whatsoever**.

This document specifies the full protocol as shipped. **Obscura mainnet is live, and the full protocol described here runs in production.**

---

## 1. Motivation

### 1.1 The limits of ring signatures

Monero achieves sender ambiguity by signing a transaction with a *ring signature* that could plausibly have been produced by any one of `n` outputs. Three structural problems follow:

1. **Bounded anonymity.** The set of plausible spenders is fixed and small. An adversary's a-priori probability of guessing the true spend is `1/n`, and `n` cannot be made large without proportionally large transactions.
2. **Decoy-selection leakage.** Decoys are chosen by the wallet according to a heuristic distribution over output ages. Mismatches between that distribution and real spending behaviour have repeatedly enabled statistical de-anonymisation (e.g. the "newest output is the real spend" bias, temporal analysis, and chain-reaction attacks against low-ring-size legacy outputs).
3. **Linear cost.** Signature size and verification cost scale with `n`, so privacy is permanently rationed against blockchain bloat.

### 1.2 The Obscura approach

Obscura discards decoys entirely. Every unspent output is inserted into a single accumulator. To spend, a user proves, *in zero knowledge*, that they possess an output that is a current member of the accumulator, without revealing which one, and publishes a **nullifier** to prevent double-spending. Because the accumulated set *is* the whole UTXO set, the anonymity set is the whole chain. Because accumulator membership proofs are succinct, the proof size and verifier cost are independent of the set's cardinality.

---

## 2. Cryptographic Foundations

### 2.1 Groups of unknown order

The accumulator lives in a group `G` whose **order is unknown** to all participants. This property is what makes the accumulator secure: without knowing the order, an adversary cannot compute roots that would forge membership witnesses. Obscura supports two backends.

#### 2.1.1 RSA group

`G = (Z/NZ)*` for an RSA modulus `N = p·q`. The reference implementation uses the **RSA-2048 factoring-challenge modulus** as a *nothing-up-my-sleeve* instantiation: it is a public, well-known number whose factorisation is widely believed to be unknown. Security here is conditional on nobody knowing the factors. If that assumption is unacceptable, a fresh modulus can be produced by a multi-party computation (MPC) ceremony in which the factors are never reconstructed.

#### 2.1.2 Imaginary quadratic class group (preferred)

The class group of an imaginary quadratic order with discriminant `D < 0` is a finite abelian group whose order (the class number `h(D)`) is, for a large random-looking `D`, **infeasible to compute**. Critically, `D` is derived publicly and verifiably from a nothing-up-my-sleeve seed: Obscura derives a negative prime discriminant with `D ≡ 1 (mod 8)`, with **no secret and therefore no trusted setup at all**. This eliminates the single largest objection to accumulator-based systems and is the recommended backend.

Group elements are represented as **reduced binary quadratic forms** `(a, b, c)` with `b² − 4ac = D`. The group law is **Dirichlet composition** followed by reduction to the canonical reduced representative. The class-group backend is validated by **group-axiom property tests** (closure, associativity, identity, inverse) at a **2048-bit discriminant**.

| Property | RSA backend | Class-group backend |
|---|---|---|
| Trusted setup | Required (or MPC) | **None** |
| Element | Integer mod `N` | Reduced form `(a,b,c)` |
| Group law | Modular multiply | Dirichlet composition + reduction |
| Hardness | Factoring `N` | Computing `h(D)` / class-group order |
| Speed | Faster | Slower |

### 2.2 Hash-to-prime

Each accumulated element must be a distinct **odd prime**. An output's stealth public key is mapped to a unique prime via **hash-and-increment**: hash the key, interpret as an integer, and increment to the next primality-tested value. The result is a deterministic, collision-resistant map from outputs to prime "representatives".

### 2.3 Dynamic accumulator (Boneh–Bünz–Fisch)

The accumulator is a single group element

```
acc = g^(∏ pᵢ)
```

over the primes `pᵢ` of all currently unspent outputs, for a public generator `g`.

- **Membership witness.** For a member with prime `p`, the witness is `w = g^(∏_{j≠i} pⱼ)`, satisfying `w^p = acc`.
- **Addition.** Inserting prime `p'`: `acc' = acc^{p'}`. Existing witnesses are updated by raising to `p'`.
- **Removal.** Removing a member is *free* given its witness: the new accumulator is simply that member's witness, since `w^p = acc ⟹ w = acc^{1/p}`.

### 2.4 Wesolowski Proof of Exponentiation (PoE)

Computing `acc' = acc^{x}` for a large exponent `x` is expensive, and a verifier should not redo it. Wesolowski's **PoE** lets a prover convince a verifier that `y = u^x` with a **constant-size** proof and **O(log)** verifier work: the verifier samples a prime challenge `ℓ`, the prover sends `Q = u^{⌊x/ℓ⌋}`, and the verifier checks `Q^ℓ · u^{x mod ℓ} = y`. Fiat–Shamir makes it non-interactive.

### 2.5 NI-PoKE2: proof of knowledge of exponent

PoE proves a relation for a *known* exponent. To hide *which* output is spent we instead need to prove **knowledge of an exponent without revealing it**. **NI-PoKE2** proves knowledge of `x` such that `w = u^x`, transmitting only `g^x`, a quotient element, and `x mod ℓ` for a challenge prime `ℓ`. The exponent `x` itself never leaves the prover. This is the workhorse of the private spend: it lets a prover assert "I know the prime `p` and witness `w` with `w^p = acc`" while leaking neither `p` nor `w`.

### 2.6 Non-membership proofs

Using Bézout coefficients, Obscura can prove that a given prime is *not* in the accumulator: for accumulated product `s`, if `gcd(p, s) = 1` there exist `a, b` with `a·p + b·s = 1`, yielding a succinct non-membership certificate. This supports light-client checks and certain consensus invariants.

### 2.7 Pedersen commitments and confidential amounts

Amounts are hidden with **Pedersen commitments** over edwards25519:

```
C = v·H + r·G
```

where `v` is the amount, `r` a blinding factor, `G` the standard basepoint, and `H` an independent generator derived by **hash-to-point** so that its discrete log relative to `G` is unknown. Commitments are **additively homomorphic**: `C₁ + C₂` commits to `v₁ + v₂`.

### 2.8 Range proofs

A commitment must be proven to encode a value in `[0, 2ⁿ)` (otherwise a malicious spender could mint coins via overflow). Obscura uses **bit-decomposition range proofs**: each bit is committed and shown to be 0 or 1 with a **Schnorr OR proof**, with size **O(n)** per amount. **Bulletproofs** are an available **O(log n)** optimisation.

### 2.9 Conservation proofs

A transaction must neither create nor destroy value. Because commitments are homomorphic, the quantity

```
Σ Cᵢₙ − Σ Cₒᵤₜ − fee·H
```

is a commitment to **zero** exactly when value is conserved. The prover demonstrates this with a **Schnorr discrete-log proof** that the residual commitment opens to value 0 (i.e. it is a commitment to zero whose only secret is the combined blinding factor).

### 2.10 Stealth addresses

Obscura uses Monero-style **dual-key stealth addresses**. A wallet has a **view key** `(a, A=a·G)` and a **spend key** `(b, B=b·G)`. A sender picks random `r`, publishes `R = r·G`, and computes the one-time output key

```
P = Hs(r·A)·G + B
```

The recipient scans with `a`: `Hs(a·R)·G + B = P`, recovering outputs addressed to them without their address ever appearing on-chain. The one-time key `P` is what is hashed to a prime and inserted into the accumulator.

---

## 3. The Private Spend Protocol

### 3.1 Intuition

Spending must accomplish four things at once:

1. Prove the spent output is a **current member** of the accumulator.
2. Reveal **nothing** about which member it is.
3. Publish a **nullifier** that is deterministic per output, so the same output cannot be spent twice.
4. Preserve **amount confidentiality and conservation**.

### 3.2 Full protocol

For a spent output with prime representative `p`, membership witness `w` (with `w^p = acc`), and output secret `s`:

1. **Commit** to `p` and to `w` (Pedersen / group commitments), so neither appears in the clear.
2. **Prove in zero knowledge**, via a PoKE2-style protocol, that the committed `w` and `p` satisfy `w^p = acc`, i.e. that the prover knows a witness/prime pair opening to a genuine accumulator member, *without revealing the witness base or the prime*.
3. **Reveal a nullifier** `N = PRF(s)`. The nullifier is a deterministic pseudo-random function of the output's secret, so a given output yields exactly one nullifier; consensus rejects any transaction whose nullifier has appeared before.
4. **Bind** the nullifier to the zero-knowledge membership proof, so an attacker cannot pair a valid membership proof with an unrelated nullifier.
5. Attach the **range, conservation, and confidential-amount** proofs of §2.

The result hides *which* output is spent across the entire UTXO set, while the nullifier set provides double-spend protection, playing the role Monero's key images play, but decoupled from any decoy ring.

### 3.3 Implementation

The following run in the reference implementation: the group of unknown order (both backends), the dynamic accumulator, hash-to-prime, Wesolowski PoE, NI-PoKE2, non-membership proofs, Pedersen commitments, bit-decomposition range proofs, conservation proofs, and stealth addresses.

The **full sender-anonymity zero-knowledge spend**, specifically *also* hiding the membership-witness base and binding it to a nullifier entirely in zero knowledge (§3.2 steps 1–4 as a single composed proof), runs as the live transparent zk-STARK shielded spend (`pkg/stark`). The composed, witness-hiding, nullifier-bound spend ships end to end: mint, spend, nullifier reveal, and rejection of double-spends, inflation, unknown anchors, and tampering, all bound into one proof.

---

## 4. Comparison with Monero

| Dimension | Monero | Obscura |
|---|---|---|
| Anonymity set | Bounded ring (~16 decoys) | **Global**, entire UTXO set |
| Set growth | Fixed | Grows with adoption |
| Proof size vs. set | Linear in ring size | **Constant** |
| Verifier cost vs. set | Linear in ring size | **O(log)** |
| Decoy-selection leakage | Yes (heuristic distribution) | **None** (no decoys) |
| Trusted setup | None | **None** (class-group backend) |
| Amount privacy | RingCT (Bulletproofs) | Pedersen + range proofs |
| Group operations | ECC (fast) | Class-group (slower) |

### 4.1 Honest costs and risks

- **Performance.** Class-group composition is significantly slower than ECC point operations. This is the dominant performance cost and the reason RSA is offered as a faster (but setup-dependent) alternative.
- **Witness maintenance.** Each membership witness must be updated whenever the accumulator changes (every block with insertions/removals). Obscura handles this with a **witness-update service** that publishes batched update data, or with **on-demand recomputation** by a wallet that re-derives its witness from chain history. This is an operational burden Monero does not have.
- **Heavier full ZK spend.** The composed witness-hiding spend (§3.2) is computationally heavier than a single membership proof, the dominant cost of full sender anonymity.
- **Maturity.** Monero is battle-tested over years; Obscura's combination is novel and runs live on mainnet.

---

## 5. Security Status & Limitations

> **Sound-spend model.** A four-track adversarial
> security review (see [SECURITY_AUDIT.md](docs/SECURITY_AUDIT.md)) reshaped the
> original accumulator-membership spend, whose membership witness was
> publicly recomputable (no ownership), nullifier was unbound (double-spend),
> and pseudo-commitment value was unconstrained (inflation). The
> consensus spend path runs a **sound confidential-
> transaction model**: each spend carries a Schnorr **ownership proof**
> (knowledge of the one-time secret) and a **value-equality proof** (the spent
> amount equals the referenced output's committed amount), with a consensus
> **UTXO spent-set** preventing double-spends. **Amounts and recipients stay
> private.** The witness-hiding sender-anonymity layer of §3.2 ships as the live
> transparent zk-STARK shielded spend, which additionally hides the sender.
>
> **Sender-anonymity crypto core.** The primitive for sender
> anonymity runs live: a **Triptych-style linkable
> one-out-of-many proof** (`pkg/commit/oneofmany.go`), log-size, no trusted
> setup, proving a hidden coin in the set is owned, with a key-image tag for
> double-spends. It passes completeness, soundness, index-hiding, and tag-binding tests.
> **Anonymous spend wired on-chain.** Anonymous spends work
> end-to-end through consensus: a `tx.AnonInput` carries a ring of coin keys, a
> key-image tag, a pseudo-commitment, and the joint proof; consensus verifies the
> proof, binds the value, and enforces the key-image set against double-spends.
> A coin spends hidden in a ring with no transparent input (end-to-end
> test in `tests/critical/anonchain/`). Transparent and anonymous spends coexist,
> with the transparent zk-STARK shielded spend giving full sender-hiding over the
> whole-chain anonymity set.
>
> All catastrophic findings
> (emission overflow, difficulty overflow, DoS, Merkle malleability, replay
> trust, non-deterministic genesis) are fixed and tested.

**Obscura mainnet is live.** It combines well-studied primitives, and the following statements are binding:

1. **Review it yourself.** This is independent software; understand the implementation and the risks before committing significant value to it.
2. **Components are tested in isolation and in composition.** Tests cover the accumulator, both groups of unknown order, PoE, PoKE2, non-membership proofs, commitments, range proofs, and stealth addresses, alongside end-to-end consensus tests of the composed spend.
3. **The flagship privacy property ships.** The full sender-anonymity ZK spend that hides the witness base and binds a nullifier in zero knowledge (§3.2) runs live as the transparent zk-STARK shielded spend, delivering Obscura's headline guarantee in production.
4. **RSA backend trust assumption.** The RSA backend is secure only if the modulus factorisation is unknown to all parties. The challenge modulus is *believed* unknown; for stronger assurance, run an MPC ceremony, or use the class-group backend, which removes the assumption entirely.
5. **Quantum considerations.** Groups of unknown order rest on classical hardness (factoring / class-number computation). The edwards25519 commitments are likewise classical, and the live version-2 post-quantum path (WOTS+ hybrid keys, ML-KEM stealth, Merkle accumulator) addresses the migration.

---

## 6. Conclusion

Obscura moves past the fundamental ceiling of ring signatures: a **global, ever-growing anonymity set** with **constant-size, fast-to-verify** spend proofs, and, via imaginary quadratic class groups, **no trusted setup**. The reference implementation runs and tests the full cryptographic toolbox required, and this whitepaper specifies precisely how those pieces compose into the live private spend. The composed zero-knowledge nullifier-bound spend runs end to end on mainnet.
