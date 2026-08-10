#!/usr/bin/env bash
set -euo pipefail

REPO_DIR=$(realpath "$(dirname "$0")/../dist")
echo "Using repository path: $REPO_DIR"
CONTAINER_NAME="mema-test-env-$$"

cleanup() {
    docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "--- Starting minimal Debian test container ---"
docker run -d --rm --name "$CONTAINER_NAME" \
    -v "$REPO_DIR:/repo:ro" \
    debian:bookworm-slim sleep infinity

docker exec "$CONTAINER_NAME" bash -c '
    if [ -f /repo/InRelease ]; then
        mkdir -p /etc/apt/keyrings
        cp /repo/mema-keyring.gpg /etc/apt/keyrings/mema.gpg
        echo "deb [signed-by=/etc/apt/keyrings/mema.gpg] file:///repo ./" > /etc/apt/sources.list.d/mema-test.list
    else
        echo "deb [trusted=yes] file:///repo ./" > /etc/apt/sources.list.d/mema-test.list
    fi
    apt-get update
    apt-get install -y mema-go-latest
'

docker exec "$CONTAINER_NAME" mema list
docker exec "$CONTAINER_NAME" go version
docker exec "$CONTAINER_NAME" test -L /usr/local/bin/go
echo "--- PASS: Mema installed and activated Go in minimal Debian ---"
