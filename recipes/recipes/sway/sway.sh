#!/bin/sh
NAME="sway"
DESCRIPTION="Mema-managed i3-compatible Wayland compositor"
SECTION="x11"
MEMA_PACKAGE_VERSION="1.9"
MEMA_AUTOINSTALL="1"
MEMA_SUPPORTED_ARCHES="amd64"
MEMA_INSTALL_DEPENDS="
  wlroots 0.17.2
  wayland 1.23.1
  wayland-protocols 1.43
"
deps="gcc, libc6-dev, meson, ninja-build, pkg-config, libjson-c-dev, libpcre2-dev, libinput-dev, libdrm-dev, libudev-dev, libxkbcommon-dev, libpixman-1-dev, libevdev-dev, libcairo2-dev, libpango1.0-dev, libseat-dev"

set -e

mema_get_versions() {
    printf '%s\n' '1.9 amd64 a63b2df8722ee595695a0ec6c84bf29a055a9767e63d8e4c07ff568cb6ee0b51 https://github.com/swaywm/sway/releases/download/1.9/sway-1.9.tar.gz'
}

mema_resolve_version() { printf '%s\n' "$MEMA_PACKAGE_VERSION"; }

mema_install() {
    local archive work source_dir
    local sudo_cmd="${MEMA_SUDO:-}"
    export PKG_CONFIG_PATH="$MEMA_PKG_CONFIG_DIR${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}"
    export CPPFLAGS="-I$MEMA_INCLUDE_DIR ${CPPFLAGS:-}"
    export LDFLAGS="-L$MEMA_LIB_LINK_DIR -Wl,-rpath,$MEMA_LIB_LINK_DIR ${LDFLAGS:-}"
    archive=$(mema_download \
        'https://github.com/swaywm/sway/releases/download/1.9/sway-1.9.tar.gz' \
        'sway-1.9.tar.gz' \
        'a63b2df8722ee595695a0ec6c84bf29a055a9767e63d8e4c07ff568cb6ee0b51')
    work=$(mktemp -d)
    tar -xzf "$archive" -C "$work"
    source_dir="$work/sway-1.9"
    (
        cd "$source_dir"
        meson setup build --prefix="$MEMA_INSTALL_DIR" --buildtype=release \
            -Dxwayland=disabled -Dman-pages=disabled -Dtray=disabled \
            -Dgdk-pixbuf=disabled -Ddefault-wallpaper=false
        meson compile -C build
        $sudo_cmd meson install -C build
    )
    $sudo_cmd mkdir -p "$MEMA_LINK_DIR"
    mema_use
    rm -rf "$work"
}

mema_use() {
    local file
    local sudo_cmd="${MEMA_SUDO:-}"
    for file in "$MEMA_INSTALL_DIR/bin"/*; do
        [ -x "$file" ] || continue
        $sudo_cmd ln -sfn "$file" "$MEMA_LINK_DIR/${file##*/}"
    done
}
