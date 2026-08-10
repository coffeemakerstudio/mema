#!/usr/bin/env bash
set -euo pipefail

KEYRING="/etc/apt/keyrings/mema.gpg"
LIST="/etc/apt/sources.list.d/mema.list"

if [ "${EUID:-$(id -u)}" -ne 0 ]; then
  printf 'Error: run this installer with sudo or as root.\n' >&2
  exit 1
fi

REPOSITORY_ROOT="${MEMA_REPOSITORY_ROOT:-https://coffeemakerstudio.github.io/mema}"
KEY_URL="${MEMA_KEY_URL:-https://raw.githubusercontent.com/coffeemakerstudio/mema/main/mema.gpg}"

printf '%s\n' '--- Setting up the Mema repository ---'
mkdir -p -m 755 /etc/apt/keyrings
tmp_key=$(mktemp)
trap 'rm -f "$tmp_key"' EXIT
curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 "$KEY_URL" -o "$tmp_key"
gpg --dearmor --yes -o "$KEYRING" "$tmp_key"
chmod 644 "$KEYRING"
printf 'deb [signed-by=%s] %s ./\n' "$KEYRING" "${REPOSITORY_ROOT%/}" > "$LIST"

printf '%s\n' '--- Updating apt ---'
apt update

printf '%s\n' '--- Installing mema ---'
apt install -y mema

printf '%s\n' '--- Installation complete! ---'
