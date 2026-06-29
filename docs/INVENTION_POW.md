# Invention Log — Block 6: ASIC-Resistant Proof-of-Work

Produced with the invention methodology + Methodology-Tree brainstorming engine.

## 1. Challenge
The PoW was a hand-rolled blake2b scratchpad — unvetted and weakly ASIC/GPU
resistant. We want a strong, memory-hard PoW implementable in **pure Go** (no
cgo, no VM), with cheap single-hash verification.

## 2. Brainstormed options (engine) + ranking
Engine ranking:
1. **Argon2id (RFC 9106)** — BEST. Pure Go (`x/crypto/argon2`), memory-hard,
   tunable memory, fast single-hash verify. ASIC must pay for memory bandwidth.
2. scrypt — pure Go but ASIC-broken at small memory; only if tiny memory is
   mandatory.
3. Yescrypt — better TMTO resistance but **no pure-Go impl** (infeasible).
4. Cuckoo Cycle — ASIC-friendly edge-trimming, poor pure-Go verify.
5. RandomX / ProgPoW — strongest/ideal but need a VM/JIT or GPU DAG; **no pure
   Go** (cgo/VM banned here).

→ **ADOPT Argon2id.** It is the only constraint-satisfying, production-grade,
tunable memory-hard option with fast verification. (RandomX remains the
long-term target if a vetted Go binding becomes acceptable.)

## 3. Decision & implementation (`pkg/pow/pow.go`)
`Hash(input) = Argon2id(input, salt, t, m, p)[:32]`, with parameters as package
vars so the opt-in light backend uses a small memory cost (`MemoryKiB = 1 MiB`,
`t=1`, `p=1`) for fast block discovery on local dev chains, while production raises `MemoryKiB` to
**256 MiB–1 GiB** via a network upgrade for a real memory-bandwidth wall.
Verification is one Argon2id call (~0.7 ms at the prototype setting). The
`Hash`/`Meets`/`Target` interface is unchanged, so consensus and the miner were
untouched.

Risk (engine-flagged): Argon2id has time-memory tradeoffs and custom-HBM ASIC
risk if memory cost is too low — mitigated by setting production memory high and
being able to bump `MemoryKiB` via upgrade if hashrate centralizes.

Tested: existing mining-heavy suites (consensus, fork choice, anon-chain) pass
unchanged; a microbenchmark confirms ~0.7 ms/hash at the prototype setting.

---

## 4. Upgrade — RandomX-style VM PoW (`pkg/pow/randomx.go`)

Argon2id is memory-hard but it is a *fixed* computation — an ASIC still runs the
same dataflow every time. RandomX's deeper insight is **compute diversity**:
generate a *different random program* per nonce and run it on a small VM, so an
ASIC would have to be a general-purpose CPU to be efficient. We graduated the PoW
from Argon2id to a RandomX-architecture VM, in pure Go, behind the same
`Hash()` interface (consensus/miner untouched).

