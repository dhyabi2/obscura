# Obscura (OBX) — Tokenomics & Incentive Design

*Live mainnet. Economic parameters describe the active emission schedule.*

---

## 1. Units and Supply

- **Ticker:** OBX
- **Atomic units:** `1 OBX = 10¹² atomic units` (12 decimals). All consensus accounting is in atomic units; OBX is a display convenience.
- **Soft cap:** approximately **18.4M OBX** emitted before the tail begins.
- **Tail emission:** a perpetual floor of approximately **0.6 OBX per block** that never terminates.

The soft cap with a perpetual tail follows the Monero philosophy: a predictable, front-loaded distribution followed by a small constant emission that funds network security **forever**, avoiding the long-run "fee-only security" cliff that purely capped coins face.

---

## 2. Proof of Work and Block Cadence

- **Target block time:** 120 seconds.
- **Algorithm:** ASIC-resistant Proof of Work. Mainnet ships canonical, KAT-verified **RandomX** by default for its CPU-friendly, ASIC-hostile profile, which keeps mining accessible to ordinary hardware and broadens the miner base.
- **Difficulty retargeting:** **LWMA** (Linearly Weighted Moving Average), chosen for fast, stable response to hashrate swings and resistance to timestamp manipulation and hashrate-hopping.

A 120s cadence balances confirmation latency against orphan rate given realistic global gossip propagation.

---

## 3. Emission Curve

Block reward follows a **smooth, decreasing curve** driven by the remaining supply:

```
reward ≈ supplyCapRemaining >> 19      (until the tail floor is reached)
reward  = tail floor (~0.6 OBX)        (thereafter, forever)
```

A shift-based decay (rather than discrete halvings) gives a *smooth* reduction with no abrupt reward cliffs — avoiding the hashrate shocks and fee spikes that periodic halvings can induce. Once the formula's output drops below the tail floor, emission is pinned at the floor permanently.

---

## 4. The Three Incentive Pillars

Obscura's emission is split to drive three behaviours that reinforce one another: **mine**, **hold**, and **share**.

### Pillar 1 — Mining Reward (base)

The majority of each block's emission is the standard PoW reward paid to the miner who finds the block. This secures the chain and is the anchor of the whole system: holding and referral bonuses are *funded from emission*, so they can never exist without the base mining incentive that issues that emission.

### Pillar 2 — Holding Bonus

A fixed fraction of each block's emission is diverted into an on-chain **incentive pool** tracked by the chain state machine. Holders earn from this pool by **time-locking** outputs.

**Mechanism.**
- A holder locks an output for a chosen duration.
- The bonus earned is proportional to **lock duration × amount**.
- The bonus becomes **claimable when the lock expires**.
- Crucially, the claim is **provable without revealing the amount**: a range proof attests the locked amount is within bounds, and a timelock proof attests the duration, so the holder draws a proportional share from the pool while preserving confidentiality.

**Why this rewards holding without classic inflation.** This is *not* a perpetual "staking interest" that mints unbounded new supply per holder. The holding bonus is paid from a **bounded pool** that is itself a slice of normal block emission. It redistributes a portion of emission toward participants who demonstrably remove supply from circulation for a period, rather than printing new tokens proportional to balances. The total emission schedule (§3) is unchanged; only its *destination* shifts. This rewards commitment (reduced sell pressure, locked liquidity) without the runaway dilution of yield-bearing models.

**Sybil / abuse considerations.** Because the bonus scales with `duration × amount`, splitting a balance across many outputs yields no advantage over locking it as one — the reward is linear, so Sybil-splitting is neutral. The genuine risks are (a) **opportunity-cost gaming**, where large holders lock idle coins purely to skim the pool, concentrating bonuses with the wealthy; and (b) **pool exhaustion** if lock participation spikes. Mitigations: the pool is a *capped* fraction of emission (claims are pro-rata against available pool funds, never an open-ended promise), and reward curves can be made **concave in duration** to cap the marginal benefit of extreme lock times. The design explicitly avoids guaranteeing a fixed APR, which would be an unfunded liability.

### Pillar 3 — Referral / Sharing Viral Loop

Coinbase transactions may carry an optional **referral tag** naming a referrer address. When a referred miner produces a block, a **small, capped, decaying** bonus is minted to the referrer, **funded from emission**.

**The viral loop.** Every participant has a direct incentive to recruit new miners, because each recruit's blocks earn the recruiter a bonus. Recruits, in turn, recruit their own, and a recruited miner's work *boosts* the recruiter's earnings — so the network's growth is self-propelling: more miners → more security and distribution → more referral earnings → more recruiting.

**Anti-abuse design.**
- **Per-referrer caps:** a referrer's total referral bonus is bounded, so no single address can farm unbounded rewards.
- **Decay over referrals:** the bonus *decays* as a referrer accumulates more referrals (and/or over time), so returns diminish and the scheme cannot dominate emission.
- **Proof of contributed work:** the bonus is paid only when a referred miner *actually finds blocks* — referrals that never mine pay nothing, so merely registering fake referrals is worthless.

**Honest discussion of Sybil resistance.** Referral schemes are intrinsically Sybil-vulnerable: a single actor can self-refer by spinning up many identities and pointing them at their own address. Obscura **cannot fully prevent** this. What it does is make it **uneconomic**: because every referred identity must contribute *real proof of work* to pay out, self-referral costs the attacker the same hashrate they would have mined anyway — the referral bonus is a *thin* slice on top, capped per referrer and decaying. The attacker gains the small bonus but spends real energy and competes with honest miners for the same blocks. Per-referrer caps bound the worst case; decay erodes the long-run advantage. The honest conclusion is that referral is a **growth accelerant with bounded downside**, not a Sybil-proof primitive. It is deliberately sized so that even fully abused it cannot meaningfully distort the emission schedule or security budget.

---

## 5. Illustrative Emission Split

The exact split is a governance/parameter choice; an illustrative allocation of each block's emission:

| Destination | Illustrative share | Purpose |
|---|---|---|
| Miner (PoW reward) | ~80% | Chain security |
| Holding incentive pool | ~12% | Reward locked, committed supply |
| Referral bonus budget | ~5% | Viral adoption (capped, decaying) |
| Tail floor (post-cap) | fixed ~0.6 OBX | Perpetual security funding |

Holding and referral budgets are **subsets of normal emission**, never additional inflation beyond the curve in §3.

---

## 6. Network Effects & Adoption Strategy

Obscura's privacy guarantee *itself* exhibits a network effect found in no decoy-based coin: the anonymity set **is** the UTXO set, so **every new user makes every existing user more private**. Privacy improves monotonically with adoption — the opposite of fee-market congestion costs.

The incentive pillars are designed to bootstrap that flywheel:

1. **ASIC-resistant PoW** keeps mining open to ordinary CPUs, widening initial distribution and decentralisation.
2. **Holding bonuses** reduce circulating sell pressure and reward long-term alignment during the volatile early phase.
3. **Referral loop** turns existing miners into a distributed growth engine, while caps and decay keep the cost bounded and prevent the scheme from cannibalising security.

Together with a smooth emission curve and a perpetual tail, the goal is durable security funding, broad distribution, and a privacy property that strengthens as the community grows.

---

## 7. Honesty Note

These parameters are the live mainnet emission schedule. The economic mechanisms, especially the holding pool's pro-rata accounting and the referral cap/decay schedule, have not had external simulation or adversarial economic review, so understand them before relying on the long-run figures. The numbers above describe the active schedule.
