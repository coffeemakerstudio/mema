#!/bin/sh
NAME="bun"
DESCRIPTION="Mema-managed Bun JavaScript runtime"
SECTION="javascript"
MEMA_PACKAGE_VERSION="1.3.14"
MEMA_SUPPORTED_ARCHES="amd64 arm64"
deps="ca-certificates, curl, unzip"

set -e

mema_get_versions() {
    printf '%s\n' \
        '1.3.14 amd64 951ee2aee855f08595aeec6225226a298d3fea83a3dcd6465c09cbccdf7e848f https://github.com/oven-sh/bun/releases/download/bun-v1.3.14/bun-linux-x64.zip' \
        '1.3.14 arm64 a27ffb63a8310375836e0d6f668ae17fa8d8d18b88c37c821c65331973a19a3b https://github.com/oven-sh/bun/releases/download/bun-v1.3.14/bun-linux-aarch64.zip'
}

mema_resolve_version() { printf '%s\n' "$MEMA_PACKAGE_VERSION"; }

mema_install() {
    local arch archive url hash
    case "$(uname -m)" in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; *) printf 'Mema Error: Bun is not supported on architecture %s.\n' "$(uname -m)" >&2; return 1 ;; esac
    url=$(mema_get_versions | awk -v a="$arch" '$2 == a {print $4}'); hash=$(mema_get_versions | awk -v a="$arch" '$2 == a {print $3}')
    archive=$(mema_download "$url" "bun-$arch.zip" "$hash")
    $MEMA_SUDO mkdir -p "$MEMA_INSTALL_DIR/bin"
    $MEMA_SUDO unzip -oq "$archive" -d "$MEMA_INSTALL_DIR/bin"
    mema_use
}

mema_use() {
    $MEMA_SUDO mkdir -p "$MEMA_LINK_DIR"
    $MEMA_SUDO ln -sfn "$MEMA_INSTALL_DIR/bin/bun" "$MEMA_LINK_DIR/bun"
}
