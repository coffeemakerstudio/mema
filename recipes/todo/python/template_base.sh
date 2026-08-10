#!/bin/sh

mema_install() {
    local expected_hash="{{HASH}}" download_url="{{URL}}" archive
    if [ ! -x "$MEMA_INSTALL_DIR/bin/python3" ]; then
        archive=$(mema_download "$download_url" "python-$MEMA_VERSION.tar.gz" "$expected_hash") || return 1
        $MEMA_SUDO mkdir -p "$MEMA_INSTALL_DIR"
        $MEMA_SUDO tar -xzf "$archive" -C "$MEMA_INSTALL_DIR" --strip-components=1
    fi
    mema_use
}

mema_use() {
    $MEMA_SUDO mkdir -p "$MEMA_LINK_DIR"
    $MEMA_SUDO ln -sf "$MEMA_INSTALL_DIR/bin/python3" "$MEMA_LINK_DIR/python3"
    $MEMA_SUDO ln -sf "$MEMA_INSTALL_DIR/bin/python3" "$MEMA_LINK_DIR/python-$MEMA_VERSION"
    $MEMA_SUDO ln -sf "$MEMA_INSTALL_DIR/bin/pip3" "$MEMA_LINK_DIR/pip3"
    $MEMA_SUDO ln -sf "$MEMA_INSTALL_DIR/bin/python3" "$MEMA_LINK_DIR/python"
}
