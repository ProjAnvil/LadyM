#!/usr/bin/env bash
# Install the Go ladym binary to ~/.local/bin (the same location uv/pipx use for
# shims, so it takes over `ladym` on PATH once installed).
# Usage: scripts/install.sh [dest-dir]   (default ~/.local/bin)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEST_DIR="${1:-$HOME/.local/bin}"
DEST="$DEST_DIR/ladym"

mkdir -p "$DEST_DIR"

echo ">> building ladym from $REPO_ROOT"
tmp="$(mktemp -t ladym-build)"
trap 'rm -f "$tmp"' EXIT
(cd "$REPO_ROOT" && go build -trimpath -o "$tmp" ./cmd/ladym)

mv -f "$tmp" "$DEST"
chmod +x "$DEST"

echo ">> installed: $DEST"
"$DEST" --help >/dev/null && echo ">> smoke check ok: $("$DEST" stats --help | head -1)"
echo ">> which ladym: $(command -v ladym || echo '(not on PATH; add '"$DEST_DIR"' to your PATH)')"
