# Obscura — Post-Quantum Security Assessment

**Verdict: Obscura is NOT resistant to a cryptographically-relevant quantum
computer (CRQC).** This is the same exposure every shipping privacy coin has today
(Monero, Bitcoin, Zcash) — plus one extra quantum-broken assumption unique to
Obscura (the accumulator). Honest disclosure for users and auditors.

## What breaks (Shor / quantum order-finding)

Obscura's entire **value, ownership, and privacy** layer rests on the
elliptic-curve discrete-log problem (ECDLP) over `edwards25519`, which Shor's
algorithm solves in polynomial time. Every public key on chain is `point =
scalar·base`; Shor recovers the scalar from the point.

| Layer | Files | Quantum impact |
|---|---|---|
| Ownership / spend auth (Schnorr, one-of-many/Triptych, key images) | `commit/spendproofs.go`, `oneofmany.go`, `anonspend.go` | Recover the secret behind any on-chain one-time key `P` → **forge spends / steal any output**; recompute key images → break double-spend + link spends |
| Signatures / adaptor / DLEQ / payment proofs | `commit/adaptor.go`, `dleq.go`, `txproof.go` | Forge signatures; recover swap/tx secrets → swap theft, forged receipts |
| Confidential amounts (Pedersen binding, range, conservation) | `commit/commit.go`, `rangeproof.go`, `conservation.go` | Binding broken → **hidden inflation** (hiding is information-theoretic, so amounts stay private *from the commitment* — but see below) |
| Stealth addresses / amount encryption (ECDH) | `commit/stealth.go` | Recover view/spend keys from public `A,B` → **full deanonymization + theft**; recover ECDH secret → **decrypt all on-chain amounts** |
| **Accumulator "group of unknown order"** | `group/rsa.go` (active), `group/classgroup.go` (target), `accumulator/` | RSA-2048 → Shor factors `N`; **class group → Hallgren-type quantum algorithms compute the class number/order**. Either way "unknown order" collapses → forge membership/witnesses → **inflation + anonymity-set break** |

The class-group backend removes the *trusted setup*, but **not** the quantum
vulnerability — the imaginary-quadratic class-group order problem reduces to a
hidden-subgroup/Pell problem with known quantum algorithms (Hallgren; Biasse-Song).

## What survives (Grover only — quadratic, ~halved strength)

| Layer | Files | Status |
|---|---|---|
| Proof-of-work (RandomX-style VM / canonical RandomX, BLAKE2b) | `pkg/pow/` | Tolerant (mining self-corrects via difficulty) |
| Consensus hashing / merkle / txid (BLAKE2b-256) | `block/`, `tx/`, `chain/` | Tolerant at 256-bit |
| Wallet-at-rest (Argon2id + XChaCha20-Poly1305) | `keystore/` | Tolerant at 256-bit (given an entropic passphrase) |
| Mnemonic / Base58 / address checksum | `mnemonic/`, `base58/`, `commit/address.go` | Encoding only — no security assumption (but the seed they encode derives the ECDLP keys) |

None of the surviving layers protect funds, amounts, or identity — only mining and
at-rest storage.

## Harvest-now, decrypt-later (already accruing)

The chain permanently publishes `P`, `R`, `A`, `B`, ring members, adaptor points,
and commitments **in the clear**. An adversary can archive the chain today and,
when a CRQC arrives, run Shor on every address to **retroactively deanonymize the
whole history and steal any still-unspent output**, and recover ECDH secrets to
**decrypt every historical amount** (defeating Pedersen's information-theoretic
hiding via the encryption side channel). There is no forward secrecy; the keystore
does not help, because the secrets are recoverable from public chain data.

## vs Monero / Bitcoin

- Spend/privacy layer: identical ECDLP exposure (Monero is also edwards25519 +
  stealth ECDH + key images; Bitcoin is secp256k1).
- Obscura-unique: a **second** quantum-broken assumption — the RSA/class-group
  accumulator — gives a second road to quantum inflation that Monero/Bitcoin lack.
- A larger (global) anonymity set gives *no* post-quantum benefit: once individual
  keys are recoverable, set size is irrelevant.

## Migration paths (all substantial; no drop-in)

1. **Ownership/signatures →** hash-based (SLH-DSA/SPHINCS+, XMSS/LMS — relies only
   on hashes we already trust, large sigs) or lattice (ML-DSA/Falcon — smaller, new
   assumptions). Replaces the whole Sigma-protocol stack.
2. **Amounts →** PQ commitments (lattice/hash) + PQ range proofs (STARK/lattice).
   Bulletproofs are NOT PQ.
3. **Stealth →** PQ-KEM-based one-time addresses (ML-KEM); unlinkable-KEM addressing
   is active research.
4. **Accumulator / global anonymity set →** the hardest: no PQ "group of unknown
   order" exists. Direction = hash-based accumulators (Merkle/Verkle + STARK
   membership) — PQ-tolerant but larger proofs; essentially a Zerocash-on-STARKs
   redesign.
5. **Keep:** RandomX PoW, BLAKE2b hashing, Argon2id/XChaCha20 keystore.

A genuinely post-quantum Obscura (PQ signatures + PQ confidential amounts + PQ
global anonymity set + no trusted setup) is a research-frontier redesign, not a
parameter change.

## Recommendation

Document and label Obscura as **classically secure, not post-quantum** (it has not
had an external audit). Treat PQ migration as a major future workstream,
and design any on-chain versioning now so a future PQ hard fork is possible.
