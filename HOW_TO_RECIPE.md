# How To Write a Mema Recipe

A recipe is a small shell script that tells Mema how to discover, download,
install, and activate one tool or library. Recipes are packaged as Debian
packages by `recipes/build.sh`, then sourced by the Mema CLI with Bash.

Keep recipes shell-first and POSIX-compatible where practical, but remember
that Bash is the execution shell. A recipe must be deterministic, auditable,
and safe to run repeatedly.

## File Layout

Create one directory and one script under `recipes/recipes/`:

```text
recipes/recipes/<tool>/<tool>.sh
```

The script filename is copied into `/etc/mema/recipe` or the local recipe
directory. The file's `NAME` must match the tool name used by the CLI.

## Metadata

Start with package metadata:

```sh
#!/bin/sh
NAME="mytool"
DESCRIPTION="Mema-managed My Tool"
SECTION="devel"
MEMA_PACKAGE_VERSION="1.2.3"
deps="ca-certificates, curl, tar"

set -e
```

Important metadata:

- `NAME` is the recipe and package name. The builder creates `mema-$NAME`.
- `DESCRIPTION` and `SECTION` become Debian package metadata.
- `MEMA_PACKAGE_VERSION` should match the pinned upstream release. If omitted,
  the builder uses `0.1`.
- `deps` lists host Debian packages required by recipe commands, such as
  `curl`, `unzip`, `xz-utils`, or build tools.
- `MEMA_AUTOINSTALL="1"` creates a versioned package whose `postinst` installs
  declared recipe dependencies and then the recipe itself.
- `MEMA_INSTALL_DEPENDS` contains whitespace-separated recipe/version pairs,
  not Debian package names:

  ```sh
  MEMA_AUTOINSTALL="1"
  MEMA_INSTALL_DEPENDS="lib-mylib 1.0.0"
  ```

  This produces an exact APT dependency on `mema-lib-mylib (= 1.0.0)` and
  installs it with `mema install lib-mylib 1.0.0` first.
- `MEMA_DEPENDS` is the legacy dependency mechanism for `-latest` packages.

## Runtime Variables

The CLI supplies these variables when it runs a recipe:

| Variable | Purpose |
| --- | --- |
| `MEMA_INSTALL_DIR` | Version-specific installation directory. Write tool files only below it. |
| `MEMA_VERSION` | Concrete version currently being installed or activated. |
| `MEMA_CACHE` | Verified download cache, normally `/tmp/mema/cache`. |
| `MEMA_LINK_DIR` | Directory for active executable links. |
| `MEMA_LIB_DIR` | Scope-specific library root. Libraries use `$MEMA_LIB_DIR/<name>/<version>`. |
| `MEMA_INCLUDE_DIR` | Shared activated header links. |
| `MEMA_LIB_LINK_DIR` | Shared activated library links. |
| `MEMA_PKG_CONFIG_DIR` | Shared activated `pkg-config` files. |
| `MEMA_SHARE_DIR` | Shared activated data files. |
| `MEMA_SUDO` | Empty for permitted writes, or `sudo` for non-root global activation. |

Global paths normally use `/opt/mema`, `/etc/mema/recipe`, and
`/usr/local/bin`. Local paths use `$HOME/.local/share/mema`,
`$HOME/.local/share/mema/recipe`, and `$HOME/.local/bin`.

## Required Functions

Production recipes should define all four functions below.

### `mema_get_versions`

Print one record per available artifact:

```text
VERSION ARCH SHA256 URL [OPTIONAL_METADATA...]
```

The first four fields are required. Extra fields may describe package
dependencies, but the CLI and package tooling consume the first four fields.
Use an upstream API when it provides reliable version and checksum metadata;
otherwise pin a reviewed release record in the recipe.

### `mema_resolve_version`

Print exactly one concrete latest version for the current architecture. Do not
print labels, URLs, or diagnostic text to stdout. Fail clearly for an
unsupported architecture.

The CLI falls back to the first record from `mema_get_versions` when this
function is absent, but production recipes should implement it explicitly.

### `mema_install`

Install one version under `MEMA_INSTALL_DIR`, then call `mema_use`.

The install function should:

1. Map `uname -m` to the upstream architecture name.
2. Select the record matching `MEMA_VERSION` and the active architecture.
3. Download with `mema_download URL FILENAME SHA256`.
4. Extract or install only below `MEMA_INSTALL_DIR`.
5. Avoid overwriting a different version's directory.
6. Call `mema_use` after successful installation.

Example structure:

```sh
mema_install() {
    local archive
    local sudo_cmd="${MEMA_SUDO:-}"

    archive=$(mema_download "$url" "$filename" "$sha256")
    $sudo_cmd mkdir -p "$MEMA_INSTALL_DIR"
    $sudo_cmd tar -xzf "$archive" -C "$MEMA_INSTALL_DIR" --strip-components=1
    mema_use
}
```

