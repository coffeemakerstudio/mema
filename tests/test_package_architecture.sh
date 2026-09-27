#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(realpath "$(dirname "$0")/..")
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT

for arch in riscv64 arm64; do
    dist="$tmp_dir/dist-$arch"
    MEMA_ARCH="$arch" \
        MEMA_BINARY_OUTPUT="$tmp_dir/mema-$arch" \
        DEB_DIR="$tmp_dir/debs-$arch" \
        DIST_DIR="$dist" \
        "$repo_dir/build-repo.sh" >/dev/null

    package="$dist/mema_0.4.1_${arch}.deb"
    [ -f "$package" ] || {
        printf 'missing architecture-specific package: %s\n' "$package" >&2
        exit 1
    }
    [ "$(dpkg-deb -f "$package" Architecture)" = "$arch" ] || {
        printf 'unexpected package architecture for %s\n' "$package" >&2
        exit 1
    }
    grep -qx "Architectures: $arch" "$dist/Release"

    case "$arch" in
        riscv64)
            unsupported='bun deno go lib-display-info lib-expat lib-libffi lib-xml2 node php python ruby rust sway wayland wlroots'
            ;;
        arm64)
            unsupported='lib-display-info lib-expat lib-libffi lib-xml2 sway wayland wlroots'
            ;;
    esac
    for recipe in $unsupported; do
        ! compgen -G "$dist/mema-${recipe}_*_${arch}.deb" >/dev/null || {
            printf 'unsupported recipe was packaged for %s: %s\n' "$arch" "$recipe" >&2
            exit 1
        }
    done
    [ -e "$dist/mema-wayland-protocols_1.43_${arch}.deb" ] || {
        printf 'architecture-independent recipe was not packaged for %s\n' "$arch" >&2
        exit 1
    }
done

printf '%s\n' '--- PASS: core package metadata for riscv64 and arm64 ---'
