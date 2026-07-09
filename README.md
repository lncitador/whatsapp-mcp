# WhatsApp MCP Server

Search and send WhatsApp messages (text, media, voice notes) from Claude or
any MCP client. Single binary — no Go, no Python required.

![example](./example-use.png)

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/lncitador/whatsapp-mcp/main/install.sh | sh
```

Then register the MCP server:

```sh
claude mcp add whatsapp -- whatsapp-mcp stdio
```

Other clients: configure a stdio server running `whatsapp-mcp stdio`.

First use: call the `auth_status` tool (or run `whatsapp-mcp status`) and scan
the QR code with WhatsApp (Settings → Linked Devices). The daemon keeps your
session in `~/.whatsapp-mcp/` and stores messages locally in SQLite; nothing
leaves your machine except through the tools you call.

Voice notes: converting non-.ogg audio requires `ffmpeg` on your PATH.

## Configuration

Environment variables:

- `WHATSAPP_MCP_DIR` — Base directory (default: `~/.whatsapp-mcp`)
- `WHATSAPP_MCP_PORT` — HTTP port (default: `8080`)
- `WHATSAPP_MEDIA_ROOTS` — Allowed media directories for `send_file` (colon-separated). When set, `send_file` rejects paths outside these roots. Prevents CWE-22 path traversal.

## How it works

`whatsapp-mcp stdio` (spawned by your MCP client) auto-starts a background
daemon (`whatsapp-mcp serve`) that holds the WhatsApp connection (via
[whatsmeow](https://github.com/tulir/whatsmeow)) and a local-only HTTP API on
127.0.0.1:8080. The daemon outlives your MCP client, so messages keep being
received. `whatsapp-mcp status` / `stop` manage it.

Windows: download the binary from
[Releases](https://github.com/lncitador/whatsapp-mcp/releases), put it on
your PATH, and register `whatsapp-mcp stdio` in your MCP client.

## Tools

search_contacts, list_messages, list_chats, get_chat,
get_direct_chat_by_contact, get_contact_chats, get_last_interaction,
get_message_context, send_message, send_file, send_audio_message,
download_media, create_group, leave_group, auth_status.

### New Features (vs upstream)

- **Reply support**: `send_message` accepts `reply_to_message_id` and `reply_to_sender_jid` to quote-reply to specific messages
- **Group management**: `create_group` and `leave_group` tools for WhatsApp group operations
- **Sender names**: `list_messages` and `list_chats` include resolved `sender_name` / `last_sender_name` fields
- **LID normalization**: Automatic resolution of WhatsApp LID addresses to phone numbers
- **FTS5 search**: Full-text search with ~500x speedup on selective queries
- **Security**: Path traversal protection (`WHATSAPP_MEDIA_ROOTS`), pagination caps
- **Performance**: SQLite indexes on hot paths

## Development

```sh
go build ./cmd/whatsapp-mcp
go test ./...
```

Upgrading from the old two-process setup (Go bridge + Python server): the
daemon migrates `whatsapp-bridge/store/` into `~/.whatsapp-mcp/store/` on
first run when started from the repo root — your session and history carry
over, no QR re-scan.
