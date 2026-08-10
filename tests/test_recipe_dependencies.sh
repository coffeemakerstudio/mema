#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(realpath "$(dirname "$0")/..")
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT

mkdir -p "$tmp_dir/recipes/lib-mylib" "$tmp_dir/recipes/myprogram" "$tmp_dir/recipes/wayland"
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
cat > "$tmp_dir/recipes/wayland/recipe.sh" <<'EOF'
NAME="wayland"
DESCRIPTION="test autoinstall package"
SECTION="devel"
MEMA_PACKAGE_VERSION="1.23.1"
MEMA_AUTOINSTALL=1
MEMA_INSTALL_DEPENDS="lib-mylib 1.0.0 myprogram 1.0.0"
mema_get_versions() { printf '%s\n' '1.23.1 amd64 hash https://example.invalid/wayland.tar.gz'; }
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

autodeb="$tmp_dir/dist/mema-wayland_1.23.1_all.deb"
[ "$(dpkg-deb -f "$autodeb" Version)" = "1.23.1" ]
auto_depends=$(dpkg-deb -f "$autodeb" Depends)
case "$auto_depends" in
    *'mema-lib-mylib (= 1.0.0)'*'mema-myprogram (= 1.0.0)'*) ;;
    *) printf 'unexpected autoinstall dependencies: %s\n' "$auto_depends" >&2; exit 1 ;;
esac
dpkg-deb -e "$autodeb" "$tmp_dir/autocontrol"
autopostinst=$(<"$tmp_dir/autocontrol/postinst")
autoexpected=$'#!/bin/sh\nset -e\n/usr/local/bin/mema install lib-mylib 1.0.0\n/usr/local/bin/mema install myprogram 1.0.0\n/usr/local/bin/mema install wayland 1.23.1'
[ "$autopostinst" = "$autoexpected" ] || {
    printf 'unexpected autoinstall postinst:\n%s\n' "$autopostinst" >&2
    exit 1
}

printf '%s\n' '--- PASS: recipe dependency package generation ---'
