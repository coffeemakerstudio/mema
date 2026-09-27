#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf -- "$tmp"' EXIT
export GNUPGHOME="$tmp/gnupg"
mkdir -m 0700 "$GNUPGHOME"

make_deb() {
    local output=$1 package=$2 version=$3 arch=$4 marker=$5 root="$tmp/root-$RANDOM"
    mkdir -p "$root/DEBIAN" "$root/usr/share/$package"
    cat > "$root/DEBIAN/control" <<EOF
Package: $package
Version: $version
Architecture: $arch
Maintainer: Mema Repository Test <test@example.invalid>
Depends: bash
Description: Repository retention fixture
 $marker
EOF
    printf '%s\n' "$marker" > "$root/usr/share/$package/marker"
    dpkg-deb --build --root-owner-group "$root" "$output" >/dev/null
}

sign_repo() {
    local dir=$1
    gpg --batch --yes --local-user "$FPR" --clearsign --digest-algo SHA256 \
        -o "$dir/InRelease" "$dir/Release"
    gpg --batch --yes --local-user "$FPR" --armor --detach-sign --digest-algo SHA256 \
        -o "$dir/Release.gpg" "$dir/Release"
}

assert_stanza() {
    local packages=$1 package=$2 version=$3 arch=$4
    awk -v p="$package" -v v="$version" -v a="$arch" '
        BEGIN { RS=""; FS="\n" }
        {
            delete f
            for (i=1; i<=NF; i++) if (index($i, ": ") && substr($i,1,1)!=" ") {
                split($i, pair, ": "); f[pair[1]]=pair[2]
            }
            if (f["Package"]==p && f["Version"]==v && f["Architecture"]==a) found=1
        }
        END { exit(found ? 0 : 1) }
    ' "$packages"
}

# Old signed repository contains 0.4.0. Candidate contains a newer version,
# a second architecture, and two versions of another package.
mkdir -p "$tmp/old" "$tmp/new" "$tmp/old-missing" "$tmp/old-corrupt" "$tmp/conflict"
make_deb "$tmp/old/mema_0.4.0_amd64.deb" mema 0.4.0 amd64 old
make_deb "$tmp/new/mema_0.4.1_amd64.deb" mema 0.4.1 amd64 new-amd64
make_deb "$tmp/new/mema_0.4.1_riscv64.deb" mema 0.4.1 riscv64 new-riscv64
make_deb "$tmp/new/tool_1.0_all.deb" tool 1.0 all tool-v1
make_deb "$tmp/new/tool_2.0_all.deb" tool 2.0 all tool-v2
make_deb "$tmp/new/dual_1.0_all.deb" dual 1.0 all dual-all
make_deb "$tmp/new/dual_1.0_amd64.deb" dual 1.0 amd64 dual-native
make_deb "$tmp/new/dual_1.0_riscv64.deb" dual 1.0 riscv64 dual-riscv

MEMA_REPOSITORY_ARCHITECTURES='amd64 riscv64' "$repo_dir/scripts/build-repository-index.sh" "$tmp/old" >/dev/null
FPR=$(gpg --batch --passphrase '' --quick-generate-key 'Mema retention test <retention@example.invalid>' ed25519 sign 0 2>/dev/null | awk '/fingerprint:/ {print $NF}')
FPR=$(gpg --batch --with-colons --list-secret-keys | awk -F: '$1=="fpr" {print $10; exit}')
gpg --batch --armor --export "$FPR" > "$tmp/test-key.asc"
sign_repo "$tmp/old"
MEMA_REPOSITORY_KEY="$tmp/test-key.asc" \
    "$repo_dir/scripts/merge-repository-artifacts.sh" "$tmp/old" "$tmp/new" "$tmp/merged" >/dev/null
sign_repo "$tmp/merged"
gpg --batch --dearmor --yes -o "$tmp/test-key.gpg" "$tmp/test-key.asc"
gpgv --keyring "$tmp/test-key.gpg" "$tmp/merged/InRelease" >/dev/null 2>&1

