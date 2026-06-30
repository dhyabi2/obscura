# Obscura (OBX) — Full-Codebase Security Audit, 2026-07-01

12 parallel auditors (one per subsystem) + 2 bonus cross-checks, covering the entire
codebase: core protocol (consensus/chain/block/PoW), crypto (class-group accumulator,
commitments, ZK/STARK, post-quantum layer), mempool/tx/fee, P2P networking, RPC API +
light client, wallet/keystore, the swap protocol core, the Nano/XNO leg + WASM taker,
CLI binaries, and the web wallet/explorer/website. Each auditor was pointed at the prior
audit docs (`SECURITY_AUDIT_2026-06-27.md`, `SECURITY_AUDIT_DEEP_2026-06-28.md`,
`SWAP_ISSUES_105.md`, `CRYPTO_AUDIT.md`, `NODE_API_AUDIT.md`, `UIUX_AUDIT.md`) to confirm
current status of known items (fixed / still-open / regressed) rather than re-litigate
them, then hunt independently on top.

**Totals: 8 CRITICAL, 16 HIGH, 21 MEDIUM, 26 LOW/INFO (71 open findings).** Full numbered
register with every finding (not a curated subset) is in
[`PROTOCOL_ISSUES_2026-07-01.md`](./PROTOCOL_ISSUES_2026-07-01.md).

