#!/bin/sh
NAME="lib-xml2"
DESCRIPTION="Mema-managed XML parser library"
SECTION="libs"
MEMA_PACKAGE_VERSION="2.9.14"
deps="gcc, make, libc6-dev"

set -e

mema_get_versions() {
    printf '%s\n' '2.9.14 amd64 4fe913dec8b1ab89d13b489b419a8203176ea39e931eaa0d25b17eafb9c279e9 https://deb.debian.org/debian/pool/main/libx/libxml2/libxml2_2.9.14+dfsg.orig.tar.xz'
}

mema_resolve_version() { printf '%s\n' "$MEMA_PACKAGE_VERSION"; }

mema_install() {
    local archive work source_dir
    local sudo_cmd="${MEMA_SUDO:-}"
    archive=$(mema_download \
        'https://deb.debian.org/debian/pool/main/libx/libxml2/libxml2_2.9.14+dfsg.orig.tar.xz' \
        'libxml2-2.9.14.tar.xz' \
        '4fe913dec8b1ab89d13b489b419a8203176ea39e931eaa0d25b17eafb9c279e9')
    work=$(mktemp -d)
    tar -xJf "$archive" -C "$work"
    source_dir="$work/libxml2-2.9.14"
    (
        cd "$source_dir"
        ./configure --prefix="$MEMA_INSTALL_DIR" --without-python --without-zlib --without-lzma --without-readline --without-icu
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
    $sudo_cmd ln -sfn "$MEMA_INSTALL_DIR/include/libxml2" "$MEMA_INCLUDE_DIR/libxml2"
    for file in "$MEMA_INSTALL_DIR/lib"/*.so* "$MEMA_INSTALL_DIR/lib"/*.la "$MEMA_INSTALL_DIR/lib/pkgconfig"/*.pc; do
        [ -f "$file" ] || continue
        case "$file" in
            */pkgconfig/*) destination="$MEMA_PKG_CONFIG_DIR/${file##*/}" ;;
            *) destination="$MEMA_LIB_LINK_DIR/${file##*/}" ;;
        esac
        $sudo_cmd ln -sfn "$file" "$destination"
    done
}
