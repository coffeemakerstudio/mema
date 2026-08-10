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

mkdir -p "$TMP/bin"
printf '%s\n' '#!/bin/sh' 'printf "%s\\n" "riscv64"' > "$TMP/bin/uname"
chmod +x "$TMP/bin/uname"
export PATH="$TMP/bin:$PATH"
. "$ROOT/recipes/recipes/go/go.sh"
mema_get_versions() { printf '%s\n' '1.0.0 amd64 deadbeef https://example.invalid/go.tar.gz'; }
if mema_resolve_version 2>/dev/null; then
    printf 'mema_resolve_version accepted an unsupported architecture\n' >&2
    exit 1
fi

printf '%s\n' '--- PASS: helper validation tests ---'
