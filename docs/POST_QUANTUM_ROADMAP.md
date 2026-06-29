# Obscura Post-Quantum Roadmap

This document describes Obscura's migration toward post-quantum (PQ) security. It
records what is **implemented** (Phase 1 + Phase 2 building blocks, all in new
packages and tested), what remains **research-frontier** (the ZK membership
STARK), and the design rationale.

Companion reading: `docs/QUANTUM.md` (the threat assessment — *why* the shipping
coin is not PQ today).

## Design constraints

1. **No impact on the shipping coin's speed.** Every PQ primitive lives in a
   **new package** (`pkg/pqsign`, `pkg/pqstealth`, `pkg/pqcommit`, `pkg/pqaccum`)
   off the default consensus path. The classical coin's validation speed and
   wire size are unchanged; PQ is opt-in. The full pre-existing test suite (50
   packages) stays green.
2. **No trusted setup.** Every primitive is transparent (hash-/lattice-based with
   public, nothing-up-my-sleeve parameters), matching Obscura's no-ceremony ethos.
3. **Conservative assumptions first.** Prefer hash-based security (only BLAKE2b)
   where possible; use standardized lattice schemes (ML-KEM / FIPS 203, and
   SIS/BDLOP) where homomorphism or KEM functionality is required.
4. **Hybrid, not flag-day.** New crypto is layered *alongside* the classical
   crypto so an output stays secure if *either* assumption holds.

## The quantum threat (summary)

A cryptographically-relevant quantum computer running Shor breaks **all** of
Obscura's classical layers: edwards25519 ECDLP (ownership/spend/Schnorr/
one-of-many/adaptor/DLEQ/payment proofs → theft), X25519 ECDH stealth
(deanonymization), Pedersen binding (inflation), **and** the accumulator's
group-of-unknown-order (RSA-2048 → factoring; class group → Hallgren) — an extra
break Monero/BTC don't even have. Grover only *halves* the security of the
hash/PoW/symmetric layers (RandomX, BLAKE2b, Argon2id/XChaCha20), which is
manageable. "Harvest-now-decrypt-later" means recorded chain data is retroactively
exposed once the quantum computer exists, so migration must precede that, and old
funds must be swept to PQ outputs.

| Layer | Classical primitive | Quantum break | PQ replacement (this roadmap) |
|---|---|---|---|
| Spend authority | edwards25519 Schnorr | Shor (total) | **WOTS+** hash-based OTS (`pkg/pqsign`) |
| Recipient privacy | X25519 ECDH stealth | Shor (total) | **ML-KEM-768** KEM stealth (`pkg/pqstealth`) |
| Amount confidentiality | Pedersen commitment | Shor (binding) | **BDLOP/SIS** lattice commitment (`pkg/pqcommit`) |
| Anonymity set | class-group / RSA accumulator | Shor / Hallgren | **Merkle** accumulator (`pkg/pqaccum`) |
| ZK membership | accumulator zk-membership | (broken with accumulator) | **zk-STARK over Merkle path** — *not yet built* |
| PoW / hashing / keystore | RandomX / BLAKE2b / Argon2id | Grover (halves only) | parameter bump only |

---

## Phase 1 — PQ spend authority (`pkg/pqsign`) ✅ implemented

**WOTS+ (Winternitz One-Time Signature), hash-based.** Rationale: every Obscura
output has a **one-time public key spent exactly once**, so a one-time signature
is a perfect fit — the reuse weakness that forces XMSS/SPHINCS+ to manage state
never arises. Security rests **only on BLAKE2b** (the most conservative PQ
assumption) with no trusted setup.

- Parameters `n=32, w=16` → `len=67` chains. **Public key 32 bytes** (fits the
  existing one-time-key slot), **signature ≈ 2 KB**, verify ≈ 1000 BLAKE2b calls.
