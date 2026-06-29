# Cross-chain atomic swaps: BTC + XNO (trustless, P2P, no admin)

Obscura already ships a trustless **XMR↔OBX** atomic swap (`pkg/swap`,
`pkg/swapd/monero.go`, `docs/INVENTION_SWAPS.md`): the OBX leg is an adaptor-
signature 2-of-2 timelocked output, and completing the OBX claim on-chain REVEALS
the swap secret `t` so the counterparty can take the other chain. This doc extends
that to **Bitcoin** and **Nano (XNO)**, and specifies how liquidity providers earn
on every swap with **no custodian and no admin**.

The whole system is non-custodial (atomic swaps — funds are only ever in
2-party contracts), peer-to-peer (offers gossip via `pkg/swapbook`, which already
PoW-stamps offer ids for sybil resistance), and has no privileged operator.

## The shared primitive: one secret `t`, two chains

Every swap turns on a single 32-byte secret `t` (an ed25519 scalar):
- On the **OBX leg** it is the adaptor secret: the claim's 2-of-2 signature is an
  adaptor pre-signature bound to `T = t·G`; publishing the completed claim lets
  the counterparty `Extract` `t`.
- On the **other chain** the funds are locked so that **knowing `t` unlocks them**.

How "knowing `t` unlocks them" is expressed depends on the chain:

| Chain | Curve | Script? | Other-leg lock | Refund |
|---|---|---|---|---|
| OBX | ed25519 | adaptor 2-of-2 | (the timelocked SwapOutput) | on-chain `UnlockHeight` |
| Monero | ed25519 | scriptless | funds at `(s_a+s_b)·G`; sweep needs `t` | via OBX timelock anchor |
| **Nano** | **ed25519** | **scriptless** | funds at `(s_a+s_b)·G`; sweep needs `t` | via OBX timelock anchor |
| **Bitcoin** | secp256k1 | **HTLC** | HTLC hashlock `H = SHA256(t)` | on-chain `OP_CLTV` timelock |

Crucial consequence: **no cross-curve adaptor cryptography is needed.**
- Nano is ed25519, identical to Monero → its backend mirrors `monero.go`.
- Bitcoin uses a plain hashlock `SHA256(t)` (the secp256k1 keys only sign the
  HTLC spend; they never have to share the adaptor scalar), so the secp256k1↔
  ed25519 mismatch never arises. The OBX adaptor secret `t` simply doubles as the
  Bitcoin HTLC preimage.

## Bitcoin ⊗ OBX

Roles: **Alice** has BTC, wants OBX. **Bob** has OBX, wants BTC. Alice owns `t`.

1. Alice funds a **BTC HTLC**: redeemable by Bob's key with preimage `t`
   (hashlock `H = SHA256(t)`), or refundable by Alice after `L_btc` (CLTV).
2. Bob waits for the HTLC to confirm, then funds the **OBX SwapOutput**: claimable
   by the 2-of-2 `K = A+B` before `L_obx`, or refundable by Bob after `L_obx`.
3. Alice **claims OBX** by adapting the 2-of-2 pre-signature with `t` → she gets
   OBX and `t` becomes recoverable from the on-chain claim.
4. Bob `Extract`s `t` from the OBX claim and **redeems the BTC HTLC** with
   preimage `t`.

**Timelock ordering (safety):** `L_obx < L_btc`, with a margin ≥ the BTC
confirmation window. After Alice reveals `t` (claiming OBX before `L_obx`), Bob
still has until `L_btc` to redeem BTC. If the swap aborts before step 3, Bob
refunds OBX after `L_obx` and Alice refunds BTC after `L_btc`; neither can be
cheated. Bob funds OBX only after the BTC HTLC is confirmed, so he never locks
against nothing.

