#!/usr/bin/env sh
# Obscura (OBX) installer / upgrader — Linux & macOS.
#
# Run it once to install + start a full node + miner. Run the SAME command again
# any time to upgrade: it notices a node is already running, checks the published
# release for a newer build, ASKS before doing anything, replaces the binary, and
# restarts it. Your wallet/miner keys live in ~/.obscura and are NEVER touched by
# an upgrade — only the program binary is replaced.
#
#   curl -fsSL https://obscura-blush.vercel.app/install.sh | sh
#
# Pass node flags after `-s --` (defaults: --mine --seeds <mainnet seed>):
#   curl -fsSL https://obscura-blush.vercel.app/install.sh | sh -s -- --mine --seeds 167.172.56.34:18080
#
# Env: OBX_DATADIR overrides the key/data directory (default ~/.obscura).
set -eu

REPO="dhyabi2/obscura"
TAG="v1.0.0"
BASE="https://github.com/$REPO/releases/download/$TAG"
DATADIR="${OBX_DATADIR:-$HOME/.obscura}"
MARKER="$DATADIR/.installed-sha"
DEFAULT_ARGS="--mine --seeds 167.172.56.34:18080"

os="$(uname -s)"; arch="$(uname -m)"
case "$os-$arch" in
  Linux-x86_64|Linux-amd64)  ASSET="Obscura-linux-amd64.tar.gz"; DIR="Obscura-linux-amd64";      BIN="$DIR/obscura-node";                KIND=tar ;;
  Linux-aarch64|Linux-arm64) ASSET="Obscura-linux-arm64.tar.gz"; DIR="Obscura-linux-arm64";      BIN="$DIR/obscura-node";                KIND=tar ;;
  Darwin-arm64)              ASSET="Obscura-darwin-arm64.zip";   DIR="Obscura-darwin-arm64.app"; BIN="$DIR/Contents/MacOS/obscura-node"; KIND=zip ;;
  Darwin-x86_64)             ASSET="Obscura-darwin-amd64.zip";   DIR="Obscura-darwin-amd64.app"; BIN="$DIR/Contents/MacOS/obscura-node"; KIND=zip ;;
  *) echo "Obscura: unsupported platform $os-$arch" >&2; exit 1 ;;
esac

# Published SHA-256 of this asset — the source of truth for "is a new build out?".
pub_sha() { curl -fsSL "$BASE/SHA256SUMS.txt" 2>/dev/null | awk -v a="$ASSET" '$2==a{print $1}'; }
sha_of()  { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi; }
short()   { printf '%.12s' "$1"; }

# Ask y/N on the controlling terminal, even when this script is piped into `sh`.
# If there is no usable terminal to confirm on (CI / cron / no tty) we DO NOT
# auto-replace a running node unattended — we skip and ask the user to re-run
# interactively. A real `curl ... | sh` in a terminal can still reach /dev/tty.
ask() {
  a=""
  if { true >/dev/tty; } 2>/dev/null; then
    printf '%s [y/N] ' "$1" > /dev/tty
    read -r a < /dev/tty 2>/dev/null || a=""
  else
    echo "  (no terminal available to confirm — re-run in an interactive shell to upgrade)" >&2
  fi
  case "$a" in y*|Y*) return 0 ;; *) return 1 ;; esac
}

PUB="$(pub_sha || true)"
PID="$(pgrep -x obscura-node 2>/dev/null | head -n1 || true)"
HAVE="$( [ -f "$MARKER" ] && cat "$MARKER" || true )"

if [ -n "$PID" ]; then
  echo "Obscura node already running (pid $PID), keys in $DATADIR."
  if [ -n "$PUB" ] && [ "$PUB" = "$HAVE" ]; then
    echo "Already on the latest published build ($(short "$PUB")…). Nothing to do."
    exit 0
  fi
  echo "A newer published build is available."
  ask "Upgrade now? Your keys in $DATADIR are preserved (only the binary is replaced)." \
    || { echo "Upgrade skipped — the running node is untouched."; exit 0; }
  echo "Stopping the running node (keys untouched)…"
  kill "$PID" 2>/dev/null || true
  i=0; while kill -0 "$PID" 2>/dev/null && [ "$i" -lt 24 ]; do sleep 0.5; i=$((i+1)); done
  kill -9 "$PID" 2>/dev/null || true
fi

tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
echo "Downloading $ASSET …"
curl -fL "$BASE/$ASSET" -o "$tmp/$ASSET"
if [ -n "$PUB" ]; then
  got="$(sha_of "$tmp/$ASSET")"
  [ "$got" = "$PUB" ] || { echo "Obscura: checksum mismatch (got $(short "$got")…, want $(short "$PUB")…) — aborting." >&2; exit 1; }
  echo "Checksum verified ($(short "$PUB")…)."
fi
echo "Unpacking into $(pwd)/$DIR …"
rm -rf "$DIR"
case "$KIND" in
  tar) tar xzf "$tmp/$ASSET" ;;
  zip) unzip -oq "$tmp/$ASSET" ;;
esac
chmod +x "$BIN" 2>/dev/null || true
[ "$os" = Darwin ] && { xattr -dr com.apple.quarantine "$DIR" 2>/dev/null || true; }

mkdir -p "$DATADIR"
[ -n "$PUB" ] && printf '%s\n' "$PUB" > "$MARKER" || true

ARGS="$*"; [ -n "$ARGS" ] || ARGS="$DEFAULT_ARGS"
echo "Starting: ./$BIN $ARGS"
if command -v setsid >/dev/null 2>&1; then
  setsid "./$BIN" $ARGS > obscura.log 2>&1 < /dev/null &
else
  nohup "./$BIN" $ARGS > obscura.log 2>&1 < /dev/null &
fi
echo "Obscura node started (pid $!). Logs: $(pwd)/obscura.log"
echo "Watch it:  tail -f $(pwd)/obscura.log"
