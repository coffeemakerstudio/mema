#!/usr/bin/env bash
set -euo pipefail

VERSION="${VERSION:-0.0.1}"
DIST_DIR="dist"
DEB_DIR="debs"

command -v dpkg-scanpackages >/dev/null || { printf 'dpkg-scanpackages is required.\n' >&2; exit 1; }
command -v apt-ftparchive >/dev/null || { printf 'apt-ftparchive is required.\n' >&2; exit 1; }
command -v go >/dev/null || { printf 'go is required to build the CLI.\n' >&2; exit 1; }
if [ "${MEMA_SIGN:-0}" = "1" ]; then
    command -v gpg >/dev/null || { printf 'gpg is required when MEMA_SIGN=1.\n' >&2; exit 1; }
    SIGNING_KEY="${MEMA_SIGNING_KEY:-66315E3863522B0C320065281C7625C7FCF952B2}"
    gpg --batch --list-secret-keys "$SIGNING_KEY" >/dev/null 2>&1 || {
        printf 'A secret key matching MEMA_SIGNING_KEY (%s) is required.\n' "$SIGNING_KEY" >&2
        exit 1
    }
fi

rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"
mkdir -p "$DEB_DIR/DEBIAN" "$DEB_DIR/usr/local/bin" "$DEB_DIR/opt/mema/config.d"

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
rm -rf "$DEB_DIR/etc/profile.d/mema.sh"
install -D -m 0644 configs/mema-loader.sh "$DEB_DIR/etc/profile.d/mema.sh"

cat > "$DEB_DIR/DEBIAN/control" <<EOF
Package: mema
Version: $VERSION
Architecture: amd64
Maintainer: Coffee Maker Studio <mema@lupricht.net>
Depends: curl, bash, git, jq, tar, xz-utils, ca-certificates, fzf, sudo
Recommends: unzip
Homepage: https://github.com/coffeemakerstudio/mema
Section: admin
Priority: optional
Description: The Minimalist Meta-Manager
 Mema manages verified, isolated binary toolchains without polluting /usr/bin.
EOF
dpkg-deb --build "$DEB_DIR" "$DIST_DIR/mema_${VERSION}_amd64.deb" >/dev/null

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
    if [ "${MEMA_SIGN:-0}" = "1" ]; then
        gpg --batch --yes --dearmor -o mema-keyring.gpg ../mema.gpg
    fi
    apt-ftparchive release . > Release

    if [ "${MEMA_SIGN:-0}" = "1" ]; then
        gpg --batch --yes --local-user "$SIGNING_KEY" --clearsign --digest-algo SHA256 -o InRelease Release
        gpg --batch --yes --local-user "$SIGNING_KEY" --armor --detach-sign --digest-algo SHA256 -o Release.gpg Release
    fi
)

printf '%s\n' "Built packages in $DIST_DIR"