Why this is safe without cross-curve crypto: Alice can only *get* OBX by revealing
`t`; revealing `t` is exactly what lets Bob take BTC. Alice gains nothing by
refunding BTC (she'd forfeit OBX). The HTLC redeem requires Bob's signature too,
so a leaked `t` alone doesn't let a third party steal Bob's BTC.

Backend: `pkg/swapd/bitcoin.go` — `BitcoinClient` (FundHTLC / Confirmed / Redeem /
Refund / RevealedPreimage / Balance). `MockBitcoin` enforces the real rules
(hashlock, CLTV via a mock clock, single-spend) for tests; a production build
points it at `bitcoind`/Electrum with a standard P2WSH HTLC:
`OP_IF OP_SHA256 <H> OP_EQUALVERIFY <redeemPub> OP_CHECKSIG OP_ELSE <L_btc>
OP_CHECKLOCKTIMEVERIFY OP_DROP <refundPub> OP_CHECKSIG OP_ENDIF`.

## Nano (XNO) ⊗ OBX

Nano is ed25519 and **feeless**, but has **no script and no timelock**. That is
fine here because the swap's refund is anchored on the **OBX** timelock (exactly
as the Monero swap already is) — the scriptless leg never needs its own timelock.

It therefore mirrors Monero almost exactly:
- A Nano "lock" = sending XNO to the address derived from the joint key
  `(s_a+s_b)·G`. The funds can only be moved by whoever knows `s_a+s_b`.
- Settlement: when the OBX claim reveals `s_a` (= `t`), the counterparty forms
  `s_a + s_b` and signs the Nano send block that sweeps the funds.

Backend: `pkg/swapd/nano.go` — `NanoClient` (Lock / Confirmed / Sweep / Balance) +
`MockNano`, structurally identical to `MoneroClient`/`MockMonero`. A production
build talks to a Nano node RPC; the joint account is an ordinary Nano account
whose secret is `s_a+s_b`.

Honest caveat vs. Bitcoin: because Nano can't express its own refund, the
protocol relies on the OBX timelock + careful ordering (lock Nano only after the
OBX side is committed, sweep promptly on reveal). This is the same trust model as
the shipped XMR swap, not a new one — but it is less forgiving than Bitcoin's
native HTLC refund, so the daemon must watch timelocks closely.

## Liquidity incentives — P2P, no admin

Atomic swaps have **makers, not pools** (you can't pool BTC/XNO inside OBX without
custody, which would break "no admin"). So liquidity is a set of makers posting
signed offers; the incentives:

1. **The spread (built-in, zero new trust).** A maker posts an `Offer` in
   `pkg/swapbook` with a give/get ratio that embeds their margin; a taker fills
   it; the maker earns the spread **on every swap**. This already exists, is fully
   P2P and non-custodial, and is the primary "LP earns a little per swap"
   mechanism. Deep liquidity = many competing makers tightening the spread.

2. **Optional protocol maker-reward (liquidity mining), Phase 2.** Pay makers a
   small reward from the existing `incentivePool` (the same monetary-neutral
   source the vaults use — see [[vaults-feature]]) when an OBX swap leg provably
   settles. The hard problem is **wash trading / sybil** (self-swapping to farm
   rewards). Mitigations, all admin-free: reward proportional to *taker fees
   actually paid* (not raw volume); require *both* legs to provably settle;
   per-maker rate limits; reuse the offer-PoW `pkg/swapbook` already stamps; and
   an optional **maker bond that is slashed for griefing** (locking a taker then
   aborting). NOT built in this pass — it is a consensus change with real abuse
   surface and must be designed adversarially before shipping.

Griefing (a maker who locks a taker then walks) never risks funds — the refund
paths protect everyone — it only wastes time/fees. Offer-PoW + optional bonds
keep it bounded.

## Status / phasing
- **Phase 1 (this pass):** BTC backend (HTLC) + Nano backend (scriptless), each
  with an in-memory mock enforcing the real rules, and end-to-end tests proving
  atomic settlement and refund safety. The OBX leg reuses the shipped adaptor
  swap unchanged.
- **Going live on real chains:** plug a `bitcoind`/Electrum and a Nano-node RPC
  into the backends and fund maker keys — an operational step (real nodes + keys
  + funds), not a consensus change.
- **Phase 2 (research):** the protocol maker-reward (liquidity mining) with the
  anti-wash design above.
