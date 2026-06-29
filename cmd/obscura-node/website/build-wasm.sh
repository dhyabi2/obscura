#!/usr/bin/env bash
# Build the browser (WASM) Obscura wallet and stage the runtime shim. The wallet
# runs entirely client-side (non-custodial); the page talks to a node via the
# Vercel proxy (api/explorer.js). Run from the repo root or anywhere.
set -euo pipefail
cd "$(dirname "$0")/.."

echo "== building website/wallet.wasm =="
GOOS=js GOARCH=wasm go build -trimpath -o website/wallet.wasm ./cmd/obscura-wasm

echo "== staging wasm_exec.js =="
GOROOT="$(go env GOROOT)"
if [ -f "$GOROOT/lib/wasm/wasm_exec.js" ]; then
  cp "$GOROOT/lib/wasm/wasm_exec.js" website/wasm_exec.js
else
  cp "$GOROOT/misc/wasm/wasm_exec.js" website/wasm_exec.js
fi

echo "== done: $(ls -la website/wallet.wasm | awk '{print $5}') bytes =="
