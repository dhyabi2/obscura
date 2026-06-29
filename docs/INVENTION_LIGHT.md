# Invention Log — Block 8: Light Client (SPV)

Produced with the invention methodology + Methodology-Tree brainstorming engine.

## 1. Challenge
Wallets/light nodes need to verify the chain and confirm a transaction without
downloading and executing every full block.

## 2. Brainstormed (engine) + ranking
The engine ranked **view-tags + SPV header sync** #1: SPV (header-chain
verification + Merkle inclusion proofs) handles chain verification; view-tags
(1 byte/output) let wallets locally skip ~99.6% of non-owned outputs with zero
network leakage. Lower-ranked / rejected: compact block filters (false-positive
leaks, Golomb-Rice complexity), checkpoints (sacrifice trustlessness), fraud
proofs (liveness issues). Core privacy-coin constraint flagged: output scanning
needs per-output data → view-tags, not server queries.

## 3. Decision & implementation
Implemented the **SPV core** this block; view-tags documented as the next
scanning refinement.
- `pkg/light/VerifyHeaderChain`: validates an ordered header chain — genesis
  match (trust root), parent linkage, LWMA-expected difficulty, PoW threshold,
  median-time-past + future-drift timestamp rules — and returns the tip, height,
  and cumulative work, **without any full blocks or tx validation** (standard SPV
  trust model).
- `pkg/block/MerkleProof` / `VerifyMerkleProof` + `light.VerifyInclusion`:
  O(log n) transaction-inclusion proofs against a header's `MerkleRoot`
  (hashing shared with `MerkleRoot`, so they are guaranteed consistent).
- `GET /headers?from=&count=` RPC + `rpc.Client.Headers`: fetch headers only
  (capped per request) so a remote light client syncs cheaply.

Tested `tests/critical/light/`: full-node header chain verifies and matches the
tip; a tampered header (bad PoW) and a wrong genesis are rejected; Merkle
inclusion proofs verify for every tx in a block and reject non-members; a remote
light client syncs headers over RPC and independently verifies the chain.

## 4. Documented refinement
**View-tags** (`viewTag = H(a·R)[0]` stored per output) would roughly halve
wallet scan cost by letting the wallet skip the second scalar-mult for ~255/256
of outputs — a small Output-format addition, deferred to keep this block focused
on chain verification.
