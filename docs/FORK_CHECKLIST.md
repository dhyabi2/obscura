# Obscura (OBX), Fork Checklist

**Status:** value-less **TEST chain**. Genesis resets are FREE and there is no live value to
protect, so the cheapest, cleanest way to fork this chain is almost always a **new-genesis
relaunch** (bump `NetworkSeed` → new `netID` → new genesis hash → new wire magic → done).
This document is the gate between "I changed a consensus rule" and "the network is running the
new rules without two chains silently mis-syncing into each other."

Cross-references:
- Network-identity / launch procedure: `docs/GO_LIVE_CHECKLIST.md`
- Consensus-surface inventory: this doc §3 (condensed) + the full inventory the audit produced
- Architecture & layering: `docs/ARCHITECTURE.md`

> **Golden rule for this repo:** `go build ./...` and `go test ./...` MUST stay green at every
> step. A fork changes *consensus*; it must NOT be used as cover to break the build or to leak a
> peripheral dependency into the 16-package consensus core. If your change touches anything
> outside `pkg/{accumulator,base58,block,chain,commit,config,consensus,fee,group,pow,pqaccum,pqsign,pqstealth,stark,swap,tx}`,
> it is (almost certainly) NOT a fork, see §2.

---

## 1. What a "fork" IS here

Three distinct things get loosely called "a fork." They have very different blast radii.

| Class | Definition | Old nodes vs new nodes | On this test chain |
|---|---|---|---|
| **HARD FORK** | A consensus-rule change that makes blocks/txs valid under the new rules **invalid** under the old rules (or vice versa). | **Incompatible.** They reject each other's blocks. Two chains. | The normal case. Because genesis resets are free, do a **new-genesis relaunch** (bump `NetworkSeed`) rather than a flag-day. |
| **SOFT FORK** | A *tightening*: the new rules are a strict subset of the old. Blocks valid under new rules are still valid under old rules; old nodes accept new-node blocks but not vice versa. | **One-way compatible.** Old nodes follow if a majority of hashpower enforces the tighter rule. | Rare here; most "tightenings" in this codebase are mempool/relay policy (non-consensus). |
| **Non-consensus node update** | A change to node runtime, DEX, wallet, UI, build, docs, or node-local policy. The consensus core does **not** change. | **Fully compatible.** No fork. Mixed old/new nodes interoperate. | The common, safe case. Ship freely. |

Why a hard fork on a test chain is cheap: genesis is **fully deterministic from config**
(`pkg/chain/apply.go:18` `initGenesis`, fixed `GenesisTimestamp = 1_700_000_000` at
`apply.go:16`, fixed conservation `"OBSCURA-GENESIS-v1"` at `apply.go:32`). The genesis header
derives from `config.GenesisDifficulty`, the accumulator initial value, and the empty CMRoot , 
all config-pinned. Change a consensus parameter and the resulting chain is a different chain
with a different genesis state; there are no real coins to migrate, so you simply relaunch.

---

## 2. The decision: does my change trigger a fork?

Answer in order. Stop at the first YES.

1. **Did I edit any file in the 16-package consensus core?**
   `pkg/{accumulator, base58, block, chain, commit, config, consensus, fee, group, pow, pqaccum, pqsign, pqstealth, stark, swap, tx}`.
   - **NO →** Non-consensus update. Not a fork. (Node runtime `p2p/rpc/miner/mempool/light`,
     DEX `swapbook/swapd/swapnet/swapsession`, wallet/keys `wallet/keystore/mnemonic/uri/pqwallet/...`,
     `cmd/*`, `website/`, `webui/`, `docs/`, `scripts/` are all peripheral. The core imports none
     of them, verified, zero upward edges.) Ship it, keep build+tests green.
   - **YES →** continue.

2. **Did I change a value that feeds `netID`** (`NetworkSeed`, `AccumulatorBackend`, `CoinName`,
   `Ticker`, `AtomicPerCoin`, `ClassGroupDiscriminantBits`; `pkg/config/params.go:393–407`)?
   - **YES →** HARD FORK, and the cleanest kind: it self-separates the chain (see §4). Run the
     full §6 runbook.

