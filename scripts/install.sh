#!/usr/bin/env bash
set -euo pipefail

DEST_DIR=""
TAG="latest"

while [ $# -gt 0 ]; do
    case "$1" in
        -d|--dest|--destination)
            if [ -z "${2:-}" ]; then
                echo "Error: Missing argument for $1" >&2
                exit 1
            fi
            DEST_DIR="$2"
            shift 2
            ;;
        --dest=*|--destination=*)
            DEST_DIR="${1#*=}"
            shift
            ;;
        -t|--tag|-v|--version)
            if [ -z "${2:-}" ]; then
                echo "Error: Missing argument for $1" >&2
                exit 1
            fi
            TAG="$2"
            shift 2
            ;;
        --tag=*|--version=*)
            TAG="${1#*=}"
            shift
            ;;
        -h|--help)
            echo "Usage: $0 [options] [destination] [version/tag]"
            echo ""
            echo "Options:"
            echo "  -d, --dest, --destination <dir>  Installation directory (default: ~/.local/bin)"
            echo "  -t, --tag, -v, --version <tag>   Release tag to install (default: latest)"
            echo "  -h, --help                       Show this help message"
            exit 0
            ;;
        *)
            case "$1" in
                latest|v[0-9]*|[0-9]*.[0-9]*)
                    if [ "$TAG" = "latest" ]; then
                        TAG="$1"
                    elif [ -z "$DEST_DIR" ]; then
                        DEST_DIR="$1"
                    else
                        echo "Error: Unexpected argument: $1" >&2
                        exit 1
                    fi
                    ;;
                *)
                    if [ -z "$DEST_DIR" ]; then
                        DEST_DIR="$1"
                    elif [ "$TAG" = "latest" ]; then
                        TAG="$1"
                    else
                        echo "Error: Unexpected argument: $1" >&2
                        exit 1
                    fi
                    ;;
            esac
            shift
            ;;
    esac
done

if [ -z "$DEST_DIR" ]; then
    DEST_DIR="${HOME}/.local/bin"
fi

case "$DEST_DIR" in
    "~"*) DEST_DIR="${HOME}${DEST_DIR#\~}" ;;
esac

OS_RAW="$(uname -s)"
case "$OS_RAW" in
    Linux*)   OS="linux" ;;
    Darwin*)  OS="darwin" ;;
    CYGWIN*|MINGW*|MSYS*|Windows_NT*) OS="windows" ;;
    *)
        echo "Error: Unsupported operating system: $OS_RAW (only linux, darwin, windows are supported)" >&2
        exit 1
        ;;
esac

ARCH_RAW="$(uname -m)"
case "$ARCH_RAW" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *)
        echo "Error: Unsupported architecture: $ARCH_RAW (only amd64, arm64 are supported)" >&2
        exit 1
        ;;
esac

if [ "$OS" = "windows" ]; then
    EXE_NAME="mcp-relayd.exe"
    SUFFIX="zip"
else
    EXE_NAME="mcp-relayd"
    SUFFIX="tar.gz"
fi

download_file() {
    url="$1"
    output="$2"

    case "$url" in
        https://*) ;;
        *)
            echo "Error: Insecure download URL rejected: $url (HTTPS required)" >&2
            exit 1
            ;;
    esac

    if command -v curl >/dev/null 2>&1; then
        curl -sSfL --proto '=https' -o "$output" "$url"
    elif command -v wget >/dev/null 2>&1; then
        wget -q --https-only -O "$output" "$url"
    else
        echo "Error: Neither curl nor wget is available for downloading" >&2
        exit 1
    fi
}

compute_sha256() {
    file="$1"
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$file" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$file" | awk '{print $1}'
    elif command -v openssl >/dev/null 2>&1; then
        openssl dgst -sha256 "$file" | awk '{print $NF}'
    else
        echo "Error: No SHA-256 tool found (need sha256sum, shasum, or openssl)" >&2
        exit 1
    fi
}

TEMP_DIR=$(mktemp -d 2>/dev/null || mktemp -d -t 'mcp-relayd-install')
trap 'rm -rf "$TEMP_DIR"' EXIT

REPO_URL="https://github.com/atlasfoo/mcp-relayd"

if [ "$TAG" = "latest" ]; then
    SUMS_URL="${REPO_URL}/releases/latest/download/SHA256SUMS"
    BASE_DOWNLOAD_URL="${REPO_URL}/releases/latest/download"
else
    RELEASE_TAG="$TAG"
    case "$RELEASE_TAG" in
        v*) ;;
        [0-9]*) RELEASE_TAG="v$RELEASE_TAG" ;;
    esac
    SUMS_URL="${REPO_URL}/releases/download/${RELEASE_TAG}/SHA256SUMS"
    BASE_DOWNLOAD_URL="${REPO_URL}/releases/download/${RELEASE_TAG}"
fi

if ! download_file "$SUMS_URL" "$TEMP_DIR/SHA256SUMS"; then
    if [ "$TAG" != "latest" ] && [ "${RELEASE_TAG:-}" != "$TAG" ]; then
        SUMS_URL="${REPO_URL}/releases/download/${TAG}/SHA256SUMS"
        BASE_DOWNLOAD_URL="${REPO_URL}/releases/download/${TAG}"
        if ! download_file "$SUMS_URL" "$TEMP_DIR/SHA256SUMS"; then
            echo "Error: Failed to download SHA256SUMS from $SUMS_URL" >&2
            exit 1
        fi
    else
        echo "Error: Failed to download SHA256SUMS from $SUMS_URL" >&2
        exit 1
    fi