- Measured: Sign ≈ 271 µs, Verify ≈ 296 µs — negligible, and off the default path.
- `KeyGen / Sign / Verify`; tests cover round-trip, determinism, wrong-message,
  wrong-key, tamper rejection, the Winternitz checksum property, length checks.

**Hybrid one-time key (`hybrid.go`).** The on-chain key is
`Key = BLAKE2b(P ‖ R)` binding a classical Schnorr point `P = x·G` **and** a
WOTS+ root `R`. A spend must present a valid Schnorr proof **and** a valid WOTS+
signature, so the output is secure as long as **either** primitive holds:
classical DLOG today (even though WOTS+ is new code), and WOTS+ after Shor breaks
the Schnorr half. This is the standard belt-and-suspenders migration posture.
Tests prove **both** halves are required (breaking either one fails verification)
and that `Key` binds `P`,`R` (substitution fails).

**Integration plan (next step, not yet wired into consensus):** add a PQ output
variant whose `OneTimeKey = Key`; the spend carries `(P, R, HybridSig)`; validate
calls `pqsign.HybridVerify` bound to the tx CoreHash. Gate behind a `pq` build
tag / output-version byte so the default path is untouched.

---

## Phase 2 — PQ confidential amounts + PQ anonymity set ✅ building blocks implemented

### 2a. PQ recipient privacy + amount detection (`pkg/pqstealth`) ✅

Uses **ML-KEM-768** (Kyber, FIPS 203) from the Go 1.25 standard library
(`crypto/mlkem`) — pure-Go, standardized, no dependency, no trusted setup. The
recipient publishes an encapsulation key as their PQ view key; the sender
encapsulates, attaches the KEM ciphertext (1088 B) to the tx, and derives from
the shared secret a **detection tag** and an **amount-encryption key + MAC**. A
quantum adversary recording the chain cannot recover the shared secret (ML-KEM is
PQ-IND-CCA2), so amounts and recipient-linkage stay private even under
harvest-now-decrypt-later. Tests: round-trip, not-mine rejection, amount-tamper
(MAC) rejection, seed-deterministic keys.

**Honest scope.** This makes payment **detection** and **amount confidentiality**
post-quantum. Fully non-interactive PQ **spend-authority** stealth keys (the
hash/lattice analogue of Monero's `P = Hs(ss)·G + B`, where the sender derives the
output key from public data *without* being able to spend) is an **open problem**
— hash/lattice signatures lack the key-homomorphism that makes classical stealth
spend keys work. Obscura's practical answer is the **hybrid one-time key** above:
the classical half supplies the non-interactive one-time spend key today, and the
WOTS+ half adds PQ spend protection. Cost: one KEM ciphertext (1088 B) per
recipient per tx — the price of PQ recipient privacy.

### 2b. PQ amount commitment (`pkg/pqcommit`) ✅

A **BDLOP-style lattice commitment** replaces Pedersen while keeping the
**additive homomorphism** the conservation proof depends on:

```
c1 = A1 · r       (mod q)        — binding part
c2 = A2 · r + v   (mod q)        — message part   (r short, |r_i| ≤ B)
```

Binding rests on **SIS** (two short openings of one commitment = a short lattice
solution); hiding on leftover-hash/LWE for short `r`. `A1,A2` come from a public
BLAKE2b XOF (nothing-up-my-sleeve). Homomorphism holds exactly:
`Commit(v1;r1)+Commit(v2;r2)=Commit(v1+v2; r1+r2)`. Tests prove round-trip,
determinism, **homomorphism**, **conservation** (balanced in/out → commitment to
zero; unbalanced → caught = no inflation), bound enforcement, collision sanity.
Commit ≈ 270 µs.

Caveats (documented in-code): parameters are **illustrative** and need formal
Module-SIS selection over a polynomial ring; amounts are single-limb (`< q`);
summed randomness must stay under the SIS bound across a tx's inputs/outputs (fine
for bounded fan-in/out). A PQ **range proof** (e.g. lattice/Bulletproof-style or
in the STARK below) is still required to prevent negative-amount overflow — noted
as remaining work.

