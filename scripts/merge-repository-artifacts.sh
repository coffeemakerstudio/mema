#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
OLD_REPOSITORY=${1:?usage: merge-repository-artifacts.sh OLD_REPOSITORY NEW_DEBS OUTPUT_REPOSITORY}
NEW_DEBS=${2:?usage: merge-repository-artifacts.sh OLD_REPOSITORY NEW_DEBS OUTPUT_REPOSITORY}
OUTPUT_REPOSITORY=${3:?usage: merge-repository-artifacts.sh OLD_REPOSITORY NEW_DEBS OUTPUT_REPOSITORY}
KEY_FILE=${MEMA_REPOSITORY_KEY:-$ROOT/mema.gpg}

for cmd in gpg gpgv dpkg-deb sha256sum awk sort diff; do
    command -v "$cmd" >/dev/null || { printf 'Required command not found: %s\n' "$cmd" >&2; exit 1; }
done
[[ -d "$OLD_REPOSITORY" && -d "$NEW_DEBS" ]] || { printf 'Old repository or new-artifact directory is missing.\n' >&2; exit 1; }
[[ -r "$KEY_FILE" ]] || { printf 'Repository public key is not readable: %s\n' "$KEY_FILE" >&2; exit 1; }

OLD_REPOSITORY=$(realpath "$OLD_REPOSITORY")
NEW_DEBS=$(realpath "$NEW_DEBS")
OUTPUT_REPOSITORY=$(realpath -m "$OUTPUT_REPOSITORY")
[[ "$OUTPUT_REPOSITORY" != "$OLD_REPOSITORY" && "$OUTPUT_REPOSITORY" != "$NEW_DEBS" ]] || {
    printf 'Output repository must be separate from source directories.\n' >&2; exit 1;
}

tmp=$(mktemp -d)
trap 'rm -rf -- "$tmp"' EXIT
KEYRING="$tmp/mema-keyring.gpg"
gpg --batch --yes --dearmor -o "$KEYRING" "$KEY_FILE"
if [[ -f "$OLD_REPOSITORY/mema-keyring.gpg" ]]; then
    cmp -s "$KEYRING" "$OLD_REPOSITORY/mema-keyring.gpg" || {
        printf 'Published repository keyring differs from the checked-in public key.\n' >&2; exit 1;
    }
fi
gpgv --keyring "$KEYRING" --output "$tmp/old.Release" "$OLD_REPOSITORY/InRelease" >/dev/null
[[ -f "$OLD_REPOSITORY/Packages" ]] || { printf 'Old repository lacks Packages.\n' >&2; exit 1; }
expected=$(awk '
    /^SHA256:$/ { in_sha=1; next }
    in_sha && /^[^[:space:]]/ { exit }
    in_sha && $3 == "Packages" { print $1; exit }
' "$tmp/old.Release")
actual=$(sha256sum "$OLD_REPOSITORY/Packages" | awk '{print $1}')
[[ -n "$expected" && "$actual" == "$expected" ]] || { printf 'Old Packages is not covered by signed InRelease.\n' >&2; exit 1; }

rm -rf -- "$OUTPUT_REPOSITORY"
mkdir -p -- "$OUTPUT_REPOSITORY"
if [[ -f "$OLD_REPOSITORY/mema-keyring.gpg" ]]; then
    cp -- "$OLD_REPOSITORY/mema-keyring.gpg" "$OUTPUT_REPOSITORY/mema-keyring.gpg"
fi
declare -A old_hash=() old_filename=()
: > "$tmp/old.expected"
while IFS='|' read -r package version arch filename digest; do
    [[ -n "$package" ]] || continue
    [[ "$filename" == ./*.deb && "$filename" != */*/* ]] || { printf 'Unsafe/unexpected Packages filename: %s\n' "$filename" >&2; exit 1; }
    basename=${filename#./}
    artifact="$OLD_REPOSITORY/$basename"
    [[ -f "$artifact" ]] || { printf 'Packages references missing artifact: %s\n' "$filename" >&2; exit 1; }
    got=$(sha256sum "$artifact" | awk '{print $1}')
    [[ "$got" == "$digest" ]] || { printf 'Packages SHA-256 mismatch: %s\n' "$filename" >&2; exit 1; }
    fields=$(dpkg-deb -f "$artifact" Package Version Architecture)
    expected_fields=$(printf 'Package: %s\nVersion: %s\nArchitecture: %s' "$package" "$version" "$arch")
    [[ "$fields" == "$expected_fields" ]] || { printf 'Packages/control mismatch: %s\n' "$filename" >&2; exit 1; }
    tuple="$package|$version|$arch"
    [[ ! ${old_hash[$tuple]+yes} ]] || { printf 'Duplicate tuple in signed Packages: %s\n' "$tuple" >&2; exit 1; }
    old_hash[$tuple]=$digest; old_filename[$tuple]=$basename
    cp -- "$artifact" "$OUTPUT_REPOSITORY/$basename"
    printf '%s|%s|%s|%s|%s\n' "$package" "$version" "$arch" "$filename" "$digest" >> "$tmp/old.expected"
done < <(awk '
    BEGIN { RS=""; FS="\n" }
    {
        delete f
        for (i=1; i<=NF; i++) {
            if (index($i, ": ") > 0 && substr($i,1,1) != " ") {
                split($i, pair, ": "); f[pair[1]]=pair[2]
            }
        }
        if (f["Package"] != "") print f["Package"] "|" f["Version"] "|" f["Architecture"] "|" f["Filename"] "|" f["SHA256"]
    }
' "$OLD_REPOSITORY/Packages")

shopt -s nullglob
new_packages=("$NEW_DEBS"/*.deb)
for artifact in "${new_packages[@]}"; do
    filename=${artifact##*/}
    package=$(dpkg-deb -f "$artifact" Package)
    version=$(dpkg-deb -f "$artifact" Version)
    arch=$(dpkg-deb -f "$artifact" Architecture)
    digest=$(sha256sum "$artifact" | awk '{print $1}')
    tuple="$package|$version|$arch"
    if [[ ${old_hash[$tuple]+yes} ]]; then
        if [[ ${old_hash[$tuple]} != "$digest" ]]; then
            printf 'Refusing to rewrite published package identity %s: old=%s new=%s\n' "$tuple" "${old_hash[$tuple]}" "$digest" >&2
            exit 1
        fi
        continue
    fi
    [[ ! -e "$OUTPUT_REPOSITORY/$filename" ]] || { printf 'Artifact filename collision: %s\n' "$filename" >&2; exit 1; }
    cp -- "$artifact" "$OUTPUT_REPOSITORY/$filename"
    old_hash[$tuple]=$digest
    printf 'Added %s\n' "$tuple"
done

# An old repository may not contain unindexed .debs; do not preserve invisible payloads.
old_debs=$(find "$OLD_REPOSITORY" -maxdepth 1 -type f -name '*.deb' | wc -l)
old_indexed=$(wc -l < "$tmp/old.expected")
[[ "$old_debs" -eq "$old_indexed" ]] || { printf 'Old repository has unindexed or multiply-indexed .deb artifacts.\n' >&2; exit 1; }

"$ROOT/scripts/build-repository-index.sh" "$OUTPUT_REPOSITORY"
