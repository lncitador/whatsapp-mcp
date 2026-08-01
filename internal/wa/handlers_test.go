package wa

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/lncitador/whatsapp-mcp/internal/store"
)

func ptr[T any](v T) *T { return &v }

// capturingLogger keeps every line so a test can assert that a discarded
// message left a trace instead of vanishing.
type capturingLogger struct {
	mu     sync.Mutex
	warns  []string
	infos  []string
	debugs []string
	errors []string
}

func (l *capturingLogger) add(dst *[]string, msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(args) == 0 {
		*dst = append(*dst, msg)
		return
	}
	*dst = append(*dst, fmt.Sprintf(msg, args...))
}

func (l *capturingLogger) Warnf(msg string, args ...any)  { l.add(&l.warns, msg, args...) }
func (l *capturingLogger) Errorf(msg string, args ...any) { l.add(&l.errors, msg, args...) }
func (l *capturingLogger) Infof(msg string, args ...any)  { l.add(&l.infos, msg, args...) }
func (l *capturingLogger) Debugf(msg string, args ...any) { l.add(&l.debugs, msg, args...) }
func (l *capturingLogger) Sub(string) waLog.Logger        { return l }

func (l *capturingLogger) joined(lines []string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(lines, "\n")
}

func (l *capturingLogger) allWarns() string { return l.joined(l.warns) }

func testClient(t *testing.T) (*Client, *capturingLogger) {
	t.Helper()
	t.Setenv("WHATSAPP_MCP_DIR", t.TempDir())
	st, err := store.Open()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	log := &capturingLogger{}
	// wm stays nil on purpose: the handlers must not need a live socket for
	// anything on the persistence path. ParseWebMessage is nil-receiver safe
	// for messages that are not from us (whatsmeow client.go getOwnID).
	return &Client{st: st, logger: log}, log
}

func resetDropCounts() {
	dropMu.Lock()
	defer dropMu.Unlock()
	dropCounts = map[string]int{}
}

func historySyncEvent(chatJID, chatName string, msgs ...*waWeb.WebMessageInfo) *events.HistorySync {
	wrapped := make([]*waHistorySync.HistorySyncMsg, 0, len(msgs))
	for _, m := range msgs {
		wrapped = append(wrapped, &waHistorySync.HistorySyncMsg{Message: m})
	}
	return &events.HistorySync{Data: &waHistorySync.HistorySync{
		Conversations: []*waHistorySync.Conversation{{
			ID:       ptr(chatJID),
			Name:     ptr(chatName),
			Messages: wrapped,
		}},
	}}
}

func webMsg(chatJID, participant, id string, ts time.Time, content *waE2E.Message) *waWeb.WebMessageInfo {
	return &waWeb.WebMessageInfo{
		Key: &waCommon.MessageKey{
			RemoteJID:   ptr(chatJID),
			FromMe:      ptr(false),
			ID:          ptr(id),
			Participant: ptr(participant),
		},
		MessageTimestamp: ptr(uint64(ts.Unix())),
		Message:          content,
	}
}

