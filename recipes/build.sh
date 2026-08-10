#!/usr/bin/env bash
set -euo pipefail
RECIPE_DIR="recipes"
MAINTAINER="Coffee Maker Studio <mema@lupricht.net>"
HOMEPAGE="https://github.com/coffeemakerstudio/mema"
OUT_DIR="dist"

if [ ! -d "$RECIPE_DIR" ]; then
    printf 'Error: directory %s not found.\n' "$RECIPE_DIR" >&2
    exit 1
fi

rm -rf "/tmp/mema-recipe" "$OUT_DIR"
mkdir -p "$OUT_DIR"
for recipe in "$RECIPE_DIR"/*/*.sh; do
    [ -f "$recipe" ] || continue
    printf '%s\n' "--- Processing recipe: $recipe ---"

    source "$recipe"
    if declare -F mema_get_versions >/dev/null; then
        PKG_NAME="mema-$NAME"
        BUILD_PATH="/tmp/mema-recipe/$PKG_NAME"
        mkdir -p "$BUILD_PATH"
        DEPENDS="mema${deps:+, $deps}"
        COMMAND=""

        JSON=$(sed -e "s|{{NAME}}|$PKG_NAME|g" \
            -e 's|{{VERSION}}|0.1|g' \
            -e 's|{{ARCH}}|all|g' \
            -e "s|{{MAINTAINER}}|$MAINTAINER|g" \
            -e "s|{{DESCRIPTION}}|$DESCRIPTION|g" \
            -e "s|{{DEPENDS}}|$DEPENDS|g" \
            -e "s|{{HOMEPAGE}}|$HOMEPAGE|g" \
            -e "s|{{SECTION}}|$SECTION|g" \
            -e "s|{{OUT_DIR}}|$BUILD_PATH|g" \
            -e "s|{{COMMAND}}|$COMMAND|g" \
            templates/template.json)
        printf '%s\n' "$JSON" | tpa json
        rm -rf "$BUILD_PATH/usr" "$BUILD_PATH/DEBIAN/postrm" \
            "$BUILD_PATH/DEBIAN/postinst" "$BUILD_PATH/DEBIAN/prerm" \
            "$BUILD_PATH/DEBIAN/preinst"
        mkdir -p "$BUILD_PATH/etc/mema/recipe"
        cp "$recipe" "$BUILD_PATH/etc/mema/recipe/"
        tpa build -in="$BUILD_PATH" -out="$OUT_DIR"
        rm -rf "$BUILD_PATH"

        PKG_NAME="mema-$NAME-latest"
        BUILD_PATH="/tmp/mema-recipe/$PKG_NAME"
        DEPENDS="mema, mema-$NAME${deps:+, $deps}"
        COMMAND=""
        JSON=$(sed -e "s|{{NAME}}|$PKG_NAME|g" \
            -e 's|{{VERSION}}|1|g' \
            -e 's|{{ARCH}}|all|g' \
            -e "s|{{MAINTAINER}}|$MAINTAINER|g" \
            -e "s|{{DESCRIPTION}}|$DESCRIPTION|g" \
            -e "s|{{DEPENDS}}|$DEPENDS|g" \
            -e "s|{{HOMEPAGE}}|$HOMEPAGE|g" \
            -e "s|{{SECTION}}|$SECTION|g" \
            -e "s|{{OUT_DIR}}|$BUILD_PATH|g" \
            -e "s|{{COMMAND}}|$COMMAND|g" \
            templates/template.json)
        printf '%s\n' "$JSON" | tpa json
        rm -rf "$BUILD_PATH/usr" "$BUILD_PATH/DEBIAN/postrm" \
            "$BUILD_PATH/DEBIAN/prerm" "$BUILD_PATH/DEBIAN/preinst" \
            "$BUILD_PATH/DEBIAN/postinst"
        cat > "$BUILD_PATH/DEBIAN/postinst" <<EOF
#!/bin/sh
set -e
/usr/local/bin/mema install $NAME latest
EOF
        chmod 755 "$BUILD_PATH/DEBIAN/postinst"
        tpa build -in="$BUILD_PATH" -out="$OUT_DIR"
        rm -rf "$BUILD_PATH"
    else
        printf 'Warning: mema_get_versions() not found in %s\n' "$recipe" >&2
    fi
    unset -f mema_get_versions mema_resolve_version mema_install mema_use || true
done
