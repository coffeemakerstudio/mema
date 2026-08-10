#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(realpath "$(dirname "$0")/..")
printf '%s\n' '--- Starting Sway POC Debian test container ---'
docker run --rm \
    -v "$repo_dir/dist:/repo:ro" \
    -v "$repo_dir/tests/sway_poc_inside.sh:/sway_poc_inside.sh:ro" \
    debian:bookworm-slim \
    bash /sway_poc_inside.sh
