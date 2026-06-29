# Obscura (OBX): Engineering Overview

A privacy cryptocurrency that hides every spend in a global, chain-wide anonymity set, keeps node state constant-size, and ships a post-quantum migration path. This document is for crypto/blockchain engineers. It assumes you know Merkle trees, nullifiers, STARKs, UTXOs, HTLCs, adaptor signatures, and memory-hard PoW, and it focuses on what Obscura actually is, how the pieces work, the data structures and tx types, the real flags/RPC/CLI, and the honest tradeoffs.

The canonical academic writeup is `docs/WHITEPAPER.md`. This is the engineer's cut.

## TL;DR

- Global anonymity set, not a decoy ring. Every shielded coin lives in one chain-wide accumulator. Your anonymity set is the whole shielded UTXO set, and it grows monotonically with adoption. No per-spend ring, no decoy-selection heuristic.
- No trusted setup, anywhere. The unknown-order accumulator runs on an imaginary quadratic class group derived from a public seed (RSA-2048 modulus is an alternate backend). The proof system is a transparent zk-STARK (FRI over Goldilocks). There is no SRS and no ceremony whose toxic waste could mint coins.
- Sender-unlinkable shielded spend, live. The default confidential path is one zk-STARK that proves commitment-tree membership, recipient-secret nullifier reveal, and value conservation in a single proof. The sender cannot precompute the nullifier of a coin it paid out, so it cannot recognize when that coin is later spent.
- Constant-size state for everyone, miners included. State folds into a few header-committed roots; Proof-of-Retrievability forces miners to actually hold recent block bodies; pruning is intrinsic. A node stays small no matter how long the chain runs.
- RandomX PoW by default. Same ASIC-resistant memory-hard VM as Monero, KAT-verified. Monero-style smooth emission with a perpetual tail. 120s blocks, LWMA retarget.
- Trustless cross-chain swaps. Scriptless 2-of-2 adaptor swap against XNO (Nano), no bridge, no custodian, no wrapped tokens. Real XNO has moved on-chain via the live path.
- Post-quantum building blocks wired in. WOTS+ hybrid spend authority, ML-KEM-768 stealth, lattice (SIS/BDLOP) commitments, and a BLAKE2b Merkle accumulator ship as a version-2 transaction kind, off the default consensus path.
- Pure Go, CGO-free, single static binary. One `obscura-node` artifact is node + miner + wallet + RPC + swap daemon, plus a turnkey desktop app via `--ui`.

Not externally audited. Soundness margins (notably FRI) rest on standard but partly conjectured proximity-gap results, and several deep subsystems (the from-scratch STARK engine, class-group accumulator) want an external audit before this carries real value. No "audited" or "proven-secure" claims are made here.

## Core model: UTXO with three spend profiles

Obscura is a UTXO chain. A `Tx` (`pkg/tx/tx.go`) is a single struct with typed input/output slices, not one opcode per tx. The relevant kinds:

- Transparent outputs (`Output`): a Pedersen `Commitment` to the amount plus a one-time stealth `OneTimeKey`. Spent by naming the `OutputRef` and proving a key-image with a DLEQ proof. Hides amount and recipient; the spend reveals the input link.
- Shielded mint (`ZKOutput`): mints a coin (a Poseidon note commitment) into the commitment tree. It creates no transparent UTXO, so there is no cross-system double-spend surface; it is spendable only via a shielded input.
- Confidential anonymous spend (`CZKSpend`): the primary private path. One zk-STARK proves membership + recipient-secret nullifier + in-field conservation. Hides input note, both amounts, and the sender link; reveals only `{nf, root, cm_out, fee}`.
- Sender-anonymous inputs (`ZKInput` / `AnonInput`): the fully sender-hiding spend leg (the "100B endgame"), with a re-blinded pseudo-commitment and an equality proof tying it to the spent value.
- Swap outputs/inputs (`SwapOut` / `SwapIn`): on-chain atomic-swap legs with a claim path (signature under the joint claim key) and a refund path (timelock).
- Vault outputs/inputs (`VaultOut` / `VaultIn`): confidential staking deposits and matured claims.
- PQ outputs (`PQOutput`, version-2): post-quantum confidential outputs in a separate value space.

Versioning is a `uint16` on the `Tx` (`tx.Version`). Version 1 is the classical consensus path. Version 2 carries the post-quantum fields and is gated by both `tx.Version == 2` and the presence of PQ fields (`pkg/chain/pqvalidate.go`); classical transactions never enter the PQ path, so the default coin's size and speed are unaffected. Atomic granularity is `AtomicPerCoin = 10^12` (12 decimals, like Monero's piconero).

### Lifecycle of a spend

The whole pipeline is deterministic, wallet to canonical chain:

