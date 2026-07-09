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

## Development

```sh
go build ./cmd/whatsapp-mcp
go test ./...
```

Upgrading from the old two-process setup (Go bridge + Python server): the
daemon migrates `whatsapp-bridge/store/` into `~/.whatsapp-mcp/store/` on
first run when started from the repo root — your session and history carry
over, no QR re-scan.
