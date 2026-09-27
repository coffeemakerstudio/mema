# Signed APT repository policy

## Retention and immutability

- Keep every successfully published, versioned `.deb` indexed and downloadable indefinitely. This includes the core `mema` package and versioned recipe packages. APT's version selection and explicit-version installation are part of the recovery path; the current version must not silently erase the ability to install a previous one.
- A package identity is the exact tuple `(Package, Version, Architecture)`. Once published, that tuple is immutable: a different `.deb` SHA-256 for the same tuple is a release error, not an update. Bump the Debian package version when package bytes change.
- `*-latest` packages are tracking-channel packages, not historical stable releases. Retain their currently published artifact. Their package identity is still immutable; bump their Debian package version whenever their bytes change.
- Generate `Packages` from the complete retained `.deb` set with multi-version indexing. Do not carry forward hand-edited `Packages` entries. Verify every indexed file and checksum, reject unindexed artifacts and duplicate/conflicting identities, then generate `Release` and signed `InRelease`.
- Retention is indefinite for released artifacts. The active set contains only published `.deb` files, not build caches. Review storage use as part of release maintenance; do not silently prune versions to meet a storage limit.

This policy preserves explicit installation and downgrade options, and avoids making disaster recovery of an older service depend on an unavailable Mema package. For example, Bookworm consumers can install `mema=0.4.0` and then upgrade to `mema=0.4.1` from the same signed index.

## Architecture scope

The package builder explicitly targets `amd64`, `arm64`, and `riscv64`; the recipe catalog also declares arm64 support for multiple production recipes. The public release workflow currently publishes **amd64 and riscv64 only**. Therefore arm64 is **supported but not currently published**: this is an intentional CI/release-scope omission, not an inference from Go cross-compilation. The public `Release` metadata currently declares `Architectures: amd64 riscv64`. No arm64 Mema `.deb` has been published in the Pages history audited for this policy. Do not add arm64 to the published repository until its artifact and runtime qualification are complete and the release matrix is explicitly extended.

`Architecture: all` package entries are valid architecture-independent artifacts and may appear in `Packages`; the `Release` architecture declaration names the native package architectures published by the repository.

## Historical migration and release workflow

Earlier Pages deployments built a fresh `dist/` containing only the current package build, then deployed it with a clean replacement. That deleted prior `.deb` files and caused `apt-ftparchive` to index only the latest staging set. The deployment cleanup was not an intentional latest-only policy.

`repository/retained-artifacts.tsv` is the bootstrap inventory selected from signed Pages history. Each entry records the package identity, filename, SHA-256, and Pages commit that last indexed that exact artifact. Its 79 artifacts were checked against their signed indexes and Debian control metadata. Historical Pages history contained multiple different hashes for 65 package identities that had been rebuilt under the same version; the migration selects the latest valid signed observation for each identity because APT cannot safely index conflicting bytes under one identity. Three malformed historical `mema-Go` index observations did not match their `.deb` control package name and are not restored. One earlier, unsigned Pages commit contained an unindexed `dist/mema.deb` (version 0.0.1, architecture `all`, SHA-256 `b7dc003ddac0522041affac2f99bd8ee1f86ff1db729815aeca9a4e0380f1222`). It was not part of any signed APT index and is absent from the current Pages tree, so it is outside this signed-repository retention repair; the original Pages commit remains in branch history. Forward from this baseline, the merge tool rejects any changed hash for an existing identity.

The first retention repair restores the exact historical `.deb` files in this lock, including the original MEMA 0.4.0 artifacts, then merges them with the current signed Pages artifact set before rebuilding metadata. This preserves any package published after the inventory was recorded and refuses a changed hash for an existing identity. Subsequent repository merges start from the complete signed Pages artifact set and add new versioned artifacts only. The regular push/PR workflow validates builds but does not deploy a fresh, incomplete artifact directory. Pages deployment is an explicit workflow dispatch (`retention-repair` or `core-release`).

Use `scripts/restore-retained-artifacts.sh` to reproduce the locked bootstrap set, `scripts/merge-repository-artifacts.sh` to verify and merge an existing signed repository with new artifacts, and `scripts/build-repository-index.sh` to generate deterministic multi-version `Packages` and `Packages.gz` plus architecture-declared `Release` metadata. The repository retention regression tests use disposable package fixtures.