1. Wallet selects notes, derives a stealth output, encodes confidential amounts, and builds the zk-STARK (membership + recipient-secret nullifier + conservation + range), then submits to the local node.
2. Mempool independently re-verifies the proof, fee, anchor freshness, and reorg-safe constraints. Admissible txs reserve their nullifiers (and any vault keys) and queue; everything else is rejected outright. Spent-tag sets are unified across the transparent and anonymous paths, so a coin cannot be spent once transparently and once anonymously.
3. P2P gossips via Dandelion++ (stem, then fluff), optionally over Tor.
4. A miner (which must be a full node, see PoR) selects txs, applies the state transition, computes the six header roots, and grinds RandomX over the preimage that binds all six.
5. Every node re-validates end to end: PoW + header binding, PoR entries (header-only), each tx zk-STARK + nullifier non-membership, and recomputes all six roots to match the header.
6. State/chain applies accumulator adds, nullifier inserts, and commitment-tree appends, then runs fork choice (snapshot-restore-and-replay if a heavier fork exists). Pruning runs intrinsically. Tx is then final.

## Global anonymity: accumulator + commitment tree + nullifiers

Obscura runs two parallel anonymity-set commitments. Both are header-committed and PoW-bound. They serve different proof systems because class-group ops are STARK-hostile.

### Class-group BBF accumulator (`pkg/accumulator`, `pkg/group`)

- Construction: Boneh-Bunz-Fisch dynamic accumulator in a group of unknown order. `acc = g^(prod p_i)` where each output `o` maps to an odd prime `p_o = HashToPrime(o)` (`primes.go`). One group element commits an arbitrarily large set in O(1) space.
- Membership: witness `w = g^(prod over o' != o)`, verified by `w^p == acc`. Non-membership via a Bezout pair. Succinct verification uses Wesolowski PoE and NI-PoKE2; `zkmem.go` + `nullifier.go` give a witness-hiding membership proof bound to a deterministic nullifier `N = U^p`, so a spend proves membership without revealing which coin.
- Backend: an imaginary quadratic class group with discriminant `D < 0` (`D ≡ 1 mod 8`, negative prime) derived from a public seed. The class number `h(D)` is infeasible to compute, so the order is unknown with no ceremony. Elements are reduced forms `(a,b,c)` composed by Dirichlet composition. `RSAGroup` over the RSA-2048 challenge modulus is the alternate, assumption-based backend.
- Security rests on the Adaptive Root and Strong RSA assumptions in a genuinely unknown-order group.
- Constant memory: `NewValueOnly` retains only `acc` + an add count, byte-identical `Value`/`Size` to the full accumulator but cannot build witnesses. Witness construction therefore needs an archive node. Duplicate-coin rejection does not lean on the accumulator; it is enforced by an authoritative on-disk `outPrimes` uniqueness set in `pkg/chain`.

Cost note: class-group group ops are materially slower than ECC (Dirichlet composition + reduction per multiply). This is why the primary shielded spend uses the Poseidon-Merkle tree below, not the class group, inside the STARK.

### Poseidon commitment tree (`pkg/stark/imt256.go`, `imt256_epoch.go`)

- An incremental Merkle tree of `Node256` values (4 Goldilocks elements ≈ 256-bit nodes). Internal nodes compress with `WideHash2`, a 2-to-1 width-8 Poseidon sponge (4-element rate, 4-element capacity) targeting ≈2^128 collision resistance.
- Append-only and O(depth) state: `Append` keeps only the frontier (`filled`) plus precomputed empty-subtree digests (`zeros`), so a full node tracks the root without materializing interior nodes. Wallets serve authentication paths (`MerklePath256`) from retained leaves.
- Epoch sharding (`EpochIMT`): the tree depth is fixed at a consensus constant (`ZKDepth = 16`, ≈65k coins/epoch). When a tree fills (`2^depth` leaves), it rolls to a fresh tree. Total supply is unbounded while proof path length, and therefore proof size and verify time, stay constant. Every recent epoch root is a valid spend anchor; a spend proves membership in its own epoch's root. Deployments can raise `ZKDepth` (e.g. 20 → ~1M/epoch). Tradeoff: the per-spend anonymity set is one epoch's coins, not literally all coins (the standard bounded-epoch model).

### Recipient-secret nullifiers

A Zcash-Sapling two-key structure realized purely in Poseidon (no EC ops, native to the STARK):

```
pk = H(nk, 0)          # published address, nk is recipient-only secret
nf = H(nk, rho)        # nullifier, revealed on spend
cm = H(H(H(pk, N(a)), N(rho)), N(beta))   # note commitment
```

A payer learns `pk` and can build a valid note, but cannot derive `nk` (a Poseidon preimage) and therefore cannot compute `nf`. Result: (i) spend authority, only the holder of `nk` can produce a spend witness; (ii) sender-to-spend unlinkability, the sender cannot precompute the nullifier of a coin it sent and cannot recognize when that coin is later spent. Double-spend prevention is the usual nullifier set; `nf` is deterministic and unforgeable without `nk`. This is enforced end-to-end by `TestNfRecipientSecretOwnership` / `nf_ownership_test.go`.

