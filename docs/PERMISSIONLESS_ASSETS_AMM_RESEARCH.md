STATUS: RESEARCH / PENDING DECISION

# Permissionless Asset Listing & "Uniswap-like" Liquidity — Feasibility & Design

_Author: research pass, 2026-06-26. Grounds every claim in the current swap code. Changes NO production code._

The question from the project owner:

> "Is it possible for anyone to add an asset other than XNO — by supplying that network's RPC — and then have swapping enabled whenever liquidity matches, i.e. a Uniswap-like permissionless liquidity model in our protocol?"

Short answer: **Posting offers for an arbitrary asset is already permissionless and almost free. Trustlessly _settling_ that asset is NOT permissionless — it requires a per-chain backend with the right cryptographic shape, and that backend is code, not config.** A literal cross-chain constant-product AMM is impossible without a bridge; the realistic analog is automated market-making _over the existing atomic-swap order book_, which the auto-liquidity loop already half-implements.

---

## 1. Feasibility: permissionless _listing_ vs. permissionless _settlement_

These are two completely different layers in the code. Conflating them is the trap.

### 1a. Listing (posting an offer for an arbitrary asset) — ALREADY POSSIBLE today

The order book is **asset-agnostic by construction**. In `pkg/swapbook/swapbook.go`:

- `Offer` (`swapbook.go:56`) carries `GiveAsset string` / `GetAsset string` — **free-text strings, not an enum and not checked against any allowlist of real chains**.
- The only validation is `validAsset` (`swapbook.go:38`): `1..MaxAssetLen=16` chars, `[A-Za-z0-9.-]`. It rejects control bytes and over-long tickers — a _syntactic_ filter, not a registry. `"DOGE"`, `"SOL"`, `"FOO"` all pass.
- `Offer.Verify` (`swapbook.go:132`) checks: 32B maker key, 64B sig, non-zero amounts, `GiveAsset != GetAsset`, `validAsset` on both, expiry within `MaxOfferTTL=6h`, the 12-bit `OfferPoWBits` PoW, and the Schnorr signature over `Core()`. **Nothing ties the asset string to a known chain.**
- `Book.Add` (`swapbook.go:251`) admits any offer that `Verify`s, up to `MaxBookSize=50000`.
- `Book.Best(takerGives, takerWants)` (`swapbook.go:293`) matches purely on the two strings.

So **anyone can already gossip a signed offer for any ticker.** The web wallet and the node's auto-liquidity loop only happen to hard-code `"OBX"`/`"XNO"` (`autoliquidity.go:25` `BuildSignedOffer`, called at `cmd/obscura-node/main.go:394` with `"OBX","XNO"`), but the book itself does not care. This is the "billboard" — see issue **[16]** in `docs/SWAP_ISSUES_105.md`: _"the order book is a billboard, not a swap protocol."_

> **Verdict 1a:** Permissionless _listing_ of an arbitrary asset string is true _today_, with no code change. It is just data on a gossip board. No RPC is even required to post.

### 1b. Settlement (trustlessly executing the swap of that asset) — NOT permissionless

Settling a swap is a different package, `pkg/swapd/`, and it is **per-chain code**. There is no "supply an RPC and it works" path, because every chain needs:

1. A **client implementation** of a chain-specific Go interface. Today there are three, each a distinct interface (not one generalized one):
   - `NanoClient` (`pkg/swapd/nano.go:23`) — `Lock / Confirmed / Sweep / Balance`. Production impl `NanoRPC` (`pkg/swapd/nanorpc.go:31`) talks JSON-RPC and contains a hand-written **Nano state-block builder, ed25519-blake2b signer, work-generate, and address codec** (~590 lines). Supplying a URL only feeds `NanoRPCConfig.URL` (`nanorpc.go:38`); all the signing logic is bespoke.
   - `MoneroClient` (`pkg/swapd/monero.go:19`) — same `Lock/Confirmed/Sweep/Balance` shape; only `MockMonero` exists, no live RPC impl.
   - `BitcoinClient` (`pkg/swapd/bitcoin.go:19`) — a _different_ shape: `FundHTLC / Confirmed / Redeem / Refund / RevealedPreimage / Balance`, plus the `BtcHTLCScript` P2WSH builder. Only `MockBitcoin` exists.

