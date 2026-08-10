# Mema Recipes

This directory vendors the maintained shell recipes used to build Mema recipe
packages. The recipes are part of this repository so the core build and CI do
not depend on a separate repository checkout.

Production recipes must define `mema_get_versions`, `mema_resolve_version`,
`mema_install`, and `mema_use`; use HTTPS downloads and verified SHA-256 values.

Run `./build.sh` from this directory through the root `build-repo.sh` command.
