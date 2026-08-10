# How to Write a Mema Recipe

Mema uses small shell-first recipes to download, install, and activate isolated binary toolchains. The current CLI sources recipes with Bash; keep recipes POSIX-compatible where practical so they remain portable and auditable.

---

## 1. Directory Structure

Mema recipes reside inside the vendored `recipes/` directory. If you are adding a new recipe for `<tool>`, create the directory and the script:

```
recipes/
└── recipes/
    └── <tool>/
        └── <tool>.sh     # The main Mema shell recipe containing metadata variables
```

---

## 2. Recipe Metadata Header

Every recipe script should start with metadata variables defining the package name, description, maintainer, and section:

```sh
#!/bin/sh
# Mema Recipe for <Tool>

NAME="tool-name"
DESCRIPTION="A description of the tool package."
MAINTAINER="Your Name <email@example.com>"
SECTION="devel"
# Exact package version for a pinned upstream release.
MEMA_PACKAGE_VERSION="1.23.1"
# Make this package the autoinstall entry point.
MEMA_AUTOINSTALL="1"
# Whitespace-separated recipe/version pairs installed first.
MEMA_INSTALL_DEPENDS="lib-mylib 1.0.0"

# Keep the shell script execution strict
set -e
```

---

## 3. Runtime Environment Variables

When Mema executes your recipe, it supplies several environment variables. You **must** utilize them to maintain isolated paths and proper execution scope (Local vs. Global):

| Variable | Description | Local Scope Example | Global Scope Example |
| --- | --- | --- | --- |
| `MEMA_INSTALL_DIR` | Target installation directory for this specific version. | `$HOME/.local/share/mema/<tool>/<version>` | `/opt/mema/<tool>/<version>` |
| `MEMA_VERSION` | The version being installed or activated. | `1.2.3` | `1.2.3` |
| `MEMA_CACHE` | Directory to cache downloaded archives. | `/tmp/mema/cache` | `/tmp/mema/cache` |
| `MEMA_LINK_DIR` | Directory where active executable symlinks must be created. | `$HOME/.local/bin` | `/usr/local/bin` |
| `MEMA_LIB_DIR` | Directory for shared libraries / helper scripts. | `$HOME/.local/share/mema/lib` | `/opt/mema/lib` |
| `MEMA_SUDO` | Prepended to write commands requiring root permissions (evaluates to `sudo` for non-root users when installing globally). | *(empty)* | `sudo` |

---

## 4. Required Functions

A Mema recipe must define the following four functions:

`MEMA_PACKAGE_VERSION` should match the pinned upstream release. A recipe with
`MEMA_AUTOINSTALL=1` becomes a versioned `mema-<tool>` package. Its
`MEMA_INSTALL_DEPENDS` value contains recipe/version pairs, not Debian package
names. The builder adds exact `mema-<dependency> (= <version>)` APT
dependencies and writes matching `mema install <dependency> <version>` commands
into the package `postinst`. This keeps APT responsible for placing recipes
while Mema performs the actual toolchain installation.

`MEMA_DEPENDS` remains available for legacy `mema-<tool>-latest` packages. It
contains recipe names and installs those dependencies at `latest`.

For example, a program that links against `lib-mylib` can use the library's
stable files through `$MEMA_LIB_DIR`:

```sh
gcc -I"$MEMA_LIB_DIR" source.c -L"$MEMA_LIB_DIR" -lmylib \
    -Wl,-rpath,"$MEMA_LIB_DIR" -o "$MEMA_INSTALL_DIR/bin/myprogram"
```

### `mema_get_versions`
Prints available upstream release records to stdout. Each record must follow the whitespace-separated format:
`VERSION ARCH SHA256 URL [METADATA...]`

- **Fallback Option**: The CLI will use the first printed record as the fallback default for `latest` if resolve logic is missing, but explicitly resolving architecture is highly recommended.

### `mema_resolve_version`
Prints the concrete version number of the latest stable version for the host machine's architecture.

### `mema_install`
Downloads, verifies, extracts, and places the binaries under `MEMA_INSTALL_DIR`. It **must** call `mema_use` at the end to activate the tool.
- Use `mema_download` to retrieve and verify the archive.
- Ensure you do not overwrite existing files or write outside of `MEMA_INSTALL_DIR`.

