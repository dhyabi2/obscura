# Invention Log — Block 12: Decentralized XMR ↔ Obscura Atomic Swaps

Produced with the invention methodology + Methodology-Tree brainstorming engine.

## TL;DR — Is it possible?
**Yes — and it is *easier* than the existing Bitcoin↔Monero atomic swap.** The
proven BTC↔XMR swap (Gugger/COMIT) works because Bitcoin has scripts and uses a
cross-curve (secp256k1↔ed25519) DLEQ proof. Obscura is a chain **we control**, it
uses **ed25519 (the same curve as Monero)**, and we can give it **adaptor
signatures + timelocked multi-path swap outputs**. So Obscura plays the
"scriptable" role Bitcoin plays — and because both chains are ed25519, the
**cross-curve DLEQ is eliminated entirely**, making the protocol simpler.

No bridge, no wrapped tokens, no trusted third party, no trusted setup.

## The protocol (trustless, scriptless on the Monero side)
Alice has XMR, wants OBX. Bob has OBX, wants XMR. Secrets `s_a` (pub `S_a`),
`s_b` (pub `S_b`), both on ed25519.
1. **Setup.** Parties exchange `S_a, S_b` with same-curve proofs of knowledge
   (no cross-curve DLEQ needed). The XMR is to be locked to the Monero spend key
   `s_a + s_b` — spendable only by someone who learns **both** secrets.
2. **Lock.** Alice locks XMR to `addr(S_a + S_b)`. Bob locks OBX in an Obscura
   **swap output** with paths: *success* (Alice spends, revealing `s_a`),
   *refund* (Bob reclaims after timelock `T1`, revealing `s_b`), *punish*
   (anti-grief after `T2`).
3. **Claim → reveal.** Alice claims the OBX using a signature **adapted with
   `s_a`** (adaptor point `S_a`). Publishing that on-chain signature lets Bob
   `Extract(s_a)` (the cornerstone below), compute `s_a + s_b`, and spend the XMR.
4. **Refund symmetry.** If Alice never claims, Bob's refund-spend reveals `s_b`,
   so Alice (who knows `s_a`) recovers `s_a + s_b` and reclaims the XMR. Nobody
   can lose funds: every branch either completes the swap or refunds both sides.
5. **Finality.** Release only after N Monero confirmations; set refund timelocks
   ≥ 2× the expected Monero reorg window (engine-flagged).

## Challenges, each brainstormed → status
- **C1 Atomicity without Monero scripts** → solved by **adaptor signatures on
  Obscura**. ✅ IMPLEMENTED + TESTED this block (`pkg/commit/adaptor.go`,
  `tests/critical/swap/`): a pre-signature verifies without the secret, adapts to
  a valid signature with it, and the published signature **reveals the secret**.
- **C2 Cross-curve DLEQ** → **eliminated** (both ed25519). ✅ Net simplification.
- **C3 Swap-output type** (success/refund/punish spend paths + timelocks) →
  MEDIUM, designed; Obscura already has Schnorr, adaptor sigs, and output
  timelocks (`LockUntil`). Remaining: a consensus swap-output with the three
  spend paths. (Next implementation block.)
- **C4 Monero-side integration** → external CLIENT software (talks to
  `monero-wallet-rpc`/`monerod` to lock/watch/spend XMR). Does **not** change
  Obscura consensus; it's a swap daemon shipped alongside the wallet.
- **C5 Monero reorg/finality** → N-confirmation gate + refund timelock ≥ 2× reorg
  window.
- **C6 Privacy of the swap leg** → the swap output is identifiable as a swap;
  participants' main wallets stay private. Documented tradeoff; can be mixed via
  an ordinary private send afterward.

## Liquidity / "a DEX like Monero has" — honestly
A trustless **cross-chain AMM is impossible** without a bridge or wrapped assets
(no single chain holds both coins). What *is* achievable, and is what the Monero
ecosystem actually uses (UnstoppableSwap, Haveno), is **peer-to-peer order
matching + atomic swaps**:
- **Maker–taker order book / RFQ intent gossip** over Obscura's existing P2P:
  add swap-intent messages (offer: amount, price, direction, maker pubkey, bond);
  takers match and run the atomic swap directly with the maker. No intermediary,
  no federation.
- **Anti-grief**: makers post a small time-locked **bond**; abandoning a swap
  forfeits it. **Anti-spam**: rate-limit intent gossip by PoW/stake-weight.
