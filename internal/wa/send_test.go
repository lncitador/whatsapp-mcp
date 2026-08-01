package wa

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"github.com/lncitador/whatsapp-mcp/internal/store"
	"github.com/lncitador/whatsapp-mcp/internal/stream"
)

// WhatsApp does not echo a message back to the session that sent it, so
// handleMessage never runs for our own sends. Without storeOutgoing the local
// store and the event stream only ever see the inbound half of a conversation.
func TestStoreOutgoingPersistsAndPublishes(t *testing.T) {
	c, _ := testClient(t)

	sub := stream.Subscribe()
	defer stream.Unsubscribe(sub)

	to := types.JID{User: "559184540751", Server: types.DefaultUserServer}
	sent := time.Now().Truncate(time.Second)
	resp := whatsmeow.SendResponse{ID: "OUT1", Timestamp: sent}
	msg := &waProto.Message{Conversation: proto.String("testing the stream")}

	c.storeOutgoing(to, resp, msg)

	msgs, err := c.st.ListMessages(store.ListMessagesArgs{ChatJID: to.String(), Limit: 10})
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected the sent message to be stored, got %d rows", len(msgs))
	}
	if msgs[0].Content != "testing the stream" {
		t.Errorf("content = %q, want %q", msgs[0].Content, "testing the stream")
	}
	if !msgs[0].IsFromMe {
		t.Error("stored message should be marked as from me")
	}
	if msgs[0].ID != "OUT1" {
		t.Errorf("id = %q, want the server-assigned OUT1", msgs[0].ID)
	}

	select {
	case evt := <-sub.C:
		if evt.ID != "OUT1" || !evt.IsFromMe {
			t.Errorf("published %+v, want the outgoing message OUT1", evt)
		}
		if evt.Text != "testing the stream" {
			t.Errorf("published text = %q", evt.Text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sent message was never published to the event stream")
	}
}

// A send with nothing storable must not create a chat row with no message
// behind it — that is the phantom-chat shape that leaves a chat unanchored.
func TestStoreOutgoingIgnoresEmptyMessage(t *testing.T) {
	c, log := testClient(t)

	to := types.JID{User: "559184540751", Server: types.DefaultUserServer}
	c.storeOutgoing(to, whatsmeow.SendResponse{ID: "OUT2", Timestamp: time.Now()}, &waProto.Message{})

	chats, err := c.st.ListChats("", 10, 0, false, "")
	if err != nil {
		t.Fatalf("list chats: %v", err)
	}
	if len(chats) != 0 {
		t.Errorf("expected no chat row for an unstorable send, got %d", len(chats))
	}
	if log.allWarns() == "" {
		t.Error("an unstorable send must be logged, not dropped in silence")
	}
}

// The server timestamp is authoritative, but a zero one must not land in the
// store as year 1 — that would sort ahead of every real message forever.
func TestStoreOutgoingFallsBackToLocalTime(t *testing.T) {
	c, _ := testClient(t)

	to := types.JID{User: "559184540751", Server: types.DefaultUserServer}
	before := time.Now().Add(-time.Second)
	c.storeOutgoing(to, whatsmeow.SendResponse{ID: "OUT3"}, &waProto.Message{
		Conversation: proto.String("no server timestamp"),
	})

	msgs, err := c.st.ListMessages(store.ListMessagesArgs{ChatJID: to.String(), Limit: 10})
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 stored message, got %d", len(msgs))
	}
	if msgs[0].Timestamp.Before(before) {
		t.Errorf("timestamp = %v, want a fallback to roughly now", msgs[0].Timestamp)
	}
}

// An album header carries no content of its own, so it used to be discarded —
// leaving an unexplained gap where a batch of photos was shared. Seen in
// production as "populated waE2E fields: [AlbumMessage]".
func TestSynthesizeAlbumMessage(t *testing.T) {
	got := synthesizeContent(&waProto.Message{
		AlbumMessage: &waE2E.AlbumMessage{
			ExpectedImageCount: proto.Uint32(3),
			ExpectedVideoCount: proto.Uint32(1),
		},
	})
	want := "[álbum: 3 imagem(ns), 1 vídeo(s)]"
	if got != want {
		t.Errorf("synthesizeContent(album) = %q, want %q", got, want)
	}
}
