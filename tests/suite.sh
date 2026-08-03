#!/usr/bin/env bash
set -euo pipefail

# This test suite tests the mema package and all recipes.
# It can be run on the host (which will launch docker) or inside the docker container.

REPO_DIR=$(realpath "$(dirname "$0")/../dist")

if [ ! -f /.dockerenv ] && [ "${MEMA_TEST_INSIDE_CONTAINER:-0}" != "1" ]; then
    echo "=== Running test suite in Docker container ==="
    # Build repository packages first to ensure we test the latest code
    "$(dirname "$0")/../build-repo.sh"

    # Set up host caching directories
    CACHE_DIR="$(realpath "$(dirname "$0")")/.cache"
    mkdir -p "$CACHE_DIR/apt-lists" "$CACHE_DIR/apt-archives" "$CACHE_DIR/mema"

    CONTAINER_NAME="mema-test-suite-$$"
    cleanup() {
        docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
    }
    trap cleanup EXIT

    # Start clean container with caching directories mounted
    docker run -d --rm --name "$CONTAINER_NAME" \
        -v "$REPO_DIR:/repo:ro" \
        -v "$(realpath "$0"):/test-suite.sh:ro" \
        -v "$CACHE_DIR/apt-lists:/var/lib/apt/lists" \
        -v "$CACHE_DIR/apt-archives:/var/cache/apt/archives" \
        -v "$CACHE_DIR/mema:/tmp/mema/cache" \
        debian:bookworm-slim sleep infinity

    # Run the test suite inside the container
    docker exec -e MEMA_TEST_INSIDE_CONTAINER=1 "$CONTAINER_NAME" bash /test-suite.sh
    echo "=== PASS: All tests succeeded! ==="
    exit 0
fi

# --- Inside Docker Container ---
# Prevent Docker clean configurations from wiping the package cache
if [ -f /etc/apt/apt.conf.d/docker-clean ]; then
    echo 'Binary::apt::APT::Keep-Downloaded-Packages "true";' > /etc/apt/apt.conf.d/keep-packages
    echo "" > /etc/apt/apt.conf.d/docker-clean
fi

echo "--- Configuring local APT repository ---"
echo "deb [trusted=yes] file:///repo ./" > /etc/apt/sources.list.d/mema-test.list
apt-get update

echo "--- Installing Mema base package ---"
apt-get install -y mema jq curl ca-certificates xz-utils tar

# Find all recipe packages in the repo
echo "--- Finding built recipe packages ---"
recipes=()
for deb in /repo/mema-*.deb; do
    name=$(basename "$deb")
    # Exclude base packages and latest meta-packages
    # Debian filenames are: name_version_arch.deb
    if [[ "$name" =~ ^mema-([a-zA-Z0-9-]+)_ ]]; then
        recipe_name="${BASH_REMATCH[1]}"
        if [[ "$recipe_name" != *-latest ]]; then
            recipes+=("$recipe_name")
        fi
    fi
done

if [ ${#recipes[@]} -eq 0 ]; then
    echo "Error: No recipe packages found in /repo"
    exit 1
fi

echo "Recipes to test: ${recipes[*]}"

# Helper for testing basic tool execution or compilation
test_tool_execution() {
    local tool="$1"
    local version="$2"
    echo "Testing execution for $tool (version $version)..."

    case "$tool" in
        go)
            # Verify compiler works by compiling a basic program
            cat << 'EOF' > /tmp/hello.go
package main
import "fmt"
func main() {
    fmt.Println("MEMA_TEST_OK")
}
EOF
            /usr/local/bin/go run /tmp/hello.go > /tmp/hello.out
            if [ "$(cat /tmp/hello.out)" != "MEMA_TEST_OK" ]; then
                echo "Error: Compiled program output did not match expected"
                exit 1
            fi
            rm -f /tmp/hello.go /tmp/hello.out
            ;;
        *)
            # Fallback check for non-compiler tools
            if ! "/usr/local/bin/$tool" --version >/dev/null 2>&1; then
                if ! "/usr/local/bin/$tool" -h >/dev/null 2>&1; then
                    echo "Warning: Tool $tool does not support --version or -h, but checking if executable exists"
                    test -x "/usr/local/bin/$tool"
                fi
            fi
            ;;
    esac
}

