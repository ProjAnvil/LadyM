#!/usr/bin/env bash
# Install the Go ladym binary to ~/.local/bin (the same location uv/pipx use for
# shims, so it takes over `ladym` on PATH once installed).
# Usage: scripts/install.sh [dest-dir]   (default ~/.local/bin)
# Set LADYM_BUILD_TAGS to add build tags, e.g. LADYM_BUILD_TAGS=fulldict
# embeds the CJK dictionary (~+31MB binary).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEST_DIR="${1:-$HOME/.local/bin}"
DEST="$DEST_DIR/ladym"

mkdir -p "$DEST_DIR"

GO_BUILD_TAGS="${LADYM_BUILD_TAGS:-}"

echo ">> building ladym from $REPO_ROOT${GO_BUILD_TAGS:+ (tags: $GO_BUILD_TAGS)}"
tmp="$(mktemp -t ladym-build)"
trap 'rm -f "$tmp"' EXIT
# -tags with no inner quotes: LADYM_BUILD_TAGS never contains spaces, so the
# unquoted ${...:+...} word-splits into the two go-build arguments "-tags" and
# the tag list (and to nothing when unset/empty).
(cd "$REPO_ROOT" && go build -trimpath ${GO_BUILD_TAGS:+-tags $GO_BUILD_TAGS} -o "$tmp" ./cmd/ladym)

mv -f "$tmp" "$DEST"
chmod +x "$DEST"

echo ">> installed: $DEST"
"$DEST" --help >/dev/null && echo ">> smoke check ok: $("$DEST" stats --help | head -1)"
echo ">> which ladym: $(command -v ladym || echo '(not on PATH; add '"$DEST_DIR"' to your PATH)')"