fi

if [ "$TAG" != "latest" ]; then
    VERSION="${TAG#v}"
    ASSET_NAME="mcp-relayd_${VERSION}_${OS}_${ARCH}.${SUFFIX}"
    EXPECTED_HASH=$(awk -v asset="$ASSET_NAME" '($2 == asset || $2 == "*" asset) { print $1 }' "$TEMP_DIR/SHA256SUMS")
else
    MATCH_COUNT=$(awk -v pattern="_${OS}_${ARCH}.${SUFFIX}\$" '$2 ~ pattern { count++ } END { print count+0 }' "$TEMP_DIR/SHA256SUMS")
    if [ "$MATCH_COUNT" -ne 1 ]; then
        echo "Error: Asset for ${OS}/${ARCH} (${SUFFIX}) not found in SHA256SUMS" >&2
        exit 1
    fi
    MATCH_LINE=$(awk -v pattern="_${OS}_${ARCH}.${SUFFIX}\$" '$2 ~ pattern { print $1, $2 }' "$TEMP_DIR/SHA256SUMS")
    EXPECTED_HASH=$(printf '%s' "$MATCH_LINE" | awk '{print $1}')
    ASSET_NAME=$(printf '%s' "$MATCH_LINE" | awk '{print $2}')
fi

case "$EXPECTED_HASH" in
    [0-9a-fA-F]*)
        if [ ${#EXPECTED_HASH} -ne 64 ]; then
            echo "Error: Invalid digest format in SHA256SUMS for ${ASSET_NAME:-unknown asset}" >&2
            exit 1
        fi
        ;;
    *)
        echo "Error: Checksum for ${ASSET_NAME:-unknown asset} not found in SHA256SUMS" >&2
        exit 1
        ;;
esac

ASSET_URL="${BASE_DOWNLOAD_URL}/${ASSET_NAME}"
if ! download_file "$ASSET_URL" "$TEMP_DIR/$ASSET_NAME"; then
    echo "Error: Failed to download asset from $ASSET_URL" >&2
    exit 1
fi

ACTUAL_HASH=$(compute_sha256 "$TEMP_DIR/$ASSET_NAME")
ACTUAL_HASH=$(printf '%s' "$ACTUAL_HASH" | tr '[:upper:]' '[:lower:]')
EXPECTED_HASH=$(printf '%s' "$EXPECTED_HASH" | tr '[:upper:]' '[:lower:]')

if [ "$ACTUAL_HASH" != "$EXPECTED_HASH" ]; then
    echo "Error: Checksum mismatch for $ASSET_NAME" >&2
    echo "Expected: $EXPECTED_HASH" >&2
    echo "Actual:   $ACTUAL_HASH" >&2
    exit 1
fi

mkdir -p "$TEMP_DIR/extracted"
if [ "$SUFFIX" = "zip" ]; then
    if command -v unzip >/dev/null 2>&1; then
        unzip -q -o "$TEMP_DIR/$ASSET_NAME" -d "$TEMP_DIR/extracted"
    elif command -v tar >/dev/null 2>&1; then
        tar -xf "$TEMP_DIR/$ASSET_NAME" -C "$TEMP_DIR/extracted"
    else
        echo "Error: Neither unzip nor tar is available to extract zip file" >&2
        exit 1
    fi
else
    tar -xzf "$TEMP_DIR/$ASSET_NAME" -C "$TEMP_DIR/extracted"
fi

if [ ! -f "$TEMP_DIR/extracted/$EXE_NAME" ]; then
    echo "Error: Executable $EXE_NAME was not found in archive $ASSET_NAME" >&2
    exit 1
fi

mkdir -p "$DEST_DIR"
cp "$TEMP_DIR/extracted/$EXE_NAME" "$DEST_DIR/$EXE_NAME"
chmod 755 "$DEST_DIR/$EXE_NAME"

DEST_DIR_ABS=$(cd "$DEST_DIR" 2>/dev/null && pwd || printf '%s' "$DEST_DIR")

in_path=0
OLD_IFS="$IFS"
IFS=':'
for dir_entry in $PATH; do
    if [ -n "$dir_entry" ]; then
        entry_abs=$(cd "$dir_entry" 2>/dev/null && pwd || printf '%s' "$dir_entry")
        if [ "$entry_abs" = "$DEST_DIR_ABS" ] || [ "$dir_entry" = "$DEST_DIR" ]; then
            in_path=1
            break
        fi
    fi
done
IFS="$OLD_IFS"

if [ "$in_path" -eq 0 ]; then
    cat >&2 <<EOF

========================================================================
WARNING: $DEST_DIR is not in your PATH!
You will not be able to run 'mcp-relayd' directly from the terminal.
To add it to your PATH, add the following line to your shell profile:
    export PATH="$DEST_DIR:\$PATH"
========================================================================

EOF
fi

echo "Installed $EXE_NAME to $DEST_DIR/$EXE_NAME"
echo ""
echo "Next steps:"
echo "1. Run 'mcp-relayd config' to generate a default configuration file."
echo "2. Refer to https://github.com/atlasfoo/mcp-relayd/blob/master/README.md for setup and usage instructions."
