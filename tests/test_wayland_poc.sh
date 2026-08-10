#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(realpath "$(dirname "$0")/..")
printf '%s\n' '--- Starting Wayland POC Debian test container ---'
docker run --rm \
    -v "$repo_dir/dist:/repo:ro" \
    -v "$repo_dir/tests/wayland_poc_inside.sh:/wayland_poc_inside.sh:ro" \
    debian:bookworm-slim \
    bash /wayland_poc_inside.sh
