#!/bin/sh
NAME="wlroots"
DESCRIPTION="Mema-managed wlroots compositor library"
SECTION="libs"
MEMA_PACKAGE_VERSION="0.17.2"
MEMA_AUTOINSTALL="1"
MEMA_SUPPORTED_ARCHES="amd64"
MEMA_INSTALL_DEPENDS="
  wayland 1.23.1
  wayland-protocols 1.43
  lib-display-info 0.2.0
"
deps="gcc, libc6-dev, meson, ninja-build, pkg-config, hwdata, libdrm-dev, libegl-dev, libgbm-dev, libgles2-mesa-dev, libinput-dev, libseat-dev, libudev-dev, libxkbcommon-dev, libpixman-1-dev"

set -e

mema_get_versions() {
    printf '%s\n' '0.17.2 amd64 f4007d3f71e190b9000ab4a30afd87833b034ab2602030a00af4465ffd4e997c https://gitlab.freedesktop.org/wlroots/wlroots/-/releases/0.17.2/downloads/wlroots-0.17.2.tar.gz'
}

mema_resolve_version() { printf '%s\n' "$MEMA_PACKAGE_VERSION"; }

mema_install() {
    local archive work source_dir
    local sudo_cmd="${MEMA_SUDO:-}"
    export PKG_CONFIG_PATH="$MEMA_PKG_CONFIG_DIR${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}"
    export CPPFLAGS="-I$MEMA_INCLUDE_DIR ${CPPFLAGS:-}"
    export LDFLAGS="-L$MEMA_LIB_LINK_DIR -Wl,-rpath,$MEMA_LIB_LINK_DIR ${LDFLAGS:-}"
    archive=$(mema_download \
        'https://gitlab.freedesktop.org/wlroots/wlroots/-/releases/0.17.2/downloads/wlroots-0.17.2.tar.gz' \
        'wlroots-0.17.2.tar.gz' \
        'f4007d3f71e190b9000ab4a30afd87833b034ab2602030a00af4465ffd4e997c')
    work=$(mktemp -d)
    tar -xzf "$archive" -C "$work"
    source_dir="$work/wlroots-0.17.2"
    (
        cd "$source_dir"
        meson setup build --prefix="$MEMA_INSTALL_DIR" --buildtype=release \
            -Dxwayland=disabled -Dexamples=false -Dbackends=drm,libinput \
            -Drenderers=gles2 -Dsession=enabled
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
    $sudo_cmd ln -sfn "$MEMA_INSTALL_DIR/include/wlr" "$MEMA_INCLUDE_DIR/wlr"
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
