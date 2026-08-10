#!/usr/bin/env bash
set -euo pipefail

ROOT=$(realpath "$(dirname "$0")/..")
REPO_DIR="$ROOT/dist"
IMAGE="mema-outside-test-$$"

if [ ! -f "$REPO_DIR/Packages" ]; then
    printf 'No built repository found. Run ./build-repo.sh first.\n' >&2
    exit 1
fi

cleanup() {
    docker rmi "$IMAGE" >/dev/null 2>&1 || true
}
trap cleanup EXIT

printf '%s\n' '--- Building clean Debian outside-test image ---'
docker build --file "$ROOT/tests/Dockerfile" --tag "$IMAGE" "$ROOT"
printf '%s\n' '--- Running clean Debian outside-test container ---'
docker run --rm "$IMAGE"
printf '%s\n' '--- PASS: Debian outside test ---'
