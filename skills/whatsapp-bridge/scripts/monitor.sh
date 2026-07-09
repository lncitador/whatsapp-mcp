#!/usr/bin/env bash
# Watchdog: keeps an eye on the bridge's tmux session and recreates it if it
# dies (e.g. after the WhatsApp session expires or the 3-minute QR timeout).
# Not meant to be run by hand — start.sh launches it in the background.
set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib.sh"

echo "[monitor] started at $(date '+%Y-%m-%d %H:%M:%S'), checking every ${CHECK_INTERVAL}s"

while true; do
  if ! bridge_session_alive; then
    echo "[monitor] $(date '+%Y-%m-%d %H:%M:%S') tmux session '$TMUX_SESSION' is down, recreating..."
    if have_tmux; then
      start_bridge_session
    fi
  elif ! port_up; then
    detect_auth_status
    if [ "$AUTH_STATUS" = "waiting_for_qr" ]; then
      echo "[monitor] $(date '+%Y-%m-%d %H:%M:%S') tmux session alive, waiting on QR scan."
    else
      echo "[monitor] $(date '+%Y-%m-%d %H:%M:%S') tmux session alive but port $BRIDGE_PORT is not responding (auth status: $AUTH_STATUS)."
    fi
  fi
  sleep "$CHECK_INTERVAL"
done