2. A **settlement cryptography** that matches what the OBX leg can enforce. The OBX leg (`pkg/swap/swap.go`) is an **adaptor-signature 2-of-2** on ed25519: `AggregateKey K=A+B` (`swap.go:34`), `CoSignClaim` produces an adaptor pre-signature (`swap.go:128`), publishing the OBX claim reveals the adaptor secret via `commit.Extract`, and `ClaimBindingOK` (`swap.go:95`) forces the claim to be the adapted signature. There are exactly **two ways** a foreign chain can be glued to this secret:

   - **Scriptless / shared-curve (XNO, XMR):** the foreign joint account key is literally `(s_a+s_b)·G` on the **same ed25519 curve** (`NanoAccountPub` `nano.go:40`, `XMRSpendPub` `monero.go:37`). The OBX adaptor secret _is_ `s_a`; recovering it (Extract) yields the foreign spend key. **No cross-curve cryptography, no script on the foreign chain.** This is why XNO/XMR "just work" with a scriptless construction. The refund is anchored on the OBX timelock because Nano has no timelock (see `nano.go` header comment).
   - **HTLC / hashlock (BTC):** the foreign chain runs a hash-timelock contract whose preimage is `SHA256(t)` where `t` is the OBX adaptor secret (`bitcoin.go` header, `HashPreimage` `bitcoin.go:123`). Publishing the OBX claim reveals `t`; the redeemer uses `t` as the HTLC preimage. Needs the foreign chain to support a hashlock + timelock script.

3. **Orchestration** that drives both legs. This lives entirely in the **CLI** `cmd/obscura-swap/main.go` (`doAtomicSwap` `main.go:150`), and it is **hard-wired to `NanoClient`** — `doAtomicSwap(c, nano swapd.NanoClient, ...)`. The BTC `BitcoinClient` has **no orchestrator at all** (issue **[54]**: _"BTC HTLC leg has zero orchestration"_). The RPC server stores a `NanoClient` (`pkg/rpc/server.go:54`, `SetNanoBackend` `server.go:101`) but **never calls it** — execution is CLI-only, the node only gossips offers.

> **Verdict 1b:** Settlement is _not_ asset-agnostic. Each new asset needs (a) a Go client implementing a per-chain interface, (b) a settlement scheme that fits scriptless-adaptor _or_ HTLC, and (c) orchestration wiring. Supplying an RPC URL is necessary but nowhere near sufficient — by itself it gives you a "billboard" listing whose offers are **un-settleable and therefore dangerous** (a taker could lock real OBX against an offer no backend can complete). The honest framing: **listing is open; settlement is gated by code + crypto compatibility.**

---

## 2. A pluggable asset / backend architecture (concrete design)

The goal: make adding an asset a matter of (i) registering metadata, (ii) implementing ONE interface, (iii) supplying RPC config — and make the system **refuse to match offers it cannot settle** so the openness of the book never strands a taker.

### 2.1 Asset registry (new file: `pkg/swapd/registry.go`)

```go
type Capability int
const (
    CapUnsupported Capability = iota // listable only; never matched for settlement
    CapScriptless                    // shared-curve adaptor (XNO, XMR)
    CapHTLC                          // hashlock+timelock script (BTC, LTC, ...)
)

type AssetInfo struct {
    Ticker     string      // canonical, must pass swapbook.validAsset
    Decimals   int         // for normalized pricing (fixes issues [59][73][75])
    Capability Capability
    NewBackend func(RPCConfig) (SwapBackend, error) // nil ⇒ listable, not settleable
}

var registry = map[string]AssetInfo{} // ticker → info
func Register(a AssetInfo) { registry[a.Ticker] = a }
func Lookup(ticker string) (AssetInfo, bool)
func Settleable(ticker string) bool // Capability != CapUnsupported && NewBackend != nil
```

Built-ins registered at init: `OBX` (native, decimals 12), `XNO` (CapScriptless, `NewNanoRPC`), `XMR` (CapScriptless, when a real Monero RPC lands), `BTC` (CapHTLC, when an orchestrator lands). Everything else defaults to `CapUnsupported` — **listable but never auto-matched**.