## The zk-STARK engine (`pkg/stark`)

A from-scratch, pure-Go transparent STARK. No external proving library, no pairings, no SRS. The only cryptographic primitive in the stack is a hash, so it is transparent and plausibly post-quantum.

- Field: Goldilocks `p = 2^64 - 2^32 + 1` (`field.go`). Fast reduction via `2^64 ≡ 2^32 - 1` and `2^96 ≡ -1`. 2-adicity 32, so in-field NTTs exist up to `2^32` points (`ntt.go`, iterative Cooley-Tukey). Reed-Solomon LDE is zero-pad-then-NTT.
- Commitments: Merkle trees over LDE evaluations. Fiat-Shamir `Transcript` is a BLAKE2b sponge with a ratchet constant and domain-separation labels.
- FRI (`fri.go`): Reed-Solomon rate `rho = 1/friBlowup = 1/4`. Commit phase folds `±` pairs via the transcript challenge, halving degree/domain to a constant; the `±` pair is committed as one paired leaf so a single Merkle path authenticates both. Grinding `friGrindBits = 16` forces a transcript nonce with 16 leading zero bits before query positions are drawn.
- AIR + DEEP-ALI (`air.go`): width-W trace, transition constraints over consecutive rows, boundary constraints, public periodic columns. The `Circuit` interface evaluates the same constraints over scalars (`Felt`) and polynomials (`Poly`), so prover and verifier cannot drift. Soundness binds (1) FRI low-degree of the DEEP polynomial, (2) an out-of-domain relation `CP(z) = sum_k a_k q_k(z)`, (3) the DEEP combination at each query point.
- In-circuit hash: Poseidon over Goldilocks (`poseidon.go`), `x^7` S-box, ARK + MDS mix, all low-degree. Width-3 (`R_F=8, R_P=22`) gives 2-to-1 with 64-bit output; width-8 (`poseidon_wide.go`) gives 256-bit `Node256` via Jive_2 feed-forward, ≈2^128 collision resistance, and backs the consensus commitment tree. Round constants from the canonical Grain LFSR; MDS is a reference Cauchy matrix.
- Zero-knowledge (`zk_mask.go`): coset LDE on `airCoset · <w>` with `airCoset = 7` (the field generator, in no proper subgroup, disjoint from the trace domain `H`), plus polynomial masking `t'(x) = t(x) + Z_H(x)·r(x)` with `r` of degree `2·nQueries + 4`. `t' = t` on `H` so constraints/boundaries are untouched; off-`H` openings are a Vandermonde-bijective image of `r`, hence witness-independent.

What a spend proof actually proves (the merged `cnfSpendCircuit`, `cnfspend_air.go`), in one proof:
- Input authority + membership + nullifier: `pk_in = H(nk_in, 0)`, the reconstructed `cm_in = sponge(pk_in, a_in, rho_in, beta_in)` is in the tree at the public `root` via a folded auth path, and `nf = H(nk_in, rho_in)`.
- Output: `cm_out = sponge(pk_out, a_out, rho_out, beta_out)`, only `cm_out` revealed.
- Value: `a_in = a_out + fee`, with `a_in, a_out ∈ [0, 2^vbits)` via in-circuit bit decomposition; only `fee` is public.

The secrets `nk_in, rho_in, a_in` sit in constant trace columns and are linked into the nullifier and balance sub-computations by gated reset transitions, so the same values that open the note also produce its nullifier and satisfy conservation. Trace length is `nextPow2(merkleBlock · (depth + 8))`, constant in total supply.

Soundness, stated honestly (from `chainparams.go` / `fri.go` comments):
- `ZKQueries = 48`, `friGrindBits = 16`.
- Under the provable unique-decoding bound, ~0.7 bits/query → ~49 bits + 16 grinding.
- Under the widely-cited proximity-gap / list-decoding figure (`log2(1/rho) = 2` bits/query) → ~2·48 + 16 ≈ 112 bits. We report ~112-bit as the industry-standard figure and note the proximity-gap result is partly conjectured. `nQueries` is a tunable consensus parameter and can be raised to hit any target under the conservative bound alone.

Cost note: FRI proofs are not tiny. A single confidential spend tx with full membership at `ZKDepth` is on the order of ~1.8-2 MB (see the comment in `pkg/config/params.go`); `MaxBlockWeight` is raised to 4 MB to fit them. This is the price of transparency + no trusted setup; it is logarithmic in epoch size, not in total supply.

## Confidential amounts

Two substrates, both verifiable by every node with no trusted party.

### Pedersen layer (`pkg/commit`) for the transparent path

