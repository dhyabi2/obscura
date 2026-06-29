# Obscura (OBX) — Architecture Diagram

Two visual views of the full system (complements the textual `ARCHITECTURE.md`):
- **`docs/ARCHITECTURE.html`** — a styled, color-coded feature map with per-feature explanations (open in any browser).
- **This file** — a Mermaid mindmap + layered data-flow (renders on GitHub or any Mermaid viewer).

> Status: **mainnet live** (value-bearing). The from-scratch zk-STARK has not had an external crypto audit, so review it yourself; the FRI/ZK soundness figures are conjectured/industry-standard pending sign-off; PoR is proof-of-*access*.

---

## Feature mindmap

```mermaid
mindmap
  root((Obscura OBX))
    Network & P2P
      Self-discovering mesh
        PEX address exchange
        Self-address discovery (addr-me, 2+ votes)
        Persistent address book
        Censorship-resistant (seedless)
      Transport privacy
        Tor / .onion (NAT traversal)
        Dandelion++ stem/fluff
      Hardening
        Magic+version handshake
        Ban scoring / per-IP caps
      Mempool
        Bounded + fee-priority eviction
        Parallel proof verification
    Consensus & PoW
      RandomX-style PoW
        Memory-hard / ASIC-resistant
        Per-epoch ungrindable seed
        LWMA retarget
        Miner pacing
      Proof-of-Retrievability
        Miners must be full nodes
        Random body challenges
        Pruned nodes still validate
      Accumulator state
        RSA-2048 O(1) membership
        Header roots Acc/Null/CM/PQ/PoR
        Class-group target (no setup)
      Reorg safety
        Snapshot + rollback
        Coinbase conservation proof
    Privacy & ZK
      Transparent zk-STARK
        No trusted setup
        Post-quantum (hash only)
        Goldilocks + FRI + DEEP-ALI
        Zero-knowledge masking
      Anonymous spend
        Poseidon commitment tree (256-bit)
        Whole-chain anonymity set
        Epoch sharding (unlimited coins)
        Nullifier double-spend guard
      Confidential amounts
        In-field value balance
        Range proofs (anti-inflation)
        Fused hidden in/out spend
      Recipient-secret nullifier
        Two-key note pk=H(nk)
        nf=H(nk,rho) unlinkable
        Spend authority
        Merged confidential+unlinkable
      Stealth & rings
        One-time stealth keys + view tags
        Triptych one-out-of-many ring
        Pedersen confidential (classical)
    Economics
      Emission
        Smooth decay + tail
        18.4M supply cap
      Confidential staking vaults
        Private yield from incentive pool
        Time-locked, no inflation
      Incentives
        Holding bonus
        Sybil-guarded referral
    Cross-chain
      BTC atomic swaps (HTLC)
      Nano scriptless swaps
      Gossiped order book
    Post-Quantum
      ML-KEM-768 stealth
      Lattice commitments
      Hybrid signatures
      PQ anonymity accumulator
    Storage & Scaling
      3-tier pruning
      Snapshot sync
      Constant-size state
      Epoch-sharded anonymity
```

---

## Layered stack & data flow

```mermaid
flowchart TB
  subgraph NET["1. Network & P2P"]
    direction LR
    PEX[Self-discovering mesh<br/>PEX, addr-me, book] --- TOR[Tor / Dandelion++]
    TOR --- MP[Bounded mempool<br/>parallel verify]
  end
  subgraph CON["2. Consensus & PoW"]
    direction LR
    POW[RandomX PoW, paced] --- POR[Proof-of-Retrievability<br/>miners = full nodes]
    POR --- ACC[RSA-2048 accumulator<br/>O(1) state + header roots]
  end
  subgraph ZK["3. Privacy & ZK"]
    direction LR
    STARK[Transparent zk-STARK<br/>ZK-masked, no setup, PQ] --- TREE[Epoch-sharded<br/>commitment tree]
    TREE --- SPEND[Confidential + unlinkable spend]
  end
  subgraph SVC["4-7. Services"]
    direction LR
    VAULT[Confidential vaults] --- XC[Atomic swaps BTC/XNO]
    XC --- PQ[Post-quantum path]
    PQ --- PRUNE[3-tier pruning + snapshot]
  end

  USER([Wallet / user]) -->|build tx| MP
  MP -->|gossip| POW
  POW -->|mined block w/ PoR| ACC
  SPEND -->|membership + nullifier| TREE
  TREE -->|CMRoot anchor| ACC
  ACC -->|state commitments| CON
  CON -->|canonical chain| NET
  SVC -.consume.-> CON

  classDef net fill:#0b2a3a,stroke:#38bdf8,color:#e7edf7;
  classDef con fill:#3a2e0b,stroke:#fbbf24,color:#e7edf7;
  classDef zk  fill:#2a1f4a,stroke:#a78bfa,color:#e7edf7;
  classDef svc fill:#0b3a2a,stroke:#34d399,color:#e7edf7;
  class PEX,TOR,MP net;
  class POW,POR,ACC con;
  class STARK,TREE,SPEND zk;
  class VAULT,XC,PQ,PRUNE svc;
```
