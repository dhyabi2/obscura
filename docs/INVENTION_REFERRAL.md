# Invention Log — Block 5: Sybil-Resistant Referral / Viral Loop

Produced with the invention methodology + Methodology-Tree brainstorming engine.

## 1. Challenge
A referral reward to drive viral growth, but the naive design (mint fresh coins
to a referrer tag in the coinbase) is **sybil-exploitable**: a miner self-refers
with throwaway tags to mint free coins. It was therefore disabled
(`ReferralMaxBps = 0`). No identity/KYC is available (privacy coin).

## 2. Brainstormed options (engine) + the honest finding
The engine's five ideas — fee-threshold rebate, PoW-proof gate, proof-of-burn
bond, hashrate-capped bonus, decay-to-miners — **all** converge on one truth:
without identity, any *minted* referral reward is gameable, so a sound referral
must be **zero-sum (funded from existing value / fees), never minted**. At that
point a "referral mint" is the wrong primitive entirely:

> **A sound, no-identity referral reward = a VOLUNTARY private payment (a tip /
> fee-share) from the referred user to the referrer.** It needs no special
> consensus primitive, it is funded by the tipper (so self-referral is net-zero,
> minus cost), and it is private (a normal stealth output). Any protocol that
> *mints* referral rewards is either inflationary or sybil-farmable.

## 3. Decision & implementation
1. **Supply invariant (defense-in-depth):** referrals can NEVER mint. Per-block
   new coins = `BlockReward` only; coinbase `minted = base − pool + fees`
   regardless of any referrer tag (`pkg/chain/apply.go`, `validate.go`). This
   makes referral-via-inflation impossible at the consensus layer.
2. **Referral = voluntary tip:** a referral reward is just a normal (private)
   payment to the referrer, built with the existing wallet `CreateTransaction`
   (target the referrer). Sound, private, sybil-neutral; no protocol mint.
3. The legacy `ReferrerTag` coinbase field is retained as a purely informational
   tag with **zero economic effect**.

Tested: `tests/critical/referral/` — a referrer tag adds nothing to the coinbase
mint (incl. self-referral), and total emission equals exactly Σ BlockReward.

## 4. Honest implication for "virality"
This is the correct, non-scammy design, and it bounds how much the protocol can
*buy* virality: it cannot hand out free money for referrals (that's always
sybil-farmable or inflationary). Real viral growth must come from the product
(privacy, UX, network effects) and from voluntary fee-sharing — not from minting.
