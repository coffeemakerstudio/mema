#!/usr/bin/env bash
set -euo pipefail

echo 'deb [trusted=yes] file:///repo ./' > /etc/apt/sources.list.d/mema-poc.list
apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y mema-wayland

export PKG_CONFIG_PATH=/opt/mema/lib/pkgconfig
export LD_LIBRARY_PATH=/opt/mema/lib/lib
export XDG_RUNTIME_DIR=/tmp/mema-runtime
mkdir -m 700 "$XDG_RUNTIME_DIR"

pkg-config --exists wayland-client
test -x /usr/local/bin/wayland-scanner
wayland-scanner --help >/dev/null

cat > /tmp/wayland-poc-client.c <<'SOURCE'
#include <wayland-client.h>
int main(void) {
    struct wl_display *display = wl_display_connect(NULL);
    if (display != NULL) {
        wl_display_disconnect(display);
    }
    return 0;
}
SOURCE

cc /tmp/wayland-poc-client.c \
    $(pkg-config --cflags --libs wayland-client) \
    -o /tmp/wayland-poc-client
/tmp/wayland-poc-client

test ! -e /usr/bin/wayland-scanner
echo '--- PASS: Wayland POC installed and linked through Mema ---'
