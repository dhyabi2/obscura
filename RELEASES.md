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
e677017440e1ae85ce13abe5f3391961135befb4ce3d2866d07dcee8431fe432  Obscura-darwin-amd64.zip
43b7ec69cc2e208ade15348bb71f4030548d26eb479117c19798e3a08fc39c29  Obscura-darwin-arm64.zip
a224eeadeaf6bbc3aee99bf0e7e2dcf179be11b1188d210a0a08ee82d0e9ceb9  Obscura-windows-amd64.zip
0b1cd950c28b28ea6a51ddde66022ff50cfeddc08ec00a3e863671c5a041de8b  Obscura-windows-arm64.zip
8ef4a9b0288e6386c8b47253156237f84daeb45f5d18a283e2841f9538591909  Obscura-linux-amd64.tar.gz
eee53d72151fb1c68d7060c0af5901796379c7fa3569f5dbf951b3e66048e4b6  Obscura-linux-arm64.tar.gz
```

_macOS:_ the build isn't Apple-signed; clear the quarantine flag after download —
`xattr -dr com.apple.quarantine Obscura-darwin-arm64.app` — then open it.
