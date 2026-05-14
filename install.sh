#!/usr/bin/env sh
# magus installer. Pipe me from curl:
#   curl -fsSL https://magus.digital/install.sh | sh

set -e

REPO="wir-drei-digital/magus-cli"
INSTALL_DIR="${MAGUS_INSTALL_DIR:-/usr/local/bin}"
TMP="$(mktemp -d)"

uname_s=$(uname -s | tr '[:upper:]' '[:lower:]')
uname_m=$(uname -m)

case "$uname_s" in
  darwin) OS=darwin ;;
  linux)  OS=linux ;;
  *)      echo "Unsupported OS: $uname_s" >&2; exit 1 ;;
esac

case "$uname_m" in
  x86_64|amd64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) echo "Unsupported arch: $uname_m" >&2; exit 1 ;;
esac

LATEST=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
  | grep '"tag_name"' | head -n1 | cut -d '"' -f4)
[ -n "$LATEST" ] || { echo "Failed to find latest release" >&2; exit 1; }

VERSION="${LATEST#v}"
ARCHIVE="magus_${VERSION}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/$REPO/releases/download/$LATEST/$ARCHIVE"

echo "Downloading $ARCHIVE..."
curl -fsSL -o "$TMP/$ARCHIVE" "$URL"
tar -xzf "$TMP/$ARCHIVE" -C "$TMP"

if [ -w "$INSTALL_DIR" ]; then
  install -m 0755 "$TMP/magus" "$INSTALL_DIR/magus"
else
  echo "Installing to $INSTALL_DIR (sudo required)..."
  sudo install -m 0755 "$TMP/magus" "$INSTALL_DIR/magus"
fi

rm -rf "$TMP"
echo "magus installed: $($INSTALL_DIR/magus version)"
