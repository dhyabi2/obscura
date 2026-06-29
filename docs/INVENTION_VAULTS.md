# Confidential Staking Vaults — built-in private yield

The one core DeFi primitive for Obscura: **lock OBX for a term, earn protocol
yield** — built into consensus, single-asset, no oracle, no external app. It is
the only DeFi primitive that needs nothing but the native coin, and it does three
jobs at once:

1. **Engagement** — a yield that grows every block is a daily reason to come back
   (and, unlike a price chart, it is always green).
2. **Monetary health (the anti-"dying" lever)** — locked coins are a **supply
   sink**: less circulating supply + a concrete reason to hold ⇒ less sell
   pressure. This is the single biggest lever for keeping the coin alive.
3. **Privacy** — yield accrues to *stealth* owners; who staked and their other
   holdings stay hidden. (v1 reveals the staked *amount*, like atomic swaps do;
   v2 hides it — see Phasing.)

## Why yield is "free" here: it is NOT new inflation
Obscura **already** routes `IncentivePoolBps` = 5% of every block reward into an
`incentivePool` (see `pkg/chain/incentives.go`). Those coins are already counted
in `emitted` and already bounded by `MoneySupplyCap` — and **today they have no
payout path; the pool just grows forever.** Vaults are simply the *first
distribution mechanism* for that already-emitted, already-capped pool. So:

- **No new inflation.** Yield is paid out of the existing `incentivePool`.
- **No supply-cap risk.** `emitted` is untouched at claim — the coins were minted
  into the pool blocks ago; a claim only moves them pool → circulation.
- This finally gives the long-dormant holding-incentive pool a purpose, and ties
  it to the holding behaviour it was always meant to reward
  (see [[docs/INVENTION_REFERRAL.md]] for the supply-invariant philosophy).

## Mechanism (v1)

A vault reuses the **audited atomic-swap public-value machinery** (`GenResidual` /
`VerifyConservationGen`) — a deposit is a public-value leg leaving the
confidential pool, a claim is a public-value leg re-entering it. No new
cryptography; no change to the conservation proof.

### Deposit (`VaultOut`)
A transaction locks a public `Amount` of OBX into a vault for a chosen `Term`:

```
VaultOut { VaultKey [32]B unique id, Amount uint64, Term uint64, OwnerKey [32]B Schnorr pubkey }
```

- The locked `Amount` is a **publicOut** leg (OBX leaves circulation into the
  vault), exactly like a `SwapOut`.
- `Term` must be one of `config.VaultTerms`; the apply step records the deposit
  height, so maturity = depositHeight + Term.
- The depositor pays the normal miner fee from confidential inputs + change.

### Claim (`VaultIn`)
After maturity, the owner claims principal + yield:

```
VaultIn { VaultKey [32]B, Sig 64B (Schnorr over CoreHash under OwnerKey) }
```

- `yield = Amount · VaultRateBps(Term) / 10_000`, deterministic from the stored
  vault — no running state, so per-tx conservation is self-contained.
- The claim is a **publicIn** leg of `Amount + yield`; the proceeds
  (`Amount + yield − fee`) land in a fresh confidential stealth output, so the
  funds are unlinkable *going forward*.
- Authorized by a Schnorr signature under `OwnerKey` (mirrors a swap refund sig).

### Where the yield comes from (soundness)
- The `Amount` portion of the claim is backed by the coins the deposit locked.
- The `yield` portion is backed by **decrementing `incentivePool`**.
- Net over a deposit+claim pair: `Amount` leaves, `Amount + yield` returns ⇒
  **+yield** into circulation, sourced 1:1 from `incentivePool` (−yield). Supply
  is conserved; `MoneySupplyCap` is never exceeded.
- **Affordability rule (block-level):** the sum of all claim yields in a block
  must be ≤ `incentivePool` at the block's start. If a claim's yield momentarily
  exceeds the pool it simply waits for a later block — and because the pool grows
  every block (5% of the reward, plus tail emission forever), any fixed yield
  becomes affordable eventually, so funds are never permanently stuck. The common
  case (yield ≪ pool) always succeeds immediately.

### Terms & rates (illustrative; `var`, tunable)
Block time ≈ 120 s ⇒ ≈ 720 blocks/day.

| Term (blocks) | ≈ duration | Rate |
|---|---|---|
| 21 600  | 30 days  | 1%  |
| 64 800  | 90 days  | 4%  |
| 262 800 | 365 days | 20% |

Longer locks pay more (term curve) to reward stronger commitment / deeper sink.

## Consensus integration points
- `pkg/tx`: `VaultOut`/`VaultIn` + serialization + CoreHash (Sig excluded, like
  `SwapIn`).
- `pkg/chain` state: `vaults map[hex(VaultKey)]*VaultEntry` (Amount, Term,
  OwnerKey, DepositHeight). Rolled back on reorg (snapshot/restore/reset) and
  rebuilt on replay — same discipline as `swaps`/PQ state.
- `validate`: deposit (term valid, amount bounds, key dedup, publicOut += Amount);
  claim (vault exists, matured, sig valid, dedup, publicIn += Amount + yield).
  Block-level: Σ claim-yields ≤ incentivePool.
- `apply`: register vault (record DepositHeight); on claim delete vault and
  `incentivePool -= yield`. Pool contribution for the block is added as today.
- `mempool`: vault-claim keys reserved as conflict keys (`vaultin:`), like swaps.

## Security properties (v1)
- **No inflation / supply-cap-safe** (yield from the pre-emitted, capped pool).
- **No double-claim** (vault deleted at apply; block-wide `vaultin:` dedup).
- **No premature claim** (maturity check).
- **Authorized** (Schnorr under OwnerKey, bound to CoreHash).
- **Reorg-safe** (vaults + incentivePool snapshotted/restored).
- **Privacy = parity with normal OBX**: stealth ownership + confidential
  surrounding txs. The staked *amount* is public (as swap amounts are); the
  deposit↔claim link is visible (as a transparent spend's is), but proceeds are
  fresh confidential outputs.

## Phasing
- **v1 (this pass):** public-amount, swap-style legs, emission-neutral yield from
  `incentivePool`. Sound, tested, gated by the version path; default suite green.
- **v2 (research):** *confidential* staked amounts + *unlinkable* claims. Make a
  vault note an **anonymity pool** (denominated notes) and claim via the existing
  one-out-of-many / accumulator-membership machinery (a vault claim = an anonymous
  spend over the vault-note set + a nullifier), with yield carried as a
  homomorphic commitment (`yield = rate · C_deposit`) and bounded by a ZK range
  proof. This converges with the PQ zk-STARK membership roadmap
  ([[docs/POST_QUANTUM_ROADMAP.md]]) and the pruning accumulator-membership work
  ([[docs/PRUNING_DESIGN.md]]) — same proving machinery.

## Engagement hooks (free, on the existing explorer)
- "Total Value Locked" + "coins earning yield" as headline stats (a supply-sink
  gauge the community watches grow).
- A vault appearing/maturing is a visible event in the live explorer stream.
- Personal "my yield so far" view in the wallet — the daily green number.
