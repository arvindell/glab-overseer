#!/usr/bin/env sh

set -eu

REPO="arvindell/glab-overseer"
BIN_NAME="glab-overseer"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
VERSION="${VERSION:-latest}"

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

detect_os() {
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  case "$os" in
    linux|darwin) echo "$os" ;;
    *) echo "unsupported operating system: $os" >&2; exit 1 ;;
  esac
}

detect_arch() {
  arch=$(uname -m)
  case "$arch" in
    x86_64|amd64) echo "amd64" ;;
    arm64|aarch64) echo "arm64" ;;
    armv7l|armv7) echo "armv7" ;;
    *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
  esac
}

fetch() {
  url="$1"
  output="$2"

  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" -o "$output"
    return
  fi

  if command -v wget >/dev/null 2>&1; then
    wget -qO "$output" "$url"
    return
  fi

  echo "curl or wget is required" >&2
  exit 1
}

resolve_version() {
  if [ "$VERSION" != "latest" ]; then
    echo "$VERSION"
    return
  fi

  api_url="https://api.github.com/repos/$REPO/releases/latest"
  version=$(curl -fsSL "$api_url" | sed -n 's/.*"tag_name": "\([^"]*\)".*/\1/p' | head -n 1)

  if [ -z "$version" ]; then
    echo "failed to resolve latest release" >&2
    exit 1
  fi

  echo "$version"
}

need_cmd tar

OS=$(detect_os)
ARCH=$(detect_arch)
VERSION=$(resolve_version)

archive="${BIN_NAME}_${VERSION}_${OS}_${ARCH}.tar.gz"
download_url="https://github.com/$REPO/releases/download/$VERSION/$archive"

tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT INT TERM

echo "Downloading $download_url"
fetch "$download_url" "$tmp_dir/$archive"

tar -xzf "$tmp_dir/$archive" -C "$tmp_dir"

if [ ! -f "$tmp_dir/$BIN_NAME" ]; then
  echo "binary not found in release archive" >&2
  exit 1
fi

chmod +x "$tmp_dir/$BIN_NAME"
mkdir -p "$INSTALL_DIR"
mv "$tmp_dir/$BIN_NAME" "$INSTALL_DIR/$BIN_NAME"

echo "Installed $BIN_NAME $VERSION to $INSTALL_DIR/$BIN_NAME"
