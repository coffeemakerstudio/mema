# Mema Usability Checklist

This checklist tracks the work required to bring Mema to a reliable, usable
state. The project contract and contributor rules are defined in `AGENTS.md`.

## P0: Core Workflow

- [x] Confirm runtime dependencies are documented and available: `bash`, `sh`,
  `curl`, `tar`, `sudo`, and `fzf`.
- [x] Run `go test ./...` and `go vet ./...` in `mema-go/`.
- [x] Build the CLI with `go build -o ../core/mema .`.
- [x] Keep `core/mema` and `debs/usr/local/bin/mema` synchronized.
- [x] Verify `./core/mema help` from the repository root.
- [x] Verify `mema init` creates the correct local and global directories.
- [x] Verify `mema install go` resolves `latest` to a concrete version.
- [x] Verify multiple versions can be installed without overwriting each other.
- [x] Verify `mema list` reports local and global installations correctly.
- [x] Verify `mema use` activates an installed version using links only.
- [ ] Verify `mema choose go` works with `fzf` interactively.
- [x] Verify `mema remove` removes only the selected version.
- [x] Verify non-root users can activate global installations using `sudo`.

## P0: Recipe Security and Contract

- [x] Review `recipes/recipes/go/go.sh` against the recipe contract.
- [x] Ensure production recipes define `mema_install`, `mema_use`,
  `mema_get_versions`, and `mema_resolve_version`.
- [x] Ensure `mema_install` installs first and then calls `mema_use`.
- [x] Ensure `mema_use` performs activation only and never downloads or extracts.
- [x] Ensure all downloads use HTTPS and mandatory SHA-256 verification.
- [x] Ensure production recipes do not use `SKIP_HASH`.
- [x] Map supported upstream architectures explicitly.
- [x] Fail clearly for unsupported architectures.
- [x] Ensure recipes write only beneath the supplied Mema directories.
- [x] Align recipe package dependencies with the commands each recipe uses.

## P0: Debian Packages and Repository

- [x] Build packages successfully with `./build-repo.sh`.
- [x] Verify the core package contains the CLI, helpers, profile loader, and
  Mema configuration files in their intended paths.
- [x] Verify recipe packages install recipes into `/etc/mema/recipe`.
- [x] Verify maintainer scripts create required directories and permissions.
- [ ] Verify package upgrades preserve installed toolchains and active links.
- [ ] Verify package removal does not unexpectedly remove user installations.
- [x] Run the Docker smoke test with `./tests/test.sh`.
- [x] Run the clean-image outside test with `./tests/test_outside.sh`.
- [x] Confirm the smoke test covers APT installation, `mema list`, `go version`,
  and `/usr/local/bin/go` activation.

## P0: Signed APT Distribution

- [ ] Configure the `MEMA_SIGNING_KEY` repository secret in CI.
- [x] Build signed metadata with `MEMA_SIGN=1 ./build-repo.sh`.
- [x] Verify `InRelease` and `Release.gpg` are generated.
- [x] Verify `install_repo.sh` installs the correct public key.
- [x] Ensure APT uses a scoped repository keyring rather than global trust.
- [ ] Test installation from the published repository in a clean Debian
  container.
- [ ] Confirm GitHub Pages publishes packages and repository metadata.

## P1: User Experience

- [x] Test the documented quick-start commands on a clean Debian system.
- [x] Document or configure `$HOME/.local/bin` in `PATH`.
- [x] Ensure `/usr/local/bin` is available in normal login shells.
- [x] Provide actionable errors for missing dependencies and recipes.
- [x] Ensure canceled `fzf` selections exit cleanly.
- [x] Handle invalid tools, versions, recipes, architectures, and permissions
  clearly.
- [x] Define behavior for reinstalling an existing version.
- [x] Handle broken or stale symlinks safely.
- [x] Test login and non-login shell environments.

## P1: Automated Testing

- [x] Add Go unit tests for scope selection, recipe lookup, version resolution,
  installation paths, and argument validation.
- [x] Add shell tests for caching, checksum failures, download failures,
  extraction failures, and symlink activation.
- [x] Add integration coverage for local installs, global installs, non-root
  global activation, multiple versions, removal, and `fzf` selection.
- [ ] Test unsupported CPU architectures in an integration environment.
- [x] Run unit tests, vet, package builds, and the Docker smoke test in CI for
  pull requests.

## P1: Documentation

- [x] Reconcile the README's POSIX-shell claim with the CLI's Bash execution.
- [x] Document runtime dependencies, including `fzf` and `sudo` where needed.
- [x] Document local versus global behavior and recipe discovery.
- [x] Document cache location, cache invalidation, and supported architectures.
- [ ] Document package upgrade and removal behavior.
- [x] Add troubleshooting guidance for missing dependencies, recipes,
  checksums, permissions, and `PATH`.

## P2: Release Hygiene

- [x] Keep `dist/` and `build/` generated and uncommitted.
- [x] Check the `recipes` submodule status separately.
- [x] Avoid incidental changes to the recipes submodule pointer.
- [x] Isolate or remove historical and experimental files from the release
  path.
- [ ] Add versioning and release notes.
- [ ] Verify executable permissions and package metadata before each release.

## Definition of Usable

Mema is usable when a clean Debian machine can:

1. Install the signed `mema` package.
2. Install a recipe package such as `mema-go-latest`.
3. Install a verified toolchain without a host Go, Node, or Python runtime.
4. Keep multiple versions installed side by side.
5. Select and activate a version with `mema use` or `mema choose`.
6. Expose only selected binaries through the intended link directory.
7. Pass `./tests/test.sh` from a clean environment.
