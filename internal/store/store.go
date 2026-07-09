// Package store owns messages.db (chats + message history) and read-only
// access to whatsmeow's contact table.
package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/lncitador/whatsapp-mcp/internal/config"
)

type Store struct {
	db     *sql.DB
	hasFTS bool
}

type NewMessage struct {
	ID, ChatJID, Sender, Content string
	Timestamp                    time.Time
	IsFromMe                     bool
	MediaType, Filename, URL     string
	MediaKey                     []byte
	FileSHA256, FileEncSHA256    []byte
	FileLength                   uint64
}

type MediaInfo struct {
	MediaType, Filename, URL  string
	MediaKey                  []byte
	FileSHA256, FileEncSHA256 []byte
	FileLength                uint64
}

func Open() (*Store, error) {
	if err := config.EnsureDirs(); err != nil {
		return nil, err
	}
	if _, err := MigrateLegacy(legacyStoreDir(), config.StoreDir()); err != nil {
		return nil, fmt.Errorf("legacy store migration: %w", err)
	}
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
		filepath.Join(config.StoreDir(), "messages.db"))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open messages db: %w", err)
	}
	if _, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS chats (
			jid TEXT PRIMARY KEY,
			name TEXT,
			last_message_time TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS messages (
			id TEXT,
			chat_jid TEXT,
			sender TEXT,
			content TEXT,
			timestamp TIMESTAMP,
			is_from_me BOOLEAN,
			media_type TEXT,
			filename TEXT,
			url TEXT,
			media_key BLOB,
			file_sha256 BLOB,
			file_enc_sha256 BLOB,
			file_length INTEGER,
			PRIMARY KEY (id, chat_jid),
			FOREIGN KEY (chat_jid) REFERENCES chats(jid)
		);
		CREATE INDEX IF NOT EXISTS idx_messages_chat_time ON messages(chat_jid, timestamp DESC);
		CREATE INDEX IF NOT EXISTS idx_messages_sender_time ON messages(sender, timestamp DESC);
		CREATE INDEX IF NOT EXISTS idx_messages_timestamp ON messages(timestamp DESC);
		CREATE INDEX IF NOT EXISTS idx_chats_last_message ON chats(last_message_time DESC);
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("create tables: %w", err)
	}

	if err := setupFTS(db); err != nil {
		fmt.Printf("Warning: FTS5 setup failed (search will use LIKE fallback): %v\n", err)
	}

	return &Store{db: db, hasFTS: hasFTS(db)}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func setupFTS(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
			content, content='messages', content_rowid='rowid',
			tokenize='unicode61 remove_diacritics 2'
		);
	`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
		CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
			INSERT INTO messages_fts(rowid, content) VALUES (new.rowid, new.content);
		END;
		CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
			INSERT INTO messages_fts(messages_fts, rowid, content) VALUES ('delete', old.rowid, old.content);
		END;
		CREATE TRIGGER IF NOT EXISTS messages_au AFTER UPDATE ON messages BEGIN
			INSERT INTO messages_fts(messages_fts, rowid, content) VALUES ('delete', old.rowid, old.content);
			INSERT INTO messages_fts(rowid, content) VALUES (new.rowid, new.content);
		END;
	`)
	if err != nil {
		return err
	}

	_, err = db.Exec("INSERT INTO messages_fts(messages_fts) VALUES ('rebuild')")
	return err
}

func hasFTS(db *sql.DB) bool {
	var name string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='messages_fts'").Scan(&name)
	return err == nil
}

func (s *Store) StoreChat(jid, name string, lastMessageTime time.Time) error {
	_, err := s.db.Exec(
		"INSERT OR REPLACE INTO chats (jid, name, last_message_time) VALUES (?, ?, ?)",
		jid, name, lastMessageTime)
	return err
}

func (s *Store) StoreMessage(m NewMessage) error {
	if m.Content == "" && m.MediaType == "" {
		return nil
	}
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO messages
		(id, chat_jid, sender, content, timestamp, is_from_me, media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.ChatJID, m.Sender, m.Content, m.Timestamp, m.IsFromMe,
		m.MediaType, m.Filename, m.URL, m.MediaKey, m.FileSHA256, m.FileEncSHA256, m.FileLength)
	return err
}

func (s *Store) StoreMediaInfo(id, chatJID, url string, mediaKey, fileSHA256, fileEncSHA256 []byte, fileLength uint64) error {
	_, err := s.db.Exec(
		"UPDATE messages SET url = ?, media_key = ?, file_sha256 = ?, file_enc_sha256 = ?, file_length = ? WHERE id = ? AND chat_jid = ?",
		url, mediaKey, fileSHA256, fileEncSHA256, fileLength, id, chatJID)
	return err
}

func (s *Store) GetMediaInfo(id, chatJID string) (MediaInfo, error) {
	var mi MediaInfo
	err := s.db.QueryRow(
		"SELECT media_type, filename, IFNULL(url,''), media_key, file_sha256, file_enc_sha256, IFNULL(file_length,0) FROM messages WHERE id = ? AND chat_jid = ?",
		id, chatJID,
	).Scan(&mi.MediaType, &mi.Filename, &mi.URL, &mi.MediaKey, &mi.FileSHA256, &mi.FileEncSHA256, &mi.FileLength)
	return mi, err
}