This registry _also_ fixes a cluster of real bugs already filed: per-asset decimals for normalized pricing (**[59] [62] [73] [75] [78] [90] [93]**) and the canonical-ticker mapping that kills the `"BTC (mock)"` dead-on-arrival bug (**[58]**).

### 2.2 Generalized backend interface

The two existing shapes (`NanoClient`, `BitcoinClient`) generalize into one tagged interface. Derive it directly from the methods that already exist:

```go
// SwapBackend is the per-chain capability the orchestrator needs.
type SwapBackend interface {
    Capability() Capability
    Confirmed(lockID string) bool            // both shapes have this
    Balance(dest string) uint64              // both have this (widen to big.Int — issue [42])
}

// ScriptlessBackend = the XNO/XMR shape (nano.go:23, monero.go:19).
type ScriptlessBackend interface {
    SwapBackend
    JointKey(Sa, Sb []byte) ([]byte, error)  // = NanoAccountPub / XMRSpendPub
    Lock(amount uint64, jointPub []byte) (lockID string, err error)
    Sweep(lockID string, jointSecret *edwards25519.Scalar, dest string) error
}

// HTLCBackend = the BTC shape (bitcoin.go:19).
type HTLCBackend interface {
    SwapBackend
    FundHTLC(amount uint64, hash, redeemPub, refundPub []byte, locktime uint32) (string, error)
    Redeem(lockID string, preimage, redeemPub []byte, dest string) error
    Refund(lockID string, refundPub []byte, dest string) error
    RevealedPreimage(lockID string) ([]byte, bool)
}
```

