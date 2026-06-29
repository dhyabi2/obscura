# Obscura (OBX) — Quickstart

This guide takes you from a fresh checkout to a **confidential transfer** between two wallets on a node you run yourself. You can follow it fully offline against a private single-node chain, or point your node at live Obscura mainnet peers.

> ⚠️ **Obscura mainnet is live.** It has not had an external third-party audit, so this is new software: review it and understand the risks before committing real value. See the [README](../README.md) and [WHITEPAPER](../WHITEPAPER.md) for the full notes.

---

## 1. Prerequisites

- **Go 1.24+** (the module targets Go 1.25). Check with `go version`.
- A terminal. The examples assume macOS/Linux; on Windows use the `.exe` binaries and adapt the shell syntax.

---

## 2. Build the binaries

From the repository root:

```bash
go build -o bin/obscura-node   ./cmd/obscura-node
go build -o bin/obscura-wallet ./cmd/obscura-wallet
```

You now have `bin/obscura-node` (full node + miner) and `bin/obscura-wallet` (CLI wallet).

> To produce static binaries for every platform at once, run `./build.sh` (outputs to `dist/`). A `Makefile` with `build`, `test`, `release`, and `clean` targets is also available.

---

## 3. Create two wallets

We'll make a sender (**A**) and a recipient (**B**). Each `new` prints an address and writes a hex **seed file**.

```bash
./bin/obscura-wallet new --wallet A.seed     # prints: Address: <hexA>
./bin/obscura-wallet new --wallet B.seed     # prints: Address: <hexB>
```

Copy the two printed addresses somewhere handy; we'll refer to them as `<hexA>` and `<hexB>`.

> 🔑 **Back up your seed files.** `A.seed` and `B.seed` *are* the wallets. Anyone with a seed controls those funds, and losing a seed loses the funds permanently. There is no recovery.

You can reprint an address at any time:

```bash
./bin/obscura-wallet address --wallet A.seed
```

---

## 4. Start a mining node

Start a node, point it at a fresh data directory, and have it **mine to wallet A**:

```bash
./bin/obscura-node --datadir ./data --mine --mine-address <hexA> &
```

What this does:

- `--datadir ./data` keeps this node's chain and state in `./data`.
- `--mine` turns on the built-in CPU miner.
- `--mine-address <hexA>` pays block rewards to wallet A. (Without it, the node mines to its own internal miner wallet at `DATADIR/miner.seed`.)

The node listens for P2P on `0.0.0.0:18080` and serves RPC on `127.0.0.1:18081` by default. On a fresh testnet the genesis difficulty is intentionally low, so a single CPU starts finding blocks within seconds; difficulty then auto-adjusts upward.

For more on mining (flags, multi-node setups, referral tags), see [MINING.md](MINING.md).

---

## 5. Watch the chain grow

```bash
./bin/obscura-wallet status
```

This prints node status — height, difficulty, total supply, the incentive-pool balance, the anonymity-set (accumulator) size, and mempool size. Run it a few times and you'll see the height climb and supply grow as blocks are mined.

---

## 6. Check wallet A's balance

```bash
./bin/obscura-wallet balance --wallet A.seed
```

This rescans the chain and prints A's balance plus its spendable-output count. Once a few blocks have been mined to A, you'll see a positive OBX balance.

> **Note:** coinbase maturity is *defined but not yet enforced* in this prototype, so freshly mined rewards may show as spendable immediately. On a production chain newly mined coins would need to mature first.

---

## 7. Send a confidential transaction

Send 5 OBX from A to B, with a 0.001 OBX fee:

```bash
./bin/obscura-wallet send --wallet A.seed --to <hexB> --amount 5 --fee 0.001
```

This builds a **confidential transaction** (hidden amount via Pedersen commitment + range proof, accumulator membership spend, conservation proof) and broadcasts it to the node, which validates it and places it in the mempool. The amounts are decimal **OBX**; `--fee` is optional and defaults to a small fee if omitted.

Now wait for the **next block** to be mined (watch `status` for the height to advance by one), so the transaction is confirmed.

---

## 8. Confirm B received the coins

```bash
./bin/obscura-wallet balance --wallet B.seed
```

Wallet B now shows **5 OBX** received. That completes a full end-to-end private transfer on your local Obscura testnet.

---

## 9. Common options

Every wallet subcommand accepts:

| Flag | Default | Meaning |
|---|---|---|
| `--wallet FILE` | `~/.obscura/wallet.seed` | path to the wallet seed file |
| `--node URL` | `http://127.0.0.1:18081` | RPC endpoint of the node to talk to |

Wallet subcommands: `new`, `address`, `balance`, `send`, `status`.

---

## 10. Where to go next

- **[MINING.md](MINING.md)** — node flags, multi-node testnets, mining to a specific address, referral tags.
- **[RPC.md](RPC.md)** — the JSON-over-HTTP API for building tooling.
- **[ARCHITECTURE.md](ARCHITECTURE.md)** — how the system is put together.
- **[TOKENOMICS.md](TOKENOMICS.md)** — emission and the incentive design.
- **[WHITEPAPER.md](../WHITEPAPER.md)** — the full protocol and its honest security status.

---

## Cleanup

To reset your local testnet, stop the node (`kill %1` if you backgrounded it) and delete the data directory:

```bash
rm -rf ./data A.seed B.seed
```