### 2c. PQ anonymity set (`pkg/pqaccum`) ✅ (data structure)

An **append-only Merkle accumulator** (BLAKE2b, RFC-6962 domain separation,
CVE-2012-2459-immune) replaces the Shor-breakable class-group/RSA accumulator.
`Add / Root / Prove / Verify` with O(log n) proofs; tests over many sizes, plus
non-member, tampered-path, root-change, and domain-separation checks.

**The privacy gap (the real remaining research).** A raw Merkle membership proof
is PQ-*sound* but **not zero-knowledge** — it reveals the leaf and its position,
deanonymizing the spend. The class-group accumulator's value was *witness-hiding*
membership. Recovering ZK membership post-quantum requires proving the Merkle
path **inside a transparent PQ zero-knowledge proof**:

> *zk-STARK statement:* "I know a leaf `L` and an authentication path to the
> published root `Root`, and `nullifier = H(spend_key)` — without revealing `L`,
> the path, or the position."

A STARK is transparent and PQ (hash-based), so it preserves the no-ceremony
ethos. Building (and optimizing) that AIR/circuit — together with a PQ range
proof over `pkg/pqcommit` — is the **largest remaining effort** and the main
thing standing between today's building blocks and a fully-PQ private spend.

> **Design note (brainstorm-confirmed).** zk-STARK is the right tool over the
> alternatives: MPC-in-the-head yields ~MB proofs with second-scale verification
> (unworkable for chain validation); lattice ZK (LaBRADOR, LatticeFold) needs
> structured assumptions and lacks transparent Merkle instantiations. STARKs give
> transparent setup, purely hash-based PQ security, native Merkle-path AIR
> constraints, and logarithmic FRI verification. Concretely: target a **Circle
> STARK / DEEP-FRI** backend (~50 KB proofs, sub-second verify per StarkWare/
> RISC0-class deployments); verify the FRI security parameter against Obscura's
> per-block space budget before finalizing — proof size is the main knob to watch.

---

## Status & next steps

**Done (this work):** WOTS+ + hybrid spend auth; ML-KEM stealth/detection; BDLOP
homomorphic commitment; Merkle accumulator. All in new packages, all tested,
default path and speed untouched. **Plus** an end-to-end PQ output+spend variant
wiring all four primitives together — see below.

**Done — end-to-end PQ output+spend variant (`pkg/pqtx`, build tag `pq`).**
A self-contained variant, gated behind the `pq` build tag **and** a version byte
(`Version = 2`), so it does not compile into the default binary (classical speed/
wire unchanged; default suite stays green, `go test -tags pq ./...` green too):
- `PQOutput` mirrors `tx.Output`: one-time key = the hybrid key (`BLAKE2b(P‖R)`),
  a `pqcommit` amount commitment, and a `pqstealth` ML-KEM announcement.
- `PQSpend` mirrors `tx.Input`+`Transaction`: references an output, reveals
  `(P, R)`, carries a nullifier `= BLAKE2b(R)` set **pre-CoreHash** (bound, like
  the classical KeyImage) and a `HybridSig` computed **post-CoreHash**.
- `Ledger` mirrors `pkg/chain`: a `pqaccum` Merkle anonymity set, a shared
  nullifier set, and a UTXO map; `ValidateSpend`/`ApplySpend` mirror
  `validateTxLocked`/`applyBlock`. Checks: version → output exists + **membership
  proof** under the root → nullifier well-formed (`= H(R)`, bound to the output
  via the hybrid key) and unseen → **`pqsign.HybridVerify`** over the CoreHash
  (both halves required) → **value conservation** via the homomorphic commitment.
- `Account` (PQ wallet): stealth detection + owned hybrid keys; `BuildOutput`
  derives the commitment blinding from the KEM shared secret (no extra transmit);
  `BuildSpend` balances value and signs the CoreHash with the hybrid key.
