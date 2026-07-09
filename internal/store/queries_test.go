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
	m, _ := s.GetLastInteraction("5511999999999")
	if m == nil || m.ID != "A2" {
		t.Fatalf("got %+v", m)
	}
	if name := s.SenderName("5511999999999@s.whatsapp.net"); name != "Alice" {
		t.Fatalf("SenderName = %q", name)
	}
}
