#!/bin/sh
NAME="rust"
DESCRIPTION="Mema-managed Rust toolchain"
SECTION="devel"
MEMA_PACKAGE_VERSION="1.97.1"
deps="ca-certificates, curl, xz-utils"

set -e

mema_get_versions() {
    printf '%s\n' \
        '1.97.1 amd64 88f28fa9af20594179f85d6df67078dfd6fa93e2f6da5e1e9b0ac4997988ca4f https://static.rust-lang.org/dist/2026-07-16/rust-1.97.1-x86_64-unknown-linux-gnu.tar.xz' \
        '1.97.1 arm64 9a7a2c336b4787f1b72f6bab7c35d5b7af2fd03cbd39b4fc721466a70d402a7d https://static.rust-lang.org/dist/2026-07-16/rust-1.97.1-aarch64-unknown-linux-gnu.tar.xz'
}

mema_resolve_version() { printf '%s\n' "$MEMA_PACKAGE_VERSION"; }

mema_install() {
    local arch archive target
    case "$(uname -m)" in
        x86_64) arch=amd64; target=x86_64-unknown-linux-gnu ;;
        aarch64) arch=arm64; target=aarch64-unknown-linux-gnu ;;
        *) printf 'Mema Error: Rust is not supported on architecture %s.\n' "$(uname -m)" >&2; return 1 ;;
    esac
    archive=$(mema_download "https://static.rust-lang.org/dist/2026-07-16/rust-1.97.1-$target.tar.xz" "rust-1.97.1-$target.tar.xz" "$(mema_get_versions | awk -v a="$arch" '$2 == a {print $3}')")
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
