package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type Message struct {
	Timestamp  time.Time `json:"timestamp"`
	Sender     string    `json:"sender"`
	SenderName string    `json:"sender_name,omitempty"`
	ChatName   string    `json:"chat_name,omitempty"`
	Content    string    `json:"content"`
	IsFromMe   bool      `json:"is_from_me"`
	ChatJID    string    `json:"chat_jid"`
	ID         string    `json:"id"`
	MediaType  string    `json:"media_type,omitempty"`
}

type Chat struct {
	JID              string     `json:"jid"`
	Name             string     `json:"name,omitempty"`
	LastMessageTime  *time.Time `json:"last_message_time,omitempty"`
	LastMessage      string     `json:"last_message,omitempty"`
	LastSender       string     `json:"last_sender,omitempty"`
	LastSenderName   string     `json:"last_sender_name,omitempty"`
	LastIsFromMe     bool       `json:"last_is_from_me,omitempty"`
}

type MessageContext struct {
	Message Message   `json:"message"`
	Before  []Message `json:"before"`
	After   []Message `json:"after"`
}

type ListMessagesArgs struct {
	After, Before              time.Time
	SenderPhoneNumber, ChatJID string
	Query                      string
	Limit, Page                int
}

const messageCols = `messages.timestamp, messages.sender, IFNULL(chats.name,''), IFNULL(messages.content,''),
	messages.is_from_me, chats.jid, messages.id, IFNULL(messages.media_type,'')`

func scanMessage(rows interface{ Scan(...any) error }) (Message, error) {
	var m Message
	var ts any
	if err := rows.Scan(&ts, &m.Sender, &m.ChatName, &m.Content, &m.IsFromMe, &m.ChatJID, &m.ID, &m.MediaType); err != nil {
		return m, err
	}
	var err error
	m.Timestamp, err = scanTime(ts)
	return m, err
}

