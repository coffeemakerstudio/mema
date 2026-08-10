# 🍱 Mema: The Minimalist Meta-Manager

**Mema** is a high-performance, POSIX-sh framework designed for deterministic binary management. It is not a bloated package manager; it is a lightweight infrastructure designed to manage toolchains (Go, Rust, Zig, Node) in `/opt/mema` without polluting your system or requiring heavy runtimes.

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Platform: Linux](https://img.shields.io/badge/Platform-Linux-lightgrey)](https://github.com/eugen252009/mema-core)

---

## 💡 The Philosophy
Mema decouples **installation logic** (how a tool is built) from **distribution** (how a tool is delivered).
*   **Modular Architecture:** The Core provides verified download and version-selection primitives, while Recipes provide specific tool definitions.
*   **System Integrity:** Zero pollution of `/usr/bin`. All binaries are contained within a deterministic directory structure.
*   **Explicit Control:** You only install the specific toolchains required for your current environment.

## 🏗️ Architecture
The ecosystem consists of the Mema engine and its vendored recipe collection:

1.  **[mema-core](https://github.com/eugen252009/mema-core):** The Engine. Handles caching, deterministic symlinking, and GPG-signed APT distribution.
2.  **`recipes/`:** The vendored registry. A collection of shell-based recipes for fetching and verifying binaries.

---

## 🚀 Quick Start

### 1. Configure Repository
Mema is distributed via a custom, GPG-signed APT repository. Install the core system with a single command:

```bash
curl -fsSL https://raw.githubusercontent.com/coffeemakerstudio/mema/main/install_repo.sh | sudo bash
sudo apt update && sudo apt install mema
```

The installer requires `curl`, `gpg`, and root access. The installed runtime
requires `bash`, `curl`, `tar`, `jq`, and `fzf`; `sudo` is additionally needed
when an unprivileged user activates a global installation.

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
A Mema recipe is pure shell script—adhering to Data-Oriented Design and avoiding "Architecture Astronaut" over-engineering.

```bash
mema_install() {
    # Download into the verified Mema cache. It prints the cached path.
    archive=$(mema_download "$URL" "tool.tar.gz" "$HASH")
    
    # Extract into the deterministic Mema directory
    tar -C "$MEMA_INSTALL_DIR" -xzf "$archive"
}
    
mema_use() {
    # Activate an installed version without downloading or extracting again.
    $MEMA_SUDO mkdir -p "$MEMA_LINK_DIR"
    $MEMA_SUDO ln -sf "$MEMA_INSTALL_DIR/bin/tool" "$MEMA_LINK_DIR/tool"
}
```

Recipes receive `MEMA_INSTALL_DIR`, `MEMA_VERSION`, `MEMA_CACHE`, `MEMA_LINK_DIR`, `MEMA_LIB_DIR`, and, for an unprivileged activation of a global installation, `MEMA_SUDO=sudo`. Production recipes must provide an upstream SHA-256 and must not use `SKIP_HASH`.

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
