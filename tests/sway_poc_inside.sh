#!/usr/bin/env bash
set -euo pipefail

echo 'deb [trusted=yes] file:///repo ./' > /etc/apt/sources.list.d/mema-sway-poc.list
apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y mema-sway

test -x /usr/local/bin/sway
test -x /usr/local/bin/swaymsg
test -x /usr/local/bin/swaybar
test -x /usr/local/bin/swaynag
sway --version
swaymsg --version
test ! -e /usr/bin/sway
echo '--- PASS: Sway compositor installed through Mema ---'