- `C = vH + rG` over edwards25519, `H` a NUMS hash-to-point generator (domain `"Obscura/Pedersen/H/v1"`). Computationally binding, perfectly hiding; additively homomorphic.
- Conservation: `R = sum C_in - sum C_out - fee·H`. If balanced, `R = zG` for known residual blinding `z`; a Schnorr DLog proof (`ProveDLog`/`VerifyDLog`, `"Conservation"` domain) shows `R` has no `H`-component. Coinbase and public-value legs (swap in/out of the confidential pool) use the same shape.
- Range proofs (`rangeproof.go`): bit-decomposition, each bit committed and shown Boolean by a Schnorr OR-proof, `sum_i C_i = C`. `RangeBits = 64`. Size O(RangeBits). This is the anti-inflation guard for the Pedersen path (stops a value that, reduced mod the group order, behaves negative).

### In-field conservation (the CZKSpend path)

The primary confidential path abandons Pedersen/ed25519 cross-system binding and proves conservation as a field equation inside the STARK: `a_in = a_out + fee` over Goldilocks, only `fee` public, one nullifier set shared with the public ZK path (keyed on `nf`).

Field-bound anti-inflation: because conservation is mod `P = 2^64 - 2^32 + 1`, a "negative" fee (a huge `uint64` ≡ `P - k`) would let `a_out > a_in` satisfy the equation. Defense: the circuit range-binds `a_in` and `a_out` to `[0, 2^vbits)`, and consensus rejects any `Fee >= 2^ConfidentialBits`. `ConfidentialBits = 60`, capped at `stark.MaxRangeBits = 60` so `2^bits < P` (every in-range value is a canonical field element, no aliasing). The ceiling is enforced at compile time:

```go
var _ = [stark.MaxRangeBits - config.ConfidentialBits]struct{}{}  // negative length => won't compile if raised
```

Practical cost: a single confidential coin is bounded to 2^60 atomic units (below the supply cap). Larger amounts spend via the public-amount path.

## Consensus: RandomX PoW, emission, PoR, fork choice

Nakamoto consensus: memory-hard PoW, heaviest-cumulative-work tip is canonical, probabilistic finality.

- PoW backend (`pkg/pow`): RandomX is the consensus PoW, the ASIC-resistant memory-hard VM used by Monero, shipped by default in the un-tagged binary (`BackendName = "randomx-canonical"`, build constraint `!protopow`, KAT-verified against Monero vectors). The canonical RandomX is the pure-Go P2Pool go-randomx port, so even the default build is CGO-free. A fast prototype backend (`BackendName = "vm-randomx-style"`, `-tags protopow`) serves dev/local nets and refuses to start unless `OBX_ALLOW_PROTOTYPE_POW=1`.
- Difficulty: LWMA over `DifficultyWindow = 60` blocks, pinning `TargetBlockTime = 120s`.
- PoW seed epochs: seed fixed per epoch to block dataset grinding. Rotates every `PoWEpochLen = 2048` blocks, drawn from a block `PoWSeedLag = 512` back. The lag is also the partition-recovery window. Invariant: `MaxReorgDepth (100) <= PoWSeedLag <= PoWEpochLen` and `PoWSeedLag <= PoRWindow`.
- Emission (`pkg/config`): `reward = remainingSupply >> EmissionShift`, `EmissionShift = 19`. `MoneySupplyCap = 18,400,000 OBX`. Initial reward ≈ 35 OBX/block; perpetual tail `TailEmissionAtomic = 0.6 OBX` once the smooth reward decays below it. At 120s blocks: ~50% emitted in ~1.38y, ~90% in ~4.6y, tail floor ~8.1y. `IncentivePoolBps = 500` (5% of each reward) is diverted from already-emitted supply into a bounded incentive pool (funds Vault yield, mints nothing new).
- Header roots, PoW-bound (`pkg/block/block.go`): six roots, `AccValue`+`AccSize` (class-group coin set), `NullRoot` (nullifier/key-image set), `CMRoot` (Poseidon commitment-tree root), `PQAccRoot` (BLAKE2b PQ accumulator), `PoRRoot`, `StateRoot` (pre-state: emitted, incentivePool, disk-set commitments, in-RAM maps incl. pqUtxo amounts). All six are concatenated (variable-length `AccValue` length-prefixed) into the PoW preimage, so a valid nonce binds every root. The node predicts and verifies each update (`acc' = acc^(prod p_o)` for BBF, `RootAfter` for Merkle roots) before accepting.
- PoR mining (`pkg/block/por.go`, `pkg/chain/por.go`): a miner must be a full node. To mine height `H` it answers `PoRChallenges = 8` pseudo-random challenges seeded from the parent hash (`BLAKE2b("OBX/por-chal/v1" || prevHash || s)`), each selecting a height in `[max(0,H-W), H)` with `W = PoRWindow = 10,000` and a tx index. Each `PoREntry` carries the full serialized tx bytes plus a Merkle branch to that block's root, forcing retention of bodies, not just txids. The entries hash into `PoRRoot`, which is in the PoW preimage, so a body-free header cannot have proofs appended after grinding. Validators check PoR header-only and need no body custody.
- Fork choice (`forkchoice.go`): heaviest cumulative PoW. Normal-finality reorgs bound by `MaxReorgDepth = 100`; partition-recovery reorgs accepted up to `PoWSeedLag = 512` deep.
- Mainnet timing lock (`pkg/config/params.go`): `OBX_NETWORK` defaults to `"mainnet"`. On mainnet the devnet timing overrides (`OBX_TARGET_BLOCK_TIME`, `OBX_FIXED_DIFFICULTY`, `OBX_GENESIS_DIFFICULTY`) are categorically ignored, so you cannot accidentally ship a fast-block mainnet. Only `OBX_NETWORK=testnet|devnet` re-enables the fast knobs. `DefaultP2PPort = 18080`, `DefaultRPCPort = 18081`.

