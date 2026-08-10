# Mema Contributor Guide

## Project Purpose

Mema is a Linux-only, Debian-distributed meta-manager for isolated binary toolchains. Its product contract is defined by [`README.md`](README.md):

- Keep toolchains in deterministic Mema-owned paths, primarily `/opt/mema`.
- Avoid polluting `/usr/bin`; expose selected executables with deterministic links in `/usr/local/bin`.
- Keep installation logic in small shell recipes and distribution in Debian packages.
- Support side-by-side versions, version selection, verified downloads, a cache, and `fzf`-driven switching/selection.
- Require only lightweight host dependencies, not a host Python, Node, Rust, or Go runtime.
- Publish signed APT packages and use CI to build, test, sign, and deploy the repository.

Treat this contract as the target behavior. Do not remove or weaken an advertised feature merely because it is incomplete in the current code.

## Repository Map

| Path | Responsibility |
| --- | --- |
| `mema-go/` | Go CLI source. `main.go` is the current source for the `mema` binary. |
| `core/` | Packaged runtime executables and shell helpers. `core/mema` is a compiled Linux amd64 Go binary; `mema_old` is a legacy shell implementation kept for reference only. |
| `configs/` | Shell configuration loaded from `/etc/profile.d/mema.sh`; defaults live in `00-mema-init.sh`. |
| `debs/` | Debian package staging tree for the core package, including control metadata and installed files. |
| `templates/` | Package metadata reference templates. |
| `recipes/` | Vendored tool recipes and recipe package build files. |
| `build-repo.sh` | Builds the core and recipe Debian packages, then writes the APT index into `dist/`. |
| `scripts/package.sh` | Compatibility entry point that delegates to `build-repo.sh`. |
| `install_repo.sh` | Installs the public APT signing key and source, then installs `mema`. |
| `tests/` | Docker-based APT installation smoke test. |
| `.github/workflows/build_repo.yml` | Build, GPG signing, integration test, and GitHub Pages deployment workflow. |

`dist/` and `build/` are generated and ignored. Do not commit their contents.

## Runtime Model

The Go CLI uses these paths:

| Scope | Install root | Executable links | Recipes | Libraries |
| --- | --- | --- | --- | --- |
| Local (default non-root) | `$HOME/.local/share/mema` | `$HOME/.local/bin` | `$HOME/.local/share/mema/recipe` | `$HOME/.local/share/mema/lib` |
| Global (root unless `--local`) | `/opt/mema` | `/usr/local/bin` | `/etc/mema/recipe` | `/opt/mema/lib` |

Downloads are cached in `/tmp/mema/cache`. Recipes are sourced by Bash. The CLI supplies `MEMA_INSTALL_DIR`, `MEMA_VERSION`, `MEMA_CACHE`, `MEMA_LINK_DIR`, and `MEMA_LIB_DIR`; global activation by a non-root user also receives `MEMA_SUDO=sudo`.

The intended user workflow is:

```sh
sudo apt install mema
sudo apt install mema-go             # a specific recipe package
sudo apt install mema-go-latest      # update-tracking meta-package
mema choose go                       # choose an available version with fzf
mema use                             # choose an installed version with fzf
```

`mema install <tool> [version]` resolves an omitted version to a concrete latest version before creating its install directory. `mema choose <tool>` queries `mema_get_versions`, deduplicates versions, and installs the `fzf` selection. `mema use [tool] [version]` runs a recipe's link-only `mema_use` function; without `--local`, a non-root user can select global installations and is prompted only for the link operation. `mema list` reports the selected scope and also reports global installations for a non-root invocation without `--local`.

## Recipe Requirements

