#!/usr/bin/env bash
# Ensures the whatsapp-mcp daemon is up. The binary self-daemonizes.
set -euo pipefail
if ! command -v whatsapp-mcp >/dev/null 2>&1; then
  echo "whatsapp-mcp not installed. Run: curl -fsSL https://raw.githubusercontent.com/lncitador/whatsapp-mcp/main/install.sh | sh" >&2
  exit 1
fi
whatsapp-mcp status | grep -q "daemon: running" || nohup whatsapp-mcp serve >/dev/null 2>&1 &
sleep 2
whatsapp-mcp status