Use `MEMA_VERSION` for the selected version; do not silently replace it with
an unpinned or newly discovered version during installation.

### `mema_use`

Activate an already-installed version only. It must not download, extract,
compile, or modify files inside another installation. It normally creates
links such as:

```sh
mema_use() {
    local sudo_cmd="${MEMA_SUDO:-}"
    $sudo_cmd mkdir -p "$MEMA_LINK_DIR"
    $sudo_cmd ln -sfn "$MEMA_INSTALL_DIR/bin/mytool" "$MEMA_LINK_DIR/mytool"
    $sudo_cmd ln -sfn "$MEMA_INSTALL_DIR/bin/mytool" \
        "$MEMA_LINK_DIR/mytool-$MEMA_VERSION"
}
```

For libraries, activate headers, shared libraries, `pkg-config` files, and
data under the corresponding shared directories. Use links back to the
version-specific installation so switching versions is reversible.

## Downloads and Verification

Always download from an HTTPS upstream URL and verify the artifact before
extracting it:

```sh
archive=$(mema_download \
    "https://example.org/mytool-1.2.3.tar.gz" \
    "mytool-1.2.3.tar.gz" \
    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
```

`mema_download` uses `MEMA_CACHE`, verifies SHA-256, and prints the verified
cache path. `mema_verify FILE SHA256` is available for separate checks.

Never make `SKIP_HASH` the default, accept an unchecked fallback, or extract
before verification. If upstream does not publish a checksum, obtain and
review it through a trusted release channel before adding the recipe.

## Architecture Support

Map host architecture names explicitly because upstream naming differs:

```sh
case "$(uname -m)" in
    x86_64) upstream_arch="amd64" ;;
    aarch64) upstream_arch="arm64" ;;
    *)
        printf 'Mema Error: unsupported architecture %s\n' "$(uname -m)" >&2
        return 1
        ;;
esac
```

Do not advertise an architecture unless its URL and checksum are present and
the archive layout has been tested.

## Package Build and Testing

For a no-install dry run, validate all production recipes and build their
Debian packages in a temporary directory:

```sh
./tests/check_recipes.sh
```

This checks shell syntax, required functions, version records, SHA-256 field
format, latest-version resolution, and package generation. It does not call
`mema_install`, compile a runtime, or write to Mema install paths. It cannot
prove that a source tree compiles or that an archive's internal layout is
correct; use the full installation smoke test for those checks.

For one installed recipe, the same contract check is available through the
CLI:

```sh
mema check-recipe php
mema check-recipe recipes/recipes/php/php.sh
```

This command sources the recipe and calls only its metadata functions. It
never calls `mema_install` or `mema_use`.

Successful output reports contract status separately from side effects:

```text
status:
  functions -> pass
  version   -> pass
  checksum  -> pass
  install   -> pass (contract checked; not executed)
  linking   -> pass (contract checked; not executed)
```

The checker does not replace recipe functions with dummy implementations. It
inspects the declared functions and verifies that `mema_install` delegates to
`mema_use`, while `mema_use` contains link behavior. This avoids accidentally
executing downloads, compilers, copies, or links during a dry run.

Build recipe packages without rebuilding the complete repository:

```sh
RECIPE_DIR=recipes/recipes ./recipes/build.sh
```

Check generated metadata:

```sh
dpkg-deb -f dist/mema-mytool_1.2.3_all.deb Package Version Depends
```

At minimum, verify:

- `sh -n recipes/recipes/<tool>/<tool>.sh` passes.
- `mema_get_versions` prints valid records for every supported architecture.
- Every listed URL is reachable and its checksum matches the archive.
- The archive extracts into the expected `bin` or library layout.
- `mema_install` is repeatable and calls `mema_use`.
- `mema_use` works without network access after installation.
- Links point into the selected version directory.
- Unsupported architectures fail with a useful error.
- Package dependencies include every external command used by the recipe.
- `./tests/test_recipe_dependencies.sh` still passes when package dependency
  behavior is affected.

Do not commit generated `dist/` or `build/` contents.

## Common Mistakes

- Writing binaries to `/usr/bin` instead of linking through `MEMA_LINK_DIR`.
- Using a shared, unversioned installation directory.
- Linking in `mema_install` but not providing a link-only `mema_use`.
- Calling `curl` directly instead of `mema_download`.
- Omitting architecture-specific checksums.
- Assuming archive contents have a particular top-level directory without
  testing extraction.
- Using `MEMA_ROOT`, `SUDO`, or the historical `mema_link` helper. These are
  not part of the current contract.
