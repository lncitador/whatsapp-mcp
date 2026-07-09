package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lncitador/whatsapp-mcp/internal/config"
)

type Contact struct {
	PhoneNumber string `json:"phone_number"`
	Name        string `json:"name,omitempty"`
	JID         string `json:"jid"`
}

// SearchContacts searches both the chat list (legacy behaviour) and
// whatsmeow's contact book, so contacts match by their real names even when
// the chat row only has a phone number.
func (s *Store) SearchContacts(query string) ([]Contact, error) {
	pattern := "%" + query + "%"
	byJID := map[string]Contact{}

	// 1. chats (paridade com o comportamento antigo)
	rows, err := s.db.Query(
		`SELECT DISTINCT jid, IFNULL(name,'') FROM chats
		WHERE (LOWER(name) LIKE LOWER(?) OR LOWER(jid) LIKE LOWER(?))
		AND jid NOT LIKE '%@g.us' LIMIT 50`, pattern, pattern)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var jid, name string
		if err := rows.Scan(&jid, &name); err != nil {
			rows.Close()
			return nil, err
		}
		byJID[jid] = Contact{PhoneNumber: phonePart(jid), Name: name, JID: jid}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 2. whatsmeow_contacts (novo: busca por nome real)
	waPath := filepath.Join(config.StoreDir(), "whatsapp.db")
	if _, err := os.Stat(waPath); err == nil {
		wadb, err := sql.Open("sqlite", "file:"+waPath+"?mode=ro")
		if err == nil {
			defer wadb.Close()
			crows, err := wadb.Query(
				`SELECT their_jid,
					COALESCE(NULLIF(full_name,''), NULLIF(push_name,''), NULLIF(business_name,''), '') AS name
				FROM whatsmeow_contacts
				WHERE LOWER(full_name) LIKE LOWER(?)
				   OR LOWER(push_name) LIKE LOWER(?)
				   OR LOWER(business_name) LIKE LOWER(?)
				   OR their_jid LIKE ?
				LIMIT 50`, pattern, pattern, pattern, pattern)
			if err == nil {
				for crows.Next() {
					var jid, name string
					if err := crows.Scan(&jid, &name); err != nil {
						break
					}
					c := Contact{PhoneNumber: phonePart(jid), Name: name, JID: jid}
					// nome do catálogo de contatos vence o nome do chat
					if prev, ok := byJID[jid]; !ok || (c.Name != "" && c.Name != prev.Name) {
						byJID[jid] = c
					}
				}
				crows.Close()
			}
		}
	}

	out := make([]Contact, 0, len(byJID))
	for _, c := range byJID {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].JID < out[j].JID
	})
	if len(out) > 50 {
		out = out[:50]
	}
	return out, nil
}

func phonePart(jid string) string {
	if i := strings.Index(jid, "@"); i >= 0 {
		return jid[:i]
	}
	return jid
}
