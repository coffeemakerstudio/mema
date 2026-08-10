#!/bin/sh
NAME="lib-expat"
DESCRIPTION="Mema-managed Expat XML parser library"
SECTION="libs"
MEMA_PACKAGE_VERSION="2.5.0"
deps="gcc, make, libc6-dev, autoconf, automake, libtool"

set -e

mema_get_versions() {
    printf '%s\n' '2.5.0 amd64 ab00ee05c7067fd10a35c5d2a4922ebba746ddd50ff83b79c828da17bbdf1757 https://deb.debian.org/debian/pool/main/e/expat/expat_2.5.0.orig.tar.gz'
}

mema_resolve_version() { printf '%s\n' "$MEMA_PACKAGE_VERSION"; }

mema_install() {
    local archive work source_dir
    local sudo_cmd="${MEMA_SUDO:-}"
    archive=$(mema_download \
        'https://deb.debian.org/debian/pool/main/e/expat/expat_2.5.0.orig.tar.gz' \
        'expat-2.5.0.tar.gz' \
        'ab00ee05c7067fd10a35c5d2a4922ebba746ddd50ff83b79c828da17bbdf1757')
    work=$(mktemp -d)
    tar -xzf "$archive" -C "$work"
    source_dir="$work/libexpat-R_2_5_0/expat"
    (
        cd "$source_dir"
        ./buildconf.sh
        ./configure --prefix="$MEMA_INSTALL_DIR"
        make
        $sudo_cmd make install
    )
    $sudo_cmd mkdir -p "$MEMA_INCLUDE_DIR" "$MEMA_LIB_LINK_DIR" "$MEMA_PKG_CONFIG_DIR"
    mema_use
    rm -rf "$work"
}

mema_use() {
    local file destination
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
