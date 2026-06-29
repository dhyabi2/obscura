# Obscura (OBX) — Feature List

Every implemented feature, with nested inner lines detailing its parts. Grouped by area.

## Core ledger & consensus

- **UTXO ledger with confidential transactions hiding amounts and recipients on every transfer.**
  - Inputs reference prior outputs by ref plus an ownership proof and pseudo-commitment.
  - Amounts and recipient links stay private; sender↔output link is the only disclosure.
- **Add-only universal accumulator giving each spend a global, chain-wide anonymity set.**
  - Hash-to-prime membership with constant ~1086-byte proofs regardless of set size.
  - Wesolowski PoE, NI-PoKE2, multi-exp PoKE, and non-membership proofs supported.
- **Key-image nullifier set (T = x·U) preventing double-spends across spend types.**
  - Each spend publishes a deterministic key-image derived from the one-time secret.
  - Re-spending an output reproduces the same key-image and is rejected.
- **Unified spent-set linking transparent UTXO spends and anonymous key-images into one namespace.**
  - Transparent inputs now carry a key-image plus a DLEQ proof.
  - Closes the critical transparent⇄anonymous double-spend gap between formerly unlinked sets.
- **LWMA difficulty adjustment with overflow-safe retargeting for stable block times.**
  - Linearly-weighted moving average over recent solve times.
  - big.Int arithmetic prevents the difficulty-overflow class of consensus bugs.
- **Median-time-past timestamp rules rejecting out-of-window and back-dated blocks.**
  - New block timestamps must exceed the median of recent block times.
  - Bounds future drift to resist timestamp-manipulation difficulty attacks.
- **Coinbase maturity, lock-time, and minimum-fee enforcement at validation.**
  - Newly mined coins are unspendable until a maturity depth passes.
  - Lock-time and per-byte minimum-fee rules enforced before mempool/block acceptance.
- **Fork-choice with reorg handling, orphan management, and bounded reorg depth.**
  - Heaviest-work chain selection with switchover and rollback of side branches.
  - Orphans are buffered by bounded bytes; reorgs are capped at MaxReorgDepth.
- **Merkle block commitments hardened against the CVE-2012-2459 duplicate-leaf ambiguity.**
  - Domain-separated leaf and internal node hashing.
  - Odd-node handling that cannot be exploited to forge an equal root.
- **Atomic chainstate persistence with replay-revalidation on restart.**
  - State is written atomically to survive mid-write crashes.
  - On startup the chain replays and revalidates to guarantee a consistent tip.

## Cryptography & privacy

- **Pedersen commitments with bit-decomposition range proofs for confidential amounts.**
  - Each amount is committed and proven in-range via Schnorr-OR over bits.
  - Prevents negative/overflow amounts that would inflate supply.
- **Value-conservation proofs verifying inputs equal outputs without revealing amounts.**
  - Homomorphic commitment sums let the verifier check balance blindly.
  - Domain-separated from ownership/value-equality proofs to block confusion.
- **Schnorr ownership proofs of one-time keys bound to the transaction CoreHash.**
  - Proves knowledge of the one-time secret x with P = x·G.
  - Binding to CoreHash stops proof replay across different transactions.
- **Triptych-style one-of-many proof for anonymous spends over the global set.**
  - Proves an input is one of many outputs without revealing which.
  - Identity-T rejection added to block degenerate forged proofs.
- **DLEQ proofs binding key-images and encrypted amounts against cross-protocol confusion.**
  - Equality-of-discrete-log proof ties the key-image to the spent key.
  - Additional authenticated data binds the encrypted amount into the challenge.
- **Adaptor signatures enabling atomic, scriptless cross-chain swap settlement.**
  - Pre-signatures complete only on revealing a hidden scalar.
  - Identity-point rejection in pre-sign, pre-verify, and full-verify paths.
- **Dual-key stealth addresses with view tags for fast output scanning.**
  - Separate view and spend keys; ECDH-derived one-time output keys.
  - One-byte view tags let wallets skip non-matching outputs quickly.
- **Encrypted on-chain amounts and blinding masks recoverable only by the recipient.**
  - Amount and mask are encrypted to the shared secret.
  - View-only wallets can decrypt amounts without spend ability.
