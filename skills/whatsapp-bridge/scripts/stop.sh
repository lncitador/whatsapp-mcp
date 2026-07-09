#!/usr/bin/env bash
# Stops the monitor and kills the bridge's tmux session.
set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib.sh"

if monitor_alive; then
  kill "$(cat "$MONITOR_PID_FILE")" 2>/dev/null
  rm -f "$MONITOR_PID_FILE"
  echo "[whatsapp-bridge] monitor stopped."
else
  echo "[whatsapp-bridge] monitor was already stopped."
fi

if bridge_session_alive; then
  tmux kill-session -t "$TMUX_SESSION"
  echo "[whatsapp-bridge] tmux session '$TMUX_SESSION' killed."
else
  echo "[whatsapp-bridge] tmux session was already stopped."
fi
