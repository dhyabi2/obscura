#!/bin/sh
# Optional convenience: mirror the embedded webui assets to the repo-root
# /webui directory for editing. The embedded copy in
# cmd/obscura-dashboard/webui is the source of truth that the binary serves
# (go:embed cannot reach across directories). This script is NOT required to
# build or run the dashboard.
set -e
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
mkdir -p "$ROOT/webui"
cp -f "$HERE/webui/"* "$ROOT/webui/"
echo "Mirrored $HERE/webui -> $ROOT/webui"