- **Identity-point and non-canonical-key rejection throughout all verifiers.**
  - Uses a properly initialized identity point to avoid panics.
  - Rejects low-order and malformed points in ownership, swap, and output checks.

## Post-quantum (off default path, opt-in)

- **WOTS+ hash-based one-time signatures fitting the spent-once one-time-key model.**
  - 32-byte public key, ~2 KB signature, BLAKE2b-only security.
  - Sign ~271 µs / Verify ~296 µs; no state management needed.
- **Hybrid one-time key binding classical Schnorr and WOTS+ together.**
  - On-chain key is BLAKE2b(P‖R) committing both halves.
  - Spend requires both proofs, so it survives if either assumption holds.
- **ML-KEM-768 stealth giving post-quantum payment detection and amount confidentiality.**
  - Go 1.25 stdlib crypto/mlkem, no external dependency, no trusted setup.
  - 1088-byte KEM ciphertext yields detection tag plus amount enc-key and MAC.
- **BDLOP/SIS lattice commitment, additively homomorphic, preserving the conservation structure.**
  - c1 = A1·r binding, c2 = A2·r + v message, r short.
  - Public matrices from a BLAKE2b XOF; binding=SIS, hiding=leftover-hash.
- **BLAKE2b Merkle accumulator replacing the Shor-breakable class-group/RSA set.**
  - RFC-6962 domain separation, CVE-2012-2459-immune, O(log n) proofs.
  - PQ-sound membership; zero-knowledge privacy deferred to the STARK roadmap.
- **Documented zk-STARK roadmap for transparent post-quantum membership and nullifier proofs.**
  - STARK over the Merkle path + nullifier, no trusted setup.
  - Brainstorm-confirmed choice over MPC-in-the-head and lattice-ZK alternatives.

## Mining & proof-of-work

- **RandomX-style VM PoW with cache, scratchpad, and randomized register program.**
  - Argon2d-seeded cache and per-nonce scratchpad.
  - Randomized integer/float/memory register VM per nonce.
- **Canonical Monero-compatible RandomX backend behind a build tag.**
  - Pure-Go binding to go-randomx, KAT-verified.
  - Default VM path keeps the shipping coin dependency-light.
- **Epoch-based PoW seed rotation with a safe seed-lag invariant.**
  - Seed changes on a fixed epoch schedule.
  - Seed lag exceeds MaxReorgDepth so reorgs can't change the active seed.
- **Deterministic IEEE-754 softfloat arithmetic for cross-platform PoW.**
  - Float operations use softfloat64 for bit-identical results.
  - Guarantees every node computes the same PoW everywhere.
- **Integrated CPU miner in the node plus a standalone miner binary.**
  - Node can mine to a configurable address.
  - Separate miner binary for dedicated mining setups.

## Networking (P2P)

- **Gossip P2P with magic/version handshake, deadlines, and per-peer write locks.**
  - Handshake rejects wrong-network and incompatible peers.
  - Read/write deadlines and locks prevent stalls and interleaving.
- **Peer and per-IP connection caps, ban scoring, and reconnect backoff.**
  - Limits connections per peer and per IP address.
  - Misbehavior accrues ban score; reconnects use exponential backoff.
- **PEX peer auto-discovery with an eclipse-resistant address book.**
  - Peers exchange known addresses to bootstrap the network.
  - Address book diversity reduces basic eclipse-attack surface.
- **Dandelion++ stem-and-fluff propagation for sender-origin privacy.**
  - Transactions relay along a stem before fluffing to the network.
  - A fluff-seen guard prevents premature broadcast leaks.
- **Tor-friendly transport support for node-level anonymity.**
  - Nodes can run behind Tor for network privacy.
  - Complements Dandelion++ for end-to-end origin hiding.
- **Graceful node shutdown releasing listeners to avoid port collisions.**
  - Node.Stop()/Addr() with a done-channel for clean teardown.
  - Prevents leaked listeners across test and restart cycles.

## Wallet

- **Confidential send/receive with automatic output selection and change.**
  - Builds confidential inputs/outputs with conservation and range proofs.
  - Auto-selects spendable outputs and creates change outputs.