## Constant-size state: accumulators, snapshots, pruning

- Add-only accumulators: the BBF coin accumulator and the PQ Merkle tree are insert-only (spends are proven via nullifiers, not by deleting coins), so the anonymity set only grows and updates are a single exponentiation/append, not O(n) recompute. `Remove` exists in the library but is off the consensus path.
- No cheap undo: there is no inverse to `acc^p` without the group order, and streaming Merkle peaks discard the leaves needed to roll back. So reorgs use snapshot-and-replay (`pkg/chain/snapshot.go`): full consensus state is checkpointed every `SnapshotInterval = 200` blocks (`snapshotsToKeep = 2`) plus a SIGTERM shutdown snapshot, with the invariant `MaxReorgDepth < SnapshotInterval`. To reorg, restore the snapshot at/below the fork point (`RestoreState`, trusting the stored `acc` because it is verified against the header-committed `AccValue`) and deterministically replay surviving blocks forward. The shutdown snapshot specifically fixed a restart pathology where a node slow-replayed the whole chain and looked hung.
- Pruning is intrinsic: every node, miners included, retains bodies only over `[tip - PoRWindow, tip]`. New nodes catch up via snapshot sync (a serving node streams a recent state snapshot instead of from-genesis replay), `snapshotsync.go`.

## Cross-chain swaps: scriptless 2-of-2 adaptor with XNO

OBX swaps against external assets peer-to-peer, no bridge, no custodian, no wrapped tokens. The settleable foreign leg is XNO (Nano). The allowlist `config.SettleableAssets = {OBX, XNO}` gates every swap; non-XNO offers are rejected at admission. An OBX/BTC SHA-256 HTLC design exists (`pkg/swapd/bitcoin.go`) but BTC is not in the allowlist and is not takeable. The XNO leg is real and has moved real XNO on-chain across two independent nodes.

There is no hashlock script on either chain. Two roles mint only their own secret shares:

- Maker (OBX funder, XNO sweeper): holds claim share `b` and XNO share `sB`. Funds the OBX `SwapOut` first, co-signs the claim, sweeps XNO. Downside protected by the SwapOut refund timelock.
- Taker (XNO funder, OBX claimer): holds claim share `a` and XNO share `sA`. Locks XNO only after verifying the maker's OBX SwapOut on-chain. Downside protected by leg ordering.

Both derive the same joint keys from public shares: OBX claim key `K = A + B` (rogue-key-safe via per-share PoPs), joint XNO account `(sA + sB)·G`. The adaptor point is `T = sA·G`, so the published OBX claim (an adapted 2-of-2 signature) necessarily reveals `sA` on-chain, which the maker combines with `sB` to sweep XNO. Same secret on both chains = atomic.

The six-message session (`pkg/swapsession` over `pkg/swapnet`, kinds `KindInit`..`KindClaimPreSig`, plus `KindAbort`):

1. Init (Taker→Maker): terms, `SwapID`, public `A`, `Sa`, PoP of `A`, nonce `Ra`, adaptor point `T = Sa`, OBX amount, raw 128-bit XNO amount, in-band fee.
2. MakerCommit (Maker→Taker): public `B`, `Sb`, PoP of `B`, nonce `Rb`. Both sides derive `K`, `R = Ra + Rb`, `(sA+sB)·G`.
3. Funded (Maker→Taker): OBX SwapOut funded/confirmed. Taker re-derives and verifies the on-chain output (key `K`, amount, unlock height, `ClaimR=R`, `ClaimT=T`) before locking anything.
4. XNOLocked (Taker→Maker): XNO locked to the joint account. Maker reads the lock's authoritative destination + amount from Nano and refuses to co-sign unless it pays exactly the joint key the exact amount.
5. ClaimRequest (Taker→Maker): core hash of the claim tx plus the taker's pre-signature half `s_a = ra + e·a`. Maker checks `s_a·G == Ra + e·A` and stores `s_a` (anti-griefing).
6. ClaimPreSig (Maker→Taker): maker's half `s_b = rb + e·b`. The maker co-signs at most one distinct core hash (a second co-sign leaks `b`), persisted durably to survive a crash.

