#!/bin/sh
NAME="lib-display-info"
DESCRIPTION="Mema-managed EDID and DisplayID parsing library"
SECTION="libs"
MEMA_PACKAGE_VERSION="0.2.0"
MEMA_SUPPORTED_ARCHES="amd64"
deps="gcc, libc6-dev, meson, ninja-build, pkg-config"

set -e

mema_get_versions() {
    printf '%s\n' '0.2.0 amd64 f7331fcaf5527251b84c8fb84238d06cd2f458422ce950c80e86c72927aa8c2b https://gitlab.freedesktop.org/emersion/libdisplay-info/-/archive/0.2.0/libdisplay-info-0.2.0.tar.gz'
}

mema_resolve_version() { printf '%s\n' "$MEMA_PACKAGE_VERSION"; }

mema_install() {
    local archive work source_dir
    local sudo_cmd="${MEMA_SUDO:-}"
    export PKG_CONFIG_PATH="$MEMA_PKG_CONFIG_DIR${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}"
    export CPPFLAGS="-I$MEMA_INCLUDE_DIR ${CPPFLAGS:-}"
    export LDFLAGS="-L$MEMA_LIB_LINK_DIR -Wl,-rpath,$MEMA_LIB_LINK_DIR ${LDFLAGS:-}"
    archive=$(mema_download \
        'https://gitlab.freedesktop.org/emersion/libdisplay-info/-/archive/0.2.0/libdisplay-info-0.2.0.tar.gz' \
        'libdisplay-info-0.2.0.tar.gz' \
        'f7331fcaf5527251b84c8fb84238d06cd2f458422ce950c80e86c72927aa8c2b')
    work=$(mktemp -d)
    tar -xzf "$archive" -C "$work"
    source_dir="$work/libdisplay-info-0.2.0"
    (
        cd "$source_dir"
        meson setup build --prefix="$MEMA_INSTALL_DIR" --buildtype=release
        meson compile -C build
        $sudo_cmd meson install -C build
    )
    $sudo_cmd mkdir -p "$MEMA_INCLUDE_DIR" "$MEMA_LIB_LINK_DIR" "$MEMA_PKG_CONFIG_DIR"
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
}
