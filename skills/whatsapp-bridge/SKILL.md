---
name: whatsapp-bridge
description: Ensures the whatsapp-mcp daemon is running before using WhatsApp MCP tools. Use when WhatsApp MCP tools fail to connect or the user asks to start/check the WhatsApp bridge.
---

# WhatsApp MCP daemon

The `whatsapp-mcp` binary self-manages: the `stdio` proxy (spawned by MCP
clients) auto-starts the daemon. You rarely need this skill; it exists for
diagnostics.

- Check: `scripts/status.sh` — shows daemon state, WhatsApp connection, and a
  QR code when re-authentication is pending.
- Start explicitly: `scripts/start.sh`.
- Stop: `scripts/stop.sh`.
- Logs: `~/.whatsapp-mcp/logs/daemon.log`.
- Re-auth: run `scripts/status.sh` and have the user scan the QR, or call the
  `auth_status` MCP tool which returns the QR inline.
