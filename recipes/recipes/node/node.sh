#!/bin/sh
NAME="node"
DESCRIPTION="Mema-managed Node.js runtime"
SECTION="javascript"
MEMA_PACKAGE_VERSION="24.19.0"
MEMA_SUPPORTED_ARCHES="amd64 arm64"
deps="ca-certificates, curl, xz-utils"

set -e

mema_get_versions() {
    printf '%s\n' \
        '24.19.0 amd64 14b342e71204f811bde6153be8e04b62aef63c236fef92b55f9c83154b409647 https://nodejs.org/dist/v24.19.0/node-v24.19.0-linux-x64.tar.xz' \
        '24.19.0 arm64 01443c1e1a29e531ccad5a46fefa6df490d2189c49f7955904aecdbb0fe86fdc https://nodejs.org/dist/v24.19.0/node-v24.19.0-linux-arm64.tar.xz'
}

mema_resolve_version() { printf '%s\n' "$MEMA_PACKAGE_VERSION"; }

mema_install() {
    local archive arch target
    case "$(uname -m)" in
        x86_64) arch=amd64; target=x64 ;;
        aarch64) arch=arm64; target=arm64 ;;
        *) printf 'Mema Error: Node.js is not supported on architecture %s.\n' "$(uname -m)" >&2; return 1 ;;
    esac
    archive=$(mema_download "https://nodejs.org/dist/v24.19.0/node-v24.19.0-linux-$target.tar.xz" "node-v24.19.0-linux-$target.tar.xz" "$(mema_get_versions | awk -v a="$arch" '$2 == a {print $3}')")
    $MEMA_SUDO mkdir -p "$MEMA_INSTALL_DIR"
    $MEMA_SUDO tar -xJf "$archive" -C "$MEMA_INSTALL_DIR" --strip-components=1
    mema_use
}

mema_use() {
    local file
    $MEMA_SUDO mkdir -p "$MEMA_LINK_DIR"
    for file in "$MEMA_INSTALL_DIR/bin"/*; do
        [ -x "$file" ] || continue
        $MEMA_SUDO ln -sfn "$file" "$MEMA_LINK_DIR/${file##*/}"
    done
}
