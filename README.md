# WhatsApp MCP Server

Search and send WhatsApp messages (text, media, voice notes) from Claude or
any MCP client. Single binary — no Go, no Python required.

![example](./example-use.png)

## Install

**macOS / Linux:**

```sh
curl -fsSL https://raw.githubusercontent.com/lncitador/whatsapp-mcp/main/install.sh | sh
```

**Windows (PowerShell):**

```powershell
iex (iwr -Uri https://raw.githubusercontent.com/lncitador/whatsapp-mcp/main/install.ps1).Content
```

Then register the MCP server:

```sh
claude mcp add whatsapp -- whatsapp-mcp stdio
```

Other clients: configure a stdio server running `whatsapp-mcp stdio`.

> **Note on PromptScript**: PromptScript does not support global skill
> installation. The installer will skip it automatically. All other agents
> (Claude Code, Codex, GitHub Copilot, Junie, OpenCode, Qwen Code, Warp, Zed,
> etc.) are supported.

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

## Security

This MCP server implements several mitigations against the
[lethal trifecta](https://simonwillison.net/2025/Jun/16/the-lethal-trifecta/)
(private data + untrusted content + external communication):

- **Content sanitization**: Message content is stripped of zero-width and
  bidirectional control characters before being returned to the LLM.
- **Path restriction**: `send_file` and `send_audio_message` can only access
  files under `~/.whatsapp-mcp/`. Path traversal attempts are blocked.
- **Rate limiting**: Each tool is limited to 10 requests per minute.
- **Human approval gate**: `send_message`, `send_file`, and
  `send_audio_message` return `pending_approval` with a `request_id`; nothing
  is sent until a second explicit step — the `approve_send` MCP tool or
  `POST /api/approve/{id}` — confirms it.
- **Audit logging**: All tool invocations are logged to
  `~/.whatsapp-mcp/logs/audit.log`.
- **Query limits**: `list_messages` is capped at 100 results per request.

**Remaining risks you should be aware of:**
- Message content from unknown contacts could still contain prompt injection
  payloads. The LLM client must treat all message content as untrusted data.
- The approval gate is enforced server-side, but the MCP client (Claude) must
  be configured to present the approval request to the user rather than
  auto-approving.

## Development

```sh
go build ./cmd/whatsapp-mcp
go test ./...
```

Upgrading from the old two-process setup (Go bridge + Python server): the
daemon migrates `whatsapp-bridge/store/` into `~/.whatsapp-mcp/store/` on
first run when started from the repo root — your session and history carry
over, no QR re-scan.
