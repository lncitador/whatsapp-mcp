---
name: whatsapp-bridge
description: >
  Ensures the WhatsApp bridge (Go process in whatsapp-bridge/, port 8080) is
  running before using any WhatsApp MCP tool. Use this skill ALWAYS before
  calling mcp__whatsapp__list_chats, list_messages, get_chat,
  get_direct_chat_by_contact, get_contact_chats, get_last_interaction,
  get_message_context, search_contacts, send_message, send_file,
  send_audio_message or download_media — or when the user asks to "start the
  whatsapp bridge", "bring whatsapp up", or reports that the whatsapp MCP is
  not responding / throwing connection errors.
---

# WhatsApp Bridge

The WhatsApp MCP (`whatsapp-mcp-server`, Python) depends on a separate Go
process — the **bridge** (`whatsapp-bridge/main.go`) — that stays connected
to WhatsApp Web and exposes a local API on `http://localhost:8080`. If the
bridge isn't running, the `mcp__whatsapp__*` tools fail or return stale data.

This skill runs the bridge in a dedicated `tmux` session plus a monitor
process that restarts it automatically if it dies, so you don't have to
provision it by hand every time.

All commands below are relative to this SKILL.md's own directory (i.e. the
`scripts/` folder that ships next to it, wherever this skill ended up
installed).

## Before calling any mcp__whatsapp__* tool

1. Check status:

   ```bash
   scripts/status.sh
   ```

2. If the tmux session is stopped or port 8080 isn't responding, bring it up:

   ```bash
   scripts/start.sh
   ```

   This is idempotent — safe to run every time, it won't duplicate processes.
   `start.sh` brings up (as needed):
   - the `whatsapp-bridge` tmux session running `go run main.go` inside the
     repo's `whatsapp-bridge/` folder;
   - `monitor.sh` in the background, which watches the tmux session and port
     8080, and restarts the bridge on its own if it goes down.

3. Only then call the WhatsApp MCP tool the user actually asked for.

## Handling the QR code — do this yourself, don't just point the user at tmux

On first use (or once the WhatsApp session expires, roughly every ~20 days),
the bridge needs a QR code scanned within its own **3-minute timeout**, or
the process exits and the monitor will restart it with a fresh QR. You are
expected to actively watch for this and drive the user through it — do not
just tell them to run `tmux attach` and figure it out on their own.

1. After running `start.sh`, run `status.sh`. Its output includes an
   `auth status:` line, one of: `connected`, `waiting_for_qr`, `timed_out`,
   `unknown`.
2. If `auth status: waiting_for_qr`, the output also contains the QR code
   between `=== QR CODE START ===` and `=== QR CODE END ===` markers. Copy
   that block verbatim into a monospace code block in your reply and tell the
   user to scan it with WhatsApp on their phone (Settings → Linked Devices →
   Link a Device), within about 3 minutes.
3. Poll `status.sh` again every ~10-15 seconds (a few tool calls) until
   `auth status: connected` (or port 8080 starts responding). Let the user
   know you're waiting; don't go silent.
4. If `auth status: timed_out`, the bridge process exited; the monitor will
   relaunch it within `CHECK_INTERVAL` seconds (default 15s) with a new QR —
   run `status.sh` again shortly and repeat step 2 with the fresh code.
5. Once connected, proceed with the WhatsApp tool call the user originally
   wanted.

## Stopping the bridge

```bash
scripts/stop.sh
```

Kills the tmux session and the monitor. Normally not needed — the idea is to
leave it running.

## Installing / updating this skill

This skill follows the [Agent Skills](https://agentskills.io) layout used by
[vercel-labs/skills](https://github.com/vercel-labs/skills), so it installs
with the `skills` CLI instead of a custom script — the user picks the agent
(Claude Code, Codex, Cursor, ...), the scope (project or global), and
symlink vs. copy interactively:

```bash
# from the whatsapp-mcp repo root, interactive prompts for agent/scope/method
npx skills add .

# non-interactive examples
npx skills add . -a claude-code            # project scope, symlinked into .claude/skills/
npx skills add . -a claude-code -g         # global scope, symlinked into ~/.claude/skills/
npx skills add . --skill whatsapp-bridge -a claude-code -y
```

Project-scope installs auto-detect the whatsapp-mcp checkout path (no
config needed) since the skill still lives inside the same repo. Global-scope
installs need a one-time `config.sh` — see `config.sh.example` in this
folder.

## Troubleshooting

- **`tmux: command not found`**: `brew install tmux`.
- **`could not locate the whatsapp-mcp checkout`**: this only happens for
  global-scope installs. Copy `config.sh.example` to `config.sh` next to it
  and set `WHATSAPP_MCP_REPO`.
- **Port 8080 already in use by something else**: `lsof -i :8080` to find the
  culprit; the bridge won't start if another process holds the port.
- **Bridge log**: `.state/bridge.log` inside the skill folder.
- **Monitor log**: `.state/monitor.log` inside the skill folder.
