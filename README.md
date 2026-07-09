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
download_media, auth_status.

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
  `send_audio_message` require explicit approval via `/api/approve/{id}`
  before messages are actually sent.
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
