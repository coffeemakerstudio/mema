#!/bin/sh

mema_install() {
    local pkg="{{NAME}}" version="{{VERSION}}" url="{{URL}}" expected_hash="{{HASH}}" archive
    if [ ! -x "$MEMA_INSTALL_DIR/bin/$pkg" ]; then
        printf 'Mema: Installing %s (%s)...\n' "$pkg" "$version"
        archive=$(mema_download "$url" "$pkg-$version.tar.gz" "$expected_hash") || return 1
        $MEMA_SUDO mkdir -p "$MEMA_INSTALL_DIR"
        $MEMA_SUDO tar -xzf "$archive" -C "$MEMA_INSTALL_DIR" --strip-components=1
    fi
    mema_use
}

mema_use() {
    $MEMA_SUDO mkdir -p "$MEMA_LINK_DIR"
    $MEMA_SUDO ln -sf "$MEMA_INSTALL_DIR/bin/{{BINARY}}" "$MEMA_LINK_DIR/{{BINARY}}"
    $MEMA_SUDO ln -sf "$MEMA_INSTALL_DIR/bin/{{BINARY}}" "$MEMA_LINK_DIR/{{BINARY}}-$MEMA_VERSION"
}
