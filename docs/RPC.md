# Obscura (OBX) — RPC API Reference

The `obscura-node` binary exposes a small **JSON-over-HTTP** API for querying chain state and submitting transactions. The CLI wallet uses this same API, and you can use it directly to build tooling, explorers, or monitoring.

> ⚠️ **Bind locally.** The RPC server binds to `127.0.0.1` by default and is intended for local use. Do not expose it to a public network. See the [README](../README.md) for details.

---

## Base URL

By default the node serves RPC at:

```
http://127.0.0.1:18081
```

Change the bind address with the node's `--rpc ADDR` flag. All endpoints below are relative to this base URL.

- Requests/responses are **JSON**.
- All endpoints are **GET** unless noted otherwise.
- Hex fields are lowercase hex strings.
- Amounts ending in `_atomic` are in **atomic units** (`1 OBX = 10¹²` atomic units); `_obx` fields are the same value rendered as decimal OBX.

---

## Endpoints

### `GET /status`

Returns a snapshot of node and chain state.

**Response fields**

| Field | Type | Meaning |
|---|---|---|
| `coin` | string | Coin name (`Obscura`). |
| `ticker` | string | Ticker (`OBX`). |
| `height` | number | Current chain height. |
| `difficulty` | number | Current PoW difficulty. |
| `emitted_atomic` | number/string | Total supply emitted, in atomic units. |
| `emitted_obx` | string | Total supply emitted, in decimal OBX. |
| `incentive_pool_atomic` | number/string | Balance of the holding-incentive pool, in atomic units. |
| `accumulator_size` | number | Size of the accumulator — i.e. the **anonymity-set** size (number of accumulated outputs). |
| `accumulator_backend` | string | Active accumulator backend (`rsa2048` or `classgroup`). |
| `mempool_size` | number | Number of unconfirmed transactions in the mempool. |

**Example**

```bash
curl -s http://127.0.0.1:18081/status
```

```json
{
  "coin": "Obscura",
  "ticker": "OBX",
  "height": 42,
  "difficulty": 12345,
  "emitted_atomic": 21000000000000,
  "emitted_obx": "21.000000000000",
  "incentive_pool_atomic": 1050000000000,
  "accumulator_size": 42,
  "accumulator_backend": "rsa2048",
  "mempool_size": 0
}
```

---

### `GET /height`

Returns just the current chain height — a cheap endpoint for polling.

**Response**

```json
{ "height": 42 }
```

**Example**

```bash
curl -s http://127.0.0.1:18081/height
```

---

### `GET /accvalue`

Returns the current **accumulator checkpoint** value (the single group element that accumulates all unspent outputs), hex-encoded.

**Response**

```json
{ "accvalue": "a1b2c3..." }
```

**Example**

```bash
curl -s http://127.0.0.1:18081/accvalue
```

---

### `GET /witness?prime=HEX`

Returns a hex-encoded **membership witness** for the accumulated prime supplied in the `prime` query parameter. The witness `w` satisfies `w^prime = accvalue`.

**Query parameters**

| Parameter | Type | Description |
|---|---|---|
| `prime` | hex | The prime representative of the output whose witness you want. |

**Response**

```json
{ "witness": "deadbeef..." }
```

**Example**

```bash
curl -s "http://127.0.0.1:18081/witness?prime=00ff..."
```

> 🔒 **Privacy caveat.** Asking the node for a witness *reveals your interest in that specific output to the node*. Production wallets should compute witnesses **client-side** from public block data rather than querying this endpoint. It exists for convenience and light-client experimentation.

---

### `GET /block?height=N`

Returns the block at height `N`, hex-serialized.

**Query parameters**

| Parameter | Type | Description |
|---|---|---|
| `height` | number | The block height to fetch. |

**Response**

```json
{ "block": "0102abcd..." }
```

**Example**

```bash
curl -s "http://127.0.0.1:18081/block?height=10"
```

---

### `POST /submittx`

Submits a serialized transaction to the node. The node validates it (proofs, conservation, nullifier-freshness) and, if valid, places it in the mempool for gossip and mining.

**Request body**

```json
{ "tx": "<hex-serialized transaction>" }
```

**Response**

```json
{ "txid": "..." }
```

The returned `txid` identifies the accepted transaction. (A validation failure is returned as an HTTP error rather than a `txid`.)

**Example**

```bash
curl -s -X POST http://127.0.0.1:18081/submittx \
  -H 'Content-Type: application/json' \
  -d '{"tx":"0102abcd...deadbeef"}'
```

> In normal use you don't build raw transactions by hand — `obscura-wallet send` constructs the confidential transaction and calls this endpoint for you. `/submittx` is here for tooling and integration.

---

## Endpoint summary

| Method | Path | Purpose |
|---|---|---|
| GET | `/status` | Full node/chain status snapshot. |
| GET | `/height` | Current chain height only. |
| GET | `/accvalue` | Current accumulator checkpoint (hex). |
| GET | `/witness?prime=HEX` | Membership witness for a prime (hex). *Privacy caveat.* |
| GET | `/block?height=N` | Hex-serialized block at height `N`. |
| POST | `/submittx` | Submit a serialized transaction; returns `txid`. |

---

## Notes & related docs

- The wallet talks to these endpoints via `--node URL` (default `http://127.0.0.1:18081`).
- The server lives in `pkg/rpc/server.go`; a Go client is in `pkg/rpc/client.go`.
- For how a transaction is built before it reaches `/submittx`, see [ARCHITECTURE.md](ARCHITECTURE.md) (transaction lifecycle).
- For the cryptography behind the accumulator and witnesses, see the [WHITEPAPER.md](../WHITEPAPER.md).
