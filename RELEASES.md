# Obscura releases

**Version 1.0.0** — single static binaries, pure Go, no runtime dependencies.
Canonical (KAT-verified) RandomX PoW. Built from this repository.

## Downloads

Download the binaries from the [**GitHub release v1.0.0**](https://github.com/dhyabi2/obscura/releases/tag/v1.0.0) (mirrored at <https://obscura-blush.vercel.app/download>).

**Linux — one command (download + run a node + miner that joins mainnet):**

```sh
curl -fL https://github.com/dhyabi2/obscura/releases/download/v1.0.0/Obscura-linux-amd64.tar.gz | tar xz \
  && ./Obscura-linux-amd64/obscura-node --mine --seeds 167.172.56.34:18080
```

| Platform | File |
| --- | --- |
| macOS (Apple Silicon) | [`Obscura-darwin-arm64.zip`](https://github.com/dhyabi2/obscura/releases/download/v1.0.0/Obscura-darwin-arm64.zip) |
| macOS (Intel) | [`Obscura-darwin-amd64.zip`](https://github.com/dhyabi2/obscura/releases/download/v1.0.0/Obscura-darwin-amd64.zip) |
| Linux (x86-64) | [`Obscura-linux-amd64.tar.gz`](https://github.com/dhyabi2/obscura/releases/download/v1.0.0/Obscura-linux-amd64.tar.gz) |
| Linux (ARM64) | `Obscura-linux-arm64.tar.gz` |
| Windows (x86-64) | `Obscura-windows-amd64.zip` |
| Windows (ARM64) | `Obscura-windows-arm64.zip` |

## SHA-256 checksums

Verify your download before running it (`shasum -a 256 -c SHA256SUMS.txt`):

```
0aacd6bfe8a251aed0469565a1afdc04b3df6c94a3a5a13ca5f27db38d9e051f  Obscura-darwin-amd64.zip
277aa7f2ba527b9d8dc3cb0a027786fca1f59326af5a1c717b61086ef9ab1e2a  Obscura-darwin-arm64.zip
f07225ddf17cd05e6f88ea7ed637b169d97e891b56e92d20037efd7770c747ac  Obscura-windows-amd64.zip
56fdc862a2b136833415a4315afccd19911cd6419167c578a46a49b075558446  Obscura-windows-arm64.zip
20f588d98391e92de3ad4f235f93ba506e6cd2d787eddbedaa87984931c7c107  Obscura-linux-amd64.tar.gz
e0d16cf39da426a51cd914ba5d86497d3dc112158ac8592d7aac7fe077ca60dd  Obscura-linux-arm64.tar.gz
```

_macOS:_ the build isn't Apple-signed; clear the quarantine flag after download —
`xattr -dr com.apple.quarantine Obscura-darwin-arm64.app` — then open it.
