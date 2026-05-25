#!/bin/bash
set -e

KEYRING="/etc/apt/keyrings/mema.gpg"
LIST="/etc/apt/sources.list.d/mema.list"

if [ "$EUID" -ne 0 ]; then
  echo "Error: Run as root."
  exit 1
fi

echo "--- Setting up Coffee Maker Studio repository ---"
mkdir -p -m 755 /etc/apt/keyrings
curl -fsSL "https://raw.githubusercontent.com/coffeemakerstudio/mema/refs/heads/main/mema.gpg" | gpg --dearmor --yes -o "$KEYRING"
chmod 644 "$KEYRING"
echo "deb [signed-by=$KEYRING] https://coffeemakerstudio.github.io/mema/ ./" > "$LIST"

echo "--- Updating apt ---"
apt update

echo "--- Installing mema ---"
apt install -y mema

echo "--- Installation complete! ---"
