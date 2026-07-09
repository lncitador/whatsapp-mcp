#!/usr/bin/env bash
# Starts the WhatsApp bridge (tmux) and the monitor, if not already running.
# Idempotent: safe to run as many times as you want.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib.sh"

if ! have_tmux; then
  echo "[whatsapp-bridge] tmux not found. Install it with: brew install tmux" >&2
  exit 1
fi

if [ ! -d "$BRIDGE_DIR" ]; then
  echo "[whatsapp-bridge] BRIDGE_DIR does not exist: $BRIDGE_DIR (check config.sh)" >&2
  exit 1
fi

if bridge_session_alive; then
  echo "[whatsapp-bridge] tmux session '$TMUX_SESSION' is already running."
else
  echo "[whatsapp-bridge] starting bridge in tmux ('$TMUX_SESSION')..."
  start_bridge_session
  sleep 1
fi

if monitor_alive; then
  echo "[whatsapp-bridge] monitor is already running (pid $(cat "$MONITOR_PID_FILE"))."
else
  echo "[whatsapp-bridge] starting monitor..."
  nohup "$SCRIPT_DIR/monitor.sh" >>"$MONITOR_LOG_FILE" 2>&1 &
  disown
  echo $! > "$MONITOR_PID_FILE"
fi

echo -n "[whatsapp-bridge] waiting for port $BRIDGE_PORT"
for _ in $(seq 1 20); do
  if port_up; then
    echo " -> up"
    exit 0
  fi
  echo -n "."
  sleep 1
done
echo ""

detect_auth_status
if [ "$AUTH_STATUS" = "waiting_for_qr" ]; then
  echo "[whatsapp-bridge] a QR code is waiting to be scanned."
  echo "  Run status.sh to fetch it, or read it directly from: $LOG_FILE"
else
  echo "[whatsapp-bridge] port did not come up yet (auth status: $AUTH_STATUS)."
  echo "  Check the log: $LOG_FILE"
fi
