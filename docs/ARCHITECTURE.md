# Obscura (OBX) — System Architecture

*Version 0.1, live mainnet.*

This document describes the architecture of the Obscura reference implementation, written in Go. It maps the system's responsibilities onto the repository's package layout and traces how a transaction flows from wallet to chain state.

---

## 1. Layered Overview

Obscura is organised in layers, from low-level cryptography up to node and wallet executables. Each layer depends only on those below it.

```
cmd/obscura-node   cmd/obscura-wallet        ← executables
        │                 │
   pkg/rpc          pkg/wallet                ← interface / client
        │                 │
pkg/consensus  pkg/mempool  pkg/p2p           ← node services
        │
    pkg/chain                                 ← state machine
        │
pkg/block   pkg/pow   pkg/tx                  ← block & transaction model
        │
pkg/commit   pkg/accumulator                  ← privacy crypto
        │
    pkg/group                                 ← group of unknown order
```

---

## 2. Cryptographic Core

### `pkg/group` — Group of Unknown Order

The foundation. Provides the abstract group `G` whose order is unknown, with two interchangeable backends:

- **`rsa.go`** — the RSA group `(Z/NZ)*`, instantiated over the RSA-2048 factoring-challenge modulus as a nothing-up-my-sleeve choice. Fast, but secure only while the factorisation stays unknown.
- **`classgroup.go`** — the imaginary quadratic class group, with elements as reduced binary quadratic forms `(a,b,c)` and the group law implemented as Dirichlet composition plus reduction. The discriminant `D` (negative prime, `D ≡ 1 (mod 8)`) is derived from a public seed, so the backend needs **no trusted setup**. Validated by group-axiom property tests at a 2048-bit discriminant.

All higher layers program against the group interface, so the backend is a deployment choice.

### `pkg/accumulator` — Accumulator and Proofs

Builds the dynamic accumulator (Boneh–Bünz–Fisch) over `pkg/group`:

- **Accumulator** `acc = g^(∏ pᵢ)` with add/remove and membership-witness operations (`w^p = acc`).
- **Hash-to-prime** mapping output keys to unique prime representatives via hash-and-increment.
- **Wesolowski PoE** for succinct, O(log)-verifier proofs of exponentiation.
- **NI-PoKE2** for proving knowledge of an exponent without revealing it — the basis of hiding *which* output is spent.
- **Non-membership proofs** via Bézout coefficients.

### `pkg/commit` — Commitments, Range, Conservation, Stealth

Confidentiality and ownership primitives over edwards25519:

- **Pedersen commitments** `C = v·H + r·G` (H via hash-to-point), additively homomorphic, for confidential amounts.
- **Range proofs** by bit decomposition with per-bit Schnorr OR proofs (O(n); Bulletproofs are a future O(log n) target).
- **Conservation proofs**: `Σin − Σout − fee·H` is a commitment to zero, proven via a Schnorr discrete-log proof.
- **Stealth addresses**: dual-key (view `(a,A)`, spend `(b,B)`), one-time keys `P = Hs(r·A)·G + B` with published `R = r·G`.

---

## 3. Transaction and Block Model

### `pkg/tx` — Transaction Model

Defines the transaction structure that ties the crypto together: accumulator-based spends (membership/PoKE2 data and nullifiers), output commitments, range proofs, and the conservation proof. A transaction asserts that its spent inputs are accumulator members, that amounts are in range, and that value is conserved — without revealing amounts, and (in the production target) without revealing which outputs are spent.

### `pkg/block` and `pkg/pow`

- **`pkg/block`** defines block headers and bodies: the link to the previous block, the accumulator and nullifier-set commitments, coinbase (including the optional referral tag), and the contained transactions.
- **`pkg/pow`** implements the ASIC-resistant proof of work (canonical RandomX on mainnet; an opt-in memory-hard backend remains for local builds) and the work/verification routines used by miners and validators.

---

## 4. State and Node Services

### `pkg/chain` — Blockchain State Machine

The authoritative state. As blocks are applied, `pkg/chain` advances three pieces of state in lockstep:

1. **The accumulator** — outputs are inserted on creation and removed on spend.
2. **The nullifier set** — every spend publishes a nullifier; replays are rejected, giving double-spend protection decoupled from any decoy ring.
3. **The incentive pool** — the on-chain balance funding holding bonuses (and the accounting backing referral payouts), updated per block from emission.

This layer enforces the consensus invariants that make a block valid in the context of all prior blocks.

### `pkg/mempool`, `pkg/p2p`, `pkg/consensus`

- **`pkg/mempool`** holds validated, unconfirmed transactions awaiting inclusion, with double-spend and nullifier checks against pending state.
- **`pkg/p2p`** is the gossip network layer — peer discovery and propagation of blocks and transactions.
- **`pkg/consensus`** owns difficulty retargeting (LWMA), block/transaction validation rules, and chain-selection logic, coordinating `pkg/chain`, `pkg/pow`, and `pkg/mempool`.

---

## 5. Client Layer

### `pkg/wallet` — Wallet

Manages keys (view/spend), scans the chain for owned stealth outputs, and — critically — performs **witness tracking**: maintaining each owned output's membership witness as the accumulator evolves (via the witness-update service or on-demand recomputation). It constructs **spend proofs**: assembling the accumulator membership/PoKE2 data, range proofs, conservation proof, and nullifier for each transaction.

### `pkg/rpc` — JSON-RPC

Exposes node and wallet functionality over JSON-RPC for the CLI and external tooling: submitting transactions, querying chain state, accumulator data, and balances.

---

## 6. Executables

- **`cmd/obscura-node`** — the full node and miner: runs P2P, mempool, consensus, the chain state machine, and (optionally) the PoW miner, exposing RPC.
- **`cmd/obscura-wallet`** — the CLI wallet: key management, scanning, witness tracking, and spend construction, talking to a node via RPC.

---

## 7. Transaction Lifecycle (end to end)

1. **Construct** (`pkg/wallet`): the wallet selects an owned output, retrieves/updates its witness (`pkg/accumulator`), builds the spend proof and nullifier, creates output commitments and range proofs (`pkg/commit`), and the conservation proof — assembling a `pkg/tx` transaction.
2. **Submit** (`pkg/rpc` → `pkg/mempool`): the transaction is validated (proofs, conservation, nullifier-freshness) and enters the mempool.
3. **Gossip** (`pkg/p2p`): peers receive and re-validate the transaction.
4. **Mine** (`pkg/pow` + `pkg/block`): a miner packages mempool transactions plus a coinbase (with optional referral tag) and solves the PoW.
5. **Apply** (`pkg/consensus` → `pkg/chain`): the block is validated and applied — accumulator updated, nullifiers recorded, incentive pool advanced.
6. **Maintain** (`pkg/wallet`): wallets observe the new accumulator and update their outstanding witnesses for future spends.

---

## 8. Status

This architecture reflects the live mainnet implementation. The cryptographic core, transaction/block model, and chain state machine are implemented and the crypto primitives are unit-tested. As stated in the whitepaper, the **fully composed, witness-hiding, nullifier-bound zero-knowledge spend** is the production target and is only partially implemented. Obscura is new software that has not had an external audit; review the code yourself before relying on it.