`NanoRPC`, `MockNano`, `MockMonero` already satisfy `ScriptlessBackend` (rename `Lock`'s arg and add `JointKey`). `MockBitcoin` already satisfies `HTLCBackend`. **No behavior change to the existing impls** — this is an interface re-cut.

The orchestrator `doAtomicSwap` (`cmd/obscura-swap/main.go:150`) then switches on `Capability()`:
- `CapScriptless` → the existing XNO path (Lock→wait→fund OBX→claim→Extract→Sweep).
- `CapHTLC` → a new path that mirrors it with `FundHTLC`/`Redeem`/`Refund` and `hash = HashPreimage(t)`. **This orchestrator does not exist yet** (issue [54]); writing it is the bulk of "add BTC."

### 2.3 Per-asset RPC config (`--<asset>-rpc`, env)

Today the plumbing is **hard-coded to Nano**: flags `--nano-rpc / --nano-rpc-auth / --nano-wallet / --nano-account / --nano-work-url` (`cmd/obscura-node/main.go:60-65`), env `OBX_NANO_*`, presets in `pkg/swapd/nanopresets.go` (`ResolveNanoSelector` `nanopresets.go:49`), wired via `swapd.NewNanoRPC` + `srv.SetNanoBackend` (`main.go:117-138`).

Generalize to a per-asset map driven by the registry:

```go
type RPCConfig struct {
    URL, AuthHeader, WalletID, Source, WorkURL string
    Timeout time.Duration
}
// flag form: --rpc xno=https://...  --rpc btc=http://user:pass@host:8332
// env form:  OBX_RPC_XNO=...  OBX_RPC_BTC=...
```

`NanoRPCConfig` (`nanorpc.go:38`) is already exactly this minus the name — promote it to the shared `RPCConfig`. The node loops over the registry: for each `Settleable` asset with a configured RPC, call `info.NewBackend(cfg)` and register the backend in a `map[string]SwapBackend` on the server (`SetBackend(ticker, b)` replacing the single `SetNanoBackend`). The presets pick-list (`nanopresets.go`) generalizes to per-asset preset tables (Nano keeps its three; BTC could ship none, requiring an explicit URL).

### 2.4 Address / codec hooks

Each chain has its own address format. Today the Nano codec (`EncodeNanoAddress`/`DecodeNanoAddress` `nanorpc.go:504`) is Nano-specific. Add to `AssetInfo`:

```go
EncodeAddr func(pub []byte) (string, error)
DecodeAddr func(addr string) ([]byte, error)
```

For scriptless chains the "address" is the joint pubkey encoding; for HTLC chains it is the witness-program/bech32 encoding (`BtcWitnessProgram` `bitcoin.go:88` is the start of BTC's).

### 2.5 What's pure-config vs. needs code, to add an asset

| To add asset X | Config only | Needs Go code |
|---|---|---|
| **List** offers for X in the book | ✅ (ticker passes `validAsset`) | none |
| Register decimals/capability so it prices & shows correctly | `Register(AssetInfo{...})` (1 struct literal) | the literal |
| Supply RPC | `--rpc x=URL` / `OBX_RPC_X` | the flag-map loop (once) |
| **Settle** a CapScriptless chain on **the same ed25519 curve** | RPC config | implement `ScriptlessBackend` (a chain-specific signer + codec, ~the size of `nanorpc.go`) |
| **Settle** a CapHTLC chain | RPC config | implement `HTLCBackend` + **write the missing HTLC orchestrator** (issue [54]) |
| **Settle** a non-ed25519 / non-script chain | — | **impossible trustlessly** without new cross-curve crypto or a bridge (see §3) |

**Files to change:** new `pkg/swapd/registry.go`; re-cut interfaces in `pkg/swapd/{nano,monero,bitcoin}.go`; promote `NanoRPCConfig`→`RPCConfig`; generalize `pkg/swapd/nanopresets.go` to per-asset presets; flag/env loop + `SetBackend` map in `cmd/obscura-node/main.go` (~lines 60-141) and the backend selection in `cmd/obscura-swap/main.go` (`doAtomicSwap` `main.go:150`, `runLive` `main.go:282`); a `backends map[string]swapd.SwapBackend` replacing `nano` in `pkg/rpc/server.go:54` + `SetNanoBackend` `server.go:101`; per-asset decimals in `Book.Best` (`swapbook.go:293`) and the web/explorer price math.

---

## 3. Feasibility tiers for real chains

The hard gate is **crypto compatibility with the OBX adaptor secret**, not having an RPC.

### Tier A — Scriptless adaptor (best: feeless/no-script foreign chain, shared curve)
Chains whose spend authorization is a **plain ed25519 / Schnorr signature** whose key is `(s_a+s_b)·G`. The OBX adaptor secret directly unlocks them; no foreign script needed.
- **XNO (Nano)** — implemented (`nanorpc.go`), shares ed25519. Refund anchored on OBX timelock (Nano has none).
- **XMR (Monero)** — interface exists (`monero.go`), shares ed25519; needs a `monero-wallet-rpc` backend. This is the canonical scriptless atomic swap (Farcaster/UnstoppableSwap).
- Any other **ed25519 account chain** with raw-key spends.

### Tier B — HTLC (needs hashlock + timelock script)
Chains with Script/CLTV-style hashlocks. Glued via `preimage = SHA256(t)`.
- **BTC, LTC, BCH, DOGE** (Bitcoin-script family) — `bitcoin.go` has the script builder; **the orchestrator is missing** (issue [54]). These are HTLC-feasible but more code and front-running-exposed (issue [55]).
- **EVM chains (ETH, BSC, Polygon, etc.)** — feasible via an HTLC _smart contract_ (hashlock+timelock); needs a Solidity HTLC + an EVM RPC backend. Still trustless, but the most code.
- **Secp256k1 Schnorr chains** could _also_ do scriptless (Tier A-style) with a cross-curve DLEQ proof binding the ed25519 OBX secret to a secp256k1 point — **not implemented**; the project deliberately avoids cross-curve crypto (`nano.go` comment: _"no cross-curve cryptography"_). So today secp256k1 chains are HTLC-only here.

### Tier C — Cannot be trustless in this protocol (list-only at most)
- Chains with **no hashlock, no timelock, and a non-ed25519 signature** and no smart contracts → no construction binds them to the adaptor secret. Trustless swap impossible without new cross-curve crypto or a bridge.
- **Custodial / permissioned ledgers, fiat, "wrapped" tokens** → require trusting an issuer/bridge (see §5). These should be `CapUnsupported`: listable as strings but **never auto-matched for settlement**, and clearly flagged in the UI.

---

## 4. The Uniswap / AMM question — honest analysis

### 4.1 Why a literal cross-chain constant-product AMM is impossible (no bridge)

A Uniswap pool is a **single on-chain contract holding reserves of both assets** and enforcing `x·y=k` atomically in one state machine. Cross-chain, the two assets live on **two different ledgers with no shared state** — there is no place to put a pool that both chains' consensus can debit/credit atomically. The only ways to fake it are:
1. A **bridge / wrapped asset** (wXNO on OBX) — reintroduces a trusted custodian or a separate bridge-security problem. The project's stance is explicitly **no bridge** (`swapbook.go:4`: _"a trustless cross-chain AMM is impossible without a bridge"_).
2. An **HTLC-routed AMM** where the pool quotes but settlement is still per-swap atomic — i.e. not a real shared-reserve AMM, just market-making over swaps (4.2).

So: **a real constant-product cross-chain pool is out.** What _is_ achievable are three analogs.

### 4.2 Analog (i): Automated market-making over the atomic-swap order book — RECOMMENDED, half-built

This is the honest "Uniswap-like" answer and the project is already pointed at it. The auto-liquidity loop in `cmd/obscura-node/main.go` (around `main.go:360-404`) already:
- continuously **posts signed OBX→XNO offers** (`swapbook.BuildSignedOffer` `autoliquidity.go:25`),
- sized by a **budget** (`AutoLiquidityMaxFraction=0.5` of spendable, `config/params.go`) in **chunks** (`AutoLiquidityChunkOBX=5`),
- priced off the **book's current best rate** or a seed rate (`bookOrSeedRate` `main.go:443`, `AutoLiquiditySeedRateXNO=1.0`),
- and re-posts to maintain depth (`MakerOffers` cap `autoliquidity.go:43`).

That **is** an automated market maker: an algorithm that continuously quotes both sides. To make it a Uniswap-like _curve_, the missing pieces are:
- **Quote both directions** (today only OBX→XNO; add XNO→OBX) so the maker is a two-sided market.
- **A pricing curve** instead of a flat rate: e.g. quote a spread around a reference and **widen the spread / skew the rate as the maker's inventory drains** — a constant-product-_like_ response (`price = f(inventory_obx, inventory_foreign)`), the off-book emulation of `x·y=k`. This is pure off-chain math in the auto-liquidity loop; no consensus change.
- **Per-asset generalization**: loop over `Settleable` registry assets instead of hard-coding `"XNO"`.

"**Swapping enabled when liquidity matches**" here means: the maker's continuously-posted offers sit in the book; a taker's `Best()` query (`swapbook.go:293`) finds a crossing offer; the taker runs `doAtomicSwap`. "Liquidity matches" = a taker order crosses a live maker quote. **No pooled reserves, no LP tokens** — the "pool" is the maker's own on-chain OBX balance plus its foreign-chain float, and "providing liquidity" = running the auto-quote loop with funds on both chains.
- **Code needed:** generalize auto-liquidity to N assets + two-sided + inventory-skewed pricing (~moderate, off-chain); _plus_ all of §2 for any non-XNO settlement; _plus_ the take/accept handshake (issue [16]) and the unresolved settlement-safety bugs in `SWAP_ISSUES_105.md` (the order book is currently a billboard with no take protocol and serious first-mover/RPC-trust holes).

### 4.3 Analog (ii): A real on-OBX AMM — only for OBX-_native_ assets

A genuine constant-product pool **is** possible if both assets live on the OBX chain (OBX + OBX-native tokens / wrapped-by-trust assets). That is an **on-chain consensus feature** (a pool UTXO/account type with `x·y=k` swap validation), entirely within one ledger — no cross-chain problem. But: it does **not** answer the owner's question (which is about other _networks_' assets via RPC), it needs **OBX-native token issuance** (does not exist today), and any "wrapped XNO" inside it is only as trustless as its bridge. Scope it as a separate future track, not the cross-chain answer.

### 4.4 Analog (iii): Intent / RFQ matching

Generalize the billboard into an **intent book**: takers post "I want X for ≤ price", makers (incl. the auto-maker) respond with quotes, a take/accept handshake (issue [16]) pairs them, settlement runs the atomic swap. "Liquidity matches" = an intent and a quote cross. This is the same machinery as 4.2 with the taker also posting; it mainly needs the **missing session protocol** (issues [16][17][18]).

---

## 5. Security / trust caveats (cross-referenced to `docs/SWAP_ISSUES_105.md`)

Permissionless listing **amplifies** existing swap risks. None of these are hypothetical — most are already filed.

- **Lying operator RPC.** Every foreign-chain fact comes from one operator-chosen endpoint with no cross-check (`nanorpc.go` reads; **[47] [49]**). A hostile/compromised RPC can report a fake or unconfirmed lock as real → executor reveals the adaptor secret and pays OBX for nothing. Worse with permissionless assets: a malicious _asset proposer_ can ship a backend pointed at an RPC they control. **Mitigation:** require ≥2 independent endpoints to agree before settling; treat a new asset's backend as untrusted until reviewed; no auto-match against single-RPC assets. Also no TLS/MITM hardening today (**[50]**).

- **Un-settleable / fake-asset offers strand takers.** Because listing is open and the book never checks settleability, a taker can `Best()`-match an offer for an asset **no backend can complete** (today literally every BTC offer — **[58]**, and there's no take/escrow binding — **[23] [24]**). **Mitigation (the key safety gate):** the matcher/UI must only surface/match offers whose `GiveAsset` AND `GetAsset` are `Settleable` in the local registry; `CapUnsupported` assets are display-only and visibly flagged.

- **Wrapped-asset / bridge trust.** Any "wrapped" or bridged asset someone proposes is only as safe as its custodian/bridge — the antithesis of the project's no-bridge stance (`swapbook.go:4`). **Mitigation:** keep these `CapUnsupported`; never auto-match; loud UI warning.

- **Sybil / spam of fake-asset offers.** PoW is a trivial 12 bits with no per-maker cap (`OfferPoWBits=12` `swapbook.go:25`; **[66] [67] [69]**). Permissionless tickers let an attacker spawn unlimited fake markets to fill `MaxBookSize=50000` and fabricate depth. **Mitigation:** per-maker live-offer cap, asset-tiered/higher PoW, distinct-maker depth annotation.

- **Price manipulation in thin books.** `Best()` ranks on an un-normalized `float64` atomic ratio (`swapbook.go:303`; **[59] [60]**) with precision loss and NaN/Inf on zero amounts; the auto-maker prices off this same book (`bookOrSeedRate` `main.go:443`). A single attacker offer can poison the reference rate the auto-maker then quotes against (wash-trading the curve). **Mitigation:** the per-asset-decimals registry + big.Rat cross-multiplication (**[59]**), median/robust reference pricing, and inventory-anchored (not book-anchored) auto-quotes.

- **All of §B/C/D in `SWAP_ISSUES_105.md` still apply per asset.** No take/accept handshake (**[16]**), secrets minted in one process (**[17] [18] [19]**), first-mover stranded send (**[2]**), no refund execution (**[7] [8]**), no completion verification (**[34]**). Adding assets multiplies these, it does not dodge them.

---

## 6. Phased implementation outline (effort sizing)

Effort: **S** ≈ hours, **M** ≈ a few days, **L** ≈ a week+ and needs external review (crypto/funds-handling).

| Phase | Deliverable | Effort | Notes |
|---|---|---|---|
| **0. Safety gate (do first)** | Registry + `Settleable()` check so the matcher/UI only matches assets with a real backend; mark others `CapUnsupported` + warn. Fixes [58]. | **S–M** | Removes the "list-only offer strands a taker" footgun before opening listing further. |
| **1. Asset registry + decimals** | `pkg/swapd/registry.go`; per-asset decimals threaded into `Best()` + web/explorer pricing. Fixes [59][62][73][75][78][90][93]. | **M** | Pure off-chain; high value (fixes price bugs regardless of new assets). |
| **2. Interface re-cut** | Generalize `NanoClient`/`BitcoinClient` → `SwapBackend`/`ScriptlessBackend`/`HTLCBackend`; promote `NanoRPCConfig`→`RPCConfig`. No behavior change. | **M** | Mechanical; existing impls already fit the shapes. |
| **3. Per-asset RPC plumbing** | `--rpc x=URL` / `OBX_RPC_X` flag-map + per-asset presets; `backends map[string]SwapBackend` replacing `SetNanoBackend`. | **M** | Generalizes `cmd/obscura-node/main.go:60-141` and `server.go:54`. |
| **4. Second scriptless asset (XMR)** | Real `monero-wallet-rpc` `ScriptlessBackend`. Proves the registry end-to-end on the easy (shared-curve) tier. | **L** | Funds-handling; needs testnet validation like the Nano live gate. |
| **5. HTLC orchestrator (BTC)** | The missing `CapHTLC` path in `doAtomicSwap`: derive hashlock from `t`, verify witness/amount/locktime before funding OBX, confirmation-gated redeem/CLTV refund. Fixes [54][55][56][57]. | **L** | Largest single piece; front-running + timelock-ordering care. |
| **6. Settlement-safety hardening** | Resolve the `SWAP_ISSUES_105.md` criticals that block any multi-asset value: take/accept handshake [16], two-party share exchange [17][18], stranded-send / refund execution [2][7][8], RPC cross-check [47][49], completion verification [34]. | **L (multiple)** | **Must precede real value on ANY asset, XNO included.** |
| **7. Automated market-making (the "AMM")** | Generalize auto-liquidity to N assets, two-sided, inventory-skewed (constant-product-_like_) pricing; reference price hardened (Phase 1). | **M** | Off-chain; the realistic "Uniswap-like" deliverable. |
| **8. (optional) On-OBX AMM** | Native-token issuance + on-chain `x·y=k` pool — only for OBX-native assets. | **L** | Separate track; does not answer the cross-chain question. |

---

## Executive summary

- **Listing is already permissionless.** `Offer.GiveAsset/GetAsset` are free-text strings (`swapbook.go:56`) gated only by a syntactic `validAsset` filter (`swapbook.go:38`), not an allowlist. Anyone can gossip a signed offer for any ticker today — no RPC, no code.
- **Settlement is NOT permissionless and never can be by config alone.** Each chain needs a Go backend implementing a per-chain interface (`NanoClient` `nano.go:23` / `BitcoinClient` `bitcoin.go:19`) plus orchestration. Supplying an RPC URL only fills `NanoRPCConfig.URL`; the signer/codec/HTLC logic is bespoke code.
- **Two and only two trustless settlement shapes exist:** scriptless-adaptor on the **shared ed25519 curve** (XNO/XMR — the OBX adaptor secret _is_ the foreign spend key, `nano.go:40`/`monero.go:37`), or **HTLC hashlock** = `SHA256(t)` (BTC, `bitcoin.go:123`). The project deliberately avoids cross-curve crypto, so secp256k1 chains are HTLC-only here.
- **A pluggable architecture is straightforward:** an asset registry (capability tag scriptless/htlc/unsupported, decimals, RPC, codec) + a re-cut `SwapBackend` interface + per-asset `--<asset>-rpc` plumbing. New asset = register metadata (config) + implement one interface (code). For scriptless shared-curve chains that's ~`nanorpc.go`-sized; for HTLC chains it _also_ needs the **currently-missing HTLC orchestrator** (issue [54]).
- **A literal constant-product cross-chain AMM is impossible** — the two assets live on separate ledgers with no shared on-chain pool, and OBX deliberately has **no bridge** (`swapbook.go:4`). The realistic "Uniswap-like" model is **automated market-making over the atomic-swap order book**, which the auto-liquidity loop (`cmd/obscura-node/main.go:360`) already half-implements (continuous signed quotes, budgeted, book-priced). "Swapping enabled when liquidity matches" = a taker's `Best()` query crosses a live maker quote, then `doAtomicSwap` runs — no pooled reserves.
- **Opening listing amplifies real, already-filed risks:** un-settleable offers stranding takers ([58][23]), lying single-RPC trust ([47][49][50]), sybil fake-asset spam ([66][67]), and thin-book price manipulation that poisons the auto-maker's reference ([59][60]). The **mandatory safety gate** is: the matcher/UI only auto-matches assets that are `Settleable` in the local registry; everything else is list-only and flagged.
- **Bottom line:** Yes to permissionless _listing_ (already true). No to permissionless _settlement_ by RPC alone — each asset is a code+crypto integration in one of two tiers. A Uniswap-like _experience_ is achievable as algorithmic market-making over atomic swaps, but it presupposes fixing the settlement-safety criticals in `docs/SWAP_ISSUES_105.md` that today make even the XNO path unsafe for real value.

Doc path: `/Users/mac/XMR_alternative/docs/PERMISSIONLESS_ASSETS_AMM_RESEARCH.md`