### `mema_use`
Creates symlinks in `MEMA_LINK_DIR` pointing to the selected executables inside `MEMA_INSTALL_DIR`.
- Only perform symlink creations here.
- Always create a versioned link (e.g. `tool-1.2.3`) alongside the main link (e.g. `tool`).

---

## 5. Built-in Helpers

Mema provides built-in shell helpers for recipe developers:

* **`mema_download URL FILENAME SHA256`**: Downloads the file from the given `URL` to `FILENAME` inside the cache directory, verifies its SHA-256 hash, and prints the absolute path to the verified file.
* **`mema_verify FILE SHA256`**: Verifies that the specified `FILE` matches the provided `SHA256` hash.

---

## 6. Architecture Mapping Best Practice

Upstream project download locations often use varying naming conventions for CPU architectures. You must map `uname -m` output to the upstream names explicitly:

```sh
local arch
case "$(uname -m)" in
    x86_64) arch="amd64" ;;
    aarch64) arch="arm64" ;;
    *)
        printf "Mema Error: Arch %s is not supported.\n" "$(uname -m)" >&2
        return 1
        ;;
esac
```

---

## 7. Sample Recipe Template

Here is a full template demonstrating how to structure the recipe:

```sh
#!/bin/sh
NAME="mytool"
DESCRIPTION="A fast developer utility tool"
MAINTAINER="Developer <dev@example.com>"
SECTION="devel"

set -e

mema_get_versions() {
    # Fetches list of versions from upstream
    curl -fsSL "https://example.com/api/releases" | jq -r '
        .[] | "\(.version) \(.arch) \(.sha256) \(.download_url)"
    '
}

mema_resolve_version() {
    local arch
    case "$(uname -m)" in
        x86_64) arch="amd64" ;;
        aarch64) arch="arm64" ;;
        *) return 1 ;;
    esac

    local versions
    versions=$(mema_get_versions) || return 1
    printf '%s\n' "$versions" | awk -v arch="$arch" '$2 == arch { print $1; exit }'
}

mema_install() {
    local target_v="${MEMA_VERSION:?MEMA_VERSION is required}"
    local install_path="$MEMA_INSTALL_DIR"
    local sudo_cmd="$MEMA_SUDO"
    
    local arch
    case "$(uname -m)" in
        x86_64) arch="amd64" ;;
        aarch64) arch="arm64" ;;
        *) return 1 ;;
    esac

    # Resolve "latest" to a concrete version
    if [ "$target_v" = "latest" ]; then
        target_v=$(mema_resolve_version)
    fi

    # Fetch corresponding download record
    local entry
    entry=$(mema_get_versions | awk -v version="$target_v" -v arch="$arch" '$1 == version && $2 == arch { print; exit }')
    if [ -z "$entry" ]; then
        printf "Mema Error: version %s for %s not found\n" "$target_v" "$arch" >&2
        return 1
    fi

    local hash=$(printf '%s\n' "$entry" | awk '{print $3}')
    local url=$(printf '%s\n' "$entry" | awk '{print $4}')

    if [ ! -f "$install_path/bin/mytool" ]; then
        local filename="${url##*/}"
        local filepath
        filepath=$(mema_download "$url" "$filename" "$hash") || return 1

        printf "Mema: Extracting to %s...\n" "$install_path"
        $sudo_cmd mkdir -p "$install_path"
        $sudo_cmd tar -xzf "$filepath" -C "$install_path" --strip-components=1
    fi

    mema_use
}

mema_use() {
    local install_path="${MEMA_INSTALL_DIR:?MEMA_INSTALL_DIR is required}"
    local link_dir="${MEMA_LINK_DIR:?MEMA_LINK_DIR is required}"
    local target_v="${MEMA_VERSION:?MEMA_VERSION is required}"
    local sudo_cmd="$MEMA_SUDO"

    if [ ! -x "$install_path/bin/mytool" ]; then
        printf "Mema Error: Not installed\n" >&2
        return 1
    fi

    printf "Mema: Activating mytool %s...\n" "$target_v"
    $sudo_cmd mkdir -p "$link_dir"
    $sudo_cmd ln -sf "$install_path/bin/mytool" "$link_dir/mytool"
    $sudo_cmd ln -sf "$install_path/bin/mytool" "$link_dir/mytool-$target_v"
}
```
