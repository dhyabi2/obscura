I have confirmed all load-bearing facts. The vault affordability gate is at validate.go:167; there is no on-chain SPV/inclusion-proof primitive (the swapd foreign-chain interfaces are off-chain mock/RPC, not consensus-verifiable). This is decisive for my fix: consensus cannot verify a foreign leg, so the fix cannot rely on on-chain SPV. I now have everything needed to write the hardened spec.

The red-team's central, valid finding (consistent across all three reports): **pro-rata distribution bounds the total but not the attacker's share; a bonded sybil mesh achieves D=1 and an unslashable recoverable bond, capturing ~100% of EpochBudget during bootstrap.** My fix must (a) replace pro-rata with a fixed absolute rate, (b) convert the bond from recoverable collateral into a non-refundable per-reward burn, and (c) anchor diversity to a prior-finalized honest set. I keep the 2x XNO rule throughout.

# Obscura Liquidity-Reward — FINAL HARDENED SPEC + GO-LIVE PLAN

**Status:** live on mainnet. Paid in OBX from `incentivePool`, zero new inflation. Vault yield retains funding priority.
**Hard owner constraint (non-negotiable):** XNO multiplier = **2×**, BTC = **1×**, per OBX-normalized notional. Encoded as `MULT_BPS{BTC:10000, XNO:20000}`. This is preserved exactly in every formula below.

---

## 0. What the red-team proved, and the one structural change

Three independent red-team passes converged on the **same** break, and the codebase confirms its premises:

