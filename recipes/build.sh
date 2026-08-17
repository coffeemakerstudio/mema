#!/usr/bin/env bash
set -euo pipefail

RECIPE_DIR="${RECIPE_DIR:-recipes}"
MAINTAINER="Coffee Maker Studio <mema@lupricht.net>"
HOMEPAGE="https://github.com/coffeemakerstudio/mema"
OUT_DIR="${OUT_DIR:-dist}"
PACKAGE_ARCH="${MEMA_ARCH:-all}"

[ -d "$RECIPE_DIR" ] || { printf 'Error: directory %s not found.\n' "$RECIPE_DIR" >&2; exit 1; }
rm -rf "/tmp/mema-recipe" "$OUT_DIR"
mkdir -p "$OUT_DIR"

build_deb() {
    local path="$1" output="$2"
    dpkg-deb --build "$path" "$output" >/dev/null
}

validate_package_version() {
    case "$MEMA_PACKAGE_VERSION" in
        ''|*[!A-Za-z0-9.+:~-]*)
            printf 'Invalid Debian package version %q for %s\n' "$MEMA_PACKAGE_VERSION" "$NAME" >&2
            return 1
            ;;
    esac
}

recipe_package_dependencies() {
    local dependency version
    set -- ${MEMA_INSTALL_DEPENDS:-}
    [ "$(( $# % 2 ))" -eq 0 ] || {
        printf 'MEMA_INSTALL_DEPENDS must contain recipe/version pairs for %s\n' "$NAME" >&2
        return 1
    }
    while [ "$#" -gt 0 ]; do
        dependency="$1"
        version="$2"
        case "$dependency" in
            ''|*[!A-Za-z0-9+.-]*)
                printf 'Invalid Mema dependency %q for %s\n' "$dependency" "$NAME" >&2
                return 1
                ;;
        esac
        printf 'mema-%s (= %s), ' "$dependency" "$version"
        shift 2
    done | sed 's/, $//'
}

recipe_install_commands() {
    local dependency version
    set -- ${MEMA_INSTALL_DEPENDS:-}
    while [ "$#" -gt 0 ]; do
        dependency="$1"
        version="$2"
        printf '/usr/local/bin/mema install %q %q\n' "$dependency" "$version"
        shift 2
    done
    printf '/usr/local/bin/mema install %q %q\n' "$NAME" "$MEMA_PACKAGE_VERSION"
}

build_recipe_package() {
    local package_name="$1" package_version="$2" package_depends="$3"
    local build_path="/tmp/mema-recipe/$package_name"
    mkdir -p "$build_path/DEBIAN" "$build_path/etc/mema/recipe"
    cp "$recipe" "$build_path/etc/mema/recipe/"
    cat > "$build_path/DEBIAN/control" <<EOF
Package: $package_name
Version: $package_version
Architecture: $PACKAGE_ARCH
Maintainer: $MAINTAINER
Depends: $package_depends
Homepage: $HOMEPAGE
Section: $SECTION
Priority: optional
Description: $DESCRIPTION
EOF
    build_deb "$build_path" "$OUT_DIR/${package_name}_${package_version}_${PACKAGE_ARCH}.deb"
    rm -rf "$build_path"
}

for recipe in "$RECIPE_DIR"/*/*.sh; do
    [ -f "$recipe" ] || continue
    printf '%s\n' "--- Processing recipe: $recipe ---"
    unset NAME DESCRIPTION SECTION deps MEMA_PACKAGE_VERSION MEMA_AUTOINSTALL MEMA_DEPENDS MEMA_INSTALL_DEPENDS MEMA_SUPPORTED_ARCHES RECIPE_DEPS
    source "$recipe"
    if [ -n "${MEMA_SUPPORTED_ARCHES:-}" ] && [ -n "${MEMA_ARCH:-}" ] &&
        ! case " $MEMA_SUPPORTED_ARCHES " in *" $MEMA_ARCH "*) true ;; *) false ;; esac; then
        printf 'Skipping %s on architecture %s\n' "$recipe" "$MEMA_ARCH"
        unset -f mema_get_versions mema_resolve_version mema_install mema_use || true
        continue
    fi
    if ! declare -F mema_get_versions >/dev/null; then
        printf 'Warning: mema_get_versions() not found in %s\n' "$recipe" >&2
        unset -f mema_get_versions mema_resolve_version mema_install mema_use || true
        continue
    fi

    MEMA_PACKAGE_VERSION="${MEMA_PACKAGE_VERSION:-0.1}"
    validate_package_version
    if [ "${MEMA_AUTOINSTALL:-0}" = "1" ]; then
        RECIPE_DEPS=$(recipe_package_dependencies)
        build_path="/tmp/mema-recipe/mema-$NAME"
        mkdir -p "$build_path/DEBIAN" "$build_path/etc/mema/recipe"
        cp "$recipe" "$build_path/etc/mema/recipe/"
        cat > "$build_path/DEBIAN/control" <<EOF
Package: mema-$NAME
Version: $MEMA_PACKAGE_VERSION
Architecture: $PACKAGE_ARCH
Maintainer: $MAINTAINER
Depends: mema${deps:+, $deps}${RECIPE_DEPS:+, $RECIPE_DEPS}
Homepage: $HOMEPAGE
Section: $SECTION
Priority: optional
Description: $DESCRIPTION
EOF
        cat > "$build_path/DEBIAN/postinst" <<EOF
#!/bin/sh
set -e
$(recipe_install_commands)
EOF
        chmod 755 "$build_path/DEBIAN/postinst"
        build_deb "$build_path" "$OUT_DIR/mema-${NAME}_${MEMA_PACKAGE_VERSION}_${PACKAGE_ARCH}.deb"
        rm -rf "$build_path"
    else
        build_recipe_package "mema-$NAME" "$MEMA_PACKAGE_VERSION" "mema${deps:+, $deps}"
        legacy_deps=""
        for dependency in ${MEMA_DEPENDS:-}; do
            legacy_deps="${legacy_deps:+$legacy_deps, }mema-$dependency-latest"
        done
        latest_path="/tmp/mema-recipe/mema-$NAME-latest"
        mkdir -p "$latest_path/DEBIAN"
        cat > "$latest_path/DEBIAN/control" <<EOF
Package: mema-$NAME-latest
Version: 1
Architecture: $PACKAGE_ARCH
Maintainer: $MAINTAINER
Depends: mema, mema-$NAME${deps:+, $deps}${legacy_deps:+, $legacy_deps}
Homepage: $HOMEPAGE
Section: $SECTION
Priority: optional
Description: $DESCRIPTION
EOF
        cat > "$latest_path/DEBIAN/postinst" <<EOF
#!/bin/sh
set -e
$(for dependency in ${MEMA_DEPENDS:-}; do printf '/usr/local/bin/mema install %q latest\n' "$dependency"; done)
/usr/local/bin/mema install $NAME latest
EOF
        chmod 755 "$latest_path/DEBIAN/postinst"
        rm -f "$OUT_DIR/mema-$NAME-latest_1_${PACKAGE_ARCH}.deb"
        build_deb "$latest_path" "$OUT_DIR/mema-${NAME}-latest_1_${PACKAGE_ARCH}.deb"
        rm -rf "$latest_path"
    fi
    unset -f mema_get_versions mema_resolve_version mema_install mema_use || true
done