- Tests (`-tags pq`): full fund→detect→spend→apply lifecycle, double-spend
  rejection, tampered-signature rejection, substituted-classical-key rejection,
  conservation-violation rejection, wrong-root membership rejection.

The nullifier is bound to the WOTS+ root (PQ-sound); membership here is
transparent (reveals the output ref, like the classical transparent path), so the
ZK-private membership STARK below is still what's needed for full anon privacy.

**Done — promoted into the real `pkg/tx` / `pkg/chain` engine (Version 2).**
The PQ output+spend variant is now a first-class transaction kind in the actual
consensus path, gated by `Transaction.Version == 2` and the presence of PQ
fields, so classical transactions never touch it (their speed is unchanged; the
default suite stays green):
- `tx.PQOutput` / `tx.PQInput` added to `pkg/tx` with full serialize/deserialize
  and CoreHash binding (HybridSig + membership proof excluded, like classical
  proofs); `tx` stays dependency-light (plain `[]byte` fields).
- `pkg/chain` gained PQ state — a `pqaccum` Merkle anonymity set, a nullifier
  set, and a PQ UTXO map — **rebuilt deterministically on replay from stored
  blocks**, so no DB-schema change was needed (verified by a restart test).
- `validateTxLocked` branches to `validatePQTxLocked` for PQ txs (pure-PQ: no
  classical value fields): checks output existence + `pqaccum` membership under
  the current root, nullifier binding/uniqueness, `pqsign.HybridVerify` over the
  CoreHash, and `pqcommit` value conservation. `applyBlock` adds PQ outputs to
  the set and records nullifiers. A coinbase may **mint** PQ outputs (its
  conservation proof binds the classical part; PQ outputs are covered by the
  block merkle root).
- `pkg/pqwallet` builds Version-2 PQ transactions from the real `tx` types
  (detect via ML-KEM, sign with the hybrid key, balance via the commitment).
- Integration tests (`tests/critical/pqchain`) drive the **real mined-block
  path**: mint→detect→spend→apply, plus rejection of tampered signatures, value
  inflation, nonexistent-output spends, double-spends, and a restart/replay test.

**Done — hardening pass (anchors, header commitment, sound value layer, fees).**
- **Header commitment:** `block.Header.PQAccRoot` now commits the post-block PQ
  anonymity-set Merkle root (in the PoW preimage + serialization). `BlockTemplate`
  predicts it and `validateBlockLocked` verifies it, so all nodes/forks agree on
  PQ state — it is consensus-bound, not just local.
- **Anchors (Zcash-style):** the chain keeps a set of all historical PQ roots; a
  `tx.PQInput` carries an `Anchor` (the root its membership proof targets), and
  validation accepts any known historical anchor. This fixes membership-witness
  staleness across intervening blocks (a witness no longer expires the moment a
  new PQ output is added). Anchors are rebuilt on replay.
- **Sound value layer (public amounts):** the wraparound/inflation gap is closed
  by making the consensus PQ amount **public** (`tx.PQOutput.Amount`). With no
  hidden value, the inflation attack is impossible and a per-output supply-cap
  check bounds amounts directly; conservation is `Σ in == Σ out + fee`
  (overflow-checked). Recipient privacy (ML-KEM stealth detection) and
  post-quantum spend authority (hybrid) are unchanged — only the amount is public.
  The confidential `pqcommit` commitment + the standalone `pkg/pqtx` demo are kept
  intact; the reserved `PQOutput.{Commitment,EncAmount,MAC}` fields will carry a
  confidential amount once the compact PQ range proof lands.
- **PQ fees:** a public PQ fee with a minimum-fee anti-spam rule, currently
  **burned** (deflationary). Integration + replay + rejection tests updated
  (`tests/critical/pqchain`): value-inflation, unknown-anchor, tampered-sig,
  nonexistent-output, and double-spend are all rejected.

