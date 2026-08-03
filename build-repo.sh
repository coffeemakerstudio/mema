#!/usr/bin/env bash
set -euo pipefail

VERSION="${VERSION:-0.0.1}"
DIST_DIR="dist"
DEB_DIR="debs"

command -v tpa >/dev/null || { printf 'tpa is required to build packages.\n' >&2; exit 1; }
command -v dpkg-scanpackages >/dev/null || { printf 'dpkg-scanpackages is required.\n' >&2; exit 1; }
command -v apt-ftparchive >/dev/null || { printf 'apt-ftparchive is required.\n' >&2; exit 1; }

rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

printf '%s\n' "--- Building mema $VERSION ---"
(
    cd mema-go
    go build -o ../core/mema .
)

install -m 0755 core/mema "$DEB_DIR/usr/local/bin/mema"
install -m 0755 core/mema_download "$DEB_DIR/usr/local/bin/mema_download"
install -m 0755 core/mema_find_recipes "$DEB_DIR/usr/local/bin/mema_find_recipes"
install -m 0755 core/mema_list "$DEB_DIR/usr/local/bin/mema_list"
install -m 0755 core/mema_verify "$DEB_DIR/usr/local/bin/mema_verify"
install -m 0644 configs/00-mema-init.sh "$DEB_DIR/opt/mema/config.d/00-init.sh"
install -m 0644 configs/mema-loader.sh "$DEB_DIR/etc/profile.d/mema.sh/mema-loader.sh"

json=$(sed \
    -e "s|{{VERSION}}|$VERSION|g" \
    -e 's|{{ARCH}}|amd64|g' \
    -e "s|{{OUT_DIR}}|$DEB_DIR|g" \
    templates/template.json)
printf '%s\n' "$json" | tpa json
tpa build -in="$DEB_DIR" -out="$DIST_DIR"

printf '%s\n' '--- Building recipe packages ---'
(
    cd recipes
    ./build.sh
)

shopt -s nullglob
recipe_packages=(recipes/dist/*.deb)
if [ "${#recipe_packages[@]}" -eq 0 ]; then
    printf 'No recipe packages were built.\n' >&2
    exit 1
fi
cp "${recipe_packages[@]}" "$DIST_DIR/"

(
    cd "$DIST_DIR"
    dpkg-scanpackages . /dev/null > Packages
    gzip -kf Packages
    apt-ftparchive release . > Release

    if [ "${MEMA_SIGN:-0}" = "1" ]; then
        gpg --batch --yes --clearsign --digest-algo SHA256 -o InRelease Release
        gpg --batch --yes --armor --detach-sign --digest-algo SHA256 -o Release.gpg Release
    fi
)

printf '%s\n' "Built packages in $DIST_DIR"
