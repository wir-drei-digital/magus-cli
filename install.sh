#!/usr/bin/env sh
# magus installer. Pipe me from curl:
#   curl -fsSL https://magus.digital/install.sh | sh
#
# Installs to $HOME/.magus/bin/magus by default. Set MAGUS_INSTALL_DIR
# to override (e.g., MAGUS_INSTALL_DIR=/usr/local/bin sh install.sh —
# you'll need write permission to whatever path you choose).

set -eu

REPO="wir-drei-digital/magus-cli"
INSTALL_DIR="${MAGUS_INSTALL_DIR:-$HOME/.magus/bin}"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

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

echo "Verifying checksum..."
curl -fsSL -o "$TMP/checksums.txt" \
  "https://github.com/$REPO/releases/download/$LATEST/checksums.txt"
EXPECTED=$(grep " $ARCHIVE\$" "$TMP/checksums.txt" | awk '{print $1}')
[ -n "$EXPECTED" ] || { echo "checksum not found for $ARCHIVE" >&2; exit 1; }
if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL=$(sha256sum "$TMP/$ARCHIVE" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  ACTUAL=$(shasum -a 256 "$TMP/$ARCHIVE" | awk '{print $1}')
else
  echo "Neither sha256sum nor shasum found; cannot verify checksum" >&2
  exit 1
fi
[ "$EXPECTED" = "$ACTUAL" ] || {
  echo "checksum mismatch: expected $EXPECTED, got $ACTUAL" >&2
  exit 1
}

tar -xzf "$TMP/$ARCHIVE" -C "$TMP"

mkdir -p "$INSTALL_DIR"
install -m 0755 "$TMP/magus" "$INSTALL_DIR/magus"

echo "magus installed: $($INSTALL_DIR/magus version)"

# ---- PATH setup ------------------------------------------------------------
# Only when installing to the default $HOME/.magus/bin; if the user overrode
# MAGUS_INSTALL_DIR they presumably know what they're doing.

DEFAULT_DIR="$HOME/.magus/bin"
if [ "$INSTALL_DIR" = "$DEFAULT_DIR" ]; then
  case ":$PATH:" in
    *":$DEFAULT_DIR:"*)
      # Already on PATH for this shell session.
      ;;
    *)
      # Try to update the user's shell rc so future shells have it.
      SHELL_NAME=$(basename "${SHELL:-}")
      RC=""
      case "$SHELL_NAME" in
        bash) RC="$HOME/.bashrc"; [ -f "$HOME/.bash_profile" ] && RC="$HOME/.bash_profile" ;;
        zsh)  RC="$HOME/.zshrc" ;;
        fish) RC="$HOME/.config/fish/config.fish" ;;
      esac

      ADD_LINE='export PATH="$HOME/.magus/bin:$PATH"'
      if [ "$SHELL_NAME" = "fish" ]; then
        ADD_LINE='set -gx PATH $HOME/.magus/bin $PATH'
      fi

      if [ -n "$RC" ] && [ -f "$RC" ] && ! grep -qsE '^[^#]*PATH=.*\.magus/bin' "$RC"; then
        printf '\n# Added by magus installer\n%s\n' "$ADD_LINE" >> "$RC"
        echo "Added $DEFAULT_DIR to PATH in $RC"
        echo "Run 'exec \$SHELL' or open a new terminal, then 'magus login'"
      else
        cat <<EOF

magus was installed to $DEFAULT_DIR but that directory is not on your PATH.

Add this line to your shell config and reopen your terminal:

  $ADD_LINE

Then run 'magus login' to authorize this machine.
EOF
      fi
      ;;
  esac
fi
