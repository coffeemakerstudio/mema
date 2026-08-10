# 🍱 Mema: The Minimalist Meta-Manager

**Mema** is a high-performance, shell-first framework designed for deterministic binary management. It is not a bloated package manager; it is lightweight infrastructure for managing isolated toolchains such as Go, Rust, Python, Node, Bun, and Deno primarily in `/opt/mema` without polluting your system or requiring heavy host runtimes.

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Platform: Linux](https://img.shields.io/badge/Platform-Linux-lightgrey)](https://github.com/coffeemakerstudio/mema)

---

## 💡 The Philosophy
Mema decouples **installation logic** (how a tool is built) from **distribution** (how a tool is delivered).
*   **Modular Architecture:** The Core provides verified download and version-selection primitives, while Recipes provide specific tool definitions.
*   **System Integrity:** Zero pollution of `/usr/bin`. All binaries are contained within a deterministic directory structure.
*   **Explicit Control:** You only install the specific toolchains required for your current environment.

## 🏗️ Architecture
The ecosystem consists of the Mema engine and its vendored recipe collection:

1.  **Mema core:** The engine in this repository. It handles caching, deterministic symlinking, and GPG-signed APT distribution.
2.  **`recipes/`:** The vendored registry. A collection of shell-based recipes for fetching and verifying binaries.

---

## 🚀 Quick Start

### 1. Configure Repository
Mema is distributed via a custom, GPG-signed APT repository. Install the core system with a single command:

```bash
curl -fsSL https://raw.githubusercontent.com/coffeemakerstudio/mema/main/install_repo.sh | sudo bash
sudo apt update && sudo apt install mema
```

The installer requires `curl`, `gpg`, and root access. The core package
declares its runtime dependencies, including `bash`, `curl`, `git`, `jq`,
`tar`, `xz-utils`, `ca-certificates`, and `fzf`; `sudo` is used for global
installation and activation when required. Individual recipes may add
dependencies such as `unzip`.

Release builds use the repository key published in `mema.gpg`. Set
`MEMA_SIGNING_KEY` to the matching secret-key fingerprint when rotating or
building with a different GPG key.

### 2. Install Toolchains
Choose the specific languages or tools you need. You can lock specific versions or use the `-latest` meta-package for automated updates:

```bash
# Example: Install the Go toolchain
sudo apt install mema-go

# OR: Always keep Go updated to the latest version
sudo apt install mema-go-latest
```

### 3. Usage & Switching
Mema features an interactive UI (via `fzf`) to switch between installed versions instantly:

```bash
# Select an available Go version from fzf, install it, and activate it.
mema choose go

# Select any installed toolchain/version and repoint its executable links.
mema use

# Install the latest release, or request one explicitly without fzf.
mema install go
mema install go 1.26.4
```

`mema install <tool>` resolves `latest` to a concrete version before installation, so `/opt/mema/<tool>/<version>` always remains side-by-side and reproducible. `mema use` activates the selected version by updating links in `/usr/local/bin`; a non-root user is prompted by `sudo` only when activating a global installation.

Root commands use the global scope by default: `/opt/mema`, `/etc/mema/recipe`,
and `/usr/local/bin`. Non-root commands use the local scope:
`$HOME/.local/share/mema`, `$HOME/.local/share/mema/recipe`, and
`$HOME/.local/bin`. Use `--local` to explicitly select the local scope.

Package upgrades replace Mema's CLI and recipes but preserve installed
toolchains under `/opt/mema` or `$HOME/.local/share/mema`. Removing the package
removes Mema-managed package files, not downloaded toolchain directories; use
`mema remove <tool> [version]` to remove toolchains explicitly.

---

## 🛠️ Writing a Recipe
Recipes are small shell scripts sourced by the CLI with Bash. They define
version discovery, verified installation, and link-only activation. The full
recipe contract, environment variables, dependency metadata, architecture
rules, and testing checklist are documented in
[`HOW_TO_RECIPE.md`](HOW_TO_RECIPE.md).

The essential rules are:

- Install only below `MEMA_INSTALL_DIR` and preserve side-by-side versions.
- Use `mema_download URL FILENAME SHA256` for every upstream archive.
- Define `mema_get_versions`, `mema_resolve_version`, `mema_install`, and
  `mema_use`.
- Make `mema_use` activation-only and link executables into `MEMA_LINK_DIR`.
- Map architectures explicitly and fail clearly when unsupported.

Run `./tests/check_recipes.sh` for a no-install recipe dry run. It validates
recipe structure, version records, checksum fields, latest-version resolution,
and Debian package generation without calling `mema_install`.

For a single installed or explicit recipe, use `mema check-recipe <tool>` or
`mema check-recipe <path/to/recipe.sh>`.

Recipe packages can declare composed, pinned dependencies with
`MEMA_INSTALL_DEPENDS`:

```sh
MEMA_PACKAGE_VERSION="1.23.1"
MEMA_AUTOINSTALL="1"
MEMA_INSTALL_DEPENDS="lib-mylib 1.0.0"
```

This generates the package `mema-myprogram` at version `1.23.1`, adds
`mema-lib-mylib (= 1.0.0)` as an APT recipe dependency, and its `postinst`
first runs `mema install lib-mylib 1.0.0`, then installs the program. A program
recipe can link against files activated by the library recipe through
`$MEMA_LIB_DIR`. Legacy recipes may still use `MEMA_DEPENDS` for latest-package
behavior.

## Troubleshooting

- If `mema` cannot be found after installation, start a new login shell and
  ensure `/usr/local/bin` or `$HOME/.local/bin` is in `PATH`.
- If interactive commands fail, install `fzf` and run `mema use` again.
- If `mema` reports a missing recipe, install `mema-<tool>` or pass
  `--file <recipe>`.
- If a checksum fails, do not bypass verification. Refresh the recipe metadata
  and retry with the upstream checksum.
- If global activation fails for a non-root user, verify that `sudo` is
  installed and permitted for the user.
- Downloads are cached in `/tmp/mema/cache`; corrupt cached archives are
  removed automatically after verification fails.

---

## 🧼 Why Mema?
*   **Zero Runtime Bloat:** No dependency on Python, Node, or Go on the host system. Requires only lightweight shell, download, archive, JSON, and selection tools.
*   **Deterministic Environments:** Predictable paths in `/opt/mema` ensure reproducible development setups.
*   **Side-by-Side Versions:** Run multiple versions of the same software simultaneously without conflicts.
*   **CI/CD Driven:** Automated pipeline that builds, signs, and deploys Debian packages via GitHub Actions and GitHub Pages.

---

**Built for efficiency. Engineered for stability.**

*Developed by [Eugen Lupricht](https://github.com/eugen252009)*
