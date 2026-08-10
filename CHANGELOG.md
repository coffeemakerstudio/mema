# Changelog

## Unreleased

- Added a Debian `bookworm-slim` outside-container package test.
- Added CLI unit tests and verified-download helper tests.
- Enforced HTTPS downloads, strict SHA-256 inputs, and safe managed paths.
- Fixed signed APT metadata to use the published repository key.
- Fixed profile-loader packaging and tested non-root global activation.

## 0.2

- Added PHP and Ruby recipes.
- Added `mema check-recipe` validation and no-install recipe checks.
- Added Rust, Python, Node, Bun, and Deno runtime recipes.

## 0.0.1

- Initial Debian package and recipe repository release.
