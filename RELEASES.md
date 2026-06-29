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
663a2b8c09a92419b2dee98fd79c38f5c5c19dc46398e546ff533d955384ac5b  Obscura-darwin-amd64.zip
355d06f151e3f32096245765b61f445d726a7bafe51d6da7e3b7d2449dca7593  Obscura-darwin-arm64.zip
659670343efb9ba51284a2978e784657b47823b34d74f528d55e2394d4ba5289  Obscura-windows-amd64.zip
6b7b1870e6555345a038c2ee7911df04f034c6fded7140b7c49ba16e24d20048  Obscura-windows-arm64.zip
75545854ba88f3ba0cdefbc5dff3b49d99389a1db0e2204fd7daf353af46319d  Obscura-linux-amd64.tar.gz
9829831e320c541c251bbfe3a8ca072fb3f0262cb1d22601d61f6ddbe748455c  Obscura-linux-arm64.tar.gz
```

_macOS:_ the build isn't Apple-signed; clear the quarantine flag after download —
`xattr -dr com.apple.quarantine Obscura-darwin-arm64.app` — then open it.
