#!/usr/bin/env bash
set -euo pipefail

RECIPE_DIR="recipes"
MAINTAINER="Coffee Maker Studio <mema@lupricht.net>"
HOMEPAGE="https://github.com/coffeemakerstudio/mema"
OUT_DIR="dist"

[ -d "$RECIPE_DIR" ] || { printf 'Error: directory %s not found.\n' "$RECIPE_DIR" >&2; exit 1; }
rm -rf "/tmp/mema-recipe" "$OUT_DIR"
mkdir -p "$OUT_DIR"

build_deb() {
    local path="$1" output="$2"
    dpkg-deb --build "$path" "$output" >/dev/null
}

for recipe in "$RECIPE_DIR"/*/*.sh; do
    [ -f "$recipe" ] || continue
    printf '%s\n' "--- Processing recipe: $recipe ---"
    source "$recipe"
    if ! declare -F mema_get_versions >/dev/null; then
        printf 'Warning: mema_get_versions() not found in %s\n' "$recipe" >&2
        unset -f mema_get_versions mema_resolve_version mema_install mema_use || true
        continue
    fi

    PKG_NAME="mema-$NAME"
    BUILD_PATH="/tmp/mema-recipe/$PKG_NAME"
    mkdir -p "$BUILD_PATH/DEBIAN" "$BUILD_PATH/etc/mema/recipe"
    cp "$recipe" "$BUILD_PATH/etc/mema/recipe/"
    cat > "$BUILD_PATH/DEBIAN/control" <<EOF
Package: $PKG_NAME
Version: 0.1
Architecture: all
Maintainer: $MAINTAINER
Depends: mema${deps:+, $deps}
Homepage: $HOMEPAGE
Section: $SECTION
Priority: optional
Description: $DESCRIPTION
EOF
    build_deb "$BUILD_PATH" "$OUT_DIR/${PKG_NAME}_0.1_all.deb"
    rm -rf "$BUILD_PATH"

    PKG_NAME="mema-$NAME-latest"
    BUILD_PATH="/tmp/mema-recipe/$PKG_NAME"
    mkdir -p "$BUILD_PATH/DEBIAN"
    cat > "$BUILD_PATH/DEBIAN/control" <<EOF
Package: $PKG_NAME
Version: 1
Architecture: all
Maintainer: $MAINTAINER
Depends: mema, mema-$NAME${deps:+, $deps}
Homepage: $HOMEPAGE
Section: $SECTION
Priority: optional
Description: $DESCRIPTION
EOF
    cat > "$BUILD_PATH/DEBIAN/postinst" <<EOF
#!/bin/sh
set -e
/usr/local/bin/mema install $NAME latest
EOF
    chmod 755 "$BUILD_PATH/DEBIAN/postinst"
    build_deb "$BUILD_PATH" "$OUT_DIR/${PKG_NAME}_1_all.deb"
    rm -rf "$BUILD_PATH"

    unset -f mema_get_versions mema_resolve_version mema_install mema_use || true
done