**What it does (the RandomX architecture, not the Monero byte-spec):**
- **Seed-derived cache** — a shared buffer built with **Argon2d over `RxCacheKiB`**
  of memory (so even building the dataset is memory-hard), keyed by `SeedKey`,
  memoized and reused across nonces (amortized over a mining search, like
  RandomX's cache/dataset).
- **Per-nonce scratchpad** — a large buffer the program reads/writes at
  register-derived addresses, making each hash **latency-bound on memory**.
- **Randomized register VM** — 8 integer + 8 float registers; a program of
  `RxProgramSize` instructions generated from the nonce, executed for
  `RxIterations` rounds. Ops mix `add/sub/mul/xor/rotate/mulhi`, **floating-point**
  `add/sub/mul/div/sqrt`, scratchpad load/store, cache loads, and a data-dependent
  branch — the exact ASIC-hostile blend RandomX uses.
- **Finalize** — fold the whole scratchpad + all registers through BLAKE2b-256.

**Determinism (consensus-critical):** only IEEE-754 `+ − × ÷` and correctly-rounded
`sqrt` are used (no FMA, no transcendentals), and every float is bit-masked finite,
so the hash is identical on amd64/arm64 and every node. Verified: deterministic,
full 32/32-byte avalanche, ~1.9 kH/s (~0.54 ms/hash) at prototype params — faster
than the old Argon2id. Tests in `tests/critical/pow/`.

**Honesty / scope:** this is the RandomX *architecture*, not a byte-for-byte
Monero RandomX (which needs the full 2 GiB dataset, AES generator, and official
test vectors). It is not interchange-compatible with Monero. Params are vars
(prototype: 64 KiB scratch, 256 KiB cache, 64 inst, 256 iters); production raises
them (e.g. 2 MiB / 256 MiB / 2048) via upgrade. This is the **default** backend
(light/fast); canonical RandomX is now available behind a build tag (§6).

## 6. Canonical RandomX backend (Monero-compatible, opt-in)

The PoW now dispatches through a **backend selector** so the canonical algorithm
can be wired in without touching consensus:

- **`backend_vm.go`** (`//go:build !randomx`, default) → the RandomX-style VM above.
- **`backend_randomx.go`** (`//go:build randomx`) → **canonical RandomX** via the
  pure-Go P2Pool port `git.gammaspectra.live/P2Pool/go-randomx/v3` (no cgo — it
  ships a `softfloat64` for cross-platform-deterministic floats, so it stays
  CGO-free and still cross-compiles to a static `.exe`).

`HashSeed(seed, input)` maps directly onto RandomX's `cache.Init(key)` +
`CalculateHash(input)` — so **Obscura's per-epoch seed IS the RandomX key**, and the
§5 epoch rotation works with the canonical backend unchanged. The 256 MiB cache is
memoized per epoch seed (small LRU); a fresh light-mode VM per call keeps it
concurrency-safe.

**Proof it's real RandomX:** `randomx_kat_test.go` (built by default, constraint
`!protopow`) runs the official RandomX known-answer vectors through `pow.HashSeed` — e.g.
`("test key 000","This is a test") → 639183aae1bf4c9a35884cb46b09cad9175f04efd7684e7262a0ac1c2f0b4e3f` —
and they pass byte-for-byte.

Plain `go build` (no tags) gives canonical RandomX; `pow.BackendName`, `wallet status`,
and the node startup log report the active backend. The insecure prototype VM is opt-in
via `-tags protopow` (light enough for fast dev/test iteration) and refuses to start a
node without `OBX_ALLOW_PROTOTYPE_POW=1`. Switching backends
changes the PoW hash → it is a hard fork; pick one per network. Keep the
`go-randomx` require in `go.mod` (the default canonical backend depends on it).

## 5. Epoch seed rotation (Monero-style reseed)

The PoW cache is now **re-keyed periodically from confirmed chain history**, exactly
as Monero reseeds RandomX every ~2048 blocks. This stops a miner from amortizing one
cache forever and ties the PoW to the live chain.

- `config.PoWSeedHeight(h)` = the height of the block whose id seeds block `h`'s
  cache: `((h − PoWSeedLag) / PoWEpochLen) × PoWEpochLen`, with **epoch 0** using a
  fixed `PoWGenesisSeed` constant (so early blocks need no lookup).
- The seed is taken **`PoWSeedLag` blocks in the past** (grind-proof: a miner can't
  influence a confirmed past block id) and, by the invariant **`PoWSeedLag ≥
  MaxReorgDepth`**, always lies in the unreorganizable common prefix — so every node
  derives the *same* seed regardless of branch.
- `chain.PoWSeed(height)` / `powSeedLocked` supply the seed; `block.PoWHashSeed(seed)`
  and `pow.HashSeed(seed,·)` compute under it; `miner.MineSeed` grinds under it. The
  cache layer memoizes a small LRU of seed-keyed caches so the boundary (where two
  epochs are briefly live) is cheap.
- Validation (`validateHeader`), the anti-spam check (`addBlockLocked`), SPV
  (`light.VerifyHeaderChain`), the node miner, and the `/blocktemplate` RPC (which now
  returns the `seed`) all use the per-epoch seed. Tests: `tests/critical/epoch/`
  mines a chain *across* a boundary (proving miner and validator agree on the rotated
  seed), checks the seed equals the seed-height block id, and that the seed genuinely
  changes the PoW hash.
