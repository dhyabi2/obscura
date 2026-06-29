# Obscura (OBX) — Mining Guide

Obscura ships a **built-in CPU miner** inside the node binary, so you don't need any external mining software to participate. This guide covers how to mine, where rewards go, and how to run a small multi-node testnet.

> ℹ️ **Obscura mainnet is live.** You can also run a local multi-node testnet for development with the commands below. This is new from-scratch software; review it yourself. See the [README](../README.md) for details.

---

## 1. What you're mining

- **Proof of Work:** ASIC-resistant, CPU-friendly. Mainnet uses canonical, KAT-verified RandomX (the default build) as its memory-hard hash. This keeps mining accessible to ordinary hardware.
- **Block time:** ~120 seconds target, with **LWMA** difficulty retargeting that responds quickly to hashrate changes.
- **Reward:** a smooth, decreasing emission per block with a perpetual tail of ~0.6 OBX/block. A slice of each block reward funds the **holding-incentive pool**, and an optional **referral bonus** can pay a referrer. See [TOKENOMICS.md](TOKENOMICS.md) for the full economic model.

> On a **fresh testnet** the genesis difficulty is intentionally low, so a single CPU finds blocks within seconds. Difficulty auto-adjusts upward as you accumulate blocks — this is expected.

> ⚠️ **Default: auto-liquid mining rewards (ON by default).** By default, a mining node **automatically posts cross-chain swap offers** selling its OBX mining rewards for XNO into the public order book at the prevailing market/seed rate, to bootstrap liquidity and establish a price. It **posts offers** — it does not auto-execute swaps and does not guarantee they fill.
> - **Privacy:** Obscura is a privacy coin. Auto-offering your coinbase output for swap can **link your mined coins to swap activity and a counter-asset (XNO) destination, reducing anonymity**. Privacy-conscious miners should disable this, or first route rewards through the shielded/anon-spend flow before offering.
> - **Financial:** Offers go out at the **prevailing/seed rate**, so you may be taken at an **unfavorable price**, and execution is not guaranteed.
> - **Disable:** run the node with `--no-auto-liquidity`, or set `OBX_AUTO_LIQUIDITY=0`.

---

## 2. The fastest way to start mining

```bash
./bin/obscura-node --mine
```

That's it. The node starts, enables its CPU miner, and (because no `--mine-address` was given) pays rewards to the node's own internal miner wallet, stored at `DATADIR/miner.seed` (default datadir `~/.obscura`).

Check progress from a second terminal:

```bash
./bin/obscura-wallet status
```

You'll see height and supply climb as blocks are found.

---

## 3. Mining to a wallet you control

Most of the time you want rewards to land in a wallet whose seed you hold. Create a wallet, grab its address, and pass it with `--mine-address`:

```bash
./bin/obscura-wallet new --wallet miner.seed     # prints: Address: <hexMiner>
./bin/obscura-node --datadir ./data --mine --mine-address <hexMiner> &
```

Then watch the balance accrue:

```bash
./bin/obscura-wallet balance --wallet miner.seed
```

> **Coinbase maturity** is *defined but not yet enforced*, so mined rewards may appear spendable immediately. Coinbase outputs are intended to mature before they can be spent.

---

## 4. Node flags reference

`obscura-node` is the full node and miner. All flags:

| Flag | Default | Description |
|---|---|---|
| `--datadir DIR` | `~/.obscura` | Data directory for chain, state, and the internal miner wallet. |
| `--p2p ADDR` | `0.0.0.0:18080` | P2P listen address. |
| `--rpc ADDR` | `127.0.0.1:18081` | RPC listen address. |
| `--seeds host:port,...` | (none) | Comma-separated seed peers to connect to on startup. |
| `--mine` | off | Enable the built-in CPU miner. |
| `--mine-address HEX` | node's `DATADIR/miner.seed` | Address (64-byte hex) to pay block rewards to. |
| `--referrer HEX` | (none) | Referrer address tag attached to coinbase for the sharing/referral bonus. |
| `--no-auto-liquidity` | off (auto-liquidity ON) | Disable auto-posting OBX→XNO swap offers for mining rewards. Also settable via `OBX_AUTO_LIQUIDITY=0`. |

---

## 5. The referral / sharing bonus

When you mine, you can tag your coinbase with a **referrer** — the address of whoever recruited you:

```bash
./bin/obscura-node --mine --mine-address <hexMiner> --referrer <hexReferrer>
```

When your blocks land, a **small, capped, decaying** bonus is minted to that referrer, funded from emission. This is the "share" pillar of Obscura's incentive design: it turns existing miners into a distributed growth engine. The bonus is paid only when a referred miner *actually finds blocks*, is capped per referrer, and decays over time so it can't dominate emission or be farmed. Full anti-abuse analysis is in [TOKENOMICS.md](TOKENOMICS.md).

---

## 6. Running a multi-node testnet

To see gossip and chain propagation, run a second node on the same machine with its own data directory, ports, and a `--seeds` entry pointing at the first node's P2P address.

**Terminal 1 — node 1 (mines):**

```bash
./bin/obscura-node \
  --datadir ./data1 \
  --p2p 127.0.0.1:18080 \
  --rpc 127.0.0.1:18081 \
  --mine --mine-address <hexA>
```

**Terminal 2 — node 2 (connects to node 1):**

```bash
./bin/obscura-node \
  --datadir ./data2 \
  --p2p 127.0.0.1:28080 \
  --rpc 127.0.0.1:28081 \
  --seeds 127.0.0.1:18080
```

Now query each node independently by pointing the wallet at the right RPC port:

```bash
./bin/obscura-wallet status --node http://127.0.0.1:18081   # node 1
./bin/obscura-wallet status --node http://127.0.0.1:28081   # node 2
```

Both should converge to the same height as node 1 mines and node 2 receives the blocks over P2P. You can enable `--mine` on node 2 as well to mine competitively.

---

## 7. Accumulator backend (advanced)

The accumulator runs over a **group of unknown order**, and Obscura ships two backends:

- **`rsa2048`** *(default)* — the RSA-2048 challenge modulus. A nothing-up-my-sleeve choice that is **fast**, but secure only while the factorisation stays unknown.
- **`classgroup`** — an imaginary quadratic class group. **No trusted setup at all**, but slower group operations.

The backend is selected in `pkg/config` (`params.go`). For day-to-day mining the default `rsa2048` backend is the fastest choice. See the [whitepaper](../WHITEPAPER.md) and [ARCHITECTURE.md](ARCHITECTURE.md) for the cryptographic detail and trade-offs.

---

## 8. Monitoring while you mine

| Want to see... | Command |
|---|---|
| Height, difficulty, supply, pool, anonymity-set size, mempool | `./bin/obscura-wallet status` |
| Just the height | `curl -s http://127.0.0.1:18081/height` |
| Your mining balance | `./bin/obscura-wallet balance --wallet miner.seed` |

The full set of node endpoints is documented in [RPC.md](RPC.md).

---

## 9. Stopping

If you backgrounded the node with `&`, stop it with `kill %1` (or find the PID and `kill` it). To reset a testnet, delete its data directory (`rm -rf ./data1 ./data2`).