Honest limitations carried over: membership is transparent (reveals
the output ref), so ZK privacy needs the STARK below; **amounts are public** on the
consensus PQ path, so confidentiality needs the range proof below; coinbase PQ
minting is unrestricted (experimental emission) and PQ fees are burned, not
credited — both are resolved together by a real PQ emission/wrap policy. None of
this affects the classical chain.

**Done — adversarial audit of the PQ consensus code + fixes.** Four parallel
adversarial reviewers attacked the new PQ wiring (serialization, validation,
apply/header/anchor/reorg, crypto primitives). Findings fixed and regression-tested:
- **CRITICAL — reorg dropped PQ state.** `forkchoice.go` snapshot/reset/restore
  omitted all five PQ fields, so any reorg on a chain that had used PQ corrupted
  the PQ accumulator/UTXO/nullifier/anchor sets (chain split / cross-reorg
  double-spend). Fixed: PQ fields added to `stateSnapshot`/`snapshotState`/
  `restoreState`/`resetState` + `pqaccum.Accumulator.Clone()`; new
  `TestPQReorgRebuildsState` proves PQ state is rebuilt to the adopted branch.
- **HIGH — nullifier bound only to R.** Two outputs sharing a WOTS root collided
  on one nullifier (spend of one froze the other). Fixed: nullifier =
  `BLAKE2b(dom‖OutputRef)` (binds the full output P‖R).
- **MEDIUM — membership proof index unauthenticated.** `pqaccum.Verify` ignored
  the index. Rewrote it to the canonical RFC-6962 inclusion algorithm (direction
  derived from index+size, path length checked); added `Size` to the proof +
  bound-checked `ParseProof`.
- **MEDIUM — Schnorr cofactor malleability.** `HybridVerify` accepted torsion
  points. Added a prime-order-subgroup check (`[8⁻¹]([8]Q)==Q`) on both P and R.
- **MEDIUM — `PQBlindDiff` txid-malleability.** Vestigial field excluded from
  CoreHash but in the txid; now required empty.
- **LOW — bloat hygiene.** Reserved `Commitment/EncAmount/MAC` must be empty;
  `ViewTag` length pinned; one-time-key dedup now against the persistent index;
  empty-set root no longer whitelisted as an anchor; spent leaves kept as decoys.

Still open (documented, lower priority): `pqRoots` anchor-set growth is unbounded
(same DoS class as the classical `accValues`; bound to a window later).

**Next, in rough order (the two research items + emission policy):**
1. **Compact PQ range proof** — Ligero / MPC-in-the-head over the bit
   decomposition (brainstorm-recommended: transparent, hash-only, no hazardous
   Gaussian sampling, ~30–50 KB), to restore CONFIDENTIAL PQ amounts on consensus
   (re-enabling the reserved commitment fields) while staying inflation-proof.
2. **zk-STARK membership** over `pkg/pqaccum` for ZK-private (sender-hiding)
   spends — the same IOP machinery as the range proof; build them together.
3. **PQ emission/wrap policy:** remove unrestricted coinbase PQ minting, define how
   PQ coins enter (subsidy or classical→PQ wrap), and then credit PQ fees to miners.
3. **zk-STARK membership** over `pkg/pqaccum` (the ZK-privacy recovery) + nullifier
   binding — the deep research item.
4. Formal **lattice parameter selection** + independent cryptographic review of
   `pkg/pqsign`, `pkg/pqcommit`.
5. **Migration**: a sweep mechanism to move classical outputs to PQ outputs
   before any quantum threat materializes (harvest-now-decrypt-later means old
   outputs cannot be retrofitted — only new PQ outputs are protected).

**Bottom line:** Obscura now has working, tested PQ building blocks for spend
authority, recipient privacy, amount confidentiality, and the anonymity-set data
structure. Full PQ *private* spends additionally need the transparent STARK
membership proof and a PQ range proof — a substantial but well-scoped research
track, not a parameter change.
