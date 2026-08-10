# Wayland Mema POC

## Goal

Produce an APT package that can be installed with:

```sh
sudo apt install mema-wayland
```

The package must place the required Mema recipes, then build a pinned Wayland
library stack through Mema. APT provides recipes and package ordering; Mema
owns the isolated source builds and activation.

This POC targets the Wayland protocol libraries and tools. It does not build a
Wayland compositor or a complete desktop environment. Weston, Sway, Mutter,
and similar projects are separate recipes and are outside this POC.

## Architecture

The package-manager layer and Mema layer have separate responsibilities:

| Layer | Responsibility |
| --- | --- |
| APT driver | Build `.deb` files, place recipes, declare recipe-package dependencies, publish packages |
| Mema CLI | Resolve a recipe, build into a versioned directory, activate links |
| Recipe | Download verified source, build, install, and activate one tool or library |
| Autoinstall postinst | Invoke the exact pinned `mema install` sequence |

Mema must not become another APT solver. The APT package only guarantees that
the recipes needed by the autoinstall sequence are present. The top-level
package decides which versions are built and in which order.

The same recipe contract should remain usable by future Homebrew, Pacman, or
other package-manager drivers. Those drivers may place recipes differently and
use different package metadata, but they should generate the same Mema install
sequence.

## Package Roles

### Recipe-only package

A recipe-only package provides one recipe and does not install the tool:

```text
mema-libffi=3.4.6
```

Its payload is the recipe under the package manager's recipe directory. Its
APT version matches the pinned upstream version.

### Autoinstall package

An autoinstall package provides the top-level entry point:

```text
mema-wayland=1.23.1
```

It declares exact APT dependencies for the recipe-only packages and its
postinst runs the pinned Mema commands:

```sh
mema install lib-libffi 3.4.6
mema install lib-expat 2.6.4
mema install wayland-protocols 1.43
mema install wayland 1.23.1
```

APT is only ensuring that these recipe files exist before the postinst runs.

## Proposed Recipe Metadata

The POC uses explicit metadata for package version and install order:

```sh
NAME="wayland"
MEMA_PACKAGE_VERSION="1.23.1"
MEMA_AUTOINSTALL="1"
MEMA_INSTALL_DEPENDS="
  lib-libffi 3.4.6
  lib-expat 2.6.4
  wayland-protocols 1.43
"
```

Each dependency line contains a Mema recipe name and an exact version. The
builder converts those lines into both APT recipe-package dependencies and
postinst commands. Dependency packages themselves remain recipe-only unless
they explicitly opt into autoinstall mode.

## Initial Dependency Graph

The first graph is deliberately bounded:

```text
mema-wayland
|-- mema-lib-libffi
|-- mema-lib-expat
|-- mema-wayland-protocols
`-- mema-wayland
```

The source builds require Debian-provided build tools for this POC:

- `gcc`
- `libc6-dev`
- `meson`
- `ninja-build`
- `pkg-config`
- `python3`
- `libffi-dev` or the Mema libffi activation
- `libexpat1-dev` or the Mema expat activation
- XML tooling required by the selected Wayland release

The exact upstream dependency list, versions, checksums, licenses, and build
flags must be recorded here before adding production recipes. A fully
self-hosted compiler and build-system graph is out of scope for this first POC.

## Library Activation

Libraries need more than a single shared-object link. The common activated
layout is:

```text
$MEMA_LIB_DIR/include/
$MEMA_LIB_DIR/lib/
$MEMA_LIB_DIR/pkgconfig/
$MEMA_LIB_DIR/share/
```

Recipes must install into their own versioned Mema directory and have `mema_use`
create links from that directory into the activated library layout. A dependent
recipe should build with paths derived from `MEMA_LIB_DIR`, for example:

```sh
CPPFLAGS="-I$MEMA_LIB_DIR/include"
LDFLAGS="-L$MEMA_LIB_DIR/lib -Wl,-rpath,$MEMA_LIB_DIR/lib"
PKG_CONFIG_PATH="$MEMA_LIB_DIR/pkgconfig"
```

Activation remains link-only. Downloads, extraction, and compilation belong in
`mema_install`.

## Implementation Steps

- [x] Record the package-manager and Mema responsibility boundary.
- [x] Define recipe-only and autoinstall package roles.
- [x] Define exact version metadata and pinned install commands.
- [x] Define the initial bounded Wayland dependency graph.
- [ ] Add versioned package metadata to the recipe builder.
- [ ] Add pinned dependency parsing and validation.
- [ ] Add explicit autoinstall postinst generation.
- [ ] Add library include, lib, pkg-config, and share activation helpers.
- [ ] Add pinned recipes for the selected Wayland dependency versions.
- [ ] Build the signed `mema-wayland` APT package.
- [ ] Install it in a clean Debian container.
- [ ] Verify `wayland-scanner` and `pkg-config` metadata.
- [ ] Compile and link a minimal Wayland client.
- [ ] Verify side-by-side library versions remain isolated.
- [ ] Record package contents and checksums.
- [ ] Define follow-up work for a compositor.

## Acceptance Test

The POC is complete when a clean Debian container can run:

```sh
sudo apt install mema-wayland
pkg-config --modversion wayland-client
command -v wayland-scanner
```

and compile a small client with:

```sh
cc client.c $(pkg-config --cflags --libs wayland-client) -o client
```

The test must prove that the client links to the activated Mema library and
that no managed file was written into `/usr/bin`.

## Known Limitations

- APT can only install versions retained in the published repository.
- Mema still owns side-by-side versions after the package install.
- This POC uses Debian for compiler and build-system bootstrap tools.
- Wayland libraries do not provide a display compositor.
- Optional Wayland documentation and test dependencies may be disabled.
- The APT driver is the first implementation; other package managers need
  separate drivers.
