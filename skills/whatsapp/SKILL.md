---
name: whatsapp
description: Ensures the whatsapp-mcp daemon is running before using WhatsApp MCP tools. Use when the user asks to start/check WhatsApp, or when tools fail to connect.
---

# WhatsApp MCP

The `whatsapp-mcp` binary self-manages: the `stdio` proxy (spawned by MCP
clients) auto-starts the daemon. You rarely need this skill; it exists for
diagnostics.

## Available tools

- `search_contacts` — search contacts by name or phone
- `list_messages` — get messages with filters and context
- `list_chats` — list chats
- `get_chat` — get chat metadata by JID
- `get_direct_chat_by_contact` — get direct chat by phone number
- `get_contact_chats` — all chats involving a contact
- `get_last_interaction` — most recent message with a contact
- `get_message_context` — messages around a specific message
- `send_message` — send a text message
- `send_file` — send a file (image, video, document)
- `send_audio_message` — send a voice note (non-.ogg converted via ffmpeg)
- `download_media` — download media from a message
- `auth_status` — check session state, shows QR when re-auth needed

## Diagnostics

- Check status: `scripts/status.sh`
- Start explicitly: `scripts/start.sh`
- Stop: `scripts/stop.sh`
- Logs: `~/.whatsapp-mcp/logs/daemon.log`
- Re-auth: run `scripts/status.sh` and have the user scan the QR, or call
  `auth_status` which returns the QR inline.