// C3: before ParseWebMessage, a chat with disappearing messages lost 100% of
// its synced history because the handler read the raw wrapper.
func TestHistorySyncStoresEphemeralMessage(t *testing.T) {
	resetDropCounts()
	c, log := testClient(t)

	chatJID := "120363000000000000@g.us"
	participant := "5511999999999@s.whatsapp.net"
	ts := time.Date(2026, 7, 31, 15, 0, 0, 0, time.UTC)

	ephemeral := &waE2E.Message{
		EphemeralMessage: &waE2E.FutureProofMessage{
			Message: &waE2E.Message{Conversation: ptr("mensagem temporária")},
		},
	}

	// Proof that unwrapping is what makes this work: read raw, as the handler
	// used to, and there is no content at all.
	if raw := extractTextContent(ephemeral); raw != "" {
		t.Fatalf("precondition broken: raw wrapper already exposes %q", raw)
	}

	c.handleHistorySync(historySyncEvent(chatJID, "Grupo Teste", webMsg(chatJID, participant, "EPH1", ts, ephemeral)))

	got, err := c.st.GetLastMessageForChat(chatJID)
	if err != nil {
		t.Fatalf("GetLastMessageForChat: %v", err)
	}
	if got == nil {
		t.Fatalf("ephemeral message was not stored; warns:\n%s", log.allWarns())
	}
	if got.Content != "mensagem temporária" {
		t.Fatalf("content = %q, want the unwrapped text", got.Content)
	}
	if got.ID != "EPH1" {
		t.Fatalf("id = %q, want EPH1", got.ID)
	}
	// Same shape as the live path: bare number, not the full JID.
	if got.Sender != "5511999999999" {
		t.Fatalf("sender = %q, want 5511999999999 (live path format)", got.Sender)
	}
	if !got.Timestamp.Equal(ts) {
		t.Fatalf("timestamp = %v, want %v", got.Timestamp, ts)
	}
	if name := c.st.ChatName(chatJID); name != "Grupo Teste" {
		t.Fatalf("chat name = %q", name)
	}
}

// C3 companion: view-once wrappers unwrap too, and the media survives.
func TestHistorySyncStoresViewOnceMedia(t *testing.T) {
	resetDropCounts()
	c, log := testClient(t)

	chatJID := "120363000000000001@g.us"
	ts := time.Date(2026, 7, 31, 16, 0, 0, 0, time.UTC)
	viewOnce := &waE2E.Message{
		ViewOnceMessageV2: &waE2E.FutureProofMessage{
			Message: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
				URL:           ptr("https://mmg.whatsapp.net/x"),
				Caption:       ptr("olha isso"),
				FileSHA256:    []byte{0xde, 0xad, 0xbe, 0xef},
				MediaKey:      []byte{1, 2, 3},
				FileLength:    ptr(uint64(1234)),
				DirectPath:    ptr("/x"),
				Mimetype:      ptr("image/jpeg"),
				FileEncSHA256: []byte{4, 5, 6},
			}},
		},
	}

	c.handleHistorySync(historySyncEvent(chatJID, "Grupo Mídia",
		webMsg(chatJID, "5511888888888@s.whatsapp.net", "VO1", ts, viewOnce)))

	got, err := c.st.GetLastMessageForChat(chatJID)
	if err != nil {
		t.Fatalf("GetLastMessageForChat: %v", err)
	}
	if got == nil {
		t.Fatalf("view-once media was not stored; warns:\n%s", log.allWarns())
	}
	if got.MediaType != "image" {
		t.Fatalf("media type = %q", got.MediaType)
	}
	if got.Content != "olha isso" {
		t.Fatalf("caption was dropped: content = %q", got.Content)
	}
}

// C4: an unsupported type must leave a log line naming the populated field
// instead of disappearing.
func TestHistorySyncLogsUnhandledTypeInsteadOfDropping(t *testing.T) {
	resetDropCounts()
	c, log := testClient(t)

	chatJID := "120363000000000002@g.us"
	ts := time.Date(2026, 7, 31, 17, 0, 0, 0, time.UTC)
	unhandled := &waE2E.Message{
		// No content, no media, not a control payload we know about.
		OrderMessage: &waE2E.OrderMessage{OrderID: ptr("123")},
	}

	c.handleHistorySync(historySyncEvent(chatJID, "Grupo Pedido",
		webMsg(chatJID, "5511777777777@s.whatsapp.net", "ORD1", ts, unhandled)))

	warns := log.allWarns()
	if !strings.Contains(warns, "OrderMessage") {
		t.Fatalf("want a warning naming OrderMessage, got:\n%s", warns)
	}
	if !strings.Contains(warns, "ORD1") {
		t.Fatalf("want the message ID in the warning, got:\n%s", warns)
	}
	if n := DroppedMessages()["unhandled:history"]; n != 1 {
		t.Fatalf("unhandled:history counter = %d, want 1", n)
	}
	// C5: the chat must NOT be created by a message we did not store.
	if name := c.st.ChatName(chatJID); name != "" {
		t.Fatalf("ghost chat created for a dropped message: name = %q", name)
	}
}

