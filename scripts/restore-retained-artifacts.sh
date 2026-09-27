#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
LOCK=${1:-$ROOT/repository/retained-artifacts.tsv}
OUTPUT_DIR=${2:-$ROOT/dist}
KEY_FILE=${MEMA_REPOSITORY_KEY:-$ROOT/mema.gpg}

for cmd in git gpg gpgv dpkg-deb sha256sum awk; do
    command -v "$cmd" >/dev/null || { printf 'Required command not found: %s\n' "$cmd" >&2; exit 1; }
done
[[ -r "$LOCK" ]] || { printf 'Artifact lock is not readable: %s\n' "$LOCK" >&2; exit 1; }
[[ -r "$KEY_FILE" ]] || { printf 'Repository public key is not readable: %s\n' "$KEY_FILE" >&2; exit 1; }

tmp=$(mktemp -d)
trap 'rm -rf -- "$tmp"' EXIT
KEYRING="$tmp/mema-keyring.gpg"
gpg --batch --yes --dearmor -o "$KEYRING" "$KEY_FILE"

rm -rf -- "$OUTPUT_DIR"
mkdir -p -- "$OUTPUT_DIR"
declare -A seen_tuple=() seen_name=() verified_commit=()

verify_commit() {
    local commit=$1 inrelease="$tmp/$1.InRelease" release="$tmp/$1.Release" packages="$tmp/$1.Packages" expected actual
    [[ ${verified_commit[$commit]+yes} ]] && return 0
    git merge-base --is-ancestor "$commit" refs/remotes/origin/gh-pages || {
        printf 'Locked source commit is not in gh-pages history: %s\n' "$commit" >&2; return 1;
    }
    git show "$commit:InRelease" > "$inrelease"
    git show "$commit:Packages" > "$packages"
    gpgv --keyring "$KEYRING" --output "$release" "$inrelease" >/dev/null
    expected=$(awk '
        /^SHA256:$/ { in_sha=1; next }
        in_sha && /^[^[:space:]]/ { exit }
        in_sha && $3 == "Packages" { print $1; exit }
    ' "$release")
    actual=$(sha256sum "$packages" | awk '{print $1}')
    [[ -n "$expected" && "$actual" == "$expected" ]] || {
        printf 'Signed Packages checksum mismatch at %s\n' "$commit" >&2; return 1;
    }
    verified_commit[$commit]=yes
}

verify_stanza() {
    local packages=$1 package=$2 version=$3 arch=$4 filename=$5 digest=$6
    awk -v want_package="$package" -v want_version="$version" \
        -v want_arch="$arch" -v want_file="./$filename" -v want_sha="$digest" '
        BEGIN { RS=""; FS="\n" }
        {
            delete f
            for (i=1; i<=NF; i++) {
                if (index($i, ": ") > 0 && substr($i,1,1) != " ") {
                    split($i, pair, ": "); f[pair[1]]=pair[2]
                }
            }
            if (f["Package"]==want_package && f["Version"]==want_version &&
                f["Architecture"]==want_arch && f["Filename"]==want_file &&
                f["SHA256"]==want_sha) found=1
        }
        END { exit(found ? 0 : 1) }
    ' "$packages"
}

while IFS=$'\t' read -r package version arch filename digest commit; do
    [[ -z "$package" || "$package" == \#* ]] && continue
    [[ "$package" =~ ^[a-z0-9][a-z0-9+.-]*$ ]] || { printf 'Invalid package name in lock: %s\n' "$package" >&2; exit 1; }
    [[ "$version" =~ ^[A-Za-z0-9.+:~_-]+$ ]] || { printf 'Invalid package version in lock: %s\n' "$version" >&2; exit 1; }
    [[ "$arch" =~ ^(all|amd64|riscv64)$ ]] || { printf 'Invalid/unpublished architecture in lock: %s\n' "$arch" >&2; exit 1; }
    [[ "$filename" == *.deb && "$filename" != */* && "$filename" != . && "$filename" != .. ]] || {
        printf 'Unsafe artifact filename in lock: %s\n' "$filename" >&2; exit 1;
    }
    [[ "$digest" =~ ^[[:xdigit:]]{64}$ ]] || { printf 'Invalid SHA-256 in lock for %s\n' "$filename" >&2; exit 1; }
    [[ "$commit" =~ ^[[:xdigit:]]{40}$ ]] || { printf 'Invalid Pages commit in lock for %s\n' "$filename" >&2; exit 1; }
    tuple="$package|$version|$arch"
    [[ ! ${seen_tuple[$tuple]+yes} ]] || { printf 'Duplicate package tuple in lock: %s\n' "$tuple" >&2; exit 1; }
    [[ ! ${seen_name[$filename]+yes} ]] || { printf 'Duplicate artifact filename in lock: %s\n' "$filename" >&2; exit 1; }
    seen_tuple[$tuple]=yes; seen_name[$filename]=yes

    verify_commit "$commit"
    packages="$tmp/$commit.Packages"
    verify_stanza "$packages" "$package" "$version" "$arch" "$filename" "$digest" || {
        printf 'Locked artifact is absent from its signed Packages index: %s %s %s\n' "$tuple" "$commit" >&2; exit 1;
    }
    target="$OUTPUT_DIR/$filename"
    git show "$commit:$filename" > "$target"
    actual=$(sha256sum "$target" | awk '{print $1}')
    [[ "$actual" == "$digest" ]] || { printf 'Locked artifact checksum mismatch: %s\n' "$filename" >&2; exit 1; }
    fields=$(dpkg-deb -f "$target" Package Version Architecture)
    expected_fields=$(printf 'Package: %s\nVersion: %s\nArchitecture: %s' "$package" "$version" "$arch")
    [[ "$fields" == "$expected_fields" ]] || {
        printf 'Debian control metadata mismatch for %s\n' "$filename" >&2; exit 1;
    }
done < "$LOCK"

((${#seen_tuple[@]} > 0)) || { printf 'Artifact lock contains no package records.\n' >&2; exit 1; }
printf 'Restored %d exact published artifacts into %s\n' "${#seen_tuple[@]}" "$OUTPUT_DIR"
