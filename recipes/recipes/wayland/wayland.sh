#!/bin/sh
NAME="wayland"
DESCRIPTION="Wayland client and server libraries and scanner"
SECTION="libs"
MEMA_PACKAGE_VERSION="1.23.1"
MEMA_AUTOINSTALL="1"
MEMA_INSTALL_DEPENDS="
  lib-libffi 3.4.4
  lib-expat 2.5.0
  lib-xml2 2.9.14
  wayland-protocols 1.43
"
deps="gcc, libc6-dev, meson, ninja-build, pkg-config, python3"

set -e

mema_get_versions() {
    printf '%s\n' '1.23.1 amd64 158ec49af498f2558c7fbf7e8b070d010d4e270cc6076003a18a6c813f87e244 https://deb.debian.org/debian/pool/main/w/wayland/wayland_1.23.1.orig.tar.gz'
}

mema_resolve_version() { printf '%s\n' "$MEMA_PACKAGE_VERSION"; }

mema_install() {
    local archive work source_dir
    local sudo_cmd="${MEMA_SUDO:-}"
    export PKG_CONFIG_PATH="$MEMA_PKG_CONFIG_DIR${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}"
    export CPPFLAGS="-I$MEMA_INCLUDE_DIR ${CPPFLAGS:-}"
    export LDFLAGS="-L$MEMA_LIB_LINK_DIR -Wl,-rpath,$MEMA_LIB_LINK_DIR ${LDFLAGS:-}"
    archive=$(mema_download \
        'https://deb.debian.org/debian/pool/main/w/wayland/wayland_1.23.1.orig.tar.gz' \
        'wayland-1.23.1.tar.gz' \
        '158ec49af498f2558c7fbf7e8b070d010d4e270cc6076003a18a6c813f87e244')
    work=$(mktemp -d)
    tar -xzf "$archive" -C "$work"
    source_dir="$work/wayland-1.23.1"
    (
        cd "$source_dir"
        meson setup build --prefix="$MEMA_INSTALL_DIR" --buildtype=release \
            -Ddocumentation=false -Dtests=false -Ddtd_validation=false
        meson compile -C build
        $sudo_cmd meson install -C build
    )
    $sudo_cmd mkdir -p "$MEMA_INCLUDE_DIR" "$MEMA_LIB_LINK_DIR" "$MEMA_PKG_CONFIG_DIR" "$MEMA_SHARE_DIR"
    mema_use
    rm -rf "$work"
}

mema_use() {
    local file destination libdir
    local sudo_cmd="${MEMA_SUDO:-}"
    for file in "$MEMA_INSTALL_DIR/include"/*; do
        [ -f "$file" ] || continue
        $sudo_cmd ln -sfn "$file" "$MEMA_INCLUDE_DIR/${file##*/}"
    done
    for libdir in "$MEMA_INSTALL_DIR/lib" "$MEMA_INSTALL_DIR/lib"/*; do
        [ -d "$libdir" ] || continue
        for file in "$libdir"/*.so* "$libdir"/*.la "$libdir"/*.pc "$libdir/pkgconfig"/*.pc; do
            [ -f "$file" ] || continue
            case "$file" in
                */pkgconfig/*|*/pkgconfig/*.pc) destination="$MEMA_PKG_CONFIG_DIR/${file##*/}" ;;
                *) destination="$MEMA_LIB_LINK_DIR/${file##*/}" ;;
            esac
            $sudo_cmd ln -sfn "$file" "$destination"
        done
    done
    for file in "$MEMA_INSTALL_DIR/bin"/*; do
        [ -x "$file" ] || continue
        $sudo_cmd ln -sfn "$file" "$MEMA_LINK_DIR/${file##*/}"
    done
}