- Keep recipes POSIX-shell compatible where practical; the current CLI sources them with Bash, but the README defines the project as shell-first and lightweight.
- Define `mema_install` and `mema_use`. `mema_install` must install then call `mema_use`; `mema_use` must only activate already-installed files by linking them.
- Define `mema_get_versions` to print `VERSION ARCH SHA256 URL` records and `mema_resolve_version` to print the latest version for the active architecture. The CLI has a first-record fallback for simple recipes, but production recipes should resolve architecture explicitly.
- Install only beneath `MEMA_INSTALL_DIR`. Link selected executables into `MEMA_LINK_DIR`, using `MEMA_SUDO` where the destination requires privileges.
- Download from the upstream HTTPS source and verify SHA-256 before extraction. Never make `SKIP_HASH` the default for a production recipe.
- Preserve side-by-side version directories. Never overwrite another version's install directory.
- Map upstream architecture names explicitly and fail clearly for unsupported architectures.
- Keep package dependencies and recipe metadata aligned with the commands the recipe uses.

Use `recipes/recipes/go/go.sh` as the current runtime recipe example. `recipes/example/` and `recipes/todo/` are reference templates using the same environment variables and helper contract; do not reintroduce `MEMA_ROOT`, `SUDO`, or `mema_link`.

`mema_download` is called as `mema_download URL FILENAME SHA256` and prints the verified cache path. It honors `MEMA_CACHE`. `mema_verify` is called as `mema_verify FILE SHA256`.

## Development And Verification

### Go CLI

```sh
cd mema-go
go test ./...
go vet ./...
go build -o ../core/mema .
```

`go.mod` declares Go `1.26.3`. Keep the compiled `core/mema` and `debs/usr/local/bin/mema` synchronized with `mema-go/main.go` when changing the CLI, and test the binary with `./core/mema help` from the repository root.

### Debian And Repository Build

The current build scripts require Debian packaging tools, Docker for the integration test, `dpkg-deb`, `dpkg-scanpackages`, `apt-ftparchive`, and GPG signing credentials when generating a release repository.

```sh
./build-repo.sh
./tests/test.sh
./tests/test_outside.sh
MEMA_SIGN=1 ./build-repo.sh
```

`build-repo.sh` builds the Go binary, synchronizes the Debian staging tree, builds recipe packages, and generates `Packages`, `Packages.gz`, and `Release`. Set `MEMA_SIGN=1` only when a release GPG key is available; this additionally produces `InRelease` and `Release.gpg`. CI imports the signing key, uses that mode, and then runs the Docker test.

Package staging and builds use `dpkg-deb` directly. Repository indexes are
generated with `dpkg-scanpackages` and `apt-ftparchive`; signed builds additionally
require GPG.

## Verification Scope

`tests/test.sh` is the release smoke test. It starts `debian:bookworm-slim`, configures only the locally built APT repository, installs `mema-go-latest`, and verifies both `mema list` and the activated `go` binary. `tests/test_outside.sh` builds a separate clean Debian image from `tests/Dockerfile` and validates package installation and tool execution during the image build. Both tests intentionally prove that the package works on a minimal Debian base; no Ubuntu container is needed.

`mema-go/main.go` and `mema-go/go.mod` are the implementation of record. `mema-go/build.sh`, `mema-go/go_installer.sh`, `mema-go/deps/`, and `mema-go/site.html` are experimental or historical files and must not be treated as part of the package build or runtime contract. `core/mema_old` is historical reference only; do not use it as a source of current paths or APIs.

## Change Rules

- Prefer small, auditable shell and Go changes. Check all external command failures and quote shell expansions.
- Never bypass checksum verification, replace a verified artifact with an unchecked one, or use untrusted APT sources in release code.
- Maintain the `/opt/mema` and `/usr/local/bin` separation for global installs. Do not write managed tool binaries into `/usr/bin`.
- Do not edit generated `dist/` or `build/` artifacts. Do not modify `mema.gpg` unless rotating the repository key intentionally.
- Keep Debian package metadata, copied file paths, executable modes, and maintainer scripts synchronized with the runtime layout.
- Keep vendored recipe changes in the main repository and review them with the core changes.
- Run the narrowest relevant verification first, then the Docker APT smoke test for packaging or installation changes.
