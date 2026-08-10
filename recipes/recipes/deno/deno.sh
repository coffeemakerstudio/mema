#!/bin/sh
NAME="deno"
DESCRIPTION="Mema-managed Deno JavaScript runtime"
SECTION="javascript"
MEMA_PACKAGE_VERSION="2.9.5"
deps="ca-certificates, curl, unzip"

set -e

mema_get_versions() {
    printf '%s\n' \
        '2.9.5 amd64 8b010a3b1a4a0188a67cdb8a7a27348b2a501af78aec7fc74f2ace167368d530 https://github.com/denoland/deno/releases/download/v2.9.5/deno-x86_64-unknown-linux-gnu.zip' \
        '2.9.5 arm64 6b7cae3a8fc4385a59dea3146fcb8bad7fea4230e0ad36a8c692afacbc254be0 https://github.com/denoland/deno/releases/download/v2.9.5/deno-aarch64-unknown-linux-gnu.zip'
}

mema_resolve_version() { printf '%s\n' "$MEMA_PACKAGE_VERSION"; }

mema_install() {
    local arch archive url hash
    case "$(uname -m)" in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; *) printf 'Mema Error: Deno is not supported on architecture %s.\n' "$(uname -m)" >&2; return 1 ;; esac
    url=$(mema_get_versions | awk -v a="$arch" '$2 == a {print $4}'); hash=$(mema_get_versions | awk -v a="$arch" '$2 == a {print $3}')
    archive=$(mema_download "$url" "deno-$arch.zip" "$hash")
    $MEMA_SUDO mkdir -p "$MEMA_INSTALL_DIR/bin"
    $MEMA_SUDO unzip -oq "$archive" -d "$MEMA_INSTALL_DIR/bin"
    mema_use
}

mema_use() {
    $MEMA_SUDO mkdir -p "$MEMA_LINK_DIR"
    $MEMA_SUDO ln -sfn "$MEMA_INSTALL_DIR/bin/deno" "$MEMA_LINK_DIR/deno"
}
