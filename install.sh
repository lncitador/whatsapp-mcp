#!/bin/sh
# whatsapp-mcp installer: downloads the latest release binary for this
# platform into ~/.local/bin (or /usr/local/bin with sudo).
set -eu

REPO="lncitador/whatsapp-mcp"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac
case "$os" in
  darwin | linux) ;;
  *) echo "unsupported OS: $os (Windows: download from https://github.com/$REPO/releases)" >&2; exit 1 ;;
esac

tag=$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest" | awk -F/ '{print $NF}')
[ -n "$tag" ] || { echo "could not resolve latest release" >&2; exit 1; }
version=${tag#v}

url="https://github.com/$REPO/releases/download/$tag/whatsapp-mcp_${version}_${os}_${arch}.tar.gz"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "downloading whatsapp-mcp $tag ($os/$arch)..."
curl -fsSL "$url" -o "$tmp/wmcp.tar.gz"
tar -xzf "$tmp/wmcp.tar.gz" -C "$tmp"

dest="$HOME/.local/bin"
if [ ! -d "$dest" ] || [ ! -w "$dest" ]; then
  mkdir -p "$dest" 2>/dev/null || dest="/usr/local/bin"
fi
if [ -w "$dest" ]; then
  install -m 0755 "$tmp/whatsapp-mcp" "$dest/whatsapp-mcp"
else
  echo "need sudo to install into $dest"
  sudo install -m 0755 "$tmp/whatsapp-mcp" "$dest/whatsapp-mcp"
fi

echo "installed: $dest/whatsapp-mcp ($("$dest/whatsapp-mcp" --version))"
case ":$PATH:" in
  *":$dest:"*) ;;
  *) echo "NOTE: add $dest to your PATH" ;;
esac

# Install the agent skill globally via npx skills
# PromptScript does not support global skill installation — this is expected.
if command -v npx >/dev/null 2>&1; then
  echo "installing agent skill..."
  npx -y skills@latest add "https://github.com/$REPO" --skill whatsapp --global -y >/dev/null 2>&1 || true
fi

echo "next steps:"
echo "  claude mcp add whatsapp -- whatsapp-mcp stdio"
echo "  # then call the auth_status tool (or run: whatsapp-mcp status) and scan the QR"
