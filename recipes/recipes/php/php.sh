#!/bin/sh
NAME="php"
DESCRIPTION="Mema-managed PHP CLI runtime"
SECTION="web"
MEMA_PACKAGE_VERSION="8.5.9"
MEMA_AUTOINSTALL="1"
MEMA_SUPPORTED_ARCHES="amd64 arm64"
deps="build-essential, autoconf, bison, re2c, pkg-config, libssl-dev, zlib1g-dev"

set -e

mema_get_versions() {
    printf '%s\n' \
        '8.5.9 amd64 d735459c2cbaeb0673d416c33d372d9ff261d562f6b29da48f3e6aeaeca083af https://www.php.net/distributions/php-8.5.9.tar.gz' \
        '8.5.9 arm64 d735459c2cbaeb0673d416c33d372d9ff261d562f6b29da48f3e6aeaeca083af https://www.php.net/distributions/php-8.5.9.tar.gz'
}

mema_resolve_version() { printf '%s\n' "$MEMA_PACKAGE_VERSION"; }

mema_install() {
    local archive work source_dir sudo_cmd
    case "$(uname -m)" in
        x86_64|aarch64) ;;
        *) printf 'Mema Error: PHP is not supported on architecture %s.\n' "$(uname -m)" >&2; return 1 ;;
    esac
    sudo_cmd="${MEMA_SUDO:-}"
    archive=$(mema_download \
        'https://www.php.net/distributions/php-8.5.9.tar.gz' \
        'php-8.5.9.tar.gz' \
        'd735459c2cbaeb0673d416c33d372d9ff261d562f6b29da48f3e6aeaeca083af')
    work=$(mktemp -d)
    trap 'rm -rf "$work"' EXIT
    tar -xzf "$archive" -C "$work"
    source_dir="$work/php-8.5.9"
    (
        cd "$source_dir"
        ./configure \
            --prefix="$MEMA_INSTALL_DIR" \
            --disable-all \
            --enable-cli \
            --enable-phar \
            --with-openssl \
            --with-zlib
        make -j"$(getconf _NPROCESSORS_ONLN 2>/dev/null || printf '1')"
        $sudo_cmd make install
    )
    mema_use
    trap - EXIT
    rm -rf "$work"
}

mema_use() {
    local file sudo_cmd
    sudo_cmd="${MEMA_SUDO:-}"
    $sudo_cmd mkdir -p "$MEMA_LINK_DIR"
    for file in "$MEMA_INSTALL_DIR/bin"/*; do
        [ -x "$file" ] || continue
        $sudo_cmd ln -sfn "$file" "$MEMA_LINK_DIR/${file##*/}"
    done
}