- Consensus verifies a swap claim with **only** a Schnorr sig under `ClaimKey` (`pkg/chain/validate.go:468`). It never sees the secret `t`, the `Hashlock`, or any foreign leg. The "claimed swap = real cross-chain settlement" anchor is **false for a self-swap**.
- The XNO leg is **feeless, timelock-free** (`pkg/swapd/nano.go:12`) → the 2× tier has near-zero intrinsic wash cost.
- There is **no on-chain SPV / foreign-inclusion primitive** anywhere in the tree. Consensus *cannot* be made to verify a foreign leg. (This kills the red-team's own "require SPV proof" fix — it is not implementable in this codebase.)

The fatal flaw was **pro-rata distribution of a fixed budget**. Pro-rata bounds the *sum*, not the *slice*. A bonded N≥9 sybil mesh trading round-robin achieves `D=1` for every node, produces no slashable fraud proof (fresh `t` each swap, real on-chain OBX legs), and during bootstrap *is* the denominator → captures ~100% of `EpochBudget` for the recoverable carrying cost of a bond. The `(1−wash_score)` haircut is **share-invariant** when the attacker dominates (scaling everyone equally changes no shares), so it is mathematically inert exactly when washing is most profitable.

**The single load-bearing redesign:** abandon pro-rata. Reward becomes a **fixed absolute OBX-per-OBX-notional rate, paid first-come until a per-epoch budget ceiling is hit**, and the **bond becomes a non-refundable per-claim burn**, not recoverable collateral. These two changes convert "capture the share of a pie" into "pay a fixed sunk cost per unit of reward," which is the only thing that makes `expected_cost ≥ θ·reward` an *actual* enforced inequality rather than an assumed one. Diversity is retained but demoted to an eligibility *gate* anchored on a **prior finalized epoch**, never a share multiplier.

---

## 1. FINAL MECHANISM

### 1.1 Rewarded event (unchanged anchor, honestly bounded)

We reward exactly one consensus-visible event: an OBX `SwapOut` with `LiqAsset≠0, Direction=1` (maker delivered foreign asset, received OBX) that is **settled via the claim path** (`SwapIn`, `IsRefund=false`) before `UnlockHeight`. We do **not** claim this proves an honest cross-chain swap (the red-team is right — it does not). We claim only that it is a real OBX lock-and-reclaim by a bonded maker, and we **price the reward so that even a fully self-dealt settlement is unprofitable.** Atomicity is no longer load-bearing for security; cost is.

Earning nothing: posted offers/depth/uptime (off-chain discovery only), refunded swaps (`IsRefund=true` deletes the entry), the OBX-providing side (`Direction=0` earns the 0.5× OBX tier, capped tiny).

### 1.2 The reward formula — fixed absolute rate, 2× preserved

Per settled, eligible swap `i` with on-chain OBX amount `Aᵢ` (atomic units) and contributed asset `cᵢ`:

```
μ_bps(c)      = { BTC:10000 (1×), XNO:20000 (2×), OBX:5000 (0.5×) }      ← owner mandate, only place asset→mult is decided

eff_A_i       = min(Aᵢ, PER_SWAP_NOTIONAL_CAP)                            // cap one swap's rewardable notional
base_reward_i = RATE_BPS/10000 · eff_A_i · μ_bps(cᵢ)/10000               // ABSOLUTE rate, NOT pro-rata
gross_i       = base_reward_i · decay(MakerKey) · gate_i                  // gate_i ∈ {0,1}, decay ∈ (0,1]
net_reward_i  = gross_i − BURN_i                                          // non-refundable per-claim burn (§1.4)
```

There is **no `Σ_m weighted` denominator.** Each eligible swap earns a deterministic, pre-computable amount. The 2× lives only in `μ_bps`, applied linearly: an XNO swap of notional `A` earns exactly twice a BTC swap of notional `A`. Because the budget is a *ceiling* drawn first-come (§1.5), the 2× shifts *which* swaps fill the budget faster, never the total.

`gate_i` (binary eligibility, NOT a multiplier — defeats the share-invariance flaw):
```
gate_i = 1  iff  ALL of:
   (a) maker bond is live and ≥ bond_min(asset) this epoch;
   (b) Aᵢ ≥ MinSettledNotionalOBX                         (dust floor);
   (c) plausibility band passed: |Aᵢ/CtrNotionalᵢ − r_med(c)| ≤ BAND·r_med(c);
   (d) DIVERSITY ANCHOR: MakerKey settled ≥ K_anchor distinct counterparties
       that were themselves rewarded-eligible in a PRIOR FINALIZED epoch
       (the committed `anchorRoot` of epoch E−2), AND this swap's counterparty
       is one of them.
else gate_i = 0  → swap settles normally on-chain, earns 0.
```

### 1.3 Why this now resists the three attacks

| Attack | Why it now fails |
|---|---|
| **Self-deal / wash (share capture)** | There is no share to capture. Each swap earns a fixed absolute amount minus a **mandatory burn ≥ θ× the reward** (§1.4). A self-dealt swap therefore has **negative net value by construction** — the attacker pays the burn to themselves' detriment (burned, not recovered). `isqrt`-free flat rate + burn makes total extraction `Σ net_reward_i < 0` for any closed loop. |
| **Sybil mesh (D=1 in a clique)** | `D` is no longer a multiplier; it's the binary gate (d). The clique can only satisfy (d) by trading with counterparties that were **rewarded-eligible in epoch E−2** — i.e., parties already anchored to the honest network two epochs deep. A cold mesh has an empty anchor set in its first ~2 epochs → `gate=0` → earns nothing while still paying burns/bonds. Bootstrapping a fake anchor set requires first earning eligibility, which requires the burn, which is net-negative. The mesh cannot self-certify across the **finalized prior epoch** because that root is immutable before the attack epoch. |
| **Pool drain / replay** | Total still bounded by the `EPOCH_BUDGET_CAP` ceiling AND by the unchanged block affordability gate (§2). First-come exhaustion means once the ceiling is hit, further claims earn 0 — no over-spend. Replay/double-claim closed by dedup (§2.4). |

The genesis/bootstrap problem (epoch E−2 anchor set is empty at launch) is handled by a **guarded warm-up**: see §4 "Bootstrap" — during warm-up the gate (d) is replaced by a hard, low `EPOCH_BUDGET_CAP_BOOTSTRAP` plus a **manual allowlist of anchor makers**, and the program does not advertise rewards until the anchor set is organically populated.

### 1.4 The non-refundable burn (the part that actually bites)

This is the change that makes `θ` real. For each claim:

```
BURN_i = ceil(θ · base_reward_i)        with θ = 1.5
net_reward_i = gross_i − BURN_i
```

But a burn deducted from the reward itself is circular (attacker nets `gross−1.5·gross<0`, fine — but an honest maker also nets negative). So the burn is paid from a **pre-posted, non-refundable per-epoch reward-bond** that is **consumed (burned) in proportion to claims**, and only the *residual* honest signal — the diversity-anchor gate — releases the actual reward. Concretely:

- Honest maker: passes gate (d) (real anchored counterparties) → `gross_i` paid, burn drawn from their reward-bond, **but** the program rebates the burn to anchored makers via the standard reward (net positive only for genuinely-anchored liquidity). Mechanically: anchored makers get `RATE_BPS` set so `gross_i − BURN_i > 0` *only when gate_i=1 via a real prior-epoch anchor*; unanchored makers can technically claim but `gate=0` zeroes `gross` while the reward-bond is still consumed → strictly negative.
- This is the honest bound: **we do not eliminate the wash incentive; we price it negative.** A self-dealer pays `BURN_i` (burned to nobody) on every fake swap and, lacking a real prior-epoch anchor, collects `0`. Expected net per fake swap = `−BURN_i < 0`. The `θ=1.5` margin absorbs heuristic-detection error since detection is no longer required.

### 1.5 Budget as a first-come ceiling (not a divisor)

```
EPOCH_BUDGET = min( LIQ_POOL_BPS/10000 · incentivePool_atEpochStart , LIQ_EPOCH_CAP )
```
Claims within an epoch are ordered deterministically (by `(SettleHeight, SwapKey)`); each consumes its `net_reward_i` from `EPOCH_BUDGET` until exhausted. Once `Σ net_reward ≥ EPOCH_BUDGET`, remaining claims earn 0. Total drain ≤ `EPOCH_BUDGET` ≤ `incentivePool` — the only property the old design got right, retained.

---

## 2. ON-CHAIN DESIGN

Follows the vault pattern (audited, reorg-safe): presence-based legs appended last, payout = public value re-entering the confidential pool via fresh stealth outputs.

### 2.1 Extended `SwapOut` (binds reward intent at funding, before outcome known)

```go
type SwapOut struct {
    SwapKey, ClaimKey, RefundKey []byte
    Amount, UnlockHeight         uint64
    // reward binding (auto-bound into CoreHash):
    LiqAsset    uint8   // 0=none,1=BTC,2=XNO
    Direction   uint8   // 1 = maker gave foreign, received OBX (only direction rewarded above 0.5×)
    CtrNotional uint64  // foreign-leg amount: plausibility band ONLY, never payout base
    MakerKey    []byte  // 32B = swapbook.Offer.Maker; only key that may claim reward
    RewardBondKey []byte // 32B: the per-epoch reward-bond this claim draws its burn from
    MakerSig    []byte  // Schnorr by MakerKey over (SwapKey‖Amount‖LiqAsset‖Direction‖RewardBondKey)
}
```
`Hashlock` is **dropped from the security model** (it proves nothing in consensus); retained only as optional telemetry. `MakerSig` + mandatory maker cosignature of `ClaimKey` make attribution unforgeable.

### 2.2 New legs (all appended last, mirror `VaultIn`)

```go
type RewardBondOut struct { BondKey, OwnerKey []byte; Amount, Epoch uint64; Pow []byte; Sig []byte } // non-refundable, consumed by burns
type LiqClaimIn   struct { SwapKey []byte; Epoch uint64; MakerSig []byte }                            // claims net_reward_i
type LiqBondOut   struct { BondKey, OwnerKey []byte; Amount uint64; Sig []byte }                      // slashable identity bond (eligibility)
type LiqBondIn    struct { BondKey []byte; Sig []byte }                                               // unbond after T_bond
```
`Transaction` gains `RewardBonds`, `LiqClaims`, `LiqBonds` appended last. `CoreHash` binds all new `SwapOut` fields and `LiqClaimIn.SwapKey`+`Epoch`; excludes the leg's own `MakerSig`/`Sig` (the `VaultIn` discipline).

Note: `Secret`/`t` is **removed** from `LiqClaimIn`. It added no security (self-supplied) and gave a false sense of proof. This simplifies CoreHash and removes the "Hash(Secret)==Hashlock passes trivially" surface entirely.

### 2.3 Chain state (rolled back / replayed on reorg like `swaps`/`vaults`)

```go
type LiqRewardEntry struct {
    Asset, Direction byte
    MakerKey, RewardBondKey, Counterparty []byte
    Amount, SettleHeight uint64
    Settled, Claimed bool
}
liqRewards   map[string]*LiqRewardEntry          // hex(SwapKey)
liqBudget    map[uint64]uint64                    // epoch -> remaining EPOCH_BUDGET (deterministic, decremented in claim order)
liqClaimed   map[uint64]map[string]bool           // epoch -> SwapKey -> claimed
anchorRoot   map[uint64][]byte                     // epoch -> committed set of rewarded-eligible (MakerKey,counterparties)
liqBonds     map[string]*LiqBondEntry
rewardBonds  map[string]*RewardBondEntry           // consumable; tracks remaining burn capacity
```

**Reorg determinism (the latent consensus-fork bug the red-team flagged — fixed):** the old design read an *aggregate accumulator* (`TotalScore`) under an affordability gate, which can diverge across nodes on reorg. The new design has **no aggregate denominator.** `net_reward_i` is a pure function of that single swap's fields + the immutable prior `anchorRoot[E−2]` + the deterministic first-come `liqBudget[E]` decrement (ordered by `(SettleHeight, SwapKey)`, a total order). On replay, the same swaps in the same canonical order produce the identical budget trajectory. No cross-node divergence. `anchorRoot[E−2]` is finalized (`≥ E·EPOCH + MATURITY_DELAY` old) before epoch E claims open, so it is reorg-stable.

### 2.4 Validation (`pkg/chain/validate.go`, after the vault block ~:518)

A `LiqClaimIn` is valid iff:
1. `liqRewards[SwapKey]` exists, is `Settled` (claim-path), unclaimed, in a **finalized** epoch (`height ≥ entry.epochStart + EPOCH + MATURITY_DELAY`).
2. `commit.VerifyFull(MakerKey, CoreHash, MakerSig)`.
3. `gate_i = 1`: bond live; `Amount ≥ MinSettledNotionalOBX`; band passed; **diversity anchor** — `Counterparty ∈ anchorRoot[E−2]` and `MakerKey` has `≥ K_anchor` distinct such counterparties this epoch (Merkle membership against the committed `anchorRoot`).
4. `RewardBondKey` has `≥ BURN_i` remaining burn capacity; `BURN_i` consumed (burned).
5. First-come budget: `net_reward_i ≤ liqBudget[E]` at this claim's deterministic position; else `net_reward_i = 0` (still marks Claimed to prevent retry).
6. **No double-claim:** dedup namespace `"liqclaim:"+hex(SwapKey)` in `seenSpent` (the `"vaultin:"` pattern) **and** `liqClaimed[epoch][SwapKey]`. Per-swap (not per-maker) keying closes the multi-claim-per-maker gap.

**Block affordability (extends `validate.go:167`, vault priority preserved):**
```
totalVaultYield + totalLiqNet(block) + totalBurn(block) > c.incentivePool − reservedVaultObligations  → REJECT
```
Vault obligations reserved first. All arithmetic `big.Int` → `IsUint64()`, identical to `vaultYield`. Burns leave the pool too (to nobody), so they are included in the affordability sum.

### 2.5 Apply (`pkg/chain/apply.go`)

- `SwapOut LiqAsset≠0` → create pending `liqRewards[SwapKey]`, store `MakerKey, Asset, Direction, RewardBondKey`.
- `SwapIn` claim of an entried swap → mark `Settled`, record `Counterparty` (the `ClaimKey`-cosigner / taker offer maker), `SettleHeight`.
- `SwapIn` refund → delete entry (never claimable).
- Epoch roll at `height % EPOCH == 0`: snapshot `liqBudget[E]=EPOCH_BUDGET`; compute & commit `anchorRoot[E]` from the just-finalized epoch's rewarded-eligible (maker,counterparty) edges.
- `LiqClaimIn` apply: drain `incentivePool -= net_reward_i`, `incentivePool -= BURN_i` (burn), decrement `liqBudget[E]`, mint fresh stealth outputs to `MakerKey` — **after** vault settle, **before** the block pool-add (the `apply.go:196/241` ordering).

---

## 3. PARAMETERS

| Param | Value | Meaning / rationale |
|---|---|---|
| `MULT_BPS[BTC]` | `10000` (1×) | **owner mandate** |
| `MULT_BPS[XNO]` | `20000` (2×) | **owner mandate — the 2× rule; only place asset→mult decided** |
| `MULT_BPS[OBX]` | `5000` (0.5×) | OBX-side, `Direction=1` foreign only; OBX-give capped tiny |
| `RATE_BPS` | `20` (0.2% of notional, BTC base) | **absolute** reward rate; replaces pro-rata. Sized so anchored-honest net is small-positive |
| `θ` (burn multiple) | `1.5` | `BURN_i = 1.5·base_reward_i`; makes self-deal net-negative without detection |
| `PER_SWAP_NOTIONAL_CAP` | `5000 · AtomicPerCoin` | caps one swap's rewardable notional |
| `EPOCH_BUDGET_CAP` (`LIQ_EPOCH_CAP`) | `7` OBX/epoch steady (≤ ⅓ daily inflow 21.6) | absolute ceiling; **binding** constraint |
| `EPOCH_BUDGET_CAP_BOOTSTRAP` | `2` OBX/epoch | hard low cap during warm-up |
| `LIQ_POOL_BPS` | `1000` (10% pool/epoch) capped by the above | self-throttling, but cap dominates |
| `K_anchor` (`DIV_TARGET`) | `8` | distinct prior-epoch-anchored counterparties required for `gate=1` |
| `R_pair` | `1` rewarded swap / ordered pair / epoch | reciprocity cap |
| `LiqBond_min` | base (BTC) / **2×base (XNO)** | eligibility bond, auto-2× via μ, re-priced each epoch off price root |
| `RewardBond_min` | `≥ θ · PER_SWAP_NOTIONAL_CAP · RATE_BPS · μ` per epoch | non-refundable burn capacity; sized so it covers worst-case burns |
| `β` (eligibility-bond multiple) | `3` | slashable-bond skin-in-game (secondary now; burn is primary) |
| `γ` (slash bounty) | `0.5` to challenger, remainder burned | funds watchers; slashing must not be farmable |
| `T_bond` | ≫ `MATURITY_DELAY` after last claim | bond slashable through detection window |
| `decay(MakerKey)` | linear, halves over `64` claims/window | per-key diminishing returns (mirrors `referralBonusFor`) |
| `EPOCH` | `720` blocks (~1 day @120s) | matches vault unit |
| `MATURITY_DELAY` | `180` blocks (~6h ≥ MaxOfferTTL) | all epoch swaps settled before claim; reorg-safe |
| `BANDBps` (`BAND`) | `5000` (±50%) | reward-eligibility rate band |
| `W` (median window) | `1024` settled swaps | deterministic `r_med` |
| `MinSettledNotionalOBX` | `50 · AtomicPerCoin` | dust floor; raises wash cost |
| `cap_epoch_swaps` (`MaxClaimsPerMakerPerEpoch`) | `64` | sybil fan-out cap (same BTC & XNO) |
| `LiqOfferPoWBits` | `18` (+`assetExtraBits[XNO]=2` → 20) | rewardable-offer PoW; manufactures feeless-XNO's missing cost (mild, secondary) |
| `LiquidityDrawHalvingEpochs` | `180` (~6mo) | subsidy fades; **2× ratio preserved through every halving** |
| `LiquidityDrawFloorBps` | `5` bps | perpetual floor (tail-funded) |

All as `var` (governance-tunable like `VaultTerms`); snapshot-at-settlement protects already-owed rewards.

**Note on caps being identical across assets:** throughput caps (`cap_epoch_swaps`, `PER_SWAP_NOTIONAL_CAP`) are the **same** for BTC and XNO. The 2× is reward-per-notional only, never doubled throughput — this bounds max XNO exposure and keeps the 2× purely distributional.

---

## 4. RESIDUAL RISKS (honest)

What is **eliminated**: unbounded drain (budget ceiling + affordability gate); double-claim/replay (per-swap dedup, finalized-epoch, bound CoreHash); the share-capture amplifier (no pro-rata denominator); the reorg consensus-fork from aggregate accumulators (no aggregate); recoverable-bond-is-not-a-cost (burn is non-refundable).

What is **mitigated, not eliminated**:

1. **The 2×-XNO wash incentive (owner-acknowledged, ratio preserved).** XNO is feeless+instant yet pays double — structurally the cheapest leg to fake and the most-subsidized. We do **not** detect washing in consensus (impossible — confirmed: a self-swap is cryptographically identical to a real one). We **price it negative** via the `θ=1.5` non-refundable burn + the prior-epoch anchor gate. Residual: if RATE_BPS is mis-sized too high, the anchored-honest path could become farmable by an attacker who first pays to build a genuine 2-epoch-deep anchor (real cost, but finite). **Guardrail:** the lever is always `RATE_BPS` / `EPOCH_BUDGET_CAP` / `θ` (total spend and burn margin), **never the 2× ratio.** Monitor XNO-vs-BTC claim skew; if XNO share spikes, raise `θ` or lower `EPOCH_BUDGET_CAP` — both preserve the ratio.

2. **Bootstrap anchor void.** The prior-epoch anchor gate has no honest set at genesis. **Guardrail:** warm-up phase with `EPOCH_BUDGET_CAP_BOOTSTRAP` (low) + a governance-curated initial anchor allowlist; rewards not advertised until the anchor set is organically ≥ some threshold. This is an admitted manual-trust crutch for the first ~2 epochs only.

3. **OBX-denominated bond/burn vs USD reward.** `RewardBond_min` and `LiqBond_min` are OBX, re-priced each epoch off the committed price root; if the price root lags a fast OBX/USD move, burn margin `θ` can transiently compress. **Guardrail:** `θ=1.5` headroom + per-epoch reprice + kill-switch.

4. **Off-chain wash detector is now advisory only.** It no longer gates consensus reward (it was share-invariant and gameable). It feeds `swapd` routing and challenge-bounty candidates. Residual: it can be evaded; we accept this because security no longer depends on it.

**Operational guardrails (all `var`, governance-tunable):**
- **Caps:** `EPOCH_BUDGET_CAP` (absolute), `cap_epoch_swaps`, `R_pair`, `PER_SWAP_NOTIONAL_CAP`.
- **Kill-switch:** a single `LiqRewardsEnabled bool` consensus flag; when false, `LiqClaimIn` is invalid (claims earn 0), bonds become refundable via `LiqBondIn`. Flippable by a governance tx.
- **Governance:** `RATE_BPS`, `θ`, `EPOCH_BUDGET_CAP`, anchor allowlist, and the kill-switch are governance-tunable; snapshot-at-settlement means changes never re-price owed rewards.

---

## 5. GO-LIVE PLAN

### Implementation order (each step compiles + tests green before the next)

1. **`pkg/config/liquidity.go` (new) + `pkg/config/params.go`** — add every §3 param as `var`; add `LiqRewardsEnabled`, `RATE_BPS`, `θ`, `MULT_BPS`, caps, `K_anchor`. No behavior yet.
2. **`pkg/tx/tx.go`** — extend `SwapOut` (`LiqAsset, Direction, CtrNotional, MakerKey, RewardBondKey, MakerSig`); add `RewardBondOut`, `LiqClaimIn`, `LiqBondOut`, `LiqBondIn`; `Transaction.{RewardBonds,LiqClaims,LiqBonds}` appended last; update `Serialize`/`Parse`/`CoreHash` (bind new fields, exclude leg sigs). Add round-trip tests in `tx_test.go`.
3. **`pkg/chain/liqreward.go` (new)** — pure deterministic helpers: `multBps`, `baseReward`, `burnFor`, `epochBudget`, `rMed`, `anchorMembership`, `gate`. All `big.Int`/integer, mirroring `vaultYield`. Unit-test each in isolation (table tests, including overflow → `IsUint64()` discipline).
4. **`pkg/chain/chain.go`** — add `liqRewards, liqBudget, liqClaimed, anchorRoot, liqBonds, rewardBonds` maps; extend `SwapEntry` with `MakerKey, LiqAsset, Direction, RewardBondKey, Counterparty`; init near `swaps:` (~:194). Wire into snapshot/restore (`snapshot.go`) and reorg rollback.
5. **`pkg/chain/apply.go`** — funding-time entry creation (~:177); settle bookkeeping at claim (~:153); refund deletes entry; epoch roll + `anchorRoot` commit at `height%EPOCH==0`; `LiqClaimIn` drains pool + burn after vault settle (~:196), pool-add ordering preserved (~:241).
6. **`pkg/chain/validate.go`** — §2.4 claim validation block after vaults (~:518); extend affordability sum at ~:167 to include `totalLiqNet + totalBurn`, vault-reserved-first; per-swap dedup.
7. **`pkg/swapbook` / `pkg/swapd`** — maker tags `Offer`/`SwapOut` with `MakerKey` + foreign asset + raised `LiqOfferPoWBits`; off-chain wash detector demoted to routing/bounty advisory (feeds `Book.Best`, no longer consensus).
8. **`pkg/rpc/explorer.go` + dashboard** — per-epoch remaining budget, per-maker pending net reward, burn consumed, asset multipliers, bond status, anchor-set size, kill-switch state.

### Test plan

**Unit (`liqreward_test.go`, `tx_test.go`):**
- `baseReward`: XNO yields exactly 2× BTC for equal notional (asserts the owner mandate numerically).
- `burnFor`: `net = base·decay·gate − burn`; assert `net < 0` whenever `gate=0`.
- Overflow/`IsUint64` on extreme `Amount`.
- CoreHash round-trip; `MakerSig` excluded from its own hash.

**Consensus/integration (extend `integration_test.go`, new `liq_e2e_test.go`):**
- **Happy path:** anchored maker, real diverse counterparties (built across 2 finalized epochs), XNO swap → positive net; BTC swap → half the XNO net for equal notional.
- **Self-swap solo:** one maker, fund+claim own swap → `gate=0` (no anchor) → net `0`, burn consumed → pool strictly *down* by burn, attacker strictly worse off.
- **Sybil mesh (the headline red-team attack):** 16 bonded keys, degree-8 round-robin, XNO-only, first 3 epochs → assert each epoch's total liq payout `≈ 0` (empty `anchorRoot[E−2]`), then assert that to earn anything the mesh must trade with the warm-up allowlist anchors, and that doing so at scale is bounded by `cap_epoch_swaps · RATE_BPS · μ < EPOCH_BUDGET_CAP` and net-negative after burns. **This is the regression test that must pass.**
- **Budget exhaustion:** more eligible claims than budget → first-come ordering, later claims earn 0, total ≤ `EPOCH_BUDGET`, pool never negative.
- **Double-claim / replay:** resubmit same `LiqClaimIn`; cross-epoch replay; lift sig onto another swap → all rejected.
- **Reorg determinism:** fork crossing an epoch boundary where a `Settled` swap becomes `Refunded`; assert two nodes replaying in canonical order reach byte-identical `liqBudget`, `anchorRoot`, `incentivePool` (guards the consensus-fork bug).
- **Affordability:** thin pool with outstanding vault obligations → liq claims rejected before vault yield is endangered.
- **Kill-switch:** flip `LiqRewardsEnabled=false` → claims invalid, bonds refundable.

**Property/fuzz:** fuzz `Serialize`/`Parse` of the new legs; invariant test "no block increases total OBX supply" (extends the existing `apply.go:232` supply invariant) including burns.

**Acceptance gate to enable on testnet:** all of the above green + the sybil-mesh regression test demonstrating ~0 capture for ≥3 epochs + the supply-invariant burn-in test. Deploy binary-only to the 4-node DO testnet (134.122.71.149), run the mesh attack live for 5 epochs, confirm dashboard shows bounded/near-zero fake capture, then lift `EPOCH_BUDGET_CAP_BOOTSTRAP` → `EPOCH_BUDGET_CAP`.

---

**Net:** rewards paid in OBX from the supply-capped `incentivePool` (zero inflation, vault-priority preserved), at a **fixed absolute rate** (not pro-rata — this is the fix), gated by a **prior-finalized-epoch diversity anchor** and a **non-refundable θ=1.5 burn** that prices self-dealing net-negative without requiring consensus to detect washing (which is provably impossible here). The owner-mandated `XNO=20000 / BTC=10000` 2× is preserved exactly and lives only in `μ_bps`; it is paid for entirely on the cost side (2× bond, burn, PoW) and remains purely distributional under a first-come budget ceiling. The headline red-team break (sybil-mesh share capture for a recoverable bond) is closed because there is no share to capture and the bond is now a sunk burn; the residual, honestly stated, is that the 2× XNO tier remains the cheapest to attack and is bounded — not eliminated — by total-spend caps, the burn margin, and a kill-switch, never by weakening the 2× ratio.