assert_stanza "$tmp/merged/Packages" mema 0.4.0 amd64
assert_stanza "$tmp/merged/Packages" mema 0.4.1 amd64
assert_stanza "$tmp/merged/Packages" mema 0.4.1 riscv64
assert_stanza "$tmp/merged/Packages" tool 1.0 all
assert_stanza "$tmp/merged/Packages" tool 2.0 all
awk '
    BEGIN { RS=""; FS="\n" }
    {
        delete f
        for (i=1; i<=NF; i++) if (index($i, ": ") && substr($i,1,1)!=" ") {
            split($i, pair, ": "); f[pair[1]]=pair[2]
        }
        if (f["Package"]=="dual" && f["Version"]=="1.0") {
            if (f["Architecture"]=="amd64") amd=NR
            if (f["Architecture"]=="riscv64") riscv=NR
            if (f["Architecture"]=="all") generic=NR
        }
    }
    END { exit(amd>0 && riscv>amd && generic>riscv ? 0 : 1) }
' "$tmp/merged/Packages"
grep -qx 'Architectures: amd64 riscv64' "$tmp/merged/Release"
[ "$(grep -c '^Package: mema$' "$tmp/merged/Packages")" -eq 3 ]

# Re-indexing the same set is deterministic; compressed Packages excludes mtime.
cp -a "$tmp/merged" "$tmp/repeat"
MEMA_REPOSITORY_ARCHITECTURES='amd64 riscv64' "$repo_dir/scripts/build-repository-index.sh" "$tmp/repeat" >/dev/null
cmp "$tmp/merged/Packages" "$tmp/repeat/Packages"
cmp "$tmp/merged/Packages.gz" "$tmp/repeat/Packages.gz"
mkdir "$tmp/reordered"
while IFS= read -r artifact; do cp "$artifact" "$tmp/reordered/"; done < <(find "$tmp/merged" -maxdepth 1 -type f -name '*.deb' | LC_ALL=C sort -r)
MEMA_REPOSITORY_ARCHITECTURES='amd64 riscv64' "$repo_dir/scripts/build-repository-index.sh" "$tmp/reordered" >/dev/null
cmp "$tmp/merged/Packages" "$tmp/reordered/Packages"
cmp "$tmp/merged/Packages.gz" "$tmp/reordered/Packages.gz"

# A conflicting package identity must fail closed.
make_deb "$tmp/conflict/mema_0.4.1_amd64.deb" mema 0.4.1 amd64 changed-bytes
if MEMA_REPOSITORY_KEY="$tmp/test-key.asc" \
    "$repo_dir/scripts/merge-repository-artifacts.sh" "$tmp/merged" "$tmp/conflict" "$tmp/conflict-out" >"$tmp/conflict.log" 2>&1; then
    printf 'conflicting same-version artifact was accepted\n' >&2; exit 1
fi
grep -q 'Refusing to rewrite published package identity mema|0.4.1|amd64' "$tmp/conflict.log"

# Missing and checksum-invalid files referenced by signed Packages are rejected.
cp -a "$tmp/old/." "$tmp/old-missing/"
rm "$tmp/old-missing/mema_0.4.0_amd64.deb"
if MEMA_REPOSITORY_KEY="$tmp/test-key.asc" \
    "$repo_dir/scripts/merge-repository-artifacts.sh" "$tmp/old-missing" "$tmp/new" "$tmp/missing-out" >"$tmp/missing.log" 2>&1; then
    printf 'missing artifact referenced by Packages was accepted\n' >&2; exit 1
fi
grep -q 'Packages references missing artifact' "$tmp/missing.log"
cp -a "$tmp/old/." "$tmp/old-corrupt/"
printf 'corruption' >> "$tmp/old-corrupt/mema_0.4.0_amd64.deb"
if MEMA_REPOSITORY_KEY="$tmp/test-key.asc" \
    "$repo_dir/scripts/merge-repository-artifacts.sh" "$tmp/old-corrupt" "$tmp/new" "$tmp/corrupt-out" >"$tmp/corrupt.log" 2>&1; then
    printf 'artifact with incorrect indexed checksum was accepted\n' >&2; exit 1
fi
grep -q 'Packages SHA-256 mismatch' "$tmp/corrupt.log"

printf '%s\n' '--- PASS: retained versions, native-architecture preference, checksum and conflict guards ---'
