#!/bin/sh
NAME="lib-libffi"
DESCRIPTION="Mema-managed libffi development library"
SECTION="libs"
MEMA_PACKAGE_VERSION="3.4.6"
MEMA_SUPPORTED_ARCHES="amd64"
deps="gcc, make, libc6-dev"

set -e

mema_get_versions() {
    printf '%s\n' '3.4.6 amd64 b0dea9df23c863a7a50e825440f3ebffabd65df1497108e5d437747843895a4e https://github.com/libffi/libffi/releases/download/v3.4.6/libffi-3.4.6.tar.gz'
}

mema_resolve_version() { printf '%s\n' "$MEMA_PACKAGE_VERSION"; }

mema_install() {
    local archive work source_dir
    local sudo_cmd="${MEMA_SUDO:-}"
    archive=$(mema_download \
        'https://github.com/libffi/libffi/releases/download/v3.4.6/libffi-3.4.6.tar.gz' \
        'libffi-3.4.6.tar.gz' \
        'b0dea9df23c863a7a50e825440f3ebffabd65df1497108e5d437747843895a4e')
    work=$(mktemp -d)
    tar -xzf "$archive" -C "$work"
    source_dir="$work/libffi-3.4.6"
    (
        cd "$source_dir"
        ./configure --prefix="$MEMA_INSTALL_DIR" --disable-docs
        make
        $sudo_cmd make install
    )
    $sudo_cmd mkdir -p "$MEMA_INCLUDE_DIR" "$MEMA_LIB_LINK_DIR" "$MEMA_PKG_CONFIG_DIR"
    mema_use
    rm -rf "$work"
}

mema_use() {
    local file
    local sudo_cmd="${MEMA_SUDO:-}"
    for file in "$MEMA_INSTALL_DIR/include"/*; do
        [ -f "$file" ] || continue
        $sudo_cmd ln -sfn "$file" "$MEMA_INCLUDE_DIR/${file##*/}"
    done
    for file in "$MEMA_INSTALL_DIR/lib"/*.so* "$MEMA_INSTALL_DIR/lib"/*.la "$MEMA_INSTALL_DIR/lib/pkgconfig"/*.pc; do
        [ -f "$file" ] || continue
        case "$file" in
            */pkgconfig/*) destination="$MEMA_PKG_CONFIG_DIR/${file##*/}" ;;
            *) destination="$MEMA_LIB_LINK_DIR/${file##*/}" ;;
        esac
        $sudo_cmd ln -sfn "$file" "$destination"
    done
}
