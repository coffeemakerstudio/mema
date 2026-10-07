#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(realpath "$(dirname "$0")/..")
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT

version_default=$(sed -n 's/^VERSION="${VERSION:-\([^}]*\)}"$/\1/p' "$repo_dir/build-repo.sh")
package_version=${VERSION:-$version_default}
[ -n "$package_version" ] || {
    printf 'could not determine package version from build-repo.sh\n' >&2
    exit 1
}

inspect_package() {
    local package=$1 arch=$2 extract_root=$3
    [ -f "$package" ] || {
        printf 'missing architecture-specific package: %s\n' "$package" >&2
        exit 1
    }
    [ "$(dpkg-deb -f "$package" Package)" = mema ]
    [ "$(dpkg-deb -f "$package" Version)" = "$package_version" ]
    [ "$(dpkg-deb -f "$package" Architecture)" = "$arch" ]
    [ "$(dpkg-deb -f "$package" Maintainer)" = 'Coffee Maker Studio <mema@lupricht.net>' ]
    [ "$(dpkg-deb -f "$package" Homepage)" = 'https://github.com/coffeemakerstudio/mema' ]
    [ "$(dpkg-deb -f "$package" Depends)" = 'curl, bash, git, jq, tar, xz-utils, ca-certificates, fzf, sudo, gpg' ]
    if dpkg-deb --contents "$package" | grep -Eiq '(^|/)(\.env|[^ ]*\.(asc|gpg|key)|[^ ]*private[^ ]*|[^ ]*qualification[^ ]*)($|/| )'; then
        printf 'package contains a secret or qualification-looking path: %s\n' "$package" >&2
        exit 1
    fi
    dpkg-deb --extract "$package" "$extract_root"
    for path in \
        usr/local/bin/mema \
        usr/local/bin/mema_download \
        usr/local/bin/mema_find_recipes \
        usr/local/bin/mema_list \
        usr/local/bin/mema_verify \
        opt/mema/config.d/00-init.sh \
        etc/profile.d/mema.sh; do
        [ -f "$extract_root/$path" ] || {
            printf 'required runtime file missing from %s: %s\n' "$package" "$path" >&2
            exit 1
        }
    done
    [ -x "$extract_root/usr/local/bin/mema" ]
}

for arch in riscv64 arm64; do
    dist="$tmp_dir/dist-$arch"
    package="$dist/mema_${package_version}_${arch}.deb"
    VERSION="$package_version" MEMA_ARCH="$arch" \
        MEMA_BINARY_OUTPUT="$tmp_dir/mema-$arch" \
        DEB_DIR="$tmp_dir/debs-$arch" \
        DIST_DIR="$dist" \
        "$repo_dir/build-repo.sh" >/dev/null

    inspect_package "$package" "$arch" "$tmp_dir/root-$arch"
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

amd64_dist="$tmp_dir/dist-amd64"
amd64_package="$amd64_dist/mema_${package_version}_amd64.deb"
VERSION="$package_version" MEMA_ARCH=amd64 MEMA_BUILD_RECIPES=0 \
    MEMA_BINARY_OUTPUT="$tmp_dir/mema-amd64" \
    DEB_DIR="$tmp_dir/debs-amd64" \
    DIST_DIR="$amd64_dist" \
    "$repo_dir/build-repo.sh" >/dev/null
inspect_package "$amd64_package" amd64 "$tmp_dir/root-amd64"
[ "$("$tmp_dir/root-amd64/usr/local/bin/mema" --version)" = "mema $package_version" ] || {
    printf 'amd64 runtime version does not match package version %s\n' "$package_version" >&2
    exit 1
}

printf '%s\n' "--- PASS: package metadata, runtime contents, and version $package_version for amd64, riscv64, and arm64 ---"
