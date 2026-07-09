#!/usr/bin/env bash
# Reports the current state of the bridge, the monitor, and the auth/QR status.
# If a QR code is waiting to be scanned, it is printed between
# "QR CODE START" / "QR CODE END" markers so it can be relayed verbatim.
set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib.sh"

if bridge_session_alive; then
  echo "tmux session '$TMUX_SESSION': alive"
else
  echo "tmux session '$TMUX_SESSION': stopped"
fi

if port_up; then
  echo "port $BRIDGE_PORT: responding"
else
  echo "port $BRIDGE_PORT: not responding"
fi

if monitor_alive; then
  echo "monitor: running (pid $(cat "$MONITOR_PID_FILE"))"
else
  echo "monitor: stopped"
fi

detect_auth_status
echo "auth status: $AUTH_STATUS"

if [ "$AUTH_STATUS" = "waiting_for_qr" ]; then
  echo ""
  echo "=== QR CODE START (scan with the WhatsApp app, valid ~3 minutes) ==="
  echo "$QR_BLOCK"
  echo "=== QR CODE END ==="
fi

echo ""
echo "last lines of the bridge log ($LOG_FILE):"
tail -n 15 "$LOG_FILE" 2>/dev/null || echo "(no log yet)"