- **Cold-start** is the real limitation (same as any new market): liquidity
  follows users; market makers arbitrage Obscura↔XMR↔fiat. This is a
  go-to-market problem, not a protocol one.

## What is built (Blocks 12–13)
- **Adaptor signatures** (cornerstone) over edwards25519 (`pkg/commit/adaptor.go`:
  `PreSign/PreVerify/Adapt/VerifyFull/Extract`) — publishing the signature reveals
  the secret. Tested.
- **2-of-2 adaptor co-signing + swap output** (`pkg/swap`): `CoSignClaim` builds
  the aggregate (maker+taker) adaptor pre-signature over `K = A + B`; `SwapOutput`
  enforces the **claim** path (valid sig under `K`, before the unlock height) and
  the **refund** path (valid sig under the funder key, after it). Verification
  logic is final and consensus-ready.
- **End-to-end test** (`tests/critical/swap/`): a full swap is simulated — Alice
  claims the OBX, Bob extracts `s_a` from the on-chain claim and reconstructs the
  Monero spend key `s_a + s_b` (atomic), plus the refund/timelock paths.

- **On-chain swap output (consensus).** `tx.SwapOut`/`tx.SwapIn` + chain
  tracking: a transaction can **lock OBX** into a swap contract (cleartext
  amount, claim key `K`, refund key, unlock height), and spend it via the
  **claim** path (sig under `K`, before unlock — reveals the secret) or the
  **refund** path (sig under the refund key, at/after unlock). Balance is checked
  by a **generalized conservation** proof (`commit.VerifyConservationGen`) that
  accounts for the public swap-value legs; the swap set is reorg-safe (snapshot).
  Wallet helpers `FundSwap`/`BuildSwapSpend`. End-to-end test
  (`tests/critical/swapchain/`): fund → claim (Bob extracts the secret on-chain
  and reconstructs the XMR key `s_a+s_b`) → refund is timelock-gated.

- **Decentralized order book / liquidity** (`pkg/swapbook` + p2p gossip). Makers
  broadcast **signed, PoW-stamped, expiring** swap offers (`Offer`: maker pubkey,
  give/get asset+amount, expiry, anti-spam PoW nonce, signature) over the
  existing gossip (`msgSwapOffer`/`msgGetOffers`). Each node keeps a deduped,
  self-pruning order **`Book`** (cap + TTL) and serves it to new peers. Takers
  pick the best offer for a pair and run the atomic swap with that maker. No AMM
  (impossible cross-chain trustlessly); this is maker-taker/RFQ matching.
  `Node.PostOffer`/`Node.Offers`; tests in `tests/critical/orderbook/` incl.
  cross-node offer propagation.

- **Swap daemon + Monero adapter** (`pkg/swapd`). `MoneroClient` is the minimal
  Monero capability (Lock to `S_a+S_b` / Confirmed / Sweep with the spend secret
  / Balance); a production build plugs in a `monero-wallet-rpc` adapter, and the
  in-memory `MockMonero` enforces the real rule (sweep requires the scalar whose
  point equals the lock key). `XMRSpendPub(S_a, S_b)` derives the Monero lock key
  (same curve → trivial sum, no DLEQ). **End-to-end test** (`tests/critical/
  swapd/`): a full swap settles BOTH legs atomically — Alice locks XMR, Bob funds
  OBX, Alice claims OBX, Bob extracts the secret from the on-chain claim and
  sweeps the XMR — plus the safety property that XMR needs *both* shares.

- **Order-book RPC + wallet CLI** (Blocks 17, 19). The node serves the book over
  RPC (`GET /offers`, `POST /offer`; `rpc.Client.Offers`/`PostOffer`), and the CLI
  wallet drives it: `obscura-wallet offers` lists the live book, and
  `obscura-wallet offer --give-asset OBX --give-amount N --get-asset XMR
  --get-amount M` publishes a signed, PoW-stamped, 1-hour offer. The maker signing
  key is **derived deterministically from the wallet seed** (`HashToScalar
  ("Obscura/offer-key", seed)`), so the same wallet always signs as the same
  maker. Tests in `tests/critical/orderbook/` (`TestOrderBookRPC`,
  `TestSeedDerivedOfferKey`).

**Status: the entire XMR↔Obscura swap is implemented and tested in code** (real
Obscura chain + mock Monero). The only production gap is a real
`monero-wallet-rpc` adapter implementing `MoneroClient` (it can't be unit-tested
here without a live Monero stack) and a GUI to drive it.
