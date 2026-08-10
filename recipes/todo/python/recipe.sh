NAME="python"
DESCRIPTION="Python standalone runtime recipe."
MAINTAINER="Coffee Maker Studio <mema@lupricht.net>"
SECTION="devel"

mema_get_versions() {
    local repo_url="https://api.github.com/repos/astral-sh/python-build-standalone"
    local releases_json
    releases_json=$(curl -fsSL "$repo_url" | jq -c '.[:5]') || return 1
    [ -n "$releases_json" ] && [ "$releases_json" != "null" ] || return 1
    printf '%s\n' "$releases_json" | jq -r '
        .[] | .tag_name as $tag | .assets[]
        | select(.name | contains("install_only") and contains("linux-gnu") and (contains("x86_64") or contains("aarch64")))
        | .name as $name | .browser_download_url as $url
        | ($name | split("-")[1]) as $ver
        | (if $name | contains("x86_64") then "amd64" else "arm64" end) as $mema_arch
        | "\($ver) \($mema_arch) \($url)"
    ' | while read -r v arch url; do
        real_hash=$(curl -fsSL "${url}.sha256" | cut -d ' ' -f1) || exit 1
        [ -n "$real_hash" ] || exit 1
        printf '%s %s %s %s\n' "$v" "$arch" "$real_hash" "$url"
    done
}

mema_resolve_version() {
    mema_get_versions | awk 'NR == 1 { print $1 }'
}
