#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(realpath "$(dirname "$0")/..")
home_dir=$(mktemp -d)
trap 'rm -rf "$home_dir"' EXIT

PATH=$(env -i HOME="$home_dir" PATH=/usr/bin:/bin bash -c \
    'source "$1"; printf "%s" "$PATH"' bash "$repo_dir/configs/mema-loader.sh")
case ":$PATH:" in
    *:"$home_dir/go/bin":*) ;;
    *) printf 'GOPATH/bin is missing from fresh login PATH: %s\n' "$PATH" >&2; exit 1 ;;
esac
printf 'Mema login environment exposes Go user-tool path.\n'