3. **Did I change a consensus *rule or format***, block header roots, tx version/types/limits,
   emission, difficulty math, PoW seeding, PoR, swap windows, reorg bounds, validation checks
   (see §3)?
   - **YES, and it makes previously-valid blocks invalid or previously-invalid blocks valid →**
     HARD FORK. **You MUST also bump `NetworkSeed`** so the new chain gets a fresh `netID`/magic
     and cannot silently mis-sync against the old one. Run §6.
   - **YES, but it is a strict tightening (subset) →** could be a SOFT FORK; on a test chain,
     prefer treating it as a hard fork + relaunch anyway (it's free and removes ambiguity).

4. **Is it a node-local policy knob** (auto-liquidity, `MinOrderSize`, session caps,
   `DefaultSeeds`, `MinFeePerByte` as relay policy, mempool ordering)?
   - **YES →** Non-consensus. Different nodes may run different values. Not a fork.

> **Format-vs-policy trap:** `MinFeePerByte` (`params.go:429`) is **not** enforced by block
> validation, it is mempool/relay policy. Raising it is a relay tightening, NOT a consensus
> soft fork. `AddressVersion` (`pkg/commit/address.go:21`) and the base58 alphabet are
> wallet-side display format: changing them breaks cross-version *address parsing* but does not
> change on-chain validation. They are non-consensus, yet still coordinate-once items.

---

## 3. Consensus-surface inventory, what triggers what

The authoritative trigger table. File:line are the canonical definitions. "HF" = hard fork,
"SF" = soft-fork-capable tightening, "NC" = non-consensus.

### 3.1 Network identity & replay protection (self-separating)
| Parameter | Source | Class | Note |
|---|---|---|---|
| `NetworkSeed` = `"obscura-mainnet-v1"` | `params.go:376` | **HF** | Feeds `netID` AND the p2p `networkMagic` (`pkg/p2p/p2p.go:91`). Bumping it re-keys both layers at once. |
| `netID` (32B blake2b) | `params.go:393–407` | **HF** | `blake2b("Obscura/netID/v1" ‖ NetworkSeed ‖ AccumulatorBackend ‖ CoinName ‖ Ticker ‖ AtomicPerCoin ‖ ClassGroupDiscriminantBits)`. Bound into every CoreHash/signature, STARK transcript, swap claim/refund sig. |
| `CoinName`/`Ticker` | `params.go:50–51` | **HF** | Feed `netID`. |
| `AtomicPerCoin` = 1e12 | `params.go:54` | **HF** | Feeds `netID`; reward + address arithmetic. |
| `AccumulatorBackend` = `"classgroup"` | `params.go:370` | **HF** | Feeds `netID`; group arithmetic. |
| `ClassGroupDiscriminantBits` = 2048 | `params.go:373` | **HF** | Feeds `netID`; group modulus. |
| Network mode (mainnet/testnet/devnet, `OBX_NETWORK`) | `params.go:173–184` | **NC** | Runtime only; gates whether emission overrides are allowed. |

### 3.2 Monetary policy
| `MoneySupplyCap` 18.4M·1e12 `params.go:74`; `TailEmissionAtomic` 0.6 OBX `:75`; `EmissionShift` 19 `:73`; `IncentivePoolBps` 500 `:80`; `Holding{Min,Max}Lock` `:95–96` | **HF** | coinbase value / accounting. |

### 3.3 Timing & difficulty
| `TargetBlockTime` 120s `params.go:162`; `DifficultyWindow` 60 `:69`; `MinDifficulty` 16 `pkg/consensus/difficulty.go:13` | **HF** (`MinDifficulty` raise = SF-capable). | LWMA retarget. `TargetBlockTime`/`GenesisDifficulty`/`FixedDifficulty` are mainnet-locked via `IsMainnet()`; on devnet they are env-overridable (`OBX_TARGET_BLOCK_TIME` etc.), an unguarded override **silently forks** that node. |

### 3.4 PoW & epoch seeding
| `PoWGenesisSeed` `params.go:356`; `PoWEpochLen` 2048 `:228`; `PoWSeedLag` 512 `:229` | **HF** | epoch boundary / seed derivation. |

### 3.5 Block structure (5 roots), `pkg/block/block.go`
| `Header.Version` `:21`; `MerkleRoot` `:27`; `AccValue` `:28`; `AccSize` `:29`; `PQAccRoot` `:30`; `NullRoot` `:31`; `CMRoot` `:34`; `NumTxs` `:37`; `PoRRoot` `:40`; `MaxBlockBytes` 2MiB `params.go:99` | **HF** | header commitment format; any derivation change diverges PoW preimage. |

### 3.6 Transactions, `pkg/tx/tx.go`
| `Transaction.Version` (1; 2 = PQ path) `:218`; tx-type set (Input/AnonInput/Swap*/Output/ZK*/Vault*/CZKSpend, PQ* on v2) `:95–249`; `MaxInputs`/`MaxOutputs` 1024 `:21–22`; `MaxFieldBytes` `:23`; `MaxTxBytes` `:24` | **HF** | serialization + validation. Adding a type without a Version bump breaks old nodes. |

### 3.7 Anonymity / accumulator / ZK
| `PoolSize` 16 `params.go:549`; `MaxAnchorWindow` 100k `:20` (SF: lowering may strand witnesses); `ConfidentialBits` 60 `:62` | **HF** | ring grouping / range bound. |

### 3.8 PoR, coinbase, swaps, fork-choice
| `PoRWindow` 10k `params.go:46`; `PoRChallenges` 4 `:66`; `CoinbaseMaturity` 60 `:433`; `SwapReorgMargin` 100 `:457`; `SwapTimelockWindow` 200 `:468`; `SwapMinClaimWindow` 50 `:501`; `SettleableAssets` `:341`; `MaxReorgDepth` 100 `pkg/chain/forkchoice.go:35`; `PartitionRecoveryMargin` `forkchoice.go:50` | **HF** | |

### 3.9 Validation rules, `pkg/chain/validate.go`
Height link `:51`, prevhash `:54`, timestamp bounds `:62–68`, difficulty match `:70–72`, PoW
`:78`, single-coinbase `:83–89`, dup-tx `:91–95`, acc-value uniqueness, swap reorg-margin
binding `:425`, ZK STARK verify (netID-domained), confidential fee range, vault affordability
`:178`, coinbase economics `:183–194`. **All HF.**

### 3.10 Non-consensus (ship freely, no fork)
Auto-liquidity (`params.go:244–270`), `MinOrderSize` `:510`, swap session caps `:535–542`,
`DefaultSeeds` `:123–126` / `OBX_SEEDS`, `MinFeePerByte` `:429` (relay policy),
`AddressVersion`/checksum (`pkg/commit/address.go:21,32`), base58 alphabet
(`pkg/base58/base58.go:13`), `GenesisTimestamp` (`apply.go:16`, hardcoded, never validated).

---

## 4. (a) Network identity & replay protection, why bumping `NetworkSeed` cleanly separates

`NetworkSeed` (`pkg/config/params.go:376`) is the single highest-leverage fork lever in the
codebase because it re-keys **two independent layers at once**:

1. **Consensus / proof layer.** `NetworkSeed` is the first input to `netID`
   (`params.go:393–407`). `netID` is bound into every CoreHash (so every transaction signature),
   every STARK proof transcript, and every swap claim/refund signature. A proof or signature made
   on the old `netID` **fails verification** under the new `netID`. This is cross-instance replay
   protection: the new chain cannot be tricked into accepting old-chain proofs, and old-chain
   peers cannot replay new-chain spends. The two chains are cryptographically disjoint.

2. **Wire layer.** `pkg/p2p/p2p.go:91` derives `networkMagic` (FNV-1a) directly from
   `config.NetworkSeed`. The 4-byte magic is the first field of both the hello handshake
   (`p2p.go:533`) and every framed message (`p2p.go:1002`). On mismatch the connection is
   dropped (`checkHello` `p2p.go:559`; frame reader `p2p.go:1021–1022`). So after a seed bump,
   old and new nodes **cannot even complete a handshake**, there is no silent cross-talk to
   mis-sync from. The chains physically refuse to peer.

Because genesis is fully config-derived (`apply.go:18` `initGenesis`), bumping `NetworkSeed`
(and/or any consensus parameter that changes the initial state) yields a fresh, byte-identical
genesis on every upgraded node and a brand-new chain. This is the preferred fork mechanism here.

### Genesis reset procedure (tied to `docs/GO_LIVE_CHECKLIST.md`)
1. Edit `pkg/config/params.go:376`: bump the seed, e.g. `"obscura-mainnet-v1"` → `"obscura-mainnet-v2"`
   (or `"obscura-testnet-<date>"`). For a relaunch driven by *another* consensus change, you may
   also leave the seed but you MUST still pick a value that has never been used on the old chain.
2. Make the consensus change(s) that motivated the fork (the actual rule/param edits from §3).
3. `go build ./...` and `go test ./...`, both green. Confirm `pkg/config/params_test.go` (if it
   pins `netID`) is updated to the new expected value, or that no test hard-codes the old netID.
4. Print the new identity: `NetIDHex()` (`params.go:416`), record it in the relaunch notes.
5. Wipe state on every node: delete the chain datadir (default from `defaultDataDir()`,
   overridable with `--datadir`; see `cmd/obscura-node/main.go:40`). Old `chain.db`, peers.json,
   and shutdown snapshot must be removed so no node replays the old chain.
6. Follow `docs/GO_LIVE_CHECKLIST.md` for the rest of the relaunch: seed-node DNS/IPs, emission
   mode (`OBX_NETWORK`), and the artifact/build steps (incl. the website/WASM re-sync in §6.4).

---

## 5. (b) Peer protocol-version / handshake gating

**Verified: the P2P layer HAS a version field and gates on it.** No prerequisite work needed.

- `protocolVersion = 2` is a constant at `pkg/p2p/p2p.go:64`.
- The hello payload carries it: `magic(4) ver(2) height(8) advLen(2) advertise observed`
  (`p2p.go:533`, written at `:540`).
- `checkHello` rejects any peer whose magic OR version does not match exactly:
  `p2p.go:559` (magic mismatch → drop) and `p2p.go:562` (`ver != protocolVersion` → drop).

Implications for forks:

- A **wire-format change** (new message types, changed handshake layout, changed framing) is a
  node-runtime change. **Bump `protocolVersion`** (`p2p.go:64`) so old and new nodes refuse to
  peer instead of mis-parsing each other. This is independent of consensus: you can bump the wire
  version without a consensus fork.
- A **consensus fork via `NetworkSeed`** automatically changes `networkMagic`, so old/new nodes
  already refuse to handshake even if `protocolVersion` is unchanged. You get separation for free.
- **Caveat to flag:** `networkMagic` is derived ONLY from `NetworkSeed`, not from any other
  consensus parameter. So a consensus fork that changes a *rule* (e.g. `EmissionShift`,
  `CoinbaseMaturity`) **without** bumping `NetworkSeed` would leave the wire magic identical , 
  old and new nodes WOULD handshake and then silently disagree on validity, producing a confusing
  partition rather than a clean split. **Therefore: any rule-only hard fork MUST also bump
  `NetworkSeed` (or `protocolVersion`).** This is enforced by step §2.3 above. Treat "rule change
  without a seed/version bump" as a fork-checklist violation.

---

## 6. (c) Activation strategy & (d) zero-impact runbook

### 6.1 Choosing the activation strategy
| Strategy | When to use | Mechanism |
|---|---|---|
| **New-genesis relaunch** (recommended for this test chain) | Any HF; whenever you can ask all participants to restart on a fresh chain. | Bump `NetworkSeed`, wipe datadirs, restart. Clean, no dual-rule code paths. |
| **Flag-day height** | Only if you must preserve chain history across the fork (rare on a value-less test chain). | Gate the new rule on `height >= ACTIVATION_HEIGHT` inside `pkg/chain/validate.go`; ship the conditional to all nodes before the height; bump `protocolVersion` so un-upgraded nodes drop off at the boundary. Requires keeping BOTH rule sets in the validator until the old chain is abandoned, more code, more risk. |

For Obscura today, **prefer new-genesis relaunch**. Use flag-day only if a specific test
explicitly needs continuous history through a rule change.

### 6.2 Runbook, perform the fork with ZERO impact on peripheral software
Peripheral apps (wallet, explorer, DEX, dashboard, loadgen) depend ONLY on the core's stable
surface (config getters, tx/block (de)serialization, RPC). They do not import each other into the
core and the core imports none of them. So if you do not change the *shape* of that surface, they
keep building and running unchanged.

1. **Branch.** This is not a git repo at root in this env; if it becomes one, branch first, never
   fork on the default branch.
2. **Make the consensus edits** (§3 params/rules) in the core packages only. Do NOT add a
   peripheral import to any core package (would break isolation; verified zero today).
3. **Bump identity:** `NetworkSeed` (`params.go:376`) and, if you changed wire format,
   `protocolVersion` (`p2p.go:64`). If rule-only, still bump `NetworkSeed` (§5 caveat).
4. **Keep the stable surface stable.** If a struct field, RPC field name, or tx/block
   serialization that peripherals read is *renamed/removed/re-typed*, that is a surface break and
   peripherals must be updated in lockstep. Prefer additive changes (new optional fields, new tx
   version 2 gated by `Transaction.Version`) so old wallet/explorer code still parses.
5. **Build everything:** `go build ./...`. All 9 `cmd/` binaries must compile against the new core
   unchanged. If `obscura-wallet`/`obscura-explorer`/`obscura-dexsim` fail to build, your change
   was a surface break, not a clean core fork, fix the surface or accept the lockstep update.
6. **Test:** `go test ./...` green (50+ test files). Update any test that pins the old `netID`,
   genesis hash, or a changed param.
7. **Re-sync build-time copies** (these are COPIES, not imports, easy to forget):
   - `cmd/obscura-node/website/` is a `//go:embed` copy of repo-root `website/`. Re-rsync before
     `go build ./cmd/obscura-node` (the rsync command is documented in `cmd/obscura-node/ui.go`).
   - `cmd/obscura-wasm` → `website/wallet.wasm`: rebuild via `website/build-wasm.sh` so the web/
     desktop wallet matches the new core (esp. if tx format changed).
   - `cmd/obscura-dashboard` embeds `webui/`; rebuild if UI changed.
8. **Wipe state & relaunch** per §4 genesis-reset steps + `docs/GO_LIVE_CHECKLIST.md`.

---

## 7. (e) Post-fork verification checklist

Run ALL of these after the relaunch. Each must pass.

- [ ] **Build green:** `go build ./...`, all `pkg/` and all 9 `cmd/` binaries compile against the
      new core with no peripheral source changes (proves the stable surface held).
- [ ] **Tests green:** `go test ./...`, every package, including any test that pins `netID`,
      genesis hash, or changed consensus params (updated to new expected values).
- [ ] **Identical `netID` across the fleet:** every node prints the SAME `NetIDHex()`
      (`params.go:416`). Spot-check 2+ nodes. Different netID ⇒ someone is on a stale binary.
- [ ] **Identical genesis hash across nodes:** every freshly-wiped node converges to the same
      genesis (height-0) block hash. Genesis is deterministic from config (`apply.go:18`), so any
      divergence means a config drift between binaries.
- [ ] **Version/magic gating works:** an OLD binary (pre-fork seed/version) attempting to connect
      to a NEW node is rejected at handshake (`checkHello`, `p2p.go:559/562`), confirm the old
      peer is dropped, not silently accepted. Conversely two NEW nodes peer and sync.
- [ ] **No cross-chain sync:** new nodes do not adopt old-chain blocks and old nodes do not adopt
      new-chain blocks (the two should be unable to even handshake).
- [ ] **Mining works:** miner produces valid blocks under the new rules; difficulty retargets;
      PoR challenges validate; coinbase economics check (`validate.go:183–194`) passes.
- [ ] **Wallet functional:** create address, build & broadcast a confidential tx, it confirms; if
      `Transaction.Version` semantics changed, the wallet built the right version. (Rebuild
      `wallet.wasm` first, §6.7.)
- [ ] **Explorer functional:** explorer reads the new chain via RPC, shows new genesis + tip,
      decodes blocks/txs without errors (proves serialization surface intact).
- [ ] **Swaps functional:** an OBX leg (and an OBX↔XNO swap if exercising the DEX) completes;
      claim/refund windows (`SwapReorgMargin`/`SwapTimelockWindow`/`SwapMinClaimWindow`) behave;
      swap signatures verify under the new `netID`.
- [ ] **Peripheral apps still build against the new core:** `obscura-dexsim`, `obscura-loadgen`,
      `obscura-dashboard`, `obscura-testwallet` all build and run a smoke test.
- [ ] **Build-time copies in sync:** `cmd/obscura-node/website/` matches repo `website/`;
      `website/wallet.wasm` rebuilt; dashboard `webui/` embed rebuilt.
- [ ] **Old datadirs gone:** no node is replaying the old chain from a leftover `chain.db`,
      `peers.json`, or shutdown snapshot.

If every box is checked, the fork is complete: one clean new chain, cryptographically and
on-the-wire separated from the old one, with all peripheral software building and running
unchanged against the stable core surface.
