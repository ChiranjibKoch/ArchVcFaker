#!/bin/bash

set -eu

NTGCALLS_VERSION="${NTGCALLS_VERSION:-v2.2.5}"

RED="\033[1;31m"
GREEN="\033[1;32m"
YELLOW="\033[1;33m"
BLUE="\033[1;34m"
RESET="\033[0m"

print_step()     { echo -e "\n${BLUE}▶ $1${RESET}"; }
print_success()  { echo -e "${GREEN}✓ $1${RESET}"; }
print_error()    { echo -e "${RED}✗ $1${RESET}"; }

fail() {
    echo -e "${RED}${1}${RESET}" >&2
    exit 1
}

require_tool() {
    if ! command -v "$1" >/dev/null 2>&1; then
        fail "✗ '$1' is required. Install it and re-run this script."
    fi
}

detect_system() {
    case "$(uname -s)" in
        Linux)              OS_TYPE="linux" ;;
        Darwin)             OS_TYPE="macos" ;;
        *) fail "Unsupported OS: $(uname -s). Use Linux/macOS." ;;
    esac

    case "$(uname -m)" in
        x86_64|amd64)   ARCH_TYPE="amd64" ;;
        aarch64|arm64)  ARCH_TYPE="arm64" ;;
        *) fail "Unsupported arch: $(uname -m). Use x86_64/arm64." ;;
    esac

    print_success "System: $OS_TYPE ($ARCH_TYPE)"
}

download() {
    local url="$1" output="$2"
    if command -v curl >/dev/null 2>&1; then
        curl -sSL -o "$output" "$url"
    elif command -v wget >/dev/null 2>&1; then
        wget -q -O "$output" "$url"
    else
        fail "✗ Neither curl nor wget is available."
    fi
}

install_ntgcalls() {
    print_step "Installing ntgcalls..."

    require_tool unzip

    local url=""
    case "$OS_TYPE" in
        linux)
            if [[ "$ARCH_TYPE" == "amd64" ]]; then
                url="https://github.com/pytgcalls/ntgcalls/releases/download/$NTGCALLS_VERSION/ntgcalls.linux-x86_64-static_libs.zip"
            else
                url="https://github.com/pytgcalls/ntgcalls/releases/download/$NTGCALLS_VERSION/ntgcalls.linux-arm64-static_libs.zip"
            fi
            ;;
        macos)
            if [[ "$ARCH_TYPE" == "arm64" ]]; then
                url="https://github.com/pytgcalls/ntgcalls/releases/download/$NTGCALLS_VERSION/ntgcalls.macos-arm64-static_libs.zip"
            else
                fail "✗ ntgcalls is unavailable for macOS x86_64. Build it from source instead."
            fi
            ;;
    esac

    print_step "Downloading ntgcalls $NTGCALLS_VERSION..."
    download "$url" "ntgcalls.zip"

    print_step "Extracting library..."
    rm -rf tmp_ntg
    unzip -q ntgcalls.zip -d tmp_ntg

    mkdir -p ntgcalls
    [[ -f tmp_ntg/include/ntgcalls.h ]] || fail "✗ ntgcalls.h not found in archive."
    cp tmp_ntg/include/ntgcalls.h ntgcalls/ntgcalls.h

    local lib_file
    lib_file=$(find tmp_ntg/lib -type f | head -n1)
    if [[ -z "$lib_file" ]]; then
        rm -rf ntgcalls.zip tmp_ntg
        fail "✗ ntgcalls library not found in archive."
    fi

    mv -f "$lib_file" "ntgcalls/$(basename "$lib_file")"
    rm -rf ntgcalls.zip tmp_ntg

    print_success "ntgcalls installed ($NTGCALLS_VERSION): ./ntgcalls/$(basename "$lib_file")"
}

main() {
    detect_system
    install_ntgcalls
    print_success "Installation complete."
}

main "$@"
