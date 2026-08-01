#!/usr/bin/env bash
# Tails new WhatsApp messages from the daemon's SSE stream (GET /api/events).
# Prints one line per message on stdout; diagnostics go to stderr. Requires
# only curl — jq is used for pretty formatting when present.
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
Usage: watch.sh [--chat <jid>] [--json] [--timeout <seconds>] [--since-count <n>]
                [--from-me <true|false>] [--no-media]

  --chat <jid>        Only messages from this chat JID (e.g. 5511999999999@s.whatsapp.net)
  --json              Emit the raw event JSON, one object per line (JSONL)
  --timeout <secs>    Stop listening after N seconds and exit 0
  --since-count <n>   Print the last N messages (via the list_messages RPC) before listening
  --from-me <bool>    true: only outgoing; false: only incoming (default: both)
  --no-media          Skip messages that carry media

Default output (pipe-separated, one message per line):
  <rfc3339> | in|out | <chat name> <<chat jid>> | <sender>: <text> | id=<message id>
EOF
}

CHAT=""
JSON=0
TIMEOUT=""
SINCE_COUNT=""
FROM_ME=""
NO_MEDIA=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --chat)        CHAT="${2:-}"; shift 2 ;;
    --json)        JSON=1; shift ;;
    --timeout)     TIMEOUT="${2:-}"; shift 2 ;;
    --since-count) SINCE_COUNT="${2:-}"; shift 2 ;;
    --from-me)     FROM_ME="${2:-}"; shift 2 ;;
    --no-media)    NO_MEDIA=1; shift ;;
    -h|--help)     usage; exit 0 ;;
    *) echo "watch.sh: unknown argument: $1" >&2; usage; exit 2 ;;
  esac
done

if [[ -n "$TIMEOUT" && ! "$TIMEOUT" =~ ^[0-9]+$ ]]; then
  echo "watch.sh: --timeout must be a whole number of seconds" >&2
  exit 2
fi
if [[ -n "$SINCE_COUNT" && ! "$SINCE_COUNT" =~ ^[0-9]+$ ]]; then
  echo "watch.sh: --since-count must be a whole number" >&2
  exit 2
fi

command -v curl >/dev/null 2>&1 || { echo "watch.sh: curl is required" >&2; exit 1; }
HAVE_JQ=0
if command -v jq >/dev/null 2>&1; then HAVE_JQ=1; fi

# Port precedence mirrors internal/config: WHATSAPP_MCP_PORT > config.json > 8080.
base_dir() {
  if [[ -n "${WHATSAPP_MCP_DIR:-}" ]]; then echo "$WHATSAPP_MCP_DIR"; else echo "$HOME/.whatsapp-mcp"; fi
}
resolve_port() {
  if [[ -n "${WHATSAPP_MCP_PORT:-}" ]]; then echo "$WHATSAPP_MCP_PORT"; return; fi
  local cfg="$(base_dir)/config.json" port=""
  if [[ -f "$cfg" ]]; then
    if [[ $HAVE_JQ -eq 1 ]]; then
      port="$(jq -r '.port // empty' "$cfg" 2>/dev/null || true)"
    else
      port="$(tr -d ' \n' < "$cfg" | sed -n 's/.*"port":\([0-9]*\).*/\1/p')"
    fi
  fi
  if [[ "$port" =~ ^[0-9]+$ ]] && [[ "$port" -gt 0 ]]; then echo "$port"; else echo 8080; fi
}

BASE="http://127.0.0.1:$(resolve_port)"

if ! curl -fsS --max-time 3 "$BASE/health" >/dev/null 2>&1; then
  echo "whatsapp-mcp daemon is not running at $BASE." >&2
  echo "Start it first: scripts/start.sh" >&2
  exit 1
fi

# --- optional backfill via the existing RPC (no direct SQLite access) --------
if [[ -n "$SINCE_COUNT" && "$SINCE_COUNT" -gt 0 ]]; then
  body="{\"limit\":$SINCE_COUNT,\"include_context\":false"
  if [[ -n "$CHAT" ]]; then body="$body,\"chat_jid\":\"$CHAT\""; fi
  body="$body}"
  echo "--- last $SINCE_COUNT message(s) ---" >&2
  resp="$(curl -fsS --max-time 10 -H 'Content-Type: application/json' \
    -d "$body" "$BASE/api/rpc/list_messages" || true)"
  if [[ -n "$resp" ]]; then
    if [[ $HAVE_JQ -eq 1 ]]; then jq -r '.result // .error // empty' <<<"$resp"; else echo "$resp"; fi
  fi
  echo "--- now listening ---" >&2