// C5 on the live path: dropping a message must not bump the chat.
func TestHandleMessageDropDoesNotCreateGhostChat(t *testing.T) {
	resetDropCounts()
	c, log := testClient(t)

	chatJID := "5511666666666@s.whatsapp.net"
	jid, err := types.ParseJID(chatJID)
	if err != nil {
		t.Fatalf("parse jid: %v", err)
	}

	c.handleMessage(&events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: jid, Sender: jid},
			ID:            "ORD2",
			Timestamp:     time.Date(2026, 7, 31, 18, 0, 0, 0, time.UTC),
		},
		Message: &waE2E.Message{OrderMessage: &waE2E.OrderMessage{OrderID: ptr("456")}},
	})

	chats, err := c.st.ListChats("", 10, 0, false, "")
	if err != nil {
		t.Fatalf("ListChats: %v", err)
	}
	if len(chats) != 0 {
		t.Fatalf("want no chat row for a dropped message, got %+v", chats)
	}
	if warns := log.allWarns(); !strings.Contains(warns, "OrderMessage") {
		t.Fatalf("want a warning naming OrderMessage, got:\n%s", warns)
	}
	if n := DroppedMessages()["unhandled:live"]; n != 1 {
		t.Fatalf("unhandled:live counter = %d, want 1", n)
	}
}

// A control payload is skipped by design, but still counted so the drop rate is
// observable.
func TestHandleMessageControlPayloadIsCountedNotWarned(t *testing.T) {
	resetDropCounts()
	c, log := testClient(t)

	chatJID := "5511555555555@s.whatsapp.net"
	jid, _ := types.ParseJID(chatJID)

	c.handleMessage(&events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: jid, Sender: jid},
			ID:            "PROTO1",
			Timestamp:     time.Now(),
		},
		Message: &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
			Type: waE2E.ProtocolMessage_REVOKE.Enum(),
		}},
	})

	if n := DroppedMessages()["control:live"]; n != 1 {
		t.Fatalf("control:live counter = %d, want 1", n)
	}
	if warns := log.allWarns(); warns != "" {
		t.Fatalf("control payloads must not warn, got:\n%s", warns)
	}
}