Two new pieces of recent work — **P2P snapshot fast-sync** (closes the "fresh node can't
bootstrap" gap) and **swap crash-resume** (`Coordinator.Resume()`) — each introduced a
new CRITICAL/HIGH regression of their own. Both are flagged below, tagged "regression."

A separate, unrelated incident surfaced during this audit: the full coin source is now
public on `github.com/dhyabi2/obscura` (confirmed live via GitHub API), including a
git-history-recoverable (redacted-not-removed) XNO test secret. User confirmed
2026-07-01 this is intentional/acceptable (tiny test amount). See memory
`no-online-publish.md` for the record. Not included in the findings below — operational
decision, not a code defect — except as residual hygiene: the `GITHUB_PAT`
(Contents:write) in `.env` should still be rotated and properly gitignored.

---

## ✅ Fixed in follow-up pass (2026-07-01, verified against code first)

A focused, non-invasive batch — each finding re-confirmed in the source before changing
anything; the CLAUDE.md-protected swap core (swapsession/swapnet coordinator/swapbook/
maker path) was NOT touched:

- **CRITICAL #3 — CORS CSRF wallet-drain:** `corsMiddleware` now withholds the wildcard
  `Access-Control-Allow-Origin` from the state-changing/secret operator routes
  (`/xno/withdraw`, `/xno/recovery`) and answers their preflight with 403, so a browser
  CSRF can no longer reach them; non-browser CLI callers are unaffected
  (`pkg/rpc/integrator.go`, `corsDeniedPath`).
- **HIGH — detached snapshot-export goroutine crash:** `serveSnapshot` now has its own
  `recover()` (the per-connection guard didn't cover this `go`-spawned goroutine)
  (`pkg/p2p/snapsync.go`).
- **HIGH — prewarm-proof goroutine crash:** each per-tx `prewarmProofCacheLocked`
  goroutine now `recover()`s a panic in `validateTxLocked`'s graph instead of crashing
  the node (the authoritative sequential pass still re-validates) (`pkg/chain/validate.go`).
- **HIGH — `obscura-swap` logs private keys:** the dest secret + joint half-keys (`sA`/`sB`)
  now go to a `swap-recovery.keys` file (chmod 600) instead of the log stream
  (`cmd/obscura-swap/main.go`).

Both trees (root + `mainnet/`) build clean. The rest of the register below is still open.

## CRITICAL

| # | Area | Finding | Location |
|---|------|---------|----------|
| 1 | Consensus/Chain | **Snapshot-bootstrapped node's reorg-failure recovery is unconditionally broken.** Recovery always walks back to genesis (`forkHeight=0`), but a fast-sync node has zero bodies below its import height. Recovery silently fails *after* live state was already mutated forward — node keeps running on state matching neither old nor new chain. | `pkg/chain/forkchoice.go:243-249,375-391,466-537,544-563` |
| 2 | P2P (regression) | **Snapshot fast-sync has no cumulative-work check.** Any connected peer can lie about its tip height, get itself picked as the snapshot source, and serve a cheap self-mined alternate history at floor difficulty. The importer never compares cumulative work against the node's existing chain — accepted unconditionally, overwriting all ledger state. Reachable against already-synced nodes, not just fresh ones. | `pkg/p2p/p2p.go:701`, `pkg/chain/snapshotsync.go:285-382`, `pkg/chain/snapshot.go:276-321` |
| 3 | RPC | **Wildcard CORS + IP-only operator trust = browser CSRF wallet drain.** `corsMiddleware` sets `Access-Control-Allow-Origin: *` on the *entire* mux including `/xno/withdraw` and `/xno/recovery`; "trusted" is `isLoopback(r) \|\| hasBearer(r)` (OR). A malicious webpage open in the operator's browser issues a same-machine request that is indistinguishable from a trusted local CLI call. PoC withdraw and recovery-secret-exfil snippets confirmed in the audit. | `pkg/rpc/integrator.go:181-196`, `server.go:356-396`, `xno.go:118-177` |
| 4 | RPC | **Light/SPV client (`pkg/light`) is fully implemented but wired to nothing.** `cmd/obscura-wallet`'s actual sync path trusts whatever block bytes its configured `--node` returns with zero PoW/difficulty/chain-linkage validation. A malicious/compromised RPC endpoint can serve a wallet a completely fabricated chain. | `pkg/light/light.go`, `cmd/obscura-wallet/main.go:1008-1028` (`scan`) |
| 5 | Mempool | **`Input.KeyImage`/`AnonInput.Tag` unified at consensus but disjoint mempool conflict keys.** Deterministic, zero-race: anyone owning one coin can get a miner to complete real PoW on a block consensus then rejects, repeatable every block, for free. | `pkg/mempool/mempool.go:58-105` vs `pkg/chain/validate.go:406-450,463-488` |
| 6 | Mempool/RPC | **`/submittx` unauthenticated CPU-exhaustion DoS.** No rate limit anywhere; full STARK/EC proof verification (~35ms) runs *before* any cheap duplicate/conflict check. | `pkg/mempool/mempool.go:108-139`, `pkg/rpc/server.go:899-934` |
| 7 | Swap relay | **Public `GET /swaps/active` leaks the literal SwapID that is the relay's *only* sender-authentication.** Anyone who polls it can inject a forged `Abort` once a browser taker has locked real XNO — maker refunds its OBX, taker's already-locked XNO becomes permanently unrecoverable. No signature forgery needed, costs nothing. | `pkg/rpc/swaps.go:230-248`, `pkg/swapnet/session_handle.go:136-166`, `pkg/swaprelay/relay.go:159-184`, `pkg/swapnet/coordinator.go:311-356` |
| 8 | CLI | **Wallet recovery mnemonic and the live-XNO funding secret can both be passed as plain CLI flags** (`--mnemonic`, `--nano-fund-secret`), persisting in `ps`/`/proc/<pid>/cmdline`/shell history for the life of the process. The usage text actively recommends the mnemonic form. | `cmd/obscura-wallet/main.go:43,122-124,226-240`, `cmd/obscura-node/main.go:66-78` |

## HIGH

| Area | Finding | Location | Status |
|---|---|---|---|
| ZK/STARK | ZK commitment-tree leaves never persisted in snapshots → coins minted before a snapshot/fast-sync become permanently unspendable (coin freeze) on any non-genesis-replay node. | `pkg/stark/imt256.go:134-179`, `pkg/chain/zkspend.go:328-358` | confirmed still-open |
| ZK/STARK | `VerifyZKMembership` proves nothing about *which* element is accumulated — trivially forged (`C=acc,p=1,y=0`). Latent: not wired to consensus today. | `pkg/accumulator/zkmem.go:124-128` | confirmed still-open |
| ZK/STARK/FRI | FRI ~65 provable bits vs ~112-bit conjectured target; Poseidon width-8 RF=8/RP=22 unvalidated for t=8 (round-constant generation improved via Grain LFSR, but no KAT). Needs external cryptographer sign-off. | `pkg/stark/fri.go:25-34`, `poseidon_wide.go:16-20` | confirmed still-open |
| P2P | Unrecovered panic in the snapshot-export goroutine (`go n.serveSnapshot(p)`) is fatal to the entire node — the only panic guard in the package covers the per-connection loop, not this detached goroutine. | `pkg/p2p/p2p.go:820`, `snapsync.go:72-97` | new |
| P2P | `msgGetBlk` (peer asking us to serve a block) is exempt from the per-peer rate limiter — cheap, unbounded inbound-request DoS. | `pkg/p2p/p2p.go:554-568` | new |
| P2P | Dandelion++ black-hole first-spy fix only delayed the origin's fluff timer; it didn't change *who* fluffs first. Application-level black-hole (vs TCP error) still deanonymizes the origin. | `pkg/p2p/dandelion.go:18-30,75-116` | confirmed still-open (fix incomplete) |
| PQ layer (regression) | `pqaccum.RestoreState` reads peer-controlled `np`/`n` allocation-size fields off the wire with no bound, now reachable over the network via the new snapshot transport. PoC: <20-byte payload forces >25s of work; larger values can fatal-OOM-crash the process. | `pkg/pqaccum/snapshot.go:67,88`, reached via `pkg/chain/snapshotsync.go` | new (elevated by snapshot-sync) |
| Swap core (regression) | `Coordinator.Resume()` (the crash-resume fix itself) never registers recovered sessions into `c.sessions`. A replayed `Init` for the same SwapID spins up a second, independent session — can double-fund OBX and delete the original recovery record. | `pkg/swapnet/coordinator.go:826-857` vs `:311-356,530-549` | new (regression from the Resume() fix) |
| Swap core | `NanoRPC.Sweep` is not idempotent — a transient RPC failure between the receive and send leg of the maker's automated payout permanently stalls recovery (every retry double-counts and fails identically). | `pkg/swapd/nanorpc.go:478-533` | confirmed still-open |
| Swap core | Taker's XNO has no unilateral recovery if the maker withholds the claim co-signature after the taker locks. Accepted/deferred by design (inherent to scriptless Nano); leg-ordering bounds frequency. | `pkg/swapsession/session.go:297-345,560-587` | confirmed still-open, deferred by design |
| Nano leg | Browser taker (`nanoLockFlow`) locks real XNO sourced from **unconfirmed** Nano receivables with no cementing check — a non-cementing deposit can strand the taker's own confirmed balance behind a dangling frontier. | `pkg/nanorpc/nanorpc.go:288-320`, `website/wallet.html:1112-1131` | new |
| RPC | Unauthenticated `/tx`, `/block?hash=`, `/explorer/block?hash=` trigger a synchronous, uncached backward scan of up to 200,000 blocks (real BoltDB disk reads) for any non-existent hash — cheap amplified disk-I/O DoS. | `pkg/rpc/integrator.go:21-49,113-173` | new |
| Website | Wallet UI has no XSS escaping for node/network-sourced data (offer id/maker, swap-session fields, error text) reaching `innerHTML`. Currently masked by the reference node's own server-side sanitization, but the product explicitly supports pointing at third-party/operator nodes, and CSP allows `'unsafe-inline'` so it provides no backstop. | `website/wallet.html:1448,1634,1644,1679-1694`, `explorer.html:760,776-777` | new |
| Consensus/Chain | `prewarmProofCacheLocked` spawns one unrecovered goroutine per tx for parallel proof pre-validation. A panic anywhere in `validateTxLocked`'s call graph (much larger surface than the one instance already fixed) crashes the entire node — `recover()` in a parent/sibling goroutine cannot catch a child goroutine's panic. | `pkg/chain/validate.go:802-829` | new |
| CLI | `obscura-wallet`'s `--passphrase`/`--new-passphrase` (the at-rest seed encryption key) accepted as CLI flags, same argv/ps/history exposure class as finding #8. | `cmd/obscura-wallet/main.go:41-42,246-254` | new |
| CLI | `obscura-swap live` logs real swap-secret private keys (joint-account recovery halves) to stdout/log — persists indefinitely if redirected to a file for unattended runs. | `cmd/obscura-swap/main.go:365,375-376` | new |

## MEDIUM (selected — full detail in source agent reports)

- Self-discovery Sybil quorum raised 2→4 (confirmed), but vote aging/reachability checks from the original fix plan were never added; `extVotes` also grows unbounded (`pkg/p2p/discovery.go`).
- `/explorer/swaps` does an uncached, unauthenticated 2×1000-block re-scan per request (`pkg/rpc/explorer.go:541-624`).
- Keystore Argon2id bounds (64 iters × up to 4 GiB) can still hang/OOM low-memory/WASM clients on an untrusted file (`pkg/keystore/keystore.go:49-54`).
- `obscura-wallet new`/`restore` defaults to **plaintext** seed storage when no passphrase is supplied — opt-in encryption, not opt-out (`cmd/obscura-wallet/main.go:164-210`).
- Missing overflow guards (`addCheck`) in `FundSwap`/`BuildVaultDeposit` input-sum loops — every other accumulation site in the package is guarded (`pkg/wallet/wallet.go:1187`, `vault.go:89`).
- Watch-only wallets silently can't detect subaddress payments — no runtime warning, balance just understated (`pkg/commit/stealth.go:144-148`).
- TOCTOU: mempool's confirmed-spend recheck only covers `Inputs`/`AnonInputs`, omitting Swap/Vault/ZK/CZK/PQ input kinds enumerated by `spendKeys()` (`pkg/mempool/mempool.go:140-150`).
- Txid malleable for any spend the holder can re-sign — Schnorr nonces (`ProveDLog`) aren't deterministic, so `Hash()` (unlike `CoreHash()`) varies per re-proof; amplifies the `/submittx` DoS (finding #6) (`pkg/commit/rangeproof.go:191-197`, `pkg/tx/tx.go:826-916`).
- `swapbook.Book.Best()` still uses float64 ratio comparison instead of the exact integer cross-multiply (`cmpRate`) used elsewhere in the same package — precision loss above ~9007 XNO/OBX offer units (`pkg/swapbook/swapbook.go:382-398`).
- `install.sh`/`install.ps1` silently skip checksum verification (fail-open) if the checksum-file fetch itself fails, then still auto-run the downloaded binary (`website/install.sh:54,76-79`).
- `--referrer` hex-decode error silently discarded, baking garbage into a permanent on-chain coinbase field with no operator feedback; `--coinbase-maturity` accepted with zero bounds despite being a "must match" network param (`cmd/obscura-node/main.go:298-301,54,96`).
- `Felt2` field-irreducibility check exists only as a unit test (`TestExt2NonResidue`), not a runtime `init()` check — no defense if the non-residue constant is ever miswired (`pkg/stark/ext2.go:19`).
- Miner's `CollectedFees` template helper double-counts PQ-tx fees that consensus excludes — any miner mixing a PQ tx with classical txs self-orphans every such block (`pkg/chain/template.go` vs `validate.go:144-149`).
- `classgroup.Unmarshal` size bound is tied to wire format, not the real discriminant size; `accumulator.RestoreState` uses short `r.Read` instead of `io.ReadFull` (both backstopped by a mandatory post-restore root check, so LOW in practice) (`pkg/group/classgroup.go:321-364`, `pkg/accumulator/snapshot.go`).
- A real `GITHUB_PAT` (Contents:write) sits in plaintext at repo-root `.env`, which is **not** actually gitignored despite memory claiming otherwise (never pushed, but unprotected on disk) — rotate it.

## LOW / INFO (high-value subset; see individual agent reports for full lists)

- `EqualExp` nullifier binding unproven; non-RSA NUMS generators have a publicly-known discrete log — both confirmed unwired/unexploitable today (`pkg/accumulator/nullifier.go`).
- No hash domain separation across Merkle/commitment/nullifier/address uses of `WideHash2` — sound only because of circuit geometry, one composition change from a confusion attack (`pkg/stark/poseidon_wide.go:138`).
- WOTS+ root reuse not prevented at consensus (needs a simultaneous classical-Schnorr break to exploit) (`pkg/chain/pqvalidate.go`).
- `BumpFee` doesn't bind `dest`/`amount` to the original tx (API footgun, not currently triggered by the CLI); taker's claim nonce lacks the durable at-most-one guard the maker side has (`pkg/wallet/wallet.go:896-923`, `pkg/swapsession/session.go:651-663`).
- `obscura-testwallet`'s real-XNO-secret path is hardcoded to one developer's home directory (`cmd/obscura-testwallet/main.go:26`).
- Orphaned marketing copy (`website/assets/showcase.js`) advertises a non-existent "XMR↔OBX swap" feature — currently dead code (not `<script>`'d by any page), but contradicts the live-only-no-fake-claims rule if ever wired up.
- Mempool `Stats()` median fee-rate uses upper-middle element instead of averaging for even pool sizes (cosmetic, observability only).

---

## Fix-first priority (non-invasive where the core packages are protected by CLAUDE.md)

1. **Snapshot-sync trio** (findings #1, #2, and the PQ-layer HIGH): add a cumulative-work comparison gate before adopting any peer-served snapshot, bound `pqaccum`/`accumulator` `RestoreState` allocations, and make reorg-failure recovery snapshot-checkpoint-aware instead of always targeting genesis. These three compound into the single biggest risk surface found — the bootstrap feature built to help new nodes currently has no protection against the peer it's syncing from.
2. **RPC operator-auth model** (#3, #4): stop deriving "operator" trust from `RemoteAddr` alone; require a real token/capability for `/xno/*` and other state-changing routes; either wire `pkg/light` into the wallet's sync path or clearly relabel it unused.
3. **Mempool nullifier unification + `/submittx` cost ordering** (#5, #6): extend the canonical-key unification already done for ZK to `Input.KeyImage`/`AnonInput.Tag`; reorder `Add()` to cheap-check before expensive verification.
4. **Swap relay auth** (#7): stop exposing routable SwapIDs on any public endpoint; give the relay a real per-session secret distinct from the SwapID.
5. **Secrets off the CLI argv** (#8 + the two HIGH CLI items): drop `--mnemonic`/`--nano-fund-secret`/`--passphrase` flag forms in favor of env var/file/stdin-prompt only; stop logging swap recovery keys to the default log stream.
6. **Unrecovered goroutines** (P2P snapshot-export, `prewarmProofCacheLocked`): add `recover()` to both — small, mechanical, closes a full-node-crash class.
7. Everything else in HIGH, then MEDIUM, roughly in the order listed.

No new **mint-from-nothing**, **conservation break**, or **forged ZK proof** was found anywhere in this pass — the value-conservation and proof-soundness core (outside the already-known FRI/Poseidon-parameter caveats requiring external review) held up under deep, partially-empirical (brute-forced/fuzzed) re-verification. The new critical risk this pass surfaced is concentrated in **availability/integrity of node bootstrap and operator-facing RPC**, and in **two recently-shipped features (snapshot fast-sync, swap crash-resume) that each introduced a regression of the severity they were trying to fix.**
