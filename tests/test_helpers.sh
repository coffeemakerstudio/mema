#!/usr/bin/env bash
set -euo pipefail

ROOT=$(realpath "$(dirname "$0")/..")
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
export PATH="$ROOT/core:$PATH"
export MEMA_CACHE="$TMP/cache"

printf 'verified content\n' > "$TMP/file"
hash=$(sha256sum "$TMP/file" | cut -d ' ' -f 1)
mema_verify "$TMP/file" "$hash"

if mema_verify "$TMP/file" "not-a-hash" 2>/dev/null; then
    printf 'mema_verify accepted an invalid hash\n' >&2
    exit 1
fi
if mema_download "http://example.invalid/file" "file" "$hash" 2>/dev/null; then
    printf 'mema_download accepted an HTTP URL\n' >&2
    exit 1
fi

printf '%s\n' '--- PASS: helper validation tests ---'
