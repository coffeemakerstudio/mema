#!/bin/sh
NAME="wayland-protocols"
DESCRIPTION="Wayland protocol extension definitions"
SECTION="devel"
MEMA_PACKAGE_VERSION="1.43"

set -e

mema_get_versions() {
    printf '%s\n' '1.43 all ba3c3425dd27c57b5291e93dba97be12479601e00bcab24d26471948cb643653 https://deb.debian.org/debian/pool/main/w/wayland-protocols/wayland-protocols_1.43.orig.tar.xz'
}

mema_resolve_version() { printf '%s\n' "$MEMA_PACKAGE_VERSION"; }

mema_install() {
    local archive work source_dir
    local sudo_cmd="${MEMA_SUDO:-}"
    archive=$(mema_download \
        'https://deb.debian.org/debian/pool/main/w/wayland-protocols/wayland-protocols_1.43.orig.tar.xz' \
        'wayland-protocols-1.43.tar.xz' \
        'ba3c3425dd27c57b5291e93dba97be12479601e00bcab24d26471948cb643653')
    work=$(mktemp -d)
    tar -xJf "$archive" -C "$work"
    source_dir="$work/wayland-protocols-1.43"
    $sudo_cmd mkdir -p "$MEMA_INSTALL_DIR/share"
    $sudo_cmd cp -a "$source_dir/stable" "$source_dir/unstable" "$MEMA_INSTALL_DIR/share/"
    $sudo_cmd mkdir -p "$MEMA_SHARE_DIR"
    mema_use
    rm -rf "$work"
}

mema_use() {
    local sudo_cmd="${MEMA_SUDO:-}"
    $sudo_cmd ln -sfn "$MEMA_INSTALL_DIR/share/wayland-protocols" "$MEMA_SHARE_DIR/wayland-protocols"
}