The taker aggregates `s_a + s_b`, adapts with `sA`, verifies under `K`, mines the claim (takes OBX, publishes `S_full` revealing `sA`). The maker scrapes the claim and extracts `sA = S_full - s_a - s_b` from chain data alone, then sweeps XNO with `sA + sB`. A withholding taker cannot freeze the maker's XNO.

Refund safety (reorg-safe timelocks, enforced at taker-session, maker-fund, and consensus layers):
- `SwapReorgMargin` defaults to `PoWSeedLag` (512). A claim is valid iff `height + SwapReorgMargin <= UnlockHeight` (tied to the true deepest accepted reorg, not `MaxReorgDepth`).
- `SwapTimelockWindow` defaults to `PoWSeedLag + 100` (612): blocks after funding before the refund branch opens.
- `SwapMinClaimWindow = 50`: minimum open claim window. Invariant `SwapTimelockWindow >= SwapReorgMargin + SwapMinClaimWindow`.

Both maker (at `Fund`) and taker (at `VerifyFundedAndLock`) refuse a swap whose unlock height leaves no usable claim window. If the taker never claims, the maker reclaims OBX at the refund branch; if the maker aborts before the taker locks XNO, the taker has locked nothing.

Operational hardening: the public RPC `/swaps/take` is disabled unless `OBX_PUBLIC_SWAPS=1`, so a public node cannot be driven to fund swaps that drain an operator's real XNO balance.

## Post-quantum path (version-2 tx kind, off the default path)

A Shor-capable adversary breaks every edwards25519 relation and the unknown-order accumulator at once, and a harvest-now-decrypt-later adversary records today. Obscura's transparent STARK is already PQ (soundness reduces to hash collision resistance + Fiat-Shamir, Grover only halves). The rest of the migration ships as version-2 fields:

- ML-KEM-768 stealth (`pkg/pqstealth`): Kyber/FIPS-203 from Go's `crypto/mlkem`. Recipient publishes an encapsulation key as a PQ view key; sender encapsulates `(ss, ct)` (ct 1088 bytes), attaches `ct`, derives a 16-byte detection tag, an amount keystream, and a 16-byte MAC from `ss` via domain-separated BLAKE2b. Recipient scans by decapsulating and recomputing the tag in constant time (implicit rejection gives a pseudorandom `ss` for foreign ciphertexts).
- Lattice/Ajtai commitments (`pkg/pqcommit`): BDLOP-style, binding under SIS. Public matrices sampled from a fixed BLAKE2b XOF seed (NUMS, no setup); `c1 = A1·r`, `c2 = A2·r + v`. Additively homomorphic so conservation carries over (summed randomness must stay under the SIS bound). Params `q = 2^32 - 5`, `n1 = 128`, `m = 512`.
- Hybrid signatures (`pkg/pqsign`): on-chain one-time key `Key = BLAKE2b(P || dom || R)` commits both an edwards25519 point `P = xG` and a WOTS+ root `R`. A spend presents both a Schnorr proof and a WOTS+ signature over the tx `CoreHash`; secure while either assumption holds. WOTS+ instance `n=32, w=16, 67 chains, ~2KB sigs`, keyed-BLAKE2b one-way function (not RFC 8391 byte-compatible). Schnorr verifier rejects identity/non-prime-order points.
- PQ Merkle accumulator (`pkg/pqaccum`): append-only RFC 6962 BLAKE2b tree with leaf/node domain prefixes (0x00/0x01) closing the CVE-2012-2459 ambiguity, streaming O(log n) peaks, path directions derived from `(Index, Size)`. Security is collision resistance only. A raw Merkle inclusion proof is PQ-sound but not ZK, so the live PQ consensus path uses transparent membership + public amounts (the per-output supply-cap check substitutes for a range proof), with nullifiers bound to the full `OutputRef`.

## Running it

Pure Go, CGO-free. `go build ./cmd/obscura-node` produces one static binary that is node + miner + wallet + RPC + swap daemon. Default build = canonical RandomX. Opt into the fast prototype PoW with `-tags protopow` (and `OBX_ALLOW_PROTOTYPE_POW=1` at runtime).

Build (Go 1.25+, no CGO):

```
make                 # node + wallet
make node            # obscura-node only
make release         # cross-compile all platforms into dist/ (-trimpath, -s -w)
make BUILDTAGS=protopow node   # fast prototype PoW backend
make test            # full suite incl. 2048-bit class-group + RandomX KATs
make devnet          # one-command 2-node devnet
make testnet N=5     # local N-node testnet
```

