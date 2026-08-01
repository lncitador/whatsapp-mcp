package store

import (
	"testing"
	"time"
)

// seed cria 2 chats (1 direto, 1 grupo) com 3 mensagens.
func seed(t *testing.T) *Store {
	t.Helper()
	s := openTestStore(t)
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	s.StoreChat("5511999999999@s.whatsapp.net", "Alice", base.Add(2*time.Hour))
	s.StoreChat("123-group@g.us", "Time Farol", base.Add(3*time.Hour))
	msgs := []NewMessage{
		{ID: "A1", ChatJID: "5511999999999@s.whatsapp.net", Sender: "5511999999999", Content: "oi", Timestamp: base},
		{ID: "A2", ChatJID: "5511999999999@s.whatsapp.net", Sender: "me", Content: "olá Alice", Timestamp: base.Add(2 * time.Hour), IsFromMe: true},
		{ID: "G1", ChatJID: "123-group@g.us", Sender: "5511888888888", Content: "reunião amanhã", Timestamp: base.Add(3 * time.Hour)},
	}
	for _, m := range msgs {
		if err := s.StoreMessage(m); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestListMessagesFilters(t *testing.T) {
	s := seed(t)
	got, err := s.ListMessages(ListMessagesArgs{Query: "reunião", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "G1" || got[0].ChatName != "Time Farol" {
		t.Fatalf("got %+v", got)
	}

	got, _ = s.ListMessages(ListMessagesArgs{ChatJID: "5511999999999@s.whatsapp.net", Limit: 20})
	if len(got) != 2 || got[0].ID != "A2" { // DESC
		t.Fatalf("chat filter got %+v", got)
	}
}

func TestGetMessageContext(t *testing.T) {
	s := seed(t)
	ctx, err := s.GetMessageContext("A2", 5, 5)
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Message.ID != "A2" || len(ctx.Before) != 1 || ctx.Before[0].ID != "A1" {
		t.Fatalf("got %+v", ctx)
	}
	if _, err := s.GetMessageContext("NOPE", 1, 1); err == nil {
		t.Fatal("want error for unknown message id")
	}
}

func TestListChatsAndGetChat(t *testing.T) {
	s := seed(t)
	chats, err := s.ListChats("", 20, 0, true, "last_active")
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 2 || chats[0].JID != "123-group@g.us" || chats[0].LastMessage != "reunião amanhã" {
		t.Fatalf("got %+v", chats)
	}

	c, err := s.GetChat("5511999999999@s.whatsapp.net", true)
	if err != nil || c == nil || c.LastMessage != "olá Alice" {
		t.Fatalf("c=%+v err=%v", c, err)
	}
	if c, _ := s.GetChat("missing@s.whatsapp.net", true); c != nil {
		t.Fatal("want nil for missing chat")
	}
}

func TestContactHelpers(t *testing.T) {
	s := seed(t)
	c, err := s.GetDirectChatByContact("5511999999999")
	if err != nil || c == nil || c.Name != "Alice" {
		t.Fatalf("c=%+v err=%v", c, err)
	}
	chats, _ := s.GetContactChats("5511888888888", 20, 0)
	if len(chats) != 1 || chats[0].JID != "123-group@g.us" {
		t.Fatalf("got %+v", chats)
	}
	// Full JID matches chat, returns newest message A2
	m, _ := s.GetLastInteraction("5511999999999@s.whatsapp.net")
	if m == nil || m.ID != "A2" {
		t.Fatalf("got %+v", m)
	}
	// Bare phone matches sender of A1
	m, _ = s.GetLastInteraction("5511999999999")
	if m == nil || m.ID != "A1" {
		t.Fatalf("bare phone: got %+v, want A1", m)
	}
	if name := s.SenderName("5511999999999@s.whatsapp.net"); name != "Alice" {
		t.Fatalf("SenderName = %q", name)
	}
}

func TestGetLastMessageForChat(t *testing.T) {
	s := seed(t)

	msg, err := s.GetLastMessageForChat("5511999999999@s.whatsapp.net")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg == nil {
		t.Fatal("expected message, got nil")
	}
	if msg.ID != "A2" {
		t.Fatalf("expected A2, got %s", msg.ID)
	}
	if msg.Content != "olá Alice" {
		t.Fatalf("expected 'olá Alice', got %s", msg.Content)
	}
}

func TestGetLastMessageForChat_Empty(t *testing.T) {
	s := openTestStore(t)
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	s.StoreChat("empty@s.whatsapp.net", "Empty", base)

	msg, err := s.GetLastMessageForChat("empty@s.whatsapp.net")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != nil {
		t.Fatalf("expected nil, got %+v", msg)
	}
}

func TestChatsWithoutLastMessage(t *testing.T) {
	s := seed(t)
	chats, err := s.ListChats("", 20, 0, false, "last_active")
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 2 {
		t.Fatalf("want 2 chats, got %d", len(chats))
	}
	for _, c := range chats {
		if c.LastMessage != "" || c.LastSender != "" {
			t.Fatalf("want empty LastMessage/LastSender, got %+v", c)
		}
		if c.LastMessageTime == nil {
			t.Fatalf("want non-nil LastMessageTime, got %+v", c)
		}
	}

	c, err := s.GetChat("5511999999999@s.whatsapp.net", false)
	if err != nil || c == nil || c.Name != "Alice" || c.LastMessage != "" {
		t.Fatalf("c=%+v err=%v", c, err)
	}
}

func TestCountMessages(t *testing.T) {
	s := seed(t)
	n, err := s.CountMessages()
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("CountMessages = %d, want 3", n)
	}

	// Redelivered history must not inflate the delta: (id, chat_jid) is the PK
	// and StoreMessage is an INSERT OR REPLACE.
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	if err := s.StoreMessage(NewMessage{
		ID: "A1", ChatJID: "5511999999999@s.whatsapp.net", Sender: "5511999999999",
		Content: "oi", Timestamp: base,
	}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountMessages(); n != 3 {
		t.Fatalf("CountMessages after re-store = %d, want 3", n)
	}

	if err := s.StoreMessage(NewMessage{
		ID: "A3", ChatJID: "5511999999999@s.whatsapp.net", Sender: "5511999999999",
		Content: "nova", Timestamp: base.Add(4 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountMessages(); n != 4 {
		t.Fatalf("CountMessages after new message = %d, want 4", n)
	}
}

func TestListChatsForResync(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()
	chats := map[string]time.Time{
		"recent1@s.whatsapp.net": now.Add(-1 * time.Hour),
		"recent2@s.whatsapp.net": now.Add(-3 * time.Hour),
		"window@s.whatsapp.net":  now.Add(-47 * time.Hour), // inside the 48h window
		"stale@s.whatsapp.net":   now.Add(-40 * 24 * time.Hour),
	}
	for jid, ts := range chats {
		if err := s.StoreChat(jid, jid, ts); err != nil {
			t.Fatal(err)
		}
	}

	jids := func(cs []Chat) map[string]bool {
		out := map[string]bool{}
		for _, c := range cs {
			out[c.JID] = true
		}
		return out
	}

	// Top-1 by recency alone would only return recent1; the 48h window is what
	// keeps chats that fell below the cut from being invisible forever.
	got, err := s.ListChatsForResync(1, now.Add(-48*time.Hour), 50)
	if err != nil {
		t.Fatal(err)
	}
	set := jids(got)
	for _, want := range []string{"recent1@s.whatsapp.net", "recent2@s.whatsapp.net", "window@s.whatsapp.net"} {
		if !set[want] {
			t.Fatalf("%s missing from %v", want, set)
		}
	}
	if set["stale@s.whatsapp.net"] {
		t.Fatalf("stale chat should be out of the window: %v", set)
	}
	if got[0].JID != "recent1@s.whatsapp.net" {
		t.Fatalf("want most recent first, got %+v", got[0])
	}

	// A big enough top-N slice reaches the stale chat as well.
	got, err = s.ListChatsForResync(4, now.Add(-48*time.Hour), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || !jids(got)["stale@s.whatsapp.net"] {
		t.Fatalf("want all 4 chats, got %+v", got)
	}

	// maxChats caps the union so a reconnect can't turn into a flood.
	got, err = s.ListChatsForResync(4, now.Add(-48*time.Hour), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 chats after cap, got %d", len(got))
	}

	// No duplicates when a chat satisfies both criteria.
	got, err = s.ListChatsForResync(4, now.Add(-48*time.Hour), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(jids(got)) != len(got) {
		t.Fatalf("duplicate chats in %+v", got)
	}
}

func TestTranscriptionRoundTrip(t *testing.T) {
	s := seed(t)
	tr := Transcription{
		MessageID:    "A1",
		ChatJID:      "5511999999999@s.whatsapp.net",
		MediaType:    "audio",
		Text:         "olá mundo",
		Segments:     `[{"start":0,"end":1,"text":"olá"}]`,
		FramesDir:    "/tmp/frames",
		MarkdownPath: "/tmp/out.md",
	}
	if err := s.StoreTranscription(tr); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetTranscription("A1", "5511999999999@s.whatsapp.net")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected transcription, got nil")
	}
	if got.Text != "olá mundo" || got.MediaType != "audio" || got.Segments != tr.Segments {
		t.Fatalf("got %+v", got)
	}
	if got.FramesDir != "/tmp/frames" || got.MarkdownPath != "/tmp/out.md" {
		t.Fatalf("got FramesDir=%q MarkdownPath=%q", got.FramesDir, got.MarkdownPath)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("expected non-zero CreatedAt")
	}

	got, err = s.GetTranscription("NOPE", "x@s.whatsapp.net")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected nil for missing transcription, got %+v", got)
	}
}