for tool in "${recipes[@]}"; do
    echo "========================================="
    echo "Testing recipe: $tool"
    echo "========================================="

    echo "--- Installing mema-$tool package ---"
    apt-get install -y "mema-$tool"

    recipe_path="/etc/mema/recipe/${tool}.sh"
    if [ ! -f "$recipe_path" ]; then
        echo "Error: Recipe file $recipe_path not installed"
        exit 1
    fi

    # Source recipe to query versions
    # We define dummy download/verify helpers in case mema_get_versions uses them
    mema_download() { :; }
    mema_verify() { :; }
    source "$recipe_path"

    # Get two available versions
    arch=$(uname -m)
    case "$arch" in
        x86_64) r_arch="amd64" ;;
        aarch64) r_arch="arm64" ;;
        *) r_arch="amd64" ;;
    esac

    echo "Resolving versions for $tool on $r_arch..."
    versions=($(mema_get_versions | awk -v arch="$r_arch" '$2 == arch { print $1 }' | head -n 2))
    unset -f mema_get_versions mema_resolve_version mema_install mema_use mema_download mema_verify

    if [ ${#versions[@]} -lt 2 ]; then
        echo "Warning: Less than 2 versions found for $tool on $r_arch. Testing single version instead."
        if [ ${#versions[@]} -eq 0 ]; then
            echo "Error: No versions found for $tool"
            exit 1
        fi
        v1="${versions[0]}"
        v2=""
    else
        v1="${versions[0]}"
        v2="${versions[1]}"
    fi

    echo "Selected version 1: $v1"
    [ -n "$v2" ] && echo "Selected version 2: $v2"

    echo "--- Testing Installation of $v1 ---"
    mema install "$tool" "$v1"
    
    # Check active version
    active=$(mema list | grep "$tool" | awk '{print $3}')
    if [ "$active" != "$v1" ]; then
        echo "Error: Active version for $tool is '$active', expected '$v1'"
        exit 1
    fi

    test_tool_execution "$tool" "$v1"

    if [ -n "$v2" ]; then
        echo "--- Testing Installation of $v2 ---"
        mema install "$tool" "$v2"

        # Check it automatically activated
        active=$(mema list | grep "$tool" | awk '{print $3}')
        if [ "$active" != "$v2" ]; then
            echo "Error: Active version for $tool is '$active', expected '$v2'"
            exit 1
        fi

        test_tool_execution "$tool" "$v2"

        echo "--- Testing Tool Change (Switch to $v1) ---"
        mema use "$tool" "$v1"

        active=$(mema list | grep "$tool" | awk '{print $3}')
        if [ "$active" != "$v1" ]; then
            echo "Error: Active version for $tool is '$active', expected '$v1'"
            exit 1
        fi

        test_tool_execution "$tool" "$v1"

        # Check list output structure
        echo "Checking mema list output:"
        mema list
        if ! mema list | grep -E "$v1.*$v2|$v2.*$v1" >/dev/null; then
            # Verify both versions are listed
            if ! (mema list | grep "$v1" >/dev/null && mema list | grep "$v2" >/dev/null); then
                echo "Error: Both installed versions $v1 and $v2 are not shown in mema list"
                exit 1
            fi
        fi

        echo "--- Testing Removal of $v1 (Active Version) ---"
        mema remove "$tool" "$v1"

        # Since active got removed, it should not be active, but v2 remains installed
        if mema list | grep "$v1" >/dev/null; then
            echo "Error: Version $v1 was not removed from list"
            exit 1
        fi

        # Remove v2
        echo "--- Testing Removal of $v2 ---"
        mema remove "$tool" "$v2"
    else
        # Only one version was tested
        echo "--- Testing Removal of $v1 ---"
        mema remove "$tool" "$v1"
    fi

    # Verify tool is completely uninstalled/inactive
    if [ -f "/usr/local/bin/$tool" ]; then
        echo "Error: Link /usr/local/bin/$tool still exists after removal"
        exit 1
    fi

    echo "--- Recipe $tool testing completed successfully ---"
done
