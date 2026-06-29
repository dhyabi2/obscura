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
72993aef07b4257cfa6dd68b16e505bf5ec943bfd850e6aabf05ba4afe1f3d26  Obscura-darwin-amd64.zip
c83bc6e3674453986d37bc4da23ad0e8a29bdea8dc718204b52c68e1dbdbfb7b  Obscura-darwin-arm64.zip
cc6bf06c284ff87dbf9a7dc8d49f0928c3f28db9b3179e7371e4ceb5ce4d191c  Obscura-windows-amd64.zip
d288f471c7e9f21c3887526a4b4b73297a0f285768d31cdfa0780effe1afe4f5  Obscura-windows-arm64.zip
01ffb89cf7142c1a283146a3958842cd56d24d7dc11ad03589db50dabb48fcf9  Obscura-linux-amd64.tar.gz
d03c818e68065047890c0ab34c3297f4d32a9067bf27dd6af324fa8a64cbfe06  Obscura-linux-arm64.tar.gz
```

_macOS:_ the build isn't Apple-signed; clear the quarantine flag after download —
`xattr -dr com.apple.quarantine Obscura-darwin-arm64.app` — then open it.
