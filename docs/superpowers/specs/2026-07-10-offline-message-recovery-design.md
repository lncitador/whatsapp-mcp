# Offline Message Recovery Design

## Problem

The whatsapp-bridge only persists messages received while the websocket is connected. When the connection drops, messages arriving on the phone during the gap are never persisted to `messages.db` — even after reconnection.

### Root Cause

The whatsmeow library automatically replays missed messages on reconnect as `*events.Message` events. However, the bridge doesn't handle `*events.OfflineSyncPreview` or `*events.OfflineSyncCompleted`, and has no fallback mechanism if the automatic replay fails (e.g., due to "no signal session established" errors).

## Solution

Add offline sync event handling and on-demand history sync as a fallback after reconnection.

### Components

#### 1. Store Layer (`internal/store/queries.go`)

Add `GetLastMessageForChat(chatJID string) (*Message, error)`:
- Returns the most recent persisted message for a given chat JID
- Used to build `lastKnownMessageInfo` for on-demand history sync requests
- Returns `nil, nil` if no messages exist for the chat

#### 2. Event Handler (`internal/wa/client.go`)

Add handlers for offline sync events:
- `*events.OfflineSyncPreview`: Log the preview (total count, messages, notifications, receipts)
- `*events.OfflineSyncCompleted`: Log completion and trigger on-demand history sync

#### 3. On-Demand History Sync (`internal/wa/client.go`)

Add `requestHistorySyncForRecentChats()`:
- Gets the last 10 chats from the store (sorted by `last_message_time`)
- For each chat with persisted messages:
  - Gets the last message via `GetLastMessageForChat`
  - Builds `types.MessageInfo` from that message
  - Calls `BuildHistorySyncRequest(msgInfo, 50)` to request 50 messages
  - Sends via `SendPeerMessage`
  - Logs each request and errors

### Data Flow

```
Reconnect → OfflineSyncPreview (logged)
         → [automatic replay of missed messages as *events.Message]
         → OfflineSyncCompleted
         → requestHistorySyncForRecentChats()
            → For each recent chat:
               → GetLastMessageForChat(chatJID)
               → BuildHistorySyncRequest(lastMsg, 50)
               → SendPeerMessage(request)
               → [response arrives as *events.HistorySync]
               → handleHistorySync(existing handler)
```

### Error Handling

- If `GetLastMessageForChat` returns nil (no messages), skip that chat
- If `BuildHistorySyncRequest` or `SendPeerMessage` fails, log warning and continue to next chat
- All errors are logged but don't block the event handler

### Testing

Manual test procedure:
1. Start daemon, verify connection
2. Disable network (e.g., turn off Wi-Fi for 2 minutes)
3. Receive message on phone during offline period
4. Re-enable network
5. Verify message appears in `messages.db` via `list_messages` MCP tool
6. Check `daemon.log` for offline sync events and history sync requests
7. Verify no duplicate messages

### Files Modified

- `internal/store/queries.go` — Add `GetLastMessageForChat`
- `internal/wa/client.go` — Add offline sync event handlers and `requestHistorySyncForRecentChats`

### Dependencies

- whatsmeow `v0.0.0-20260630180629-b572e5bcb92b` (already in go.mod)
- `BuildHistorySyncRequest` requires valid `*types.MessageInfo` (never nil)
