# Offline Message Recovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Recover messages that arrive during offline periods by handling offline sync events and requesting on-demand history sync after reconnection.

**Architecture:** Add event handlers for `OfflineSyncPreview` and `OfflineSyncCompleted`, then trigger on-demand history sync requests for recent chats using the last persisted message as anchor.

**Tech Stack:** Go, whatsmeow v0.0.0-20260630, SQLite

## Global Constraints

- whatsmeow `v0.0.0-20260630180629-b572e5bcb92b` (already in go.mod)
- `BuildHistorySyncRequest` requires valid `*types.MessageInfo` (never nil)
- Messages stored with `INSERT OR REPLACE` (dedup by `(id, chat_jid)`)
- Timestamps as TEXT `2026-07-10 12:32:25 -0300 -03`

---

### Task 1: Add GetLastMessageForChat to Store

**Files:**
- Modify: `internal/store/queries.go`
- Test: `internal/store/queries_test.go`

**Interfaces:**
- Produces: `GetLastMessageForChat(chatJID string) (*Message, error)` — returns nil, nil when no messages exist

- [ ] **Step 1: Write the failing test**

```go
func TestGetLastMessageForChat(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	// Store a chat and messages
	s.StoreChat("555123456789@s.whatsapp.net", "Test Contact", time.Now())
	s.StoreMessage(NewMessage{
		ID: "msg1", ChatJID: "555123456789@s.whatsapp.net",
		Sender: "555123456789", Content: "first",
		Timestamp: time.Now().Add(-2 * time.Hour),
	})
	s.StoreMessage(NewMessage{
		ID: "msg2", ChatJID: "555123456789@s.whatsapp.net",
		Sender: "555123456789", Content: "last",
		Timestamp: time.Now(),
	})

	msg, err := s.GetLastMessageForChat("555123456789@s.whatsapp.net")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg == nil {
		t.Fatal("expected message, got nil")
	}
	if msg.ID != "msg2" {
		t.Fatalf("expected msg2, got %s", msg.ID)
	}
	if msg.Content != "last" {
		t.Fatalf("expected 'last', got %s", msg.Content)
	}
}

func TestGetLastMessageForChat_Empty(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	s.StoreChat("555123456789@s.whatsapp.net", "Test Contact", time.Now())

	msg, err := s.GetLastMessageForChat("555123456789@s.whatsapp.net")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != nil {
		t.Fatalf("expected nil, got %+v", msg)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestGetLastMessageForChat -v`
Expected: FAIL with "GetLastMessageForChat not defined"

- [ ] **Step 3: Write minimal implementation**

```go
func (s *Store) GetLastMessageForChat(chatJID string) (*Message, error) {
	row := s.db.QueryRow(
		"SELECT "+messageCols+" FROM messages JOIN chats ON messages.chat_jid = chats.jid"+
			" WHERE messages.chat_jid = ? ORDER BY messages.timestamp DESC LIMIT 1",
		chatJID)
	m, err := scanMessage(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestGetLastMessageForChat -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/queries.go internal/store/queries_test.go
git commit -m "feat(store): add GetLastMessageForChat for history sync"
```

---

### Task 2: Add Offline Sync Event Handlers

**Files:**
- Modify: `internal/wa/client.go:84-97`

**Interfaces:**
- Consumes: `*events.OfflineSyncPreview`, `*events.OfflineSyncCompleted` from whatsmeow
- Produces: Logs offline sync stats, triggers `requestHistorySyncForRecentChats()`

- [ ] **Step 1: Add event handlers**

In `internal/wa/client.go`, add cases to the event handler switch (after the `*events.HistorySync` case):

```go
case *events.OfflineSyncPreview:
	c.logger.Infof("Offline sync preview: %d total (%d messages, %d notifications, %d receipts)",
		v.Total, v.Messages, v.Notifications, v.Receipts)
case *events.OfflineSyncCompleted:
	c.logger.Infof("Offline sync completed: %d events processed", v.Count)
	go c.requestHistorySyncForRecentChats()
```

- [ ] **Step 2: Build to verify compilation**

Run: `go build ./...`
Expected: SUCCESS

- [ ] **Step 3: Commit**

```bash
git add internal/wa/client.go
git commit -m "feat(wa): handle OfflineSyncPreview and OfflineSyncCompleted events"
```

---

### Task 3: Implement requestHistorySyncForRecentChats

**Files:**
- Modify: `internal/wa/client.go`

**Interfaces:**
- Consumes: `GetLastMessageForChat`, `ListChats` from store; `BuildHistorySyncRequest`, `SendPeerMessage` from whatsmeow
- Produces: On-demand history sync requests sent to WhatsApp server

- [ ] **Step 1: Add the method**

```go
func (c *Client) requestHistorySyncForRecentChats() {
	chats, err := c.st.ListChats("", 10, 0, false, "")
	if err != nil {
		c.logger.Warnf("Failed to list chats for history sync: %v", err)
		return
	}

	for _, chat := range chats {
		lastMsg, err := c.st.GetLastMessageForChat(chat.JID)
		if err != nil || lastMsg == nil {
			continue
		}

		jid, err := types.ParseJID(chat.JID)
		if err != nil {
			continue
		}

		msgInfo := &types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:     jid,
				IsFromMe: lastMsg.IsFromMe,
			},
			ID:        types.MessageID(lastMsg.ID),
			Timestamp: lastMsg.Timestamp,
		}

		req := c.wm.BuildHistorySyncRequest(msgInfo, 50)
		_, err = c.wm.SendPeerMessage(context.Background(), req)
		if err != nil {
			c.logger.Warnf("Failed to request history sync for %s: %v", chat.JID, err)
		} else {
			c.logger.Infof("Requested on-demand history sync for %s (last msg: %s)", chat.JID, lastMsg.ID)
		}
	}
}
```

- [ ] **Step 2: Build to verify compilation**

Run: `go build ./...`
Expected: SUCCESS

- [ ] **Step 3: Commit**

```bash
git add internal/wa/client.go
git commit -m "feat(wa): add on-demand history sync after offline sync"
```

---

### Task 4: Run Full Test Suite

**Files:**
- None (verification only)

- [ ] **Step 1: Run all tests**

Run: `go test ./...`
Expected: All tests PASS

- [ ] **Step 2: Build binary**

Run: `go build -o whatsapp-mcp ./cmd/whatsapp-mcp/`
Expected: SUCCESS

- [ ] **Step 3: Final commit if needed**

If any fixes were needed:
```bash
git add -A
git commit -m "fix: test fixes for offline message recovery"
```

---

### Task 5: Manual Testing Procedure

**Files:**
- None (documentation only)

Document the manual test procedure for verification:

1. Start daemon: `~/.claude/skills/whatsapp/scripts/start.sh`
2. Verify connection: check `daemon.log` for "Successfully authenticated"
3. Send a test message to yourself or have someone send one
4. Verify message appears in `messages.db`
5. Disable network (turn off Wi-Fi for 2 minutes)
6. Have someone send a message during offline period
7. Re-enable network
8. Check `daemon.log` for:
   - "Offline sync preview: X total (Y messages, ...)"
   - "Offline sync completed: Z events processed"
   - "Requested on-demand history sync for ..."
9. Verify message appears in `messages.db` via `list_messages` MCP tool
10. Verify no duplicate messages
