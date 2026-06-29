# Invention Log — Block 7: Frozen Anonymity Pools (anonymity-set policy)

Produced with the invention methodology + Methodology-Tree brainstorming engine.

## 1. Challenge
The anonymous spend (Block 3) let the sender pick an arbitrary ring of coins.
Problems: verifier cost is O(ring size) and a malicious sender could pass a huge
ring (DoS) or weak decoys (privacy leak); decoy-selection heuristics are a known
deanonymization surface (Monero's history).

## 2. Brainstormed (engine) + decision
The ring-selection brainstorm ranked **growing-then-frozen pools** #1: coins grow
a pool until it freezes; the ring is the whole frozen pool; fixed N per pool, no
trusted party, no decoy heuristic. Adopted.

## 3. Implementation
- Coins get a canonical **creation-order index** (`CoinInfo.Index`, `c.coinList`).
- A **pool** = `config.PoolSize` consecutive coins (power of two). A pool is
  usable only once **complete (frozen)** and all members are mature.
- An anonymous spend names a **`PoolID`** (a uint64) instead of an explicit key
  list. Consensus **reconstructs the canonical ring** from the pool membership
  (`poolMembersLocked`) and verifies the joint proof against it. The sender
  cannot choose decoys, the ring is uniform, and verifier cost is bounded by
  `PoolSize`. Tx inputs shrink (one id vs N keys).
- `PoolSize` is a var (dev 16; tests 4–8; production 64–256).

Benefits over Block 3's free-form ring: (1) bounded, predictable verification
cost; (2) no decoy-selection heuristic to exploit; (3) uniform anonymity set;
(4) smaller transactions.

Tradeoff (documented): a spend reveals which *pool* (coarse creation-epoch info),
not which coin; the newest, not-yet-frozen pool's coins must wait until their
pool fills. A future refinement permutes within-pool order by a block-hash seed.

Tested: `tests/critical/anonchain/` — end-to-end anonymous spend against a frozen
pool (sender hidden, recipient paid, double-spend blocked) and rejection of
spends against an incomplete pool.
