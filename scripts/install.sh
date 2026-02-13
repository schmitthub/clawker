#!/usr/bin/env bash
#
# install.sh — Download and install the clawker binary from GitHub releases.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/schmitthub/clawker/main/scripts/install.sh | bash
#   curl -fsSL ... | CLAWKER_VERSION=v0.1.3 bash
#   curl -fsSL ... | CLAWKER_INSTALL_DIR=$HOME/.local/bin bash
#   bash scripts/install.sh --version v0.1.3 --dir /usr/local/bin
#
set -euo pipefail

# ── Constants ────────────────────────────────────────────────────────────────
REPO_OWNER="schmitthub"
REPO_NAME="clawker"
BINARY_NAME="clawker"
GITHUB_API="https://api.github.com"
GITHUB_RELEASES="https://github.com/${REPO_OWNER}/${REPO_NAME}/releases"

# ── Defaults ─────────────────────────────────────────────────────────────────
VERSION="${CLAWKER_VERSION:-}"
INSTALL_DIR="${CLAWKER_INSTALL_DIR:-/usr/local/bin}"
NO_COLOR="${NO_COLOR:-false}"

# ── Parse flags ──────────────────────────────────────────────────────────────
usage() {
    cat <<'EOF'
Usage: install.sh [OPTIONS]

Download and install the clawker binary from GitHub releases.

Options:
  --version VERSION   Install a specific version (default: latest)
  --dir DIR           Install directory (default: /usr/local/bin)
  --no-color          Disable colored output
  --help              Show this help message

Environment variables:
  CLAWKER_VERSION       Same as --version
  CLAWKER_INSTALL_DIR   Same as --dir
  NO_COLOR              Same as --no-color (any non-empty value)
EOF
    exit 0
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --version)
            VERSION="$2"
            shift 2
            ;;
        --version=*)
            VERSION="${1#--version=}"
            shift
            ;;
        --dir)
            INSTALL_DIR="$2"
            shift 2
            ;;
        --dir=*)
            INSTALL_DIR="${1#--dir=}"
            shift
            ;;
        --no-color)
            NO_COLOR=true
            shift
            ;;
        --help)
            usage
            ;;
        *)
            echo "Unknown option: $1" >&2
            echo "Run with --help for usage." >&2
            exit 1
            ;;
    esac
done

# ── Colors ───────────────────────────────────────────────────────────────────
# In curl|bash, stdin/stdout are the pipe. Check stderr (fd 2) for TTY.
if [[ "$NO_COLOR" != "false" ]] || [[ ! -t 2 ]]; then
    RED="" GREEN="" YELLOW="" CYAN="" BOLD="" RESET=""
else
    RED='\033[0;31m' GREEN='\033[0;32m' YELLOW='\033[0;33m' CYAN='\033[0;36m'
    BOLD='\033[1m' RESET='\033[0m'
fi

# ── Utility functions ────────────────────────────────────────────────────────
msg()     { echo -e "$*" >&2; }
info()    { msg "${CYAN}${BOLD}==>${RESET} $*"; }
success() { msg "${GREEN}${BOLD}==>${RESET} $*"; }
warn()    { msg "${YELLOW}${BOLD}warning:${RESET} $*"; }
error()   { msg "${RED}${BOLD}error:${RESET} $*"; }

has_cmd() { command -v "$1" >/dev/null 2>&1; }

# ── Cleanup ──────────────────────────────────────────────────────────────────
TMPDIR=""
cleanup() {
    if [[ -n "$TMPDIR" ]] && [[ -d "$TMPDIR" ]]; then
        rm -rf "$TMPDIR"
    fi
}
trap cleanup EXIT INT TERM

# ── HTTP helpers ─────────────────────────────────────────────────────────────
check_http_tool() {
    if ! has_cmd curl && ! has_cmd wget; then
        error "Either curl or wget is required but neither was found."
        exit 1
    fi
}

# http_get URL — print response body to stdout
http_get() {
    local url="$1"
    if has_cmd curl; then
        curl -fsSL "$url"
    else
        wget -qO- "$url"
    fi
}

# http_download URL FILE — download URL to FILE
http_download() {
    local url="$1" dest="$2"
    if has_cmd curl; then
        curl -fsSL -o "$dest" "$url"
    else
        wget -q -O "$dest" "$url"
    fi
}