// The live path stores previously-unsupported types via a synthetic content, so
// the row exists in the database.
func TestHandleMessageStoresPreviouslyUnsupportedTypes(t *testing.T) {
	resetDropCounts()
	c, _ := testClient(t)

	chatJID := "5511444444444@s.whatsapp.net"
	jid, _ := types.ParseJID(chatJID)
	// Pre-seed the name so chatName does not need the (nil) whatsmeow client.
	if err := c.st.StoreChat(chatJID, "Fulano", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("StoreChat: %v", err)
	}

	cases := []struct {
		name    string
		id      string
		msg     *waE2E.Message
		wantSub string
	}{
		{
			name:    "location",
			id:      "LOC1",
			msg:     &waE2E.Message{LocationMessage: &waE2E.LocationMessage{Name: ptr("Padaria"), DegreesLatitude: ptr(-23.5), DegreesLongitude: ptr(-46.6)}},
			wantSub: "[localização: Padaria]",
		},
		{
			name: "poll",
			id:   "POLL1",
			msg: &waE2E.Message{PollCreationMessage: &waE2E.PollCreationMessage{
				Name: ptr("Pizza?"),
				Options: []*waE2E.PollCreationMessage_Option{
					{OptionName: ptr("sim")}, {OptionName: ptr("não")},
				},
			}},
			wantSub: "[enquete: Pizza? | sim / não]",
		},
		{
			name:    "reaction",
			id:      "REACT1",
			msg:     &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Text: ptr("👍"), Key: &waCommon.MessageKey{ID: ptr("TARGET")}}},
			wantSub: "[reação: 👍 a TARGET]",
		},
		{
			name:    "contact",
			id:      "CONTACT1",
			msg:     &waE2E.Message{ContactMessage: &waE2E.ContactMessage{DisplayName: ptr("Zé")}},
			wantSub: "[contato: Zé]",
		},
		{
			name: "button reply",
			id:   "BTN1",
			msg: &waE2E.Message{ButtonsResponseMessage: &waE2E.ButtonsResponseMessage{
				Response: &waE2E.ButtonsResponseMessage_SelectedDisplayText{SelectedDisplayText: "Confirmar"},
			}},
			wantSub: "[botão: Confirmar]",
		},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := time.Date(2026, 7, 31, 19, i, 0, 0, time.UTC)
			c.handleMessage(&events.Message{
				Info: types.MessageInfo{
					MessageSource: types.MessageSource{Chat: jid, Sender: jid},
					ID:            tc.id,
					Timestamp:     ts,
				},
				Message: tc.msg,
			})

			got, err := c.st.GetLastMessageForChat(chatJID)
			if err != nil {
				t.Fatalf("GetLastMessageForChat: %v", err)
			}
			if got == nil || got.ID != tc.id {
				t.Fatalf("message %s was not stored (got %+v)", tc.id, got)
			}
			if got.Content != tc.wantSub {
				t.Fatalf("content = %q, want %q", got.Content, tc.wantSub)
			}
		})
	}

	if drops := DroppedMessages(); len(drops) != 0 {
		t.Fatalf("nothing should have been dropped, got %v", drops)
	}
}

func TestDescribeMessageMedia(t *testing.T) {
	cases := []struct {
		name          string
		msg           *waE2E.Message
		wantMediaType string
		wantPrefix    string
	}{
		{
			name:          "sticker",
			msg:           &waE2E.Message{StickerMessage: &waE2E.StickerMessage{FileSHA256: []byte{0xaa, 0xbb}, URL: ptr("u")}},
			wantMediaType: "sticker",
			wantPrefix:    "sticker_",
		},
		{
			name:          "ptv keeps video so download still works",
			msg:           &waE2E.Message{PtvMessage: &waE2E.VideoMessage{FileSHA256: []byte{0xcc}, URL: ptr("u")}},
			wantMediaType: "video",
			wantPrefix:    "ptv_",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, ok := describeMessage(tc.msg)
			if !ok {
				t.Fatalf("describeMessage said there is nothing to store")
			}
			if s.mediaType != tc.wantMediaType {
				t.Fatalf("mediaType = %q, want %q", s.mediaType, tc.wantMediaType)
			}
			if !strings.HasPrefix(s.filename, tc.wantPrefix) {
				t.Fatalf("filename = %q, want prefix %q", s.filename, tc.wantPrefix)
			}
			if s.content == "" {
				t.Fatalf("want a synthetic content so the row reads as something")
			}
		})
	}
}

func TestPopulatedMessageFields(t *testing.T) {
	fields := populatedMessageFields(&waE2E.Message{
		OrderMessage:       &waE2E.OrderMessage{OrderID: ptr("1")},
		MessageContextInfo: &waE2E.MessageContextInfo{},
	})
	if strings.Join(fields, ",") != "OrderMessage" {
		t.Fatalf("fields = %v, want just OrderMessage (context info is noise)", fields)
	}
	if got := populatedMessageFields(&waE2E.Message{}); got[0] != "none" {
		t.Fatalf("empty message fields = %v", got)
	}
	if got := populatedMessageFields(nil); got[0] != "nil" {
		t.Fatalf("nil message fields = %v", got)
	}
}