fi

# --- build the stream URL ---------------------------------------------------
urlencode() {
  local s="$1" out="" c i
  for (( i = 0; i < ${#s}; i++ )); do
    c="${s:i:1}"
    case "$c" in
      [a-zA-Z0-9.~_-]) out+="$c" ;;
      *) out+="$(printf '%%%02X' "'$c")" ;;
    esac
  done
  printf '%s' "$out"
}

query=""
add_param() { if [[ -z "$query" ]]; then query="?$1"; else query="$query&$1"; fi; }
if [[ -n "$CHAT" ]]; then add_param "chat_jid=$(urlencode "$CHAT")"; fi
if [[ -n "$FROM_ME" ]]; then add_param "from_me=$FROM_ME"; fi
if [[ $NO_MEDIA -eq 1 ]]; then add_param "include_media=false"; fi
URL="$BASE/api/events$query"

DEADLINE=""
if [[ -n "$TIMEOUT" ]]; then DEADLINE=$(( $(date +%s) + TIMEOUT )); fi

# jq program for the default compact line. Kept here so the no-jq path can
# simply fall back to printing the raw JSON instead of reimplementing it.
#
# oneline collapses embedded newlines: a WhatsApp message can span many lines,
# and this format promises one line per message so an agent can read it with
# `while read`. Use --json when the original line breaks matter.
JQ_LINE='
  def oneline: (. // "") | gsub("\\s+"; " ") | sub("^ +"; "") | sub(" +$"; "");
  (if .is_from_me then "out" else "in " end) as $dir
  | (if .chat_name != "" then (.chat_name | oneline) else .chat_jid end) as $chat
  | (if .is_from_me then "Me"
     elif .sender_name != "" then (.sender_name | oneline)
     else .sender end) as $who
  | (if .media_type != "" then "[" + .media_type + ": " + .filename + "] " else "" end) as $media
  | .timestamp + " | " + $dir + " | " + $chat + " <" + .chat_jid + "> | "
    + $who + ": " + $media + (.text | oneline) + " | id=" + .id
'

emit() {
  local payload="$1"
  if [[ $JSON -eq 1 ]]; then
    printf '%s\n' "$payload"
  elif [[ $HAVE_JQ -eq 1 ]]; then
    jq -r "$JQ_LINE" <<<"$payload" 2>/dev/null || printf '%s\n' "$payload"
  else
    printf '%s\n' "$payload"
  fi
}

if [[ $JSON -eq 0 && $HAVE_JQ -eq 0 ]]; then
  echo "watch.sh: jq not found — emitting raw JSON lines instead of formatted output." >&2
fi

backoff=1
while :; do
  curl_args=(-sS -N --no-buffer)
  if [[ -n "$DEADLINE" ]]; then
    remaining=$(( DEADLINE - $(date +%s) ))
    if (( remaining <= 0 )); then break; fi
    curl_args+=(--max-time "$remaining")
  fi

  got_data=0
  # Process substitution keeps the loop in this shell so got_data survives it.
  while IFS= read -r line; do
    line="${line%$'\r'}"
    case "$line" in
      "data: "*)
        got_data=1
        emit "${line#data: }"
        ;;
      ":"*)
        # SSE comment: the connection preamble or a keepalive ping.
        got_data=1
        ;;
    esac
    if [[ -n "$DEADLINE" ]] && (( $(date +%s) >= DEADLINE )); then
      break
    fi
  done < <(curl "${curl_args[@]}" "$URL" 2>/dev/null || true)

  if [[ -n "$DEADLINE" ]] && (( $(date +%s) >= DEADLINE )); then
    break
  fi

  # Stream dropped. Reset the backoff if the connection had been working.
  if [[ $got_data -eq 1 ]]; then
    backoff=1
  fi
  echo "watch.sh: stream closed, reconnecting in ${backoff}s..." >&2
  sleep "$backoff"
  backoff=$(( backoff * 2 ))
  if (( backoff > 30 )); then backoff=30; fi
done

exit 0
