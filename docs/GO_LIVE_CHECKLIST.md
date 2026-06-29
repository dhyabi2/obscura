# Obscura (OBX) — Go-Live Checklist

**Status:** working prototype on a value-less TEST chain. This checklist is the gate
between "demo" and "a network people can actually join, mine, and swap on." Work it
top-to-bottom. Items are tagged:

- ✅ **DONE** — landed in this repo (with file references).
- 🔧 **DO** — a concrete action for the operator before launch.
- ⛔ **BLOCKER** — must be resolved before the chain holds **any real value** (not required for a test-net relaunch, but called out so nobody mistakes test-net-ready for value-ready).

Cross-reference: the full new-user issue register is `docs/NEW_USER_CRITICAL_ISSUES.md`
(101 issues). The five structural showstoppers there are S1–S5; their fixes are in §1.

---

## 0. Decide what "go live" means

Pick ONE and stop pretending it's the other:

| Mode | Real value? | Audit needed? | Block time | Use |
|---|---|---|---|---|
| **Public test-net** | No | No | 120s (recommended) or fast | What everything below readies you for **today**. |
| **Main-net** | Yes | **YES (external)** | 120s locked | Requires every ⛔ BLOCKER closed first. |

> Recommendation: relaunch as a **public test-net** with the 120s mainnet emission
> schedule (so the economics are real even though the coins aren't), gather miners,
> and only flip to value-bearing main-net after an external crypto audit clears the
> ⛔ items. The rest of this doc assumes that path.

---

## 1. Structural showstopper fixes (S1–S5) — ✅ landed

These were the "a new user literally cannot succeed" blockers. All are now in the repo;
verify they're built into the artifacts you ship.

- **S1 — Download path exists.** ✅ `website/download.html` (per-OS bundles, install +
  unsigned-app bypass + checksum verify); linked from the home and wallet navs and the
  "Get started" CTA. 🔧 **DO:** upload the built archives + `SHA256SUMS.txt` to
  `website/releases/` (see §6) so the links resolve.
- **S2 — Fresh node joins the real network.** ✅ `pkg/config/params.go` `DefaultSeeds`
  now points at the live seed droplets (was the unroutable `192.0.2.1` placeholder that
  made every fresh app mine an isolated fork). `OBX_SEEDS="h:p,h:p"` overrides at
  runtime. 🔧 **DO:** confirm the seed IPs in `DefaultSeeds` are the ones you'll keep
  running, or replace with DNS seed hostnames (§4).
- **S3 — Public swap-taking can't drain the operator.** ✅ `/swaps/take` is gated:
  untrusted callers are refused unless `OBX_PUBLIC_SWAPS=1` (`pkg/rpc/server.go`,
  `pkg/rpc/swaps.go`); the operator's real Nano address is no longer exposed through the
  public Vercel proxy (`website/api/explorer.js`, audit S3a). The wallet shows the
  refusal reason and the take dialog now shows **amounts**, not just asset names.
- **S4 — Trust model is disclosed.** ✅ `website/wallet.html` shows a prominent
  disclosure banner (shared-operator visibility, test chain, unencrypted local seed);
  it auto-softens to a lighter notice when the page is served from a local node (your
  own machine). 🔧 **DO:** mirror the disclosure in `README.md` top-of-file.
- **S5 — Binary trust.** ✅ `scripts/package-desktop.sh` now emits `SHA256SUMS.txt` and
  ad-hoc-signs the macOS bundle. ⛔ **BLOCKER for a polished launch:** real
  **Developer-ID notarization (macOS)** + **Authenticode signing (Windows)** need paid
  certs — without them users still hit Gatekeeper/SmartScreen (the download page
  documents the bypass, which is acceptable for a test-net but not for a mainstream
  release).

---

## 2. Distribution / emission review (the "years not days" requirement)

**Finding:** the emission curve itself is fine and Monero-like — the only way coins
"distribute in days" is the **devnet fast block time** leaking into a real launch.

Computed schedule (`MoneySupplyCap=18.4M`, `EmissionShift=19`, `TailEmission=0.6
OBX/block`; see the math in `pkg/config/params.go`):

| Block time | Initial reward | 50% emitted | 90% emitted | Tail (0.6/blk) reached |
|---|---|---|---|---|
| **120s (mainnet)** | 35.1 OBX/blk | **1.38 yr** | 4.6 yr | **8.1 yr** (~98.3%) |
| 1s (devnet) | 35.1 OBX/blk | 4.8 days | 15 days | **~26 days** ← the danger |
| 120s + `shift=20` (Monero-exact) | 17.6 OBX/blk | 2.76 yr | 9.2 yr | 13.5 yr |

For comparison, Monero reached its tail emission ~8 years after launch — Obscura at 120s
with the current `shift=19` matches that profile (first "halving"-equivalent ~1.4yr,
tail ~8yr). Perpetual tail is ~0.86%/yr initially and falls as a share of supply.

- ✅ **Mainnet timing lock.** `OBX_NETWORK=mainnet` (the default) now **ignores** the
  devnet overrides `OBX_TARGET_BLOCK_TIME` / `OBX_FIXED_DIFFICULTY` /
  `OBX_GENESIS_DIFFICULTY` (`pkg/config/params.go`, `IsMainnet()`), so a production
  build cannot accidentally ship 1–2s blocks and dump the whole supply in weeks. Only
  `OBX_NETWORK=testnet|devnet` may accelerate blocks.
- ✅ **DECIDED — emission curve: `shift=19`.** `config.EmissionShift = 19`
  (`pkg/config/params.go:81`): ~35 OBX initial block reward, ~50% of the ~18.4M-OBX
  supply emitted in ~1.38 yr at 120s blocks, with a perpetual `TailEmissionAtomic =
  0.6 OBX`/block floor from ~year 8 onward and no halving cliffs. Rationale: front-load
  distribution to bootstrap hashpower and adoption while the tail funds security forever.
  `shift=20` (slower, ~Monero-exact) was considered and rejected: it roughly doubles the
  time-to-half and contradicts the published whitepaper economics for no clear benefit.
  This is a compile-time consensus constant, freely changeable **before** genesis.
- 🔧 **DO:** verify on a staging node that with no env overrides, `--mine` produces
  ~35 OBX coinbases and blocks land ~120s apart.
- ✅ **DECIDED — no premine, no dev fund (100% mined).** The genesis coinbase has **no
  outputs and mints 0** (`pkg/chain/apply.go:20-27`, `Minted: 0`); every atomic unit of
  supply is created by `config.BlockReward` from height 1 onward. There is no premine,
  no founder allocation, and no dev tax. Stated publicly in `README.md`.
- ✅ **DONE — coinbase maturity enforced on spend (audit #32).** `config.CoinbaseMaturity`
  (60) is checked in the transparent spend path (`pkg/chain/validate.go:417`) and in the
  wallet's available-balance view (`pkg/chain/query.go:113`), so freshly mined coins
  cannot be spent or sold before they mature.

---

## 3. Genesis reset — delete all blocks, start from 0

Every node must start from an identical fresh genesis. A half-wipe forks the network
(learned the hard way — see memory). Do this **in lockstep** across all seed nodes.

1. 🔧 **Bump the network identity** so old test-chain proofs/coins can never replay onto
   the new chain. In `pkg/config/params.go` set a fresh `NetworkSeed` (e.g.
   `"obscura-mainnet-2026-06"`). This re-derives `netID`, which is bound into every
   proof/signature/transcript — a clean cryptographic break from the old chain.
2. 🔧 **Stop every node** (all seeds + your own miners).
3. 🔧 **Delete the data directory on each** (chain DB, snapshots, peers.json — **keep
   `miner.seed` only if you intend to keep the same payout wallet**; delete it too for a
   truly clean identity):
   - Server / default: `~/.obscura/`
   - macOS desktop app: `~/Library/Application Support/Obscura/`
   - Windows desktop app: `%LOCALAPPDATA%\Obscura\`
   - Linux desktop app: `~/.local/share/obscura/`
   ```bash
   systemctl stop obscura-node          # on each seed droplet
   rm -rf ~/.obscura                     # full wipe (or keep miner.seed deliberately)
   ```
4. 🔧 **Rebuild** all binaries from the same commit (so genesis params match exactly):
   `CGO_ENABLED=0 go build -o obscura-node ./cmd/obscura-node`.
5. 🔧 **Start the seed nodes first**, together, with `OBX_NETWORK=mainnet`, and let them
   peer before opening to the public.
6. 🔧 **Verify identical genesis:** `GET /status` (or `/height`) on each seed returns the
   same genesis tip hash at height 0/1. If they differ, params differ — stop and fix.
7. 🔧 **Snapshot cadence:** confirm `SnapshotInterval` (200) + the SIGTERM
   shutdown-snapshot are active so restarts don't slow-replay the whole chain.

---

## 4. Seed & network infrastructure

- 🔧 **Run ≥2 stable seed nodes** on separate hosts/regions. Current `DefaultSeeds`:
  `178.128.162.171:18080`, `134.122.71.149:18080`. Keep them up or replace.
- 🔧 **DNS seeds (recommended over raw IPs):** register `seed1.<domain>:18080` etc. and
  put the hostnames in `DefaultSeeds`, so you can rotate the underlying droplets without
  shipping a new binary. (Audit robustness item.)
- ✅ **Peer exchange (PEX)** is built in — one reachable seed teaches a node the whole
  network. ✅ Eclipse defense: `/16` group caps in the address book (task #105).
- 🔧 **Firewall:** open P2P `18080/tcp` on seeds; keep RPC `18081` bound to loopback
  (default) — never expose RPC publicly except through the hardened Vercel proxy.
- ⛔ **BLOCKER (scale):** RPC has **no rate limiting** (audit #44) and the proxy points
  at **one node** (single point of failure, #53). Before significant traffic, add a
  per-IP limiter (`golang.org/x/time/rate`) and a multi-node proxy with health checks.
- 🔧 **NAT/UPnP** is not implemented — home miners behind NAT will connect out (fine via
  PEX) but won't accept inbound. Document this; seeds must be publicly reachable.

---

## 5. Node / operator configuration

For each **public-facing operator node** (the one the website proxy talks to):

- 🔧 `OBX_NETWORK=mainnet` (locks emission timing — §2).
- 🔧 `OBX_RPC_TOKEN=<long-random>` so operator endpoints (`/blocktemplate`,
  `/submitblock`, `/xno/recovery`, `/xno/withdraw`) are reachable by you over the token,
  not just loopback. **Never** put this token in the website/proxy.
- 🔧 **Leave `OBX_PUBLIC_SWAPS` UNSET.** Public swap-taking stays disabled (S3) until
  per-user XNO funding + receive-address exist. Only set `=1` on a **mock** demo node
  with no real funds.
- 🔧 **Nano secret hygiene:** `OBX_NANO_FUND_SECRET` / `--nano-wallet` go in the process
  environment **only** — never a file, never a systemd unit on disk, never the proxy
  (learned constraint). Prefer `systemctl set-environment` / a secrets manager.
- 🔧 **Auto-liquidity:** decide per node. It auto-sells mined OBX for XNO, which **leaks
  the miner→XNO link** on a public ledger (audit #16). For a privacy coin, default it
  **off** on user-facing builds: `--no-auto-liquidity` or `OBX_AUTO_LIQUIDITY=0`.
- 🔧 **Backups:** snapshot the datadir + (separately, encrypted) the `miner.seed`
  off-box on a schedule (#54). Disk loss otherwise = chain + payout wallet gone.

---

## 6. Binary build, sign & publish

1. 🔧 **Cross-compile** the matrix (CGO-free):
   ```bash
   for t in darwin/amd64 darwin/arm64 windows/amd64 windows/arm64 linux/amd64 linux/arm64; do
     GOOS=${t%/*} GOARCH=${t#*/} CGO_ENABLED=0 \
       go build -o dist/obscura-app-${t%/*}-${t#*/}$( [ ${t%/*} = windows ] && echo .exe ) ./cmd/obscura-node
   done
   ```
   (The plain build above already uses the real canonical RandomX PoW — see §8;
   the `vm-randomx-style` prototype PoW, opt-in via `-tags protopow`, is
   **insecure** and for devnets only.)
2. 🔧 **Resync the embedded site** so the app serves the latest wallet/download pages:
   ```bash
   rsync -a --exclude='.vercel' --exclude='api' --exclude='*.sh' --exclude='vercel.json' \
         --exclude='.gitignore' website/ cmd/obscura-node/website/
   ```
   (then rebuild — the site is `//go:embed`-ed).
3. 🔧 **Package + checksum:** `VER=x.y.z bash scripts/package-desktop.sh` → produces
   `dist/Obscura-<os>-<arch>.{zip,tar.gz}` + `dist/SHA256SUMS.txt` (and ad-hoc-signs the
   `.app`).
4. ⛔ **BLOCKER (polish):** notarize macOS (`codesign` with a Developer-ID + `xcrun
   notarytool`) and Authenticode-sign Windows. Until then, the download page's bypass
   instructions are the stopgap.
5. 🔧 **Publish:** copy `dist/Obscura-*.{zip,tar.gz}` + `SHA256SUMS.txt` into
   `website/releases/` (or a GitHub Releases page and update the links in
   `download.html`). Confirm every `/releases/...` link on `/download` resolves.

---

## 7. Website deploy

- ✅ Disclosure banner (S4), download page (S1), `xnoaccount` removed from the public
  proxy (S3a), take-confirm shows amounts (#57), graceful XNO-tab/node-down handling.
- 🔧 **Set `NODE_RPC`** in the Vercel project to the operator node's RPC URL (server-side
  only; never the token).
- 🔧 **Deploy** the static site + serverless proxy. Smoke-test on the live URL:
  - Home → Download link works; OS auto-detected.
  - Wallet loads, disclosure banner shows; create wallet → seed-backup gate.
  - Swap tab lists offers with amounts; "Take" returns the **disabled** message
    (proves S3 gate is live), not a silent operator drain.
  - Explorer renders; XNO tab shows the "view from your own node" message (proves S3a).
- 🔧 **Verify the proxy whitelist** (`website/api/explorer.js`) exposes only read paths +
  `submittx`/`offer`; `xno/recovery`, `xno/withdraw`, `xno/account` are **not** present.

---

## 8. Cryptography & consensus — ⛔ BLOCKERS before real value

These do **not** block a test-net relaunch but **must** be closed before OBX is worth
anything. From the 2026-06 NO-GO audits + the new-user register:

- ⛔ **External audit** of the from-scratch zk-STARK + class-group accumulator + ring
  spend logic. The README itself says these are unaudited with known critical bugs.
- ✅ **DONE — Real RandomX PoW is now the DEFAULT.** The KAT-verified canonical RandomX
  backend (`pkg/pow/backend_randomx.go`, `//go:build !protopow`) is built by plain
  `go build` / `make` / `build.sh`; the insecure prototype VM is now opt-in only via the
  explicit `-tags protopow`. The `--ui` flag no longer relaxes the start guard (audit
  #27 closed, `cmd/obscura-node/main.go`); a node on the prototype backend refuses to
  start without `OBX_ALLOW_PROTOTYPE_POW=1`. Release binaries therefore ship canonical
  RandomX by construction.
- ⛔ Wallet fund-safety fixes from `NEW_USER_CRITICAL_ISSUES.md` §1: encrypt the
  localStorage seed (#1), enforce seed-backup before first send (#2), CSP/SRI on the
  wasm (#3/#4), reorg rollback in the wallet (#19), multi-tab lock (#21), send-
  confirmation screen (#58).
- ⛔ Swap fund-safety: per-user XNO funding + receive-address (so takers fund themselves
  and receive their own coins, #8/#9), auto-refund for stalled locks (#11/#13), fail-loud
  on swapstate persistence (#12).

---

## 9. Monitoring, ops & launch-day runbook

- 🔧 **Health checks** on each seed: `GET /height` advancing, peer count > 0, block
  interval ~120s. Alert if height stalls or a node forks (different tip at same height).
- 🔧 **Difficulty sanity:** watch the first ~DifficultyWindow (60) blocks — LWMA needs a
  little hashrate before it settles; expect some early variance (audit #92).
- 🔧 **Dashboards:** supply emitted vs schedule, mempool size, active swaps.
- 🔧 **Rollback plan:** if a consensus bug appears post-launch, the genesis-reset
  procedure (§3) + a `NetworkSeed` bump is your clean restart. Keep it scripted.
- 🔧 **Support channel:** add a "report a bug" link (audit #101) and watch it launch day.

---

## 10. Launch-day sequence (condensed)

1. Freeze code at the audited commit; tag it. Run `go build ./...` + `go test ./...`.
2. Finalize consensus params (`NetworkSeed`, `EmissionShift`, seeds) — **no changes
   after this point.**
3. Genesis reset across all seeds in lockstep (§3); verify identical genesis hash.
4. Bring seeds up (`OBX_NETWORK=mainnet`), confirm they peer + advance.
5. Build, sign, checksum, publish binaries (§6); upload to `website/releases/`.
6. Deploy website; smoke-test the four flows (§7).
7. Announce. Watch dashboards + the bug channel.
8. Keep the rollback runbook (§9) within reach.

---

### Quick verification snippet (run before announcing)

```bash
go build ./... && go test ./pkg/rpc/... ./pkg/chain/... -timeout 30m   # green
OBX_NETWORK=mainnet ./obscura-node --mine            # ~35 OBX coinbase, ~120s blocks
curl -s localhost:18081/height                       # advancing
curl -s -X POST localhost:18081/swaps/take ...       # from a NON-loopback box → "disabled" (S3)
shasum -a 256 -c dist/SHA256SUMS.txt                 # binaries verify
```
