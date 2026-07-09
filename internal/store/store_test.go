package store

import (
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	t.Setenv("WHATSAPP_MCP_DIR", t.TempDir())
	s, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestStoreChatAndMessageRoundTrip(t *testing.T) {
	s := openTestStore(t)
	ts := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)

	if err := s.StoreChat("5511999999999@s.whatsapp.net", "Alice", ts); err != nil {
		t.Fatal(err)
	}
	err := s.StoreMessage(NewMessage{
		ID: "MSG1", ChatJID: "5511999999999@s.whatsapp.net", Sender: "5511999999999",
		Content: "hello", Timestamp: ts, IsFromMe: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	// content vazio e sem mídia não é gravado (paridade com bridge atual)
	if err := s.StoreMessage(NewMessage{ID: "MSG2", ChatJID: "x@s.whatsapp.net", Timestamp: ts}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("messages = %d, want 1 (empty message must be skipped)", n)
	}
}

func TestMediaInfoRoundTrip(t *testing.T) {
	s := openTestStore(t)
	ts := time.Now()
	s.StoreChat("c@s.whatsapp.net", "C", ts)
	s.StoreMessage(NewMessage{ID: "M1", ChatJID: "c@s.whatsapp.net", Sender: "c",
		Timestamp: ts, MediaType: "image", Filename: "img.jpg"})
	if err := s.StoreMediaInfo("M1", "c@s.whatsapp.net", "https://u", []byte{1}, []byte{2}, []byte{3}, 42); err != nil {
		t.Fatal(err)
	}
	mi, err := s.GetMediaInfo("M1", "c@s.whatsapp.net")
	if err != nil {
		t.Fatal(err)
	}
	if mi.MediaType != "image" || mi.URL != "https://u" || mi.FileLength != 42 {
		t.Fatalf("unexpected media info: %+v", mi)
	}
}

func TestScanTimeAcceptsMattnFormats(t *testing.T) {
	// formatos gravados pelo driver antigo (mattn/go-sqlite3)
	for _, raw := range []string{
		"2026-07-08 12:00:00.123456789-03:00",
		"2026-07-08 12:00:00+00:00",
		"2026-07-08T12:00:00Z",
	} {
		if _, err := scanTime(raw); err != nil {
			t.Errorf("scanTime(%q): %v", raw, err)
		}
	}
	if _, err := scanTime(time.Now()); err != nil {
		t.Errorf("scanTime(time.Time): %v", err)
	}
}
