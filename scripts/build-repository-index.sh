#!/usr/bin/env bash
set -euo pipefail

REPOSITORY_DIR=${1:-dist}
ARCHITECTURES=${MEMA_REPOSITORY_ARCHITECTURES:-'amd64 riscv64'}
[[ "$ARCHITECTURES" =~ ^(amd64|arm64|riscv64)(\ (amd64|arm64|riscv64))*$ ]] || {
    printf 'Invalid repository architecture list: %s\n' "$ARCHITECTURES" >&2; exit 1;
}
REPOSITORY_DIR=$(realpath "$REPOSITORY_DIR")
for cmd in dpkg-deb dpkg-scanpackages apt-ftparchive sha256sum gzip awk sort diff; do
    command -v "$cmd" >/dev/null || { printf 'Required command not found: %s\n' "$cmd" >&2; exit 1; }
done
[[ -d "$REPOSITORY_DIR" ]] || { printf 'Repository directory not found: %s\n' "$REPOSITORY_DIR" >&2; exit 1; }
shopt -s nullglob
packages=("$REPOSITORY_DIR"/*.deb)
((${#packages[@]} > 0)) || { printf 'No .deb artifacts found in %s\n' "$REPOSITORY_DIR" >&2; exit 1; }

tmp=$(mktemp -d)
trap 'rm -rf -- "$tmp"' EXIT
: > "$tmp/expected"
declare -A seen_tuple=()

for package_file in "${packages[@]}"; do
    filename=${package_file##*/}
    [[ -f "$package_file" && ! -L "$package_file" ]] || { printf 'Artifact must be a regular file: %s\n' "$filename" >&2; exit 1; }
    [[ "$filename" =~ ^[-A-Za-z0-9.+_~]+\.deb$ ]] || { printf 'Unsupported artifact filename: %q\n' "$filename" >&2; exit 1; }
    package=$(dpkg-deb -f "$package_file" Package)
    version=$(dpkg-deb -f "$package_file" Version)
    arch=$(dpkg-deb -f "$package_file" Architecture)
    [[ "$package" =~ ^[a-z0-9][a-z0-9+.-]*$ ]] || { printf 'Invalid Debian package name in %s: %s\n' "$filename" "$package" >&2; exit 1; }
    [[ "$version" =~ ^[A-Za-z0-9.+:~_-]+$ ]] || { printf 'Invalid Debian version in %s: %s\n' "$filename" "$version" >&2; exit 1; }
    case " $ARCHITECTURES " in *" $arch "*) ;; *) [[ "$arch" == all ]] || { printf 'Package architecture %s is not published by this repository: %s\n' "$arch" "$filename" >&2; exit 1; } ;; esac
    tuple="$package|$version|$arch"
    digest=$(sha256sum "$package_file" | awk '{print $1}')
    if [[ ${seen_tuple[$tuple]+yes} ]]; then
        if [[ ${seen_tuple[$tuple]} != "$digest" ]]; then
            printf 'Conflicting artifacts claim %s (different SHA-256).\n' "$tuple" >&2
        else
            printf 'Duplicate artifacts claim %s.\n' "$tuple" >&2
        fi
        exit 1
    fi
    seen_tuple[$tuple]=$digest
    printf '%s|%s|%s|./%s|%s\n' "$package" "$version" "$arch" "$filename" "$digest" >> "$tmp/expected"
done

(
    cd "$REPOSITORY_DIR"
    LC_ALL=C dpkg-scanpackages --multiversion . /dev/null > "$tmp/Packages.raw"
)
mkdir -p "$tmp/stanzas"
awk -v dir="$tmp/stanzas" '
    BEGIN { RS=""; FS="\n" }
    {
        delete f
        for (i=1; i<=NF; i++) {
            if (index($i, ": ") > 0 && substr($i,1,1) != " ") {
                split($i, pair, ": "); f[pair[1]]=pair[2]
            }
        }
        if (f["Package"] != "") {
            file=sprintf("%s/%06d", dir, ++count)
            print $0 > file
            close(file)
            print f["Package"] "\t" f["Version"] "\t" f["Architecture"] "\t" f["Filename"] "\t" f["SHA256"] "\t" file
        }
    }
' "$tmp/Packages.raw" | LC_ALL=C sort -t $'\t' -k1,1 -k2,2 -k3,3 -k4,4 > "$tmp/stanzas.sorted"
: > "$tmp/Packages"
while IFS=$'\t' read -r _package _version _arch _filename _sha stanza; do
    cat "$stanza" >> "$tmp/Packages"
    printf '\n\n' >> "$tmp/Packages"
done < "$tmp/stanzas.sorted"
awk '
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
' "$tmp/Packages" | LC_ALL=C sort > "$tmp/actual"
LC_ALL=C sort "$tmp/expected" > "$tmp/expected.sorted"
diff -u "$tmp/expected.sorted" "$tmp/actual" || {
    printf 'Generated Packages does not exactly represent the artifact set.\n' >&2; exit 1;
}

rm -f -- "$REPOSITORY_DIR/Packages" "$REPOSITORY_DIR/Packages.gz" \
    "$REPOSITORY_DIR/Release" "$REPOSITORY_DIR/Release.gpg" "$REPOSITORY_DIR/InRelease"
install -m 0644 "$tmp/Packages" "$REPOSITORY_DIR/Packages"
gzip -n -9 -c "$REPOSITORY_DIR/Packages" > "$REPOSITORY_DIR/Packages.gz"
cat > "$tmp/apt-ftparchive.conf" <<EOF
APT::FTPArchive::Release::Origin "Mema";
APT::FTPArchive::Release::Label "Mema";
APT::FTPArchive::Release::Suite "stable";
APT::FTPArchive::Release::Codename "stable";
APT::FTPArchive::Release::Architectures "$ARCHITECTURES";
EOF
(
    cd "$REPOSITORY_DIR"
    apt-ftparchive -c "$tmp/apt-ftparchive.conf" release . > "$tmp/Release"
)
install -m 0644 "$tmp/Release" "$REPOSITORY_DIR/Release"
printf 'Indexed %d package artifacts (%s) in %s\n' "${#packages[@]}" "$ARCHITECTURES" "$REPOSITORY_DIR"
