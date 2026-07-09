#!/usr/bin/env bash
# Shared config and helpers for the whatsapp-bridge skill scripts.
# Sourced by start.sh, stop.sh, monitor.sh and status.sh — do not run directly.

SKILL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Optional machine-specific override. Only needed when this skill is
# installed at global scope (e.g. ~/.claude/skills/whatsapp-bridge), where it
# no longer lives next to the whatsapp-mcp checkout. See config.sh.example.
if [ -f "$SKILL_DIR/config.sh" ]; then
  # shellcheck disable=SC1091
  source "$SKILL_DIR/config.sh"
fi

# Auto-detect the whatsapp-mcp repo by walking up from this skill's own
# directory. Works out of the box for project-scope installs, since the
# skill then lives inside (a copy or symlink within) the same repo checkout,
# e.g.:
#   <repo>/skills/whatsapp-bridge                  (source, 2 levels up)
#   <repo>/.claude/skills/whatsapp-bridge           (installed copy/symlink, 3 levels up)
if [ -z "${WHATSAPP_MCP_REPO:-}" ]; then
  dir="$SKILL_DIR"
  for _ in 1 2 3 4 5; do
    dir="$(dirname "$dir")"
    if [ -f "$dir/whatsapp-bridge/main.go" ]; then
      WHATSAPP_MCP_REPO="$dir"
      break
    fi
  done
fi

if [ -z "${WHATSAPP_MCP_REPO:-}" ]; then
  echo "[whatsapp-bridge] could not locate the whatsapp-mcp checkout." >&2
  echo "  This usually means the skill was installed at global scope, so it" >&2
  echo "  no longer lives next to the repo. Fix it by copying config.sh.example" >&2
  echo "  to config.sh next to this file and setting WHATSAPP_MCP_REPO:" >&2
  echo "    cp \"$SKILL_DIR/config.sh.example\" \"$SKILL_DIR/config.sh\"" >&2
  echo "    \$EDITOR \"$SKILL_DIR/config.sh\"" >&2
  exit 1
fi

BRIDGE_DIR="${BRIDGE_DIR:-$WHATSAPP_MCP_REPO/whatsapp-bridge}"

TMUX_SESSION="whatsapp-bridge"
BRIDGE_PORT=8080

STATE_DIR="$SKILL_DIR/.state"
LOG_FILE="$STATE_DIR/bridge.log"
MONITOR_LOG_FILE="$STATE_DIR/monitor.log"
MONITOR_PID_FILE="$STATE_DIR/monitor.pid"

# seconds between monitor health checks
CHECK_INTERVAL="${WHATSAPP_BRIDGE_CHECK_INTERVAL:-15}"

mkdir -p "$STATE_DIR"

have_tmux() {
  command -v tmux >/dev/null 2>&1
}

# returns 0 if something is listening on localhost:$BRIDGE_PORT
port_up() {
  if command -v nc >/dev/null 2>&1; then
    nc -z -w 2 localhost "$BRIDGE_PORT" >/dev/null 2>&1
    return $?
  fi
  # fallback without netcat, using bash's /dev/tcp
  (exec 3<>"/dev/tcp/localhost/$BRIDGE_PORT") >/dev/null 2>&1
  local status=$?
  exec 3<&- 3>&- 2>/dev/null || true
  return $status
}

bridge_session_alive() {
  have_tmux && tmux has-session -t "$TMUX_SESSION" 2>/dev/null
}

monitor_alive() {
  [ -f "$MONITOR_PID_FILE" ] && kill -0 "$(cat "$MONITOR_PID_FILE")" 2>/dev/null
}

start_bridge_session() {
  tmux new-session -d -s "$TMUX_SESSION" -c "$BRIDGE_DIR" \
    "go run main.go 2>&1 | tee -a '$LOG_FILE'"
}

# Inspects $LOG_FILE and sets two globals:
#   AUTH_STATUS  one of: connected | waiting_for_qr | timed_out | unknown
#   QR_BLOCK     the ASCII-art QR block to show the user (only when waiting_for_qr)
detect_auth_status() {
  AUTH_STATUS="unknown"
  QR_BLOCK=""

  [ -f "$LOG_FILE" ] || return 0

  local qr_line
  qr_line="$(grep -n "Scan this QR code" "$LOG_FILE" | tail -n 1 | cut -d: -f1 || true)"

  if [ -n "${qr_line:-}" ]; then
    local after=$((qr_line + 1))
    local rest
    rest="$(tail -n +"$after" "$LOG_FILE")"

    if echo "$rest" | grep -q "Successfully connected and authenticated\|Connected to WhatsApp"; then
      AUTH_STATUS="connected"
    elif echo "$rest" | grep -q "Timeout waiting for QR code scan"; then
      AUTH_STATUS="timed_out"
    else
      AUTH_STATUS="waiting_for_qr"
      QR_BLOCK="$(echo "$rest" | sed -n '1,60p')"
    fi
  elif grep -q "Connected to WhatsApp\|Successfully connected and authenticated" "$LOG_FILE" 2>/dev/null; then
    AUTH_STATUS="connected"
  fi
}
