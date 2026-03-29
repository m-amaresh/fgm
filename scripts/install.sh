#!/bin/sh
# FGM – Fast Go Manager installer
# Usage: curl -fsSL https://raw.githubusercontent.com/m-amaresh/fgm/main/scripts/install.sh | bash
set -e

REPO="m-amaresh/fgm"
INSTALL_DIR="${HOME}/.local/bin"
FGM_DIR="${HOME}/.fgm"

# ── helpers ──────────────────────────────────────────────────────────
info()  { printf '\033[1;34m=>\033[0m %s\n' "$*"; }
ok()    { printf '\033[1;32m✓\033[0m %s\n' "$*"; }
err()   { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    err "Required command '$1' not found. Please install it and try again."
  fi
}

detect_os() {
  case "$(uname -s)" in
    Linux*)  echo "linux";;
    Darwin*) echo "darwin";;
    *)       err "Unsupported OS: $(uname -s)";;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)   echo "amd64";;
    aarch64|arm64)   echo "arm64";;
    *)               err "Unsupported architecture: $(uname -m)";;
  esac
}

# Determine which HTTP client to use (set once, reused everywhere).
HTTP_CLIENT=""
detect_http_client() {
  if command -v curl >/dev/null 2>&1; then
    HTTP_CLIENT="curl"
  elif command -v wget >/dev/null 2>&1; then
    HTTP_CLIENT="wget"
  else
    err "Neither curl nor wget found. Please install one of them."
  fi
}

latest_version() {
  _lv_url="https://api.github.com/repos/${REPO}/releases/latest"
  _lv_body=""
  if [ "$HTTP_CLIENT" = "curl" ]; then
    _lv_body=$(curl -fsSL "$_lv_url") || err "Failed to fetch latest release info from GitHub."
  else
    _lv_body=$(wget -qO- "$_lv_url") || err "Failed to fetch latest release info from GitHub."
  fi
  echo "$_lv_body" | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"//;s/".*//'
}

download() {
  url="$1"; dest="$2"
  if [ "$HTTP_CLIENT" = "curl" ]; then
    curl -fSL --progress-bar -o "$dest" "$url" || err "Download failed: $url"
  else
    wget --show-progress -qO "$dest" "$url" || err "Download failed: $url"
  fi
  # Verify the file is non-empty.
  if [ ! -s "$dest" ]; then
    err "Downloaded file is empty: $dest"
  fi
}

# Determine available SHA-256 command (sha256sum on Linux, shasum on macOS).
SHA_CMD=""
detect_sha_cmd() {
  if command -v sha256sum >/dev/null 2>&1; then
    SHA_CMD="sha256sum"
  elif command -v shasum >/dev/null 2>&1; then
    SHA_CMD="shasum -a 256"
  else
    err "Neither sha256sum nor shasum found. Cannot verify checksum."
  fi
}

# ── dependency checks ────────────────────────────────────────────────
need_cmd uname
need_cmd tar
need_cmd mktemp
need_cmd grep
need_cmd sed
detect_http_client
detect_sha_cmd

# ── main ─────────────────────────────────────────────────────────────
# Warn if a system Go installation exists that could cause confusion.
if [ -d "/usr/local/go" ]; then
  printf '\033[1;33mwarning:\033[0m A Go installation was found at /usr/local/go.\n'
  printf '         fgm will take priority (its bin is prepended to PATH),\n'
  printf '         but you may want to remove it to avoid confusion:\n'
  printf '           sudo rm -rf /usr/local/go\n\n'
fi
OS="$(detect_os)"
ARCH="$(detect_arch)"

if [ "$OS" = "darwin" ] && [ "$ARCH" = "amd64" ]; then
  err "macOS on Intel is not supported. fgm supports Apple Silicon (darwin/arm64) only."
fi

VERSION="$(latest_version)"

if [ -z "$VERSION" ]; then
  err "Could not determine latest FGM release version."
fi

info "Installing FGM ${VERSION} (${OS}/${ARCH})..."

ARCHIVE="fgm_${VERSION}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE}"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

CHECKSUMS_URL="https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt"

download "$URL" "${TMP}/${ARCHIVE}"
download "$CHECKSUMS_URL" "${TMP}/checksums.txt"

info "Verifying checksum..."
# Validate checksums.txt is non-empty and looks like sha256sum output.
if [ ! -s "${TMP}/checksums.txt" ]; then
  err "checksums.txt is empty or missing"
fi
if ! grep -qE '^[0-9a-f]{64}  ' "${TMP}/checksums.txt"; then
  err "checksums.txt has unexpected format — possible tampering"
fi
# Strict match: exact filename anchored to end of line.
EXPECTED=$(grep -E "^[0-9a-f]{64}  ${ARCHIVE}$" "${TMP}/checksums.txt" || true)
if [ -z "$EXPECTED" ]; then
  err "Checksum not found for ${ARCHIVE} in checksums.txt"
fi
# Verify exactly one match to prevent ambiguity.
MATCH_COUNT=$(echo "$EXPECTED" | wc -l)
if [ "$MATCH_COUNT" -ne 1 ]; then
  err "Multiple checksum entries found for ${ARCHIVE} — possible tampering"
fi
echo "$EXPECTED" | (cd "$TMP" && $SHA_CMD -c -) || err "Checksum verification failed for ${ARCHIVE}"

tar -xzf "${TMP}/${ARCHIVE}" -C "$TMP"

mkdir -p "$INSTALL_DIR"
mv "${TMP}/fgm" "${INSTALL_DIR}/fgm"
chmod +x "${INSTALL_DIR}/fgm"

# Create FGM home + bin dir for shims
mkdir -p "${FGM_DIR}/bin"

# ── PATH setup ───────────────────────────────────────────────────────
MARKER="# fgm"
FGM_PATH_LINE="export PATH=\"\${HOME}/.local/bin:\${HOME}/.fgm/bin:\${HOME}/go/bin:\$PATH\" ${MARKER}"

add_to_profile() {
  file="$1"
  if [ ! -f "$file" ]; then
    mkdir -p "$(dirname "$file")"
    : > "$file"
  fi
  if grep -qF "$MARKER" "$file" 2>/dev/null; then
    return 0
  fi
  printf '\n%s\n' "$FGM_PATH_LINE" >> "$file"
  info "Updated $file"
}

SHELL_NAME="$(basename "${SHELL:-/bin/sh}")"

case "$SHELL_NAME" in
  bash)
    add_to_profile "$HOME/.bashrc"
    add_to_profile "$HOME/.bash_profile"
    ;;
  zsh)
    add_to_profile "$HOME/.zshrc"
    ;;
  fish)
    FISH_CONF="${HOME}/.config/fish/config.fish"
    if [ -f "$FISH_CONF" ] && ! grep -qF "fgm" "$FISH_CONF" 2>/dev/null; then
      printf '\nfish_add_path %s/.local/bin %s/.fgm/bin %s/go/bin # fgm\n' "$HOME" "$HOME" "$HOME" >> "$FISH_CONF"
      info "Updated $FISH_CONF"
    fi
    ;;
  *)
    add_to_profile "$HOME/.profile"
    ;;
esac

ok "FGM ${VERSION} installed to ${INSTALL_DIR}/fgm"
echo ""
echo "  To get started, restart your terminal or run:"
echo ""
case "$SHELL_NAME" in
  bash) echo "    source ~/.bashrc";;
  zsh)  echo "    source ~/.zshrc";;
  fish) echo "    source ~/.config/fish/config.fish";;
  *)    echo "    source ~/.profile";;
esac
echo ""
echo "  Then install Go:"
echo ""
echo "    fgm install latest"
echo ""
