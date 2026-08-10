#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(realpath "$(dirname "$0")/..")
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT

mkdir -p "$tmp_dir/recipes/lib-mylib" "$tmp_dir/recipes/myprogram"
cat > "$tmp_dir/recipes/lib-mylib/recipe.sh" <<'EOF'
NAME="lib-mylib"
DESCRIPTION="test library"
SECTION="devel"
mema_get_versions() { printf '%s\n' '1.0.0 amd64 hash https://example.invalid/lib.tar.gz'; }
mema_install() { mkdir -p "$MEMA_INSTALL_DIR"; mema_use; }
mema_use() { :; }
EOF
cat > "$tmp_dir/recipes/myprogram/recipe.sh" <<'EOF'
NAME="myprogram"
DESCRIPTION="test program"
SECTION="devel"
MEMA_DEPENDS="lib-mylib"
mema_get_versions() { printf '%s\n' '1.0.0 amd64 hash https://example.invalid/program.tar.gz'; }
mema_install() { mkdir -p "$MEMA_INSTALL_DIR"; mema_use; }
mema_use() { :; }
EOF

(cd "$tmp_dir" && RECIPE_DIR=recipes OUT_DIR=dist "$repo_dir/recipes/build.sh")

depends=$(dpkg-deb -f "$tmp_dir/dist/mema-myprogram-latest_1_all.deb" Depends)
case "$depends" in
    *mema-lib-mylib-latest*) ;;
    *) printf 'missing recipe dependency in Depends: %s\n' "$depends" >&2; exit 1 ;;
esac

dpkg-deb -e "$tmp_dir/dist/mema-myprogram-latest_1_all.deb" "$tmp_dir/control"
postinst=$(<"$tmp_dir/control/postinst")
expected=$'#!/bin/sh\nset -e\n/usr/local/bin/mema install lib-mylib latest\n/usr/local/bin/mema install myprogram latest'
[ "$postinst" = "$expected" ] || {
    printf 'unexpected postinst:\n%s\n' "$postinst" >&2
    exit 1
}

printf '%s\n' '--- PASS: recipe dependency package generation ---'
