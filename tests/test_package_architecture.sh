#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(realpath "$(dirname "$0")/..")
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT

MEMA_ARCH=riscv64 DIST_DIR="$tmp_dir/dist" "$repo_dir/build-repo.sh" >/dev/null

package="$tmp_dir/dist/mema_0.2_riscv64.deb"
[ -f "$package" ] || {
    printf 'missing architecture-specific package: %s\n' "$package" >&2
    exit 1
}
[ "$(dpkg-deb -f "$package" Architecture)" = riscv64 ] || {
    printf 'unexpected package architecture\n' >&2
    exit 1
}
for recipe in bun deno go lib-display-info lib-expat lib-libffi lib-xml2 node php python ruby rust sway wayland wlroots; do
    ! compgen -G "$tmp_dir/dist/mema-${recipe}_*_riscv64.deb" >/dev/null || {
        printf 'unsupported recipe was packaged for riscv64: %s\n' "$recipe" >&2
        exit 1
    }
done
[ -e "$tmp_dir/dist/mema-wayland-protocols_1.43_riscv64.deb" ] || {
    printf 'architecture-independent recipe was not packaged for riscv64\n' >&2
    exit 1
}

printf '%s\n' '--- PASS: core package architecture metadata ---'
