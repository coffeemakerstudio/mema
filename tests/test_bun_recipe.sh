#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(realpath "$(dirname "$0")/..")
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT

mkdir -p "$tmp_dir/archive/bun-linux-x64"
cat >"$tmp_dir/archive/bun-linux-x64/bun" <<'EOF'
#!/bin/sh
printf 'bun-test-runtime\n'
EOF
chmod 755 "$tmp_dir/archive/bun-linux-x64/bun"
(
    cd "$tmp_dir/archive"
    zip -qr "$tmp_dir/bun-linux-x64.zip" bun-linux-x64
)

# Exercise the real Bun recipe against the actual upstream archive layout:
# bun-linux-x64/bun.  The old recipe expected bin/bun and failed here.
source "$repo_dir/recipes/recipes/bun/bun.sh"
MEMA_INSTALL_DIR="$tmp_dir/install"
MEMA_LINK_DIR="$tmp_dir/links"
MEMA_SUDO=""
mema_download() { printf '%s\n' "$tmp_dir/bun-linux-x64.zip"; }

mema_install

expected="$MEMA_INSTALL_DIR/bin/bun-linux-x64/bun"
test -x "$expected"
test "$(readlink -f "$MEMA_LINK_DIR/bun")" = "$expected"
test "$($MEMA_LINK_DIR/bun)" = bun-test-runtime
printf 'Bun recipe archive-layout and activation regression passed.\n'
