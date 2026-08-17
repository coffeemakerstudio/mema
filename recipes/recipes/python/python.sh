#!/bin/sh
NAME="python"
DESCRIPTION="Mema-managed Python interpreter"
SECTION="python"
MEMA_PACKAGE_VERSION="3.14.7"
MEMA_SUPPORTED_ARCHES="amd64 arm64"
deps="ca-certificates, curl"

set -e

mema_get_versions() {
    printf '%s\n' \
        '3.14.7 amd64 3d1705fee7747c491d774e26fa91fad67e25d1eb3ede4124dc88501279f2e7d4 https://github.com/astral-sh/python-build-standalone/releases/download/20260807/cpython-3.14.7%2B20260807-x86_64-unknown-linux-gnu-install_only.tar.gz' \
        '3.14.7 arm64 3657f14592d0a9c3f459ded52bf6f38976698cb07365e4025684bb11dd6be1cb https://github.com/astral-sh/python-build-standalone/releases/download/20260807/cpython-3.14.7%2B20260807-aarch64-unknown-linux-gnu-install_only.tar.gz'
}

mema_resolve_version() { printf '%s\n' "$MEMA_PACKAGE_VERSION"; }

mema_install() {
    local arch archive target
    case "$(uname -m)" in
        x86_64) arch=amd64; target=x86_64-unknown-linux-gnu ;;
        aarch64) arch=arm64; target=aarch64-unknown-linux-gnu ;;
        *) printf 'Mema Error: Python is not supported on architecture %s.\n' "$(uname -m)" >&2; return 1 ;;
    esac
    archive=$(mema_download "https://github.com/astral-sh/python-build-standalone/releases/download/20260807/cpython-3.14.7%2B20260807-$target-install_only.tar.gz" "python-3.14.7-$target.tar.gz" "$(mema_get_versions | awk -v a="$arch" '$2 == a {print $3}')")
    $MEMA_SUDO mkdir -p "$MEMA_INSTALL_DIR"
    $MEMA_SUDO tar -xzf "$archive" -C "$MEMA_INSTALL_DIR" --strip-components=1
    mema_use
}

mema_use() {
    local file
    $MEMA_SUDO mkdir -p "$MEMA_LINK_DIR"
    for file in "$MEMA_INSTALL_DIR/bin"/python* "$MEMA_INSTALL_DIR/bin"/pip*; do
        [ -x "$file" ] || continue
        $MEMA_SUDO ln -sfn "$file" "$MEMA_LINK_DIR/${file##*/}"
    done
}