func (s *Store) ListMessages(a ListMessagesArgs) ([]Message, error) {
	if a.Limit <= 0 {
		a.Limit = 20
	}
	if a.Limit > 100 {
		a.Limit = 100
	}
	if a.Page < 0 {
		a.Page = 0
	}
	q := []string{"SELECT " + messageCols + " FROM messages JOIN chats ON messages.chat_jid = chats.jid"}
	var where []string
	var params []any
	if !a.After.IsZero() {
		where, params = append(where, "messages.timestamp > ?"), append(params, a.After)
	}
	if !a.Before.IsZero() {
		where, params = append(where, "messages.timestamp < ?"), append(params, a.Before)
	}
	if a.SenderPhoneNumber != "" {
		where, params = append(where, "messages.sender = ?"), append(params, a.SenderPhoneNumber)
	}
	if a.ChatJID != "" {
		where, params = append(where, "messages.chat_jid = ?"), append(params, a.ChatJID)
	}
	if a.Query != "" {
		if s.hasFTS {
			q[0] = "SELECT " + messageCols + " FROM messages JOIN chats ON messages.chat_jid = chats.jid JOIN messages_fts ON messages.rowid = messages_fts.rowid"
			where = append(where, "messages_fts MATCH ?")
			params = append(params, `"`+a.Query+`"`)
		} else {
			where, params = append(where, "LOWER(messages.content) LIKE LOWER(?)"), append(params, "%"+a.Query+"%")
		}
	}
	if len(where) > 0 {
		q = append(q, "WHERE "+strings.Join(where, " AND "))
	}
	q = append(q, "ORDER BY messages.timestamp DESC LIMIT ? OFFSET ?")
	params = append(params, a.Limit, a.Page*a.Limit)

	rows, err := s.db.Query(strings.Join(q, " "), params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) GetMessageContext(messageID string, before, after int) (MessageContext, error) {
	var mc MessageContext
	row := s.db.QueryRow("SELECT "+messageCols+" FROM messages JOIN chats ON messages.chat_jid = chats.jid WHERE messages.id = ?", messageID)
	m, err := scanMessage(row)
	if err == sql.ErrNoRows {
		return mc, fmt.Errorf("message with ID %s not found", messageID)
	}
	if err != nil {
		return mc, err
	}
	mc.Message = m

	fetch := func(cmp, order string, limit int) ([]Message, error) {
		rows, err := s.db.Query(
			"SELECT "+messageCols+" FROM messages JOIN chats ON messages.chat_jid = chats.jid"+
				" WHERE messages.chat_jid = ? AND messages.timestamp "+cmp+" ? ORDER BY messages.timestamp "+order+" LIMIT ?",
			m.ChatJID, m.Timestamp, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []Message
		for rows.Next() {
			mm, err := scanMessage(rows)
			if err != nil {
				return nil, err
			}
			out = append(out, mm)
		}
		return out, rows.Err()
	}
	if mc.Before, err = fetch("<", "DESC", before); err != nil {
		return mc, err
	}
	mc.After, err = fetch(">", "ASC", after)
	return mc, err
}

const chatCols = `c.jid, IFNULL(c.name,''), c.last_message_time,
	IFNULL(m.content,''), IFNULL(m.sender,''), IFNULL(m.is_from_me,0)`

const chatLastMsgJoin = ` LEFT JOIN messages m ON c.jid = m.chat_jid AND c.last_message_time = m.timestamp`

func scanChat(rows interface{ Scan(...any) error }) (Chat, error) {
	var c Chat
	var ts any
	if err := rows.Scan(&c.JID, &c.Name, &ts, &c.LastMessage, &c.LastSender, &c.LastIsFromMe); err != nil {
		return c, err
	}
	if ts != nil {
		t, err := scanTime(ts)
		if err != nil {
			return c, err
		}
		c.LastMessageTime = &t
	}
	return c, nil
}

func (s *Store) ListChats(query string, limit, page int, includeLastMessage bool, sortBy string) ([]Chat, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if page < 0 {
		page = 0
	}
	q := "SELECT " + chatCols + " FROM chats c"
	if includeLastMessage {
		q += chatLastMsgJoin
	} else {
		q = "SELECT c.jid, IFNULL(c.name,''), c.last_message_time, '', '', 0 FROM chats c"
	}
	var params []any
	if query != "" {
		q += " WHERE (LOWER(c.name) LIKE LOWER(?) OR c.jid LIKE ?)"
		params = append(params, "%"+query+"%", "%"+query+"%")
	}
	if sortBy == "name" {
		q += " ORDER BY c.name"
	} else {
		q += " ORDER BY c.last_message_time DESC"
	}
	q += " LIMIT ? OFFSET ?"
	params = append(params, limit, page*limit)

	rows, err := s.db.Query(q, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chat
	for rows.Next() {
		c, err := scanChat(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetChat(chatJID string, includeLastMessage bool) (*Chat, error) {
	q := "SELECT " + chatCols + " FROM chats c"
	if includeLastMessage {
		q += chatLastMsgJoin
	} else {
		q = "SELECT c.jid, IFNULL(c.name,''), c.last_message_time, '', '', 0 FROM chats c"
	}
	q += " WHERE c.jid = ?"
	c, err := scanChat(s.db.QueryRow(q, chatJID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) GetDirectChatByContact(phone string) (*Chat, error) {
	c, err := scanChat(s.db.QueryRow(
		"SELECT "+chatCols+" FROM chats c"+chatLastMsgJoin+
			" WHERE c.jid LIKE ? AND c.jid NOT LIKE '%@g.us' LIMIT 1", "%"+phone+"%"))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) GetContactChats(jid string, limit, page int) ([]Chat, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if page < 0 {
		page = 0
	}
	rows, err := s.db.Query(
		`SELECT DISTINCT c.jid, IFNULL(c.name,''), c.last_message_time,
			IFNULL(m.content,''), IFNULL(m.sender,''), IFNULL(m.is_from_me,0)
		FROM chats c JOIN messages m ON c.jid = m.chat_jid
		WHERE m.sender = ? OR c.jid = ?
		ORDER BY c.last_message_time DESC LIMIT ? OFFSET ?`,
		jid, jid, limit, page*limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chat
	for rows.Next() {
		c, err := scanChat(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetLastInteraction(jid string) (*Message, error) {
	row := s.db.QueryRow(
		"SELECT "+messageCols+" FROM messages JOIN chats ON messages.chat_jid = chats.jid"+
			" WHERE messages.sender = ? OR chats.jid = ? ORDER BY messages.timestamp DESC LIMIT 1",
		jid, jid)
	m, err := scanMessage(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// SenderName mirrors whatsapp.py get_sender_name: exact chat JID first, then
// LIKE on the phone part, falling back to the JID itself.
func (s *Store) SenderName(senderJID string) string {
	var name string
	err := s.db.QueryRow("SELECT IFNULL(name,'') FROM chats WHERE jid = ? LIMIT 1", senderJID).Scan(&name)
	if err == nil && name != "" {
		return name
	}
	phone := senderJID
	if i := strings.Index(senderJID, "@"); i >= 0 {
		phone = senderJID[:i]
	}
	err = s.db.QueryRow("SELECT IFNULL(name,'') FROM chats WHERE jid LIKE ? LIMIT 1", "%"+phone+"%").Scan(&name)
	if err == nil && name != "" {
		return name
	}
	return senderJID
}

// ChatName returns the stored name for a chat, or "" when unknown.
func (s *Store) ChatName(jid string) string {
	var name string
	if err := s.db.QueryRow("SELECT IFNULL(name,'') FROM chats WHERE jid = ?", jid).Scan(&name); err != nil {
		return ""
	}
	return name
}
