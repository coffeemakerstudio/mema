#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(realpath "$(dirname "$0")/..")
recipe_dir="${RECIPE_DIR:-$repo_dir/recipes/recipes}"
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT

check_recipe() {
    local recipe="$1" versions resolved
    printf 'Checking %s\n' "${recipe#"$repo_dir/"}"

    sh -n "$recipe"
    for function in mema_get_versions mema_resolve_version mema_install mema_use; do
        bash -c 'source "$1"; declare -F "$2" >/dev/null' check "$recipe" "$function"
    done

    versions=$(bash -c 'source "$1"; mema_get_versions' check "$recipe")
    [ -n "$versions" ] || { printf 'empty version list: %s\n' "$recipe" >&2; return 1; }
    printf '%s\n' "$versions" | awk '
        NF < 4 || $1 == "" || $2 == "" || $3 !~ /^[[:xdigit:]]{64}$/ || $4 !~ /^https:\/\// {
            printf "invalid version record: %s\n", $0 > "/dev/stderr"; bad = 1
        }
        END { exit bad }
    '

    resolved=$(bash -c 'source "$1"; mema_resolve_version' check "$recipe")
    case "$resolved" in
        ''|*[[:space:]]*) printf 'invalid resolved version: %s\n' "$recipe" >&2; return 1 ;;
    esac
}

shopt -s nullglob
recipes=("$recipe_dir"/*/*.sh)
[ "${#recipes[@]}" -gt 0 ] || { printf 'no recipes found in %s\n' "$recipe_dir" >&2; exit 1; }
for recipe in "${recipes[@]}"; do
    check_recipe "$recipe"
done

RECIPE_DIR="$recipe_dir" OUT_DIR="$tmp_dir/dist" "$repo_dir/recipes/build.sh" >/dev/null
printf 'Recipe dry run passed: %s recipe(s) checked; no runtime was installed.\n' "${#recipes[@]}"
