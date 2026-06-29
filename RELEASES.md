# Obscura releases

**Version 1.0.0** — single static binaries, pure Go, no runtime dependencies.
Canonical (KAT-verified) RandomX PoW. Built from this repository.

## Downloads

Download the binaries from the [**GitHub release v1.0.0**](https://github.com/dhyabi2/obscura/releases/tag/v1.0.0) (mirrored at <https://obscura-blush.vercel.app/download>).

**One command — install a node + miner that joins mainnet, and re-run to upgrade.**
Re-running the same command detects the running node, checks for a newer published
build, asks to confirm, verifies its SHA-256, replaces **only** the binary, and
restarts — your keys in `~/.obscura` (Windows: `%USERPROFILE%\.obscura`) are untouched.

```sh
# Linux / macOS
curl -fsSL https://obscura-blush.vercel.app/install.sh | sh

# Windows (PowerShell)
iwr -useb https://obscura-blush.vercel.app/install.ps1 | iex
```

Manual alternative (no upgrade logic):

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
4e9b7a493792d82e40f1701de12bbb800bc9210c4fe8c8554778d9c84931b791  Obscura-darwin-amd64.zip
80b22c7550596fdcbd643488efb11a8fda5e3d9a4f933854f0e6447ac0019db5  Obscura-darwin-arm64.zip
f837b5b0ecfbc60102932ea86a195e8cc99299ea844b41ce9ce2d4302cf643bc  Obscura-windows-amd64.zip
65e604a49ef2c9e775cc229b55bddd21631868e982926f39e254dbc7707f8865  Obscura-windows-arm64.zip
eaef3351b22a7b457cb8bf95da146c6ea33488b06e035256132c37c968fe9614  Obscura-linux-amd64.tar.gz
d0562026131dee7a48599fba27df5c99b6f24123adeacf4cb42f3ab30a3d7f17  Obscura-linux-arm64.tar.gz
```

_macOS:_ the build isn't Apple-signed; clear the quarantine flag after download —
`xattr -dr com.apple.quarantine Obscura-darwin-arm64.app` — then open it.
