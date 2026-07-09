---
name: whatsapp
description: Use before WhatsApp MCP tools when they fail to connect, or when the user asks to check/start/stop WhatsApp. Handles daemon lifecycle and QR re-authentication.
---

# WhatsApp MCP

The `whatsapp-mcp` binary runs as a background daemon. The `stdio` proxy
(auto-started by MCP clients) manages the daemon lifecycle — you usually
don't need this skill unless something is broken.

## When tools fail or return connection errors

1. Check daemon status:

   ```bash
   scripts/status.sh
   ```

2. If not running, start it:

   ```bash
   scripts/start.sh
   ```

3. If `auth status: waiting_qr`, copy the QR block into your reply and tell
   the user to scan it (Settings → Linked Devices → Link a Device). Poll
   `status.sh` every ~10s until connected.

4. If `auth status: timed_out`, the daemon will restart automatically with a
   fresh QR — repeat step 3.

## Manual control

- **Start:** `scripts/start.sh`
- **Stop:** `scripts/stop.sh`
- **Logs:** `~/.whatsapp-mcp/logs/daemon.log`
- **Data:** `~/.whatsapp-mcp/` (session, messages, media)

## Re-authentication

When the WhatsApp session expires (~20 days), call `auth_status` — it returns
the QR inline. Or run `scripts/status.sh` and have the user scan the QR from
the terminal output.