- **BIP39-style mnemonic seed phrases for deterministic backup and recovery.**
  - Human-readable word list encodes the master seed.
  - Restores all keys and subaddresses deterministically.
- **Argon2id + XChaCha20 encrypted keystore with DoS-bounded KDF.**
  - Secrets encrypted at rest with a memory-hard KDF.
  - KDF parameters are bounded to prevent KDF-DoS on load.
- **Subaddresses for unlinkable per-payment receive addresses.**
  - Many receive addresses derive from one wallet.
  - Payments to different subaddresses are unlinkable on-chain.
- **View-only wallets that detect incoming funds without spend authority.**
  - View key reveals incoming amounts and outputs.
  - Spend key withheld so the wallet cannot move funds.
- **Payment proofs letting a sender prove a specific payment.**
  - Proves a given output was paid to a given recipient.
  - Amount-bound so the proof can't be reused for a different value.
- **Replace-by-fee and fee-bump support for stuck transactions.**
  - Resubmit with higher fee to replace a pending transaction.
  - Eviction ordering keeps the mempool consistent during replacement.
- **Manipulation-resistant dynamic fee estimation from block history.**
  - Estimates fees from recent confirmed blocks.
  - Resists short-term spam-driven fee manipulation.
- **BIP21-style payment URIs and Base58 address encoding.**
  - obscura: URIs carry address, amount, and label.
  - Bitcoin-style Base58 for human-readable addresses, with parsing.
- **Reservation system preventing double-reservation during fee auto-selection.**
  - Outputs are reserved while building a transaction.
  - Release-on-cancel avoids accidental double-spend reservation with --fee.

## Atomic swaps (XMR ↔ OBX)

- **Trustless adaptor-signature atomic swaps between Monero and Obscura.**
  - No custodian; settlement is enforced by adaptor-signature reveal.
  - Either both sides complete or both can refund.
- **Decentralized swap order-book liquidity layer for counterparties.**
  - Makers post offers; takers discover and match them.
  - Network-id and offer fields validated to avoid mismatches.
- **Cross-chain swap daemon coordinating the multi-step protocol.**
  - Drives lock, claim, and refund stages across both chains.
  - Runs the end-to-end swap state machine.

## Light client

- **SPV light-client verification of headers and proofs without full sync.**
  - Validates the header chain and membership proofs.
  - Lets constrained clients verify without downloading everything.

## Tooling & UI

- **Full node binary with integrated miner and JSON-RPC server.**
  - Single binary runs node, miner, and RPC.
  - Configurable mining address and network parameters.
- **CLI wallet binary for sending, receiving, and key management.**
  - Create/restore wallets and build confidential transactions.
  - Accepts both human and hex address formats.
- **Stdlib-only web dashboard for wallet and mining.**
  - Embedded UI assets; execs the wallet and proxies node RPC.
  - No third-party frontend dependencies.
- **JSON-RPC API with timeouts and request-size limits.**
  - Bounded request bodies via MaxBytesReader.
  - Per-request timeouts for safe remote exposure.
- **Cross-compilation build producing binaries for six platforms.**
  - One script builds for Linux/macOS/Windows targets.
  - Outputs Windows .exe and other platform binaries into dist/.
- **Marketing website (static), deployed live on Vercel.**
  - Landing and docs pages in website/.
  - Coin source stays local; only the site is published.

## Security & quality

- **Multi-track adversarial audits plus enumerate-and-break validation review.**
  - Parallel subagents hunt theft/inflation/double-spend and DoS issues.
  - Validation checks listed and broken one by one.
- **Critical-workflow test suite across all subsystems.**
  - Covers crypto, ledger, consensus, wallet, and network flows.
  - Built on a shared test harness package.
- **Whole-repo suite of 160+ tests, green under repeated and race runs.**
  - Passes with -count=2/3 and -race without flakes.
  - Includes a live multi-node sync regression.
- **Fixed the critical transparent⇄anonymous double-spend.**
  - Unified the UTXO spent-set and key-image nullifier set.
  - Regression test reproduces and guards the original break.
- **Hardened against overflow, amplification, and denial-of-service vectors.**
  - Emitted-supply overflow, message amplification, and KDF-DoS addressed.
  - io.ReadFull and bounds checks applied across deserialization.