`cmd/` binaries: `obscura-node` (the all-in-one), `obscura-wallet` (CLI wallet), `obscura-miner` (standalone miner against a node's RPC), `obscura-swap` (swap orchestrator with `selftest`/`live` modes), `obscura-dashboard` (standalone web dashboard), plus `obscura-wasm`, `obscura-loadgen`, `obscura-dexsim`.

CLI quick start:

```
./bin/obscura-node --mine --ui                                  # node + miner + desktop UI
./bin/obscura-wallet balance --wallet ~/.obscura/wallet.seed    # wallet (subcommands: new, restore,
./bin/obscura-wallet send --to <addr> --amount 1.5 --fee 0.0001 #   address, balance, send, sweep, proof,
./bin/obscura-miner --node http://127.0.0.1:18081 --address <a> #   offer, zkmint, zkspend)
```

The CLI wallet talks to a node over `--node http://127.0.0.1:18081`. Passphrase/mnemonic come from env (`OBSCURA_WALLET_PASSPHRASE`, `OBSCURA_WALLET_MNEMONIC`) to keep them out of shell history.

Key `obscura-node` flags (`cmd/obscura-node/main.go`):

```
--datadir <dir>           data directory
--p2p 0.0.0.0:18080       P2P listen address (DefaultP2PPort)
--rpc 127.0.0.1:18081     RPC listen address (DefaultRPCPort)
--seeds host:port,...     comma-separated seed peers (also OBX_SEEDS)
--mine                    enable the built-in CPU miner
--mine-address <hex>      address to mine to (default: node miner wallet)
--advertise host:port     public address to announce (auto-discovered if empty)
--coinbase-maturity <n>   coinbase aging (network param, all nodes must match)
--ui                      launch the turnkey desktop app (see below)
--nano-rpc <preset|url>   Nano RPC: rainstorm|somenano|nanoto|public or a URL; empty disables XNO (also OBX_NANO_RPC)
--nano-rpc-auth <hdr>     optional Authorization header for the Nano RPC (OBX_NANO_RPC_AUTH)
--nano-wallet <id>        Nano node wallet id used to fund XNO locks (OBX_NANO_WALLET)
--nano-account <nano_...> funding account inside --nano-wallet (OBX_NANO_ACCOUNT)
--nano-work-url <url>     override the work_generate endpoint (OBX_NANO_WORK_URL)
--tor-proxy / --onion-address   route via a local SOCKS5 Tor proxy / advertise a .onion
```

Nano endpoint is never hardcoded: `NewNanoRPC` requires an operator-supplied URL/preset. `rainstorm` (default) is the only preset that does `work_generate`; `somenano`/`nanoto` fall back to it for work. Print presets with `--nano-rpc-list`.

Desktop app (`--ui`, `cmd/obscura-node/ui.go`): `obscura-node --ui` embeds the website assets + a local `/api` proxy and opens them in a borderless Chrome `--app` window, giving a wallet/explorer/swaps GUI with no third-party frontend. `--ui` still runs canonical RandomX and does not relax the prototype-PoW start guard. In hosted mode (`OBX_UI_PUBLIC`) wallet-proxy callers are treated as untrusted (swap-take gated, operator XNO account hidden); bind `--ui-addr` to loopback for single-operator desktop mode.

RPC endpoints (HTTP, `pkg/rpc/server.go`). Public (no auth): chain/wallet `/status`, `/height`, `/accvalue`, `/block`, `/blocks`, `/headers`, `/submittx`, `/zkwitness`, `/feerate`, `/mempool`, `/peers`; swaps/DEX `/offers`, `/offers/json`, `/offer`, `/offer/cancel`, `/quote`, `/depth`, `/liquidity`, `/swaps/active`, `/swaps/take` (gated by `OBX_PUBLIC_SWAPS=1`), `/trades`, `/candles`, `/stats`, `/orders`, `/order/<id>`; explorer `/explorer/{summary,block,mempool,vaults,pricehistory,swaps}`; XNO `/xno/account`. Operator-only (loopback or `OBX_RPC_TOKEN` bearer auth): `/blocktemplate`, `/submitblock`, `/xno/recovery`, `/xno/withdraw`.

Runtime env vars: `OBX_NETWORK` (mainnet|testnet|devnet), `OBX_SEEDS`, `OBX_ALLOW_PROTOTYPE_POW`, `OBX_PUBLIC_SWAPS`, `OBX_NANO_RPC` and the `OBX_NANO_*` family, `OBX_AUTO_LIQUIDITY`, `OBX_UI_PUBLIC`. On mainnet the devnet timing overrides are ignored.

Networking and origin privacy (`pkg/p2p`): up to `maxPeers = 32` (24 inbound, 8 reserved outbound). PEX discovery (`getaddr`/`addr`, up to 16 sampled entries), a `discoveryLoop` (20s) topping connections up, and Bitcoin-style vote-based "addr-me" self-address discovery: an external address is adopted only after `minSelfDiscoveryGroups = 4` distinct `/16` reporters agree (raised from 2 to resist Sybil poisoning), routable-only. Eclipse resistance: IP-group caps (`/16` for IPv4, `/32` for IPv6, host for `.onion`), `maxInboundPerIP = 3`, `maxInboundPerGroup = 4`, a book cap, diversity sampling (`Sample(n)` round-robins one address per group, so owning many IPs in one `/16` buys little), and persistent per-`/16` ban scoring (`groupBanThreshold = 100`, 1h ban, linear decay) plus a per-peer token-bucket rate limit (~50 msg/s). Dandelion++ (`dandelion.go`): per-epoch (60s) stem successor, fluff probability `p = 0.30` (~3-hop stem), 8s embargo fail-safe with origin bump + jitter, per-epoch per-node stem/fluff mode (not a per-tx coin flip, which would leak the origin statistically). Tor (`transport.go`): `NewTorDialer` routes dials through a local SOCKS5 proxy (typically `127.0.0.1:9050`) with no DNS leak; `.onion`-only mode is fail-closed on the address layer.

Confidential staking Vaults (`pkg/chain/vault.go`, `pkg/tx`): a `VaultOut` locks an amount for `Term` blocks under a Schnorr `OwnerKey`; a `VaultIn` after `depositHeight + Term` claims principal + `Amount × VaultRateBps(Term) / 10_000`, paid by decrementing the incentive pool (no new supply). Lock bounds `HoldingMinLock = 10,000` blocks (~2 weeks) to `HoldingMaxLock = 525,600` (~2 years). Amounts are not revealed to third parties.

## Parameters at a glance

| Param | Value | Where |
|---|---|---|
| `TargetBlockTime` | 120 s | `pkg/config` |
| `AtomicPerCoin` | 10^12 | `pkg/config` |
| `MoneySupplyCap` | 18,400,000 OBX | `pkg/config` |
| `EmissionShift` / `TailEmissionAtomic` | 19 / 0.6 OBX | `pkg/config` |
| `IncentivePoolBps` | 500 (5%) | `pkg/config` |
| `DifficultyWindow` (LWMA) | 60 | `pkg/config` |
| `PoWEpochLen` / `PoWSeedLag` | 2048 / 512 | `pkg/config` |
| `PoRWindow` / `PoRChallenges` | 10,000 / 8 | `pkg/config` |
| `MaxReorgDepth` | 100 (partition-recovery up to 512) | `forkchoice.go` |
| `SnapshotInterval` / `snapshotsToKeep` | 200 / 2 | `pkg/chain` |
| `MaxBlockWeight` | 4 MB | `pkg/config` |
| `ZKDepth` / `ZKQueries` | 16 / 48 | `pkg/stark/chainparams.go` |
| `friBlowup` / `friGrindBits` | 4 / 16 | `pkg/stark/fri.go` |
| `ConfidentialBits` / `RangeBits` / `MaxRangeBits` | 60 / 64 / 60 | `pkg/config`, `pkg/stark` |
| `SwapReorgMargin` / `SwapTimelockWindow` / `SwapMinClaimWindow` | 512 / 612 / 50 | `pkg/config` |
| `SettleableAssets` | {OBX, XNO} | `pkg/config` |
| `DefaultP2PPort` / `DefaultRPCPort` | 18080 / 18081 | `pkg/config` |

## Honest tradeoffs / what is not done

- Not externally audited. The from-scratch STARK engine, FRI parameters, and the class-group accumulator have had internal adversarial testing but no external review. Treat the chain as test-only until that lands.
- FRI soundness margin. The ~112-bit figure relies on partly conjectured proximity-gap / list-decoding results; the provable floor is ~49+16 bits. `ZKQueries` can be raised, at proof-size and verify-time cost.
- Class-group ops are slow. Dirichlet composition + form reduction per multiply is much heavier than ECC. It is kept off the STARK hot path for exactly this reason; witness construction needs an archive node.
- Proof sizes are large. A confidential spend with full membership runs ~1.8-2 MB. Logarithmic in epoch size, not total supply, but not SNARK-small.
- Anonymity set is per-epoch, not literally all coins. With `ZKDepth = 16` that is ~65k coins/epoch (raisable). This is the standard bounded-epoch shielded-pool model; the global accumulator still grows monotonically.
- Probabilistic finality only. No fast/economic finality gadget. LWMA can oscillate under variable hashrate (surfaced and devnet-mitigated).
- Censorship/PoR is economic, not cryptographic. PoR proves access to recent bodies, not custody; censorship resistance is conditioned on honest hashrate majority (alpha < 1/2).
- Default spend path is classical. Shor breaks edwards25519 and the unknown-order accumulator; the PQ track (WOTS+ hybrid, ML-KEM stealth, BLAKE2b accumulator) is wired and running but is the version-2 path, and its private membership is transparent (Merkle inclusion is not ZK) pending a PQ-ZK membership proof.
- BTC swaps are design-only. Only OBX/XNO is takeable; the BTC HTLC path has no wired orchestrator.
- ConfidentialBits cap. A single confidential coin is bounded to 2^60 atomic units; larger amounts use the public-amount path.
