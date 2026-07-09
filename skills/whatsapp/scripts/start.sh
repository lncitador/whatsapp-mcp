#!/usr/bin/env bash
# Starts the whatsapp-mcp daemon detached and exits once it is healthy.
set -euo pipefail
if ! command -v whatsapp-mcp >/dev/null 2>&1; then
  echo "whatsapp-mcp not installed. Run: curl -fsSL https://raw.githubusercontent.com/lncitador/whatsapp-mcp/main/install.sh | sh" >&2
  exit 1
fi
whatsapp-mcp start
exec whatsapp-mcp status
