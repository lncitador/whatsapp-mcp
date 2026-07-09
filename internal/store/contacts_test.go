package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/lncitador/whatsapp-mcp/internal/config"
)

// seedWhatsmeowContacts cria um whatsapp.db mínimo com a tabela de contatos.
func seedWhatsmeowContacts(t *testing.T, contacts [][3]string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(config.StoreDir(), "whatsapp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE whatsmeow_contacts (
		our_jid TEXT, their_jid TEXT, first_name TEXT, full_name TEXT,
		push_name TEXT, business_name TEXT, redacted_phone TEXT,
		PRIMARY KEY (our_jid, their_jid))`)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range contacts { // [their_jid, full_name, push_name]
		if _, err := db.Exec(
			"INSERT INTO whatsmeow_contacts (our_jid, their_jid, full_name, push_name) VALUES ('me@s.whatsapp.net', ?, ?, ?)",
			c[0], c[1], c[2]); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSearchContactsFindsByName(t *testing.T) {
	s := openTestStore(t)
	seedWhatsmeowContacts(t, [][3]string{
		{"555596565658@s.whatsapp.net", "Carlos Coord. Suporte", ""},
		{"5511777777777@s.whatsapp.net", "", "Carla Push"},
		{"5511666666666@s.whatsapp.net", "Outra Pessoa", ""},
	})

	got, err := s.SearchContacts("carl")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d contacts: %+v", len(got), got)
	}
	byJID := map[string]Contact{}
	for _, c := range got {
		byJID[c.JID] = c
	}
	if byJID["555596565658@s.whatsapp.net"].Name != "Carlos Coord. Suporte" {
		t.Fatalf("full_name lookup failed: %+v", got)
	}
	if byJID["5511777777777@s.whatsapp.net"].Name != "Carla Push" {
		t.Fatalf("push_name fallback failed: %+v", got)
	}
}

func TestSearchContactsMergesChatsAndDedupes(t *testing.T) {
	s := openTestStore(t)
	s.StoreChat("555596565658@s.whatsapp.net", "Carlos (chat name)", time.Now())
	s.StoreChat("123@g.us", "Grupo do Carlos", time.Now()) // grupo: excluído
	seedWhatsmeowContacts(t, [][3]string{
		{"555596565658@s.whatsapp.net", "Carlos Coord. Suporte", ""},
	})

	got, err := s.SearchContacts("carlos")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 deduped contact, got %+v", got)
	}
	if got[0].Name != "Carlos Coord. Suporte" { // nome do contato vence
		t.Fatalf("got %+v", got[0])
	}
	if got[0].PhoneNumber != "555596565658" {
		t.Fatalf("phone = %q", got[0].PhoneNumber)
	}
}

func TestSearchContactsNoWhatsappDB(t *testing.T) {
	s := openTestStore(t) // sem whatsapp.db
	s.StoreChat("5511999999999@s.whatsapp.net", "Alice", time.Now())
	got, err := s.SearchContacts("alice")
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v err=%v (must degrade to chats-only)", got, err)
	}
}
