#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(realpath "$(dirname "$0")/..")
home_dir=$(mktemp -d)
trap 'rm -rf "$home_dir"' EXIT

PATH=$(env -i HOME="$home_dir" PATH=/usr/bin:/bin bash -c \
    'source "$1"; printf "%s" "$PATH"' bash "$repo_dir/configs/mema-loader.sh")
for expected in "$home_dir/go/bin" "$home_dir/.bun/bin"; do
    case ":$PATH:" in
        *:"$expected":*) ;;
        *) printf 'user tool path is missing from fresh login PATH: %s\n' "$expected" >&2; exit 1 ;;
    esac
done
printf 'Mema login environment exposes Go and Bun user-tool paths.\n'
