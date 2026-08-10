#!/bin/sh
# Mema Recipe for Go (Google Golang)
NAME="go"
DESCRIPTION="mema recipe, which installs the go recipe to use with mema for better version control. this package is auto-generated."
MAINTAINER="Coffee Maker Studio <mema@lupricht.net>"
SECTION="devel"

set -e

mema_get_versions() {
    local json
    json=$(curl -fsSL "https://go.dev/dl/?mode=json&include=all") || return 1
    echo "$json" | jq -r '
        .[]
        | select(.stable == true)
        | .version as $v
        | .files[]?
        | select(.os == "linux" and .kind == "archive")
        | "\($v | sub("go";"")) \(.arch) \(.sha256) \("https://go.dev/dl/" + .filename) libc6"
    '
}

mema_resolve_version() {
    local arch
    case "$(uname -m)" in
        x86_64) arch="amd64" ;;
        aarch64) arch="arm64" ;;
        *)
            printf 'Mema Error: Go is not supported on architecture %s.\n' "$(uname -m)" >&2
            return 1
            ;;
    esac
    local versions
    versions=$(mema_get_versions) || return 1
    printf '%s\n' "$versions" | awk -v arch="$arch" '$2 == arch { print $1; exit }'
}

mema_install() {
    local target_v="${MEMA_VERSION:?MEMA_VERSION is required}"
    local install_path="$MEMA_INSTALL_DIR"
    local sudo_cmd="$MEMA_SUDO"
    local arch
    case "$(uname -m)" in
        x86_64) arch="amd64" ;;
        aarch64) arch="arm64" ;;
        *)
            printf 'Mema Error: Go is not supported on architecture %s.\n' "$(uname -m)" >&2
            return 1
            ;;
    esac

    local versions_cache
    if ! versions_cache=$(mema_get_versions); then
        printf '%s\n' 'Mema Error: Failed to fetch version list.' >&2
        return 1
    fi
    if [ "$target_v" = "latest" ]; then
        target_v=$(printf '%s\n' "$versions_cache" | awk -v arch="$arch" '$2 == arch { print $1; exit }')
        [ -n "$target_v" ] || { printf 'Mema Error: Could not resolve latest version for %s\n' "$arch" >&2; return 1; }
    fi

    local entry hash url filename filepath
    entry=$(printf '%s\n' "$versions_cache" | awk -v version="$target_v" -v arch="$arch" '$1 == version && $2 == arch { print; exit }')
    [ -n "$entry" ] || { printf 'Mema Error: Version %s for %s not found!\n' "$target_v" "$arch" >&2; return 1; }
    hash=$(printf '%s\n' "$entry" | awk '{print $3}')
    url=$(printf '%s\n' "$entry" | awk '{print $4}')

    if [ ! -f "$install_path/bin/go" ]; then
        filename="${url##*/}"
        filepath=$(mema_download "$url" "$filename" "$hash") || return 1
        printf 'Mema: Extracting Go %s to %s...\n' "$target_v" "$install_path"
        $sudo_cmd mkdir -p "$install_path"
        $sudo_cmd tar -xzf "$filepath" -C "$install_path" --strip-components=1
    else
        printf 'Mema: Go %s already installed at %s\n' "$target_v" "$install_path"
    fi
    mema_use
}

mema_use() {
    local install_path="${MEMA_INSTALL_DIR:?MEMA_INSTALL_DIR is required}"
    local link_dir="${MEMA_LINK_DIR:?MEMA_LINK_DIR is required}"
    local target_v="${MEMA_VERSION:?MEMA_VERSION is required}"
    local sudo_cmd="$MEMA_SUDO"
    if [ ! -x "$install_path/bin/go" ] || [ ! -x "$install_path/bin/gofmt" ]; then
        printf 'Mema Error: Go %s is not installed at %s.\n' "$target_v" "$install_path" >&2
        return 1
    fi
    printf 'Mema: Activating Go %s in %s...\n' "$target_v" "$link_dir"
    $sudo_cmd mkdir -p "$link_dir"
    $sudo_cmd ln -sf "$install_path/bin/go" "$link_dir/go"
    $sudo_cmd ln -sf "$install_path/bin/gofmt" "$link_dir/gofmt"
    $sudo_cmd ln -sf "$install_path/bin/go" "$link_dir/go-$target_v"
}