# ── Platform detection ───────────────────────────────────────────────────────
detect_os() {
    local os
    os="$(uname -s | tr '[:upper:]' '[:lower:]')"
    case "$os" in
        linux)  echo "linux" ;;
        darwin) echo "darwin" ;;
        *)
            error "Unsupported operating system: $os"
            msg "Supported: linux, darwin (macOS)"
            exit 1
            ;;
    esac
}

detect_arch() {
    local arch
    arch="$(uname -m)"
    case "$arch" in
        x86_64|amd64)   echo "amd64" ;;
        arm64|aarch64)  echo "arm64" ;;
        *)
            error "Unsupported architecture: $arch"
            msg "Supported: amd64 (x86_64), arm64 (aarch64)"
            exit 1
            ;;
    esac
}

# ── Version resolution ───────────────────────────────────────────────────────
resolve_version() {
    if [[ -n "$VERSION" ]]; then
        # Ensure v prefix
        case "$VERSION" in
            v*) echo "$VERSION" ;;
            *)  echo "v${VERSION}" ;;
        esac
        return
    fi

    info "Fetching latest release..."
    local response
    if ! response=$(http_get "${GITHUB_API}/repos/${REPO_OWNER}/${REPO_NAME}/releases/latest" 2>&1); then
        if echo "$response" | grep -qi "rate limit"; then
            error "GitHub API rate limit exceeded."
            msg "Try again later, or specify a version:"
            msg "  CLAWKER_VERSION=v0.1.3 bash install.sh"
        else
            error "Failed to fetch latest release from GitHub API."
            msg "Check your internet connection, or specify a version:"
            msg "  CLAWKER_VERSION=v0.1.3 bash install.sh"
        fi
        exit 1
    fi

    local tag
    tag=$(echo "$response" | grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*"tag_name"[[:space:]]*:[[:space:]]*"//;s/"//')
    if [[ -z "$tag" ]]; then
        error "Could not determine latest version from GitHub API response."
        msg "Specify a version manually:"
        msg "  CLAWKER_VERSION=v0.1.3 bash install.sh"
        exit 1
    fi

    echo "$tag"
}

# ── Checksum verification ───────────────────────────────────────────────────
verify_checksum() {
    local archive="$1" checksums_file="$2" archive_name="$3"

    # Find the expected checksum for this archive
    local expected
    expected=$(grep "  ${archive_name}$" "$checksums_file" | awk '{print $1}')
    if [[ -z "$expected" ]]; then
        # Try without leading spaces (some formats use tab or single space)
        expected=$(grep "${archive_name}" "$checksums_file" | awk '{print $1}')
    fi

    if [[ -z "$expected" ]]; then
        warn "Could not find checksum for ${archive_name} in checksums.txt"
        warn "Skipping checksum verification."
        return 0
    fi

    local actual=""
    if has_cmd sha256sum; then
        actual=$(sha256sum "$archive" | awk '{print $1}')
    elif has_cmd shasum; then
        actual=$(shasum -a 256 "$archive" | awk '{print $1}')
    else
        warn "Neither sha256sum nor shasum found — skipping checksum verification."
        return 0
    fi

    if [[ "$actual" != "$expected" ]]; then
        error "Checksum verification failed!"
        msg "  Expected: ${expected}"
        msg "  Actual:   ${actual}"
        msg ""
        msg "The downloaded archive may be corrupted or tampered with."
        msg "Download manually from: ${GITHUB_RELEASES}"
        exit 1
    fi
}

# ── Installed version detection ──────────────────────────────────────────────
# get_installed_version INSTALL_DIR — print the installed version (bare, no "v"
# prefix) or nothing if clawker is not found.
get_installed_version() {
    local install_dir="$1"

    local bin=""
    # Check install dir first, then PATH
    if [[ -x "${install_dir}/${BINARY_NAME}" ]]; then
        bin="${install_dir}/${BINARY_NAME}"
    elif has_cmd "$BINARY_NAME"; then
        bin="$BINARY_NAME"
    else
        return
    fi

    # clawker version output: "clawker version 0.1.3 (2025-01-15)"
    "$bin" version 2>/dev/null | sed -n 's/clawker version \([^ ]*\).*/\1/p'
}

