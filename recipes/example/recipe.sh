#!/bin/sh
# Copy this file to recipes/<tool>/<tool>.sh and replace every placeholder.
NAME="example-tool"
DESCRIPTION="Example Mema recipe."
MAINTAINER="Mema contributors"
SECTION="devel"

mema_get_versions() {
    : "${EXAMPLE_TOOL_RELEASES_URL:?set an upstream release API URL}"
    curl -fsSL "$EXAMPLE_TOOL_RELEASES_URL" | jq -r '.[] | "REPLACE_VERSION REPLACE_ARCH REPLACE_SHA256 REPLACE_URL"'
}

mema_resolve_version() {
    mema_get_versions | awk 'NR == 1 { print $1 }'
}