# ── Main ─────────────────────────────────────────────────────────────────────
main() {
    msg "${BOLD}Clawker Installer${RESET}"
    msg ""

    check_http_tool

    local os arch
    os=$(detect_os)
    arch=$(detect_arch)
    info "Detected platform: ${os}/${arch}"

    local version_tag
    version_tag=$(resolve_version)
    info "Version: ${version_tag}"

    # Version without v prefix (archive names don't have it)
    local version_bare="${version_tag#v}"

    # Check if already installed (same version → skip, different → upgrade message)
    local installed
    installed=$(get_installed_version "$INSTALL_DIR")
    if [[ "$installed" == "$version_bare" ]]; then
        success "${BINARY_NAME} ${version_tag} is already installed."
        return 0
    elif [[ -n "$installed" ]]; then
        info "Upgrading ${BINARY_NAME} from ${installed} to ${version_tag}..."
    fi

    # Archive naming matches .goreleaser.yaml: clawker_VERSION_OS_ARCH.tar.gz
    local archive_name="${BINARY_NAME}_${version_bare}_${os}_${arch}.tar.gz"
    local download_url="${GITHUB_RELEASES}/download/${version_tag}/${archive_name}"
    local checksums_url="${GITHUB_RELEASES}/download/${version_tag}/checksums.txt"

    # Create temp directory
    TMPDIR=$(mktemp -d)

    local archive_path="${TMPDIR}/${archive_name}"
    local checksums_path="${TMPDIR}/checksums.txt"

    # Download archive
    info "Downloading ${archive_name}..."
    if ! http_download "$download_url" "$archive_path"; then
        error "Failed to download ${archive_name}"
        msg "URL: ${download_url}"
        msg ""
        msg "Check that version ${version_tag} exists:"
        msg "  ${GITHUB_RELEASES}"
        exit 1
    fi

    # Download and verify checksums
    info "Verifying checksum..."
    if http_download "$checksums_url" "$checksums_path" 2>/dev/null; then
        verify_checksum "$archive_path" "$checksums_path" "$archive_name"
    else
        warn "Could not download checksums.txt — skipping verification."
    fi

    # Extract
    info "Extracting..."
    tar -xzf "$archive_path" -C "$TMPDIR"

    if [[ ! -f "${TMPDIR}/${BINARY_NAME}" ]]; then
        error "Expected binary '${BINARY_NAME}' not found in archive."
        exit 1
    fi

    # Install
    info "Installing to ${INSTALL_DIR}..."
    if [[ ! -d "$INSTALL_DIR" ]]; then
        if ! mkdir -p "$INSTALL_DIR" 2>/dev/null; then
            error "Cannot create install directory: ${INSTALL_DIR}"
            msg "Try with sudo or use a writable directory:"
            msg "  sudo bash install.sh --dir ${INSTALL_DIR}"
            msg "  bash install.sh --dir \$HOME/.local/bin"
            exit 1
        fi
    fi

    if ! install -m 755 "${TMPDIR}/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}" 2>/dev/null; then
        error "Permission denied writing to ${INSTALL_DIR}"
        msg "Try with sudo or use a writable directory:"
        msg "  sudo bash install.sh --dir ${INSTALL_DIR}"
        msg "  bash install.sh --dir \$HOME/.local/bin"
        exit 1
    fi

    if [[ -n "$installed" ]]; then
        success "Upgraded ${BINARY_NAME} from ${installed} to ${version_tag} at ${INSTALL_DIR}/${BINARY_NAME}"
    else
        success "Installed ${BINARY_NAME} ${version_tag} to ${INSTALL_DIR}/${BINARY_NAME}"
    fi

    # Check if install dir is on PATH
    case ":${PATH}:" in
        *":${INSTALL_DIR}:"*)
            ;;
        *)
            warn "${INSTALL_DIR} is not in your PATH."
            msg "Add it with:"
            msg "  export PATH=\"${INSTALL_DIR}:\$PATH\""
            msg ""
            msg "To make it permanent, add that line to your shell profile (~/.bashrc, ~/.zshrc, etc.)"
            ;;
    esac

    msg ""
    success "Run 'clawker --help' to get started."
}

main
