# whatsapp-mcp Go Single-Binary Rewrite — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Substituir o par bridge-Go + MCP-server-Python por um único binário Go `whatsapp-mcp` (subcomandos `serve`/`stdio`/`status`/`stop`), com `search_contacts` buscando por nome, tool `auth_status` com QR, e pipeline de release (GoReleaser + install.sh) no fork `lncitador/whatsapp-mcp`.

**Architecture:** Daemon (`serve`) mantém a sessão whatsmeow, SQLite e um HTTP local (`127.0.0.1:8080`) com endpoints RPC genéricos (`/api/rpc/<tool>`) + `/health` + `/status` + `/shutdown`. O proxy (`stdio`) é o servidor MCP que o cliente spawna: valida schema, encaminha cada tool ao daemon via HTTP e auto-inicia o daemon se ausente. Toda lógica de dados/WhatsApp vive no daemon; o proxy não abre SQLite.

**Tech Stack:** Go (mesma versão do go.mod atual), `go.mau.fi/whatsmeow`, `modernc.org/sqlite` (Go puro, sem CGO), `github.com/modelcontextprotocol/go-sdk/mcp`, `github.com/mdp/qrterminal`, GoReleaser, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-07-08-go-rewrite-single-binary-design.md`

## Global Constraints

- Module path: `github.com/lncitador/whatsapp-mcp`. Binário: `whatsapp-mcp`.
- SQLite **somente** via `modernc.org/sqlite` (driver name `"sqlite"`). `mattn/go-sqlite3` é proibido; todo build usa `CGO_ENABLED=0`.
- MCP SDK: `github.com/modelcontextprotocol/go-sdk/mcp`. Padrão de tool: `mcp.AddTool(server, &mcp.Tool{Name, Description}, handler)` com handler `func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error)`.
- Diretório de dados: `~/.whatsapp-mcp` (override: env `WHATSAPP_MCP_DIR` — usado nos testes). Subdirs: `store/`, `logs/`, `media/`.
- Porta HTTP: `8080`; override por env `WHATSAPP_MCP_PORT`, depois `~/.whatsapp-mcp/config.json` (`{"port": N}`). Bind **sempre** `127.0.0.1`.
- Nada de `requestHistorySync`/gap recovery (whatsmeow `BuildHistorySyncRequest(nil, …)` panica — fora de escopo, ver spec).
- Timestamps: o store antigo foi escrito pelo driver mattn (strings `2006-01-02 15:04:05.999999999-07:00`). Toda leitura de timestamp usa o helper `scanTime` (Task 2) — nunca `rows.Scan(&t time.Time)` direto em coluna TIMESTAMP.
- Código movido do `whatsapp-bridge/main.go` é referenciado por intervalo de linhas do arquivo **no commit atual (`7b1c17d`)**; não deletar `whatsapp-bridge/` antes da Task 13.
- Commits frequentes, formato Conventional Commits. Testes: `go test ./...` na raiz.

---

### Task 1: Bootstrap do módulo raiz + `internal/config`

**Files:**
- Create: `go.mod` (raiz), `go.sum` (raiz)
- Create: `cmd/whatsapp-mcp/main.go`
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`
- Modify: `.gitignore`

**Interfaces:**
- Consumes: nada (primeira task).
- Produces: `config.BaseDir() string`, `config.StoreDir() string`, `config.LogsDir() string`, `config.MediaDir() string`, `config.Port() int`, `config.BaseURL() string`, `config.PIDFile() string`, `config.LockFile() string`, `config.EnsureDirs() error`. `main.go` com dispatch de subcomandos e `var version = "dev"` (ldflags).

- [ ] **Step 1: Criar go.mod raiz**

```bash
cd /Users/lncitador/Projects/whatsapp-mcp
go mod init github.com/lncitador/whatsapp-mcp
```

O `whatsapp-bridge/go.mod` continua existindo até a Task 13 (dois módulos coexistem; o antigo não é tocado).

- [ ] **Step 2: Escrever teste falhando de config**

`internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBaseDirRespectsOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WHATSAPP_MCP_DIR", dir)
	if got := BaseDir(); got != dir {
		t.Fatalf("BaseDir() = %q, want %q", got, dir)
	}
	if got := StoreDir(); got != filepath.Join(dir, "store") {
		t.Fatalf("StoreDir() = %q", got)
	}
}

func TestBaseDirDefaultsToHome(t *testing.T) {
	t.Setenv("WHATSAPP_MCP_DIR", "")
	home, _ := os.UserHomeDir()
	if got := BaseDir(); got != filepath.Join(home, ".whatsapp-mcp") {
		t.Fatalf("BaseDir() = %q", got)
	}
}

func TestPortPrecedence(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WHATSAPP_MCP_DIR", dir)

	t.Setenv("WHATSAPP_MCP_PORT", "")
	if got := Port(); got != 8080 {
		t.Fatalf("default Port() = %d, want 8080", got)
	}

	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"port": 9001}`), 0644)
	if got := Port(); got != 9001 {
		t.Fatalf("config.json Port() = %d, want 9001", got)
	}

	t.Setenv("WHATSAPP_MCP_PORT", "9002")
	if got := Port(); got != 9002 {
		t.Fatalf("env Port() = %d, want 9002", got)
	}
}

func TestEnsureDirs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WHATSAPP_MCP_DIR", dir)
	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{StoreDir(), LogsDir(), MediaDir()} {
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			t.Fatalf("missing dir %s: %v", d, err)
		}
	}
}
```

- [ ] **Step 3: Rodar e ver falhar**

Run: `go test ./internal/config/`
Expected: FAIL (package não existe / funções não definidas)

- [ ] **Step 4: Implementar `internal/config/config.go`**

```go
// Package config resolves whatsapp-mcp's on-disk layout and local HTTP address.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

func BaseDir() string {
	if dir := os.Getenv("WHATSAPP_MCP_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".whatsapp-mcp"
	}
	return filepath.Join(home, ".whatsapp-mcp")
}

func StoreDir() string { return filepath.Join(BaseDir(), "store") }
func LogsDir() string  { return filepath.Join(BaseDir(), "logs") }
func MediaDir() string { return filepath.Join(BaseDir(), "media") }

func PIDFile() string  { return filepath.Join(BaseDir(), "daemon.pid") }
func LockFile() string { return filepath.Join(BaseDir(), "daemon.lock") }

type fileConfig struct {
	Port int `json:"port"`
}

// Port precedence: WHATSAPP_MCP_PORT env > config.json > 8080.
func Port() int {
	if v := os.Getenv("WHATSAPP_MCP_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			return p
		}
	}
	data, err := os.ReadFile(filepath.Join(BaseDir(), "config.json"))
	if err == nil {
		var fc fileConfig
		if json.Unmarshal(data, &fc) == nil && fc.Port > 0 {
			return fc.Port
		}
	}
	return 8080
}

func BaseURL() string { return fmt.Sprintf("http://127.0.0.1:%d", Port()) }

func EnsureDirs() error {
	for _, d := range []string{BaseDir(), StoreDir(), LogsDir(), MediaDir()} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 5: Rodar testes**

Run: `go test ./internal/config/`
Expected: PASS

- [ ] **Step 6: Criar `cmd/whatsapp-mcp/main.go` (dispatch mínimo)**

```go
package main

import (
	"fmt"
	"os"
)

// version is stamped by GoReleaser via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		fmt.Fprintln(os.Stderr, "serve: not implemented yet")
		os.Exit(1)
	case "stdio":
		fmt.Fprintln(os.Stderr, "stdio: not implemented yet")
		os.Exit(1)
	case "status":
		fmt.Fprintln(os.Stderr, "status: not implemented yet")
		os.Exit(1)
	case "stop":
		fmt.Fprintln(os.Stderr, "stop: not implemented yet")
		os.Exit(1)
	case "--version", "version":
		fmt.Println("whatsapp-mcp", version)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: whatsapp-mcp <command>

commands:
  serve    run the WhatsApp daemon (session + local HTTP API)
  stdio    run the MCP stdio proxy (spawned by MCP clients; auto-starts serve)
  status   show daemon/connection status
  stop     stop the daemon
  version  print version`)
}
```

- [ ] **Step 7: Build + smoke**

Run: `go build ./... && go run ./cmd/whatsapp-mcp --version`
Expected: `whatsapp-mcp dev`

- [ ] **Step 8: `.gitignore` — adicionar na raiz**

Acrescentar linhas (manter conteúdo existente):

```
/whatsapp-mcp
dist/
```

- [ ] **Step 9: Commit**

```bash
git add go.mod go.sum cmd/ internal/config/ .gitignore
git commit -m "feat: bootstrap root Go module with subcommand skeleton and config package"
```

---

### Task 2: `internal/store` — schema, escrita e migração do store antigo

**Files:**
- Create: `internal/store/store.go`
- Create: `internal/store/time.go`
- Create: `internal/store/migrate.go`
- Test: `internal/store/store_test.go`, `internal/store/migrate_test.go`

**Interfaces:**
- Consumes: `config.StoreDir()`.
- Produces:
  - `store.Open() (*store.Store, error)` — abre/cria `<StoreDir>/messages.db` (roda migração antes).
  - `(*Store).Close() error`
  - `(*Store).StoreChat(jid, name string, lastMessageTime time.Time) error`
  - `(*Store).StoreMessage(m store.NewMessage) error` (struct abaixo)
  - `(*Store).StoreMediaInfo(id, chatJID, url string, mediaKey, fileSHA256, fileEncSHA256 []byte, fileLength uint64) error`
  - `(*Store).GetMediaInfo(id, chatJID string) (store.MediaInfo, error)`
  - `store.scanTime(v any) (time.Time, error)` (interno, usado nas Tasks 3-4)
  - `store.MigrateLegacy(legacyDir, storeDir string) (migrated bool, err error)`

- [ ] **Step 1: Teste falhando — round-trip de escrita**

`internal/store/store_test.go`:

```go
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
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/store/`
Expected: FAIL (tipos não definidos)

- [ ] **Step 3: Implementar `internal/store/time.go`**

```go
package store

import (
	"fmt"
	"time"
)

// scanTime tolerates every timestamp representation found in the wild:
// values written by this binary (time.Time via modernc) and legacy rows
// written by mattn/go-sqlite3 (space-separated strings with offset).
func scanTime(v any) (time.Time, error) {
	switch t := v.(type) {
	case time.Time:
		return t, nil
	case []byte:
		return parseTimeString(string(t))
	case string:
		return parseTimeString(t)
	case nil:
		return time.Time{}, nil
	default:
		return time.Time{}, fmt.Errorf("unsupported time type %T", v)
	}
}

var timeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05-07:00",
	"2006-01-02 15:04:05",
}

func parseTimeString(s string) (time.Time, error) {
	for _, l := range timeLayouts {
		if ts, err := time.Parse(l, s); err == nil {
			return ts, nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable timestamp %q", s)
}
```

- [ ] **Step 4: Implementar `internal/store/store.go`**

Schema idêntico ao atual (`whatsapp-bridge/main.go:63-87`), driver trocado:

```go
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
	db *sql.DB
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
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("create tables: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

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
```

- [ ] **Step 5: Teste falhando de migração**

`internal/store/migrate_test.go`:

```go
package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateLegacyCopiesOnce(t *testing.T) {
	legacy := t.TempDir()
	dest := t.TempDir()
	os.WriteFile(filepath.Join(legacy, "messages.db"), []byte("legacy-messages"), 0644)
	os.WriteFile(filepath.Join(legacy, "whatsapp.db"), []byte("legacy-session"), 0644)

	migrated, err := MigrateLegacy(legacy, dest)
	if err != nil || !migrated {
		t.Fatalf("migrated=%v err=%v", migrated, err)
	}
	got, _ := os.ReadFile(filepath.Join(dest, "whatsapp.db"))
	if string(got) != "legacy-session" {
		t.Fatalf("whatsapp.db not copied")
	}

	// segunda chamada: destino não-vazio -> no-op
	os.WriteFile(filepath.Join(legacy, "messages.db"), []byte("changed"), 0644)
	migrated, err = MigrateLegacy(legacy, dest)
	if err != nil || migrated {
		t.Fatalf("second run migrated=%v err=%v, want false,nil", migrated, err)
	}
}

func TestMigrateLegacyNoSource(t *testing.T) {
	migrated, err := MigrateLegacy(filepath.Join(t.TempDir(), "nope"), t.TempDir())
	if err != nil || migrated {
		t.Fatalf("migrated=%v err=%v, want false,nil", migrated, err)
	}
}
```

- [ ] **Step 6: Implementar `internal/store/migrate.go`**

```go
package store

import (
	"io"
	"log"
	"os"
	"path/filepath"
)

// legacyStoreDir is where the pre-rewrite bridge kept its databases:
// ./whatsapp-bridge/store relative to the process working directory.
func legacyStoreDir() string {
	return filepath.Join("whatsapp-bridge", "store")
}

// MigrateLegacy copies messages.db/whatsapp.db (and -wal/-shm siblings) from
// legacyDir into storeDir when storeDir has no databases yet. It preserves
// the WhatsApp session so users don't re-scan the QR after upgrading.
func MigrateLegacy(legacyDir, storeDir string) (bool, error) {
	if _, err := os.Stat(filepath.Join(storeDir, "whatsapp.db")); err == nil {
		return false, nil // já migrado / já em uso
	}
	if _, err := os.Stat(filepath.Join(legacyDir, "whatsapp.db")); err != nil {
		return false, nil // nada para migrar
	}
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		return false, err
	}
	entries, err := os.ReadDir(legacyDir)
	if err != nil {
		return false, err
	}
	copied := false
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := copyFile(filepath.Join(legacyDir, e.Name()), filepath.Join(storeDir, e.Name())); err != nil {
			return copied, err
		}
		copied = true
	}
	if copied {
		log.Printf("migrated legacy store from %s to %s", legacyDir, storeDir)
	}
	return copied, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
```

- [ ] **Step 7: Rodar testes**

Run: `go test ./internal/store/`
Expected: PASS (todos)

- [ ] **Step 8: Commit**

```bash
git add internal/store/ go.mod go.sum
git commit -m "feat: message store on modernc sqlite with legacy-store migration"
```

---

### Task 3: `internal/store` — queries de leitura das tools

**Files:**
- Create: `internal/store/queries.go`
- Test: `internal/store/queries_test.go`

**Interfaces:**
- Consumes: `*Store` e `scanTime` (Task 2).
- Produces (assinaturas exatas — a Task 7/9 depende delas):

```go
type Message struct {
	Timestamp time.Time `json:"timestamp"`
	Sender    string    `json:"sender"`
	ChatName  string    `json:"chat_name,omitempty"`
	Content   string    `json:"content"`
	IsFromMe  bool      `json:"is_from_me"`
	ChatJID   string    `json:"chat_jid"`
	ID        string    `json:"id"`
	MediaType string    `json:"media_type,omitempty"`
}

type Chat struct {
	JID             string     `json:"jid"`
	Name            string     `json:"name,omitempty"`
	LastMessageTime *time.Time `json:"last_message_time,omitempty"`
	LastMessage     string     `json:"last_message,omitempty"`
	LastSender      string     `json:"last_sender,omitempty"`
	LastIsFromMe    bool       `json:"last_is_from_me,omitempty"`
}

type MessageContext struct {
	Message Message   `json:"message"`
	Before  []Message `json:"before"`
	After   []Message `json:"after"`
}

type ListMessagesArgs struct {
	After, Before                time.Time // zero = sem filtro
	SenderPhoneNumber, ChatJID   string
	Query                        string
	Limit, Page                  int
}

func (s *Store) ListMessages(a ListMessagesArgs) ([]Message, error)
func (s *Store) GetMessageContext(messageID string, before, after int) (MessageContext, error)
func (s *Store) ListChats(query string, limit, page int, includeLastMessage bool, sortBy string) ([]Chat, error)
func (s *Store) GetChat(chatJID string, includeLastMessage bool) (*Chat, error)      // nil se não achar
func (s *Store) GetDirectChatByContact(phone string) (*Chat, error)                  // nil se não achar
func (s *Store) GetContactChats(jid string, limit, page int) ([]Chat, error)
func (s *Store) GetLastInteraction(jid string) (*Message, error)                     // nil se não achar
func (s *Store) SenderName(senderJID string) string
```

Semântica = port fiel das queries de `whatsapp-mcp-server/whatsapp.py`: `list_messages` (linhas 124-223), `get_message_context` (226-316), `list_chats` (319-390), `get_chat` (535-580), `get_direct_chat_by_contact` (583-623), `get_contact_chats` (435-483), `get_last_interaction` (486-532), `get_sender_name` (50-92). Mesmos JOINs, mesmos filtros (`LOWER(content) LIKE`), mesma paginação `LIMIT ? OFFSET page*limit`, mesma ordenação.

- [ ] **Step 1: Teste falhando com fixture**

`internal/store/queries_test.go`:

```go
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
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/store/ -run 'TestList|TestGet|TestContact'`
Expected: FAIL (funções não definidas)

- [ ] **Step 3: Implementar `internal/store/queries.go`**

```go
package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type Message struct {
	Timestamp time.Time `json:"timestamp"`
	Sender    string    `json:"sender"`
	ChatName  string    `json:"chat_name,omitempty"`
	Content   string    `json:"content"`
	IsFromMe  bool      `json:"is_from_me"`
	ChatJID   string    `json:"chat_jid"`
	ID        string    `json:"id"`
	MediaType string    `json:"media_type,omitempty"`
}

type Chat struct {
	JID             string     `json:"jid"`
	Name            string     `json:"name,omitempty"`
	LastMessageTime *time.Time `json:"last_message_time,omitempty"`
	LastMessage     string     `json:"last_message,omitempty"`
	LastSender      string     `json:"last_sender,omitempty"`
	LastIsFromMe    bool       `json:"last_is_from_me,omitempty"`
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
		where, params = append(where, "LOWER(messages.content) LIKE LOWER(?)"), append(params, "%"+a.Query+"%")
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
```

Nota: teste `TestListMessagesFilters` usa sender JID `5511999999999@s.whatsapp.net` no `SenderName` — o seed grava chat com esse JID, então o primeiro `SELECT` por JID exato não bate (sender ≠ chat jid completo? bate sim: chat jid = `5511999999999@s.whatsapp.net`). Comportamento idêntico ao Python.

- [ ] **Step 4: Rodar testes**

Run: `go test ./internal/store/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/
git commit -m "feat: port read queries for all MCP tools to Go store layer"
```

---

### Task 4: `internal/store` — `SearchContacts` com `whatsmeow_contacts`

**Files:**
- Create: `internal/store/contacts.go`
- Test: `internal/store/contacts_test.go`

**Interfaces:**
- Consumes: `*Store` (Task 2), `config.StoreDir()`.
- Produces:

```go
type Contact struct {
	PhoneNumber string `json:"phone_number"`
	Name        string `json:"name,omitempty"`
	JID         string `json:"jid"`
}

func (s *Store) SearchContacts(query string) ([]Contact, error)
```

Comportamento: união de (a) busca atual em `chats` (nome/jid, excluindo grupos — paridade com `whatsapp.py:393-432`) e (b) **novo**: busca em `whatsmeow_contacts` de `<StoreDir>/whatsapp.db` por `full_name`/`push_name`/`business_name`/`their_jid`. Dedupe por JID; nome do contato (whatsmeow) vence nome do chat. Ordenação por nome, limite 50. Schema real da tabela (validado em produção): `whatsmeow_contacts(our_jid, their_jid, first_name, full_name, push_name, business_name, redacted_phone)`.

- [ ] **Step 1: Teste falhando**

`internal/store/contacts_test.go`:

```go
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
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/store/ -run TestSearchContacts`
Expected: FAIL

- [ ] **Step 3: Implementar `internal/store/contacts.go`**

```go
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
```

- [ ] **Step 4: Rodar testes**

Run: `go test ./internal/store/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/contacts.go internal/store/contacts_test.go
git commit -m "feat: search_contacts matches names via whatsmeow_contacts"
```

---

### Task 5: `internal/audio` — ffmpeg + análise Ogg Opus

**Files:**
- Create: `internal/audio/convert.go`
- Create: `internal/audio/ogg.go`
- Test: `internal/audio/convert_test.go`

**Interfaces:**
- Consumes: nada interno.
- Produces:
  - `audio.ConvertToOpusOggTemp(inputPath string) (string, error)` — converte para `.ogg` opus em arquivo temp (`bitrate 32k`, `sample rate 24000`, paridade com `whatsapp-mcp-server/audio.py:64-110`); erro claro quando `ffmpeg` ausente.
  - `audio.AnalyzeOggOpus(data []byte) (durationSeconds uint32, waveform []byte, err error)` — movido de `whatsapp-bridge/main.go:1185-1289`.
  - `audio.PlaceholderWaveform(duration uint32) []byte` — movido de `whatsapp-bridge/main.go:1301-1346` (junto com o helper `min`, linhas 1292-1297).

- [ ] **Step 1: Teste falhando**

`internal/audio/convert_test.go`:

```go
package audio

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestAnalyzeOggOpusRejectsGarbage(t *testing.T) {
	if _, _, err := AnalyzeOggOpus([]byte("not an ogg")); err == nil {
		t.Fatal("want error for non-ogg data")
	}
}

func TestPlaceholderWaveformShape(t *testing.T) {
	wf := PlaceholderWaveform(30)
	if len(wf) != 64 {
		t.Fatalf("len = %d, want 64", len(wf))
	}
	for i, v := range wf {
		if v > 100 {
			t.Fatalf("waveform[%d] = %d out of 0-100", i, v)
		}
	}
}

func TestConvertMissingInput(t *testing.T) {
	if _, err := ConvertToOpusOggTemp("/nonexistent/file.mp3"); err == nil {
		t.Fatal("want error for missing input")
	}
}

// Integração real só quando ffmpeg existe no PATH.
func TestConvertWithFfmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	// gera 1s de silêncio wav via ffmpeg e converte
	in := t.TempDir() + "/in.wav"
	if out, err := exec.Command("ffmpeg", "-f", "lavfi", "-i", "anullsrc=r=24000:cl=mono", "-t", "1", in).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
	got, err := ConvertToOpusOggTemp(in)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(got)
	if !strings.HasSuffix(got, ".ogg") {
		t.Fatalf("output %q not .ogg", got)
	}
	data, _ := os.ReadFile(got)
	if len(data) < 4 || string(data[:4]) != "OggS" {
		t.Fatal("output is not an Ogg file")
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/audio/`
Expected: FAIL

- [ ] **Step 3: Implementar**

`internal/audio/ogg.go`: copiar de `whatsapp-bridge/main.go` as funções `analyzeOggOpus` (linhas 1185-1289), `min` (1292-1297) e `placeholderWaveform` (1301-1346) **sem alteração de lógica**, com estas mudanças mecânicas:
- `package audio`; exportar como `AnalyzeOggOpus` e `PlaceholderWaveform`.
- Trocar os `fmt.Printf`/`fmt.Println` de debug por nada (deletar as linhas) — daemon loga via logger próprio, não stdout.
- Substituir `rand.Seed(int64(duration))` + `rand.Float64()` por gerador local: `r := rand.New(rand.NewSource(int64(duration)))` e `r.Float64()` (rand.Seed é deprecated).

`internal/audio/convert.go`:

```go
// Package audio shells out to ffmpeg for voice-note conversion, mirroring the
// old Python audio.py behaviour.
package audio

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ConvertToOpusOggTemp converts inputPath to a temporary .ogg (opus, 32k,
// 24000 Hz) and returns the temp file path. Caller removes the file.
func ConvertToOpusOggTemp(inputPath string) (string, error) {
	if _, err := os.Stat(inputPath); err != nil {
		return "", fmt.Errorf("input file not found: %s", inputPath)
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return "", fmt.Errorf("ffmpeg not found in PATH — install ffmpeg or provide an .ogg opus file")
	}
	base := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	tmp, err := os.CreateTemp("", base+"-*.ogg")
	if err != nil {
		return "", err
	}
	tmp.Close()
	cmd := exec.Command("ffmpeg", "-y",
		"-i", inputPath,
		"-c:a", "libopus",
		"-b:a", "32k",
		"-ar", "24000",
		"-application", "voip",
		tmp.Name())
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("ffmpeg conversion failed: %v: %s", err, out)
	}
	return tmp.Name(), nil
}
```

- [ ] **Step 4: Rodar testes**

Run: `go test ./internal/audio/`
Expected: PASS (TestConvertWithFfmpeg roda se ffmpeg local; senão SKIP)

- [ ] **Step 5: Commit**

```bash
git add internal/audio/
git commit -m "feat: audio package with ffmpeg conversion and ogg opus analysis"
```

---

### Task 6: `internal/wa` — cliente WhatsApp com estado de auth/QR

**Files:**
- Create: `internal/wa/client.go`
- Create: `internal/wa/handlers.go`
- Create: `internal/wa/send.go`
- Create: `internal/wa/download.go`
- Test: `internal/wa/client_test.go` (estado apenas; sem rede)

**Interfaces:**
- Consumes: `store.Store` (Tasks 2-3), `audio.AnalyzeOggOpus`/`PlaceholderWaveform` (Task 5), `config.StoreDir()`/`MediaDir()`.
- Produces:

```go
type AuthState string

const (
	AuthConnected  AuthState = "connected"
	AuthWaitingQR  AuthState = "waiting_qr"
	AuthLoggedOut  AuthState = "logged_out"
	AuthConnecting AuthState = "connecting"
)

type Status struct {
	State   AuthState `json:"state"`
	QRCode  string    `json:"qr_code,omitempty"`  // raw code para render
	Message string    `json:"message,omitempty"`
}

func New(st *store.Store) (*Client, error)          // monta whatsmeow + handlers
func (c *Client) Start(ctx context.Context) error   // conecta; gerencia QR em background
func (c *Client) Stop()
func (c *Client) Status() Status
func (c *Client) SendMessage(recipient, message, mediaPath string) (bool, string)
func (c *Client) DownloadMedia(messageID, chatJID string) (path, mediaType, filename string, err error)
```

- [ ] **Step 1: Teste de estado (falhando)**

`internal/wa/client_test.go`:

```go
package wa

import "testing"

func TestStatusTransitions(t *testing.T) {
	c := &Client{}
	c.setState(AuthConnecting, "", "")
	if s := c.Status(); s.State != AuthConnecting {
		t.Fatalf("state = %s", s.State)
	}
	c.setState(AuthWaitingQR, "QRDATA", "scan me")
	s := c.Status()
	if s.State != AuthWaitingQR || s.QRCode != "QRDATA" || s.Message != "scan me" {
		t.Fatalf("got %+v", s)
	}
	// conectar limpa o QR
	c.setState(AuthConnected, "", "")
	if s := c.Status(); s.QRCode != "" {
		t.Fatalf("QR must be cleared on connect: %+v", s)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/wa/`
Expected: FAIL

- [ ] **Step 3: Implementar `internal/wa/client.go`**

```go
// Package wa owns the whatsmeow session: connection lifecycle, QR-based
// authentication state, and message send/receive.
package wa

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	_ "modernc.org/sqlite"

	"github.com/lncitador/whatsapp-mcp/internal/config"
	"github.com/lncitador/whatsapp-mcp/internal/store"
)

type AuthState string

const (
	AuthConnected  AuthState = "connected"
	AuthWaitingQR  AuthState = "waiting_qr"
	AuthLoggedOut  AuthState = "logged_out"
	AuthConnecting AuthState = "connecting"
)

type Status struct {
	State   AuthState `json:"state"`
	QRCode  string    `json:"qr_code,omitempty"`
	Message string    `json:"message,omitempty"`
}

type Client struct {
	wm     *whatsmeow.Client
	st     *store.Store
	logger waLog.Logger

	mu     sync.RWMutex
	status Status
}

func (c *Client) setState(s AuthState, qr, msg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status = Status{State: s, QRCode: qr, Message: msg}
}

func (c *Client) Status() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status
}

func New(st *store.Store) (*Client, error) {
	logger := waLog.Stdout("wa", "INFO", false)
	dbLog := waLog.Stdout("db", "WARN", false)
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
		filepath.Join(config.StoreDir(), "whatsapp.db"))
	container, err := sqlstore.New(context.Background(), "sqlite", dsn, dbLog)
	if err != nil {
		return nil, fmt.Errorf("open whatsapp.db: %w", err)
	}
	device, err := container.GetFirstDevice(context.Background())
	if err != nil {
		if err == sql.ErrNoRows {
			device = container.NewDevice()
		} else {
			return nil, fmt.Errorf("get device: %w", err)
		}
	}
	c := &Client{
		wm:     whatsmeow.NewClient(device, logger),
		st:     st,
		logger: logger,
	}
	c.setState(AuthConnecting, "", "starting")
	c.wm.AddEventHandler(func(evt any) {
		switch v := evt.(type) {
		case *events.Message:
			c.handleMessage(v)
		case *events.HistorySync:
			c.handleHistorySync(v)
		case *events.Connected:
			c.setState(AuthConnected, "", "")
		case *events.Disconnected:
			c.setState(AuthConnecting, "", "disconnected, reconnecting")
		case *events.LoggedOut:
			c.setState(AuthLoggedOut, "", "device logged out — re-pair via auth_status QR")
		}
	})
	return c, nil
}

// Start connects. When the device is unpaired it keeps consuming the QR
// channel in a goroutine, publishing each fresh code through Status() so the
// auth_status MCP tool (and /status endpoint) can render it. Never blocks on
// a terminal.
func (c *Client) Start(ctx context.Context) error {
	if c.wm.Store.ID == nil {
		qrChan, err := c.wm.GetQRChannel(ctx)
		if err != nil {
			return fmt.Errorf("qr channel: %w", err)
		}
		if err := c.wm.Connect(); err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		go func() {
			for evt := range qrChan {
				switch evt.Event {
				case "code":
					c.setState(AuthWaitingQR, evt.Code, "scan the QR code with WhatsApp")
				case "success":
					c.setState(AuthConnected, "", "")
					return
				case "timeout":
					c.setState(AuthLoggedOut, "", "QR timed out — restart daemon to get a new code")
					return
				}
			}
		}()
		return nil
	}
	if err := c.wm.Connect(); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	return nil
}

func (c *Client) Stop() { c.wm.Disconnect() }
```

- [ ] **Step 4: Implementar `internal/wa/handlers.go` (código movido)**

Copiar de `whatsapp-bridge/main.go`, adaptando para métodos de `*Client` (trocar parâmetros `client *whatsmeow.Client, messageStore *MessageStore` por `c.wm` / `c.st`, e chamadas `messageStore.StoreMessage(id, chatJID, ...)` pela struct `store.NewMessage{...}` da Task 2):

- `extractTextContent` — linhas 176-190, sem mudança de lógica.
- `extractMediaInfo` — linhas 375-409, sem mudança de lógica.
- `handleMessage` — linhas 412-471 → `func (c *Client) handleMessage(msg *events.Message)`.
- `GetChatName` — linhas 924-1004 → `func (c *Client) chatName(jid types.JID, chatJID string, conversation any, sender string) string`. A consulta `messageStore.db.QueryRow("SELECT name FROM chats ...")` vira novo método no store: adicionar em `internal/store/queries.go`:

```go
// ChatName returns the stored name for a chat, or "" when unknown.
func (s *Store) ChatName(jid string) string {
	var name string
	if err := s.db.QueryRow("SELECT IFNULL(name,'') FROM chats WHERE jid = ?", jid).Scan(&name); err != nil {
		return ""
	}
	return name
}
```

- `handleHistorySync` — linhas 1007-1146 → `func (c *Client) handleHistorySync(hs *events.HistorySync)`.
- **NÃO** portar `requestHistorySync` (linhas 1149-1182) — fora de escopo (panic conhecido).

- [ ] **Step 5: Implementar `internal/wa/send.go` e `internal/wa/download.go` (código movido)**

- `send.go`: copiar `sendWhatsAppMessage` (linhas 206-372) → `func (c *Client) SendMessage(recipient, message, mediaPath string) (bool, string)`. Única mudança de lógica: as chamadas `analyzeOggOpus(mediaData)` viram `audio.AnalyzeOggOpus(mediaData)`.
- `download.go`: copiar `MediaDownloader` + métodos (linhas 511-554), `downloadMedia` (557-656) → `func (c *Client) DownloadMedia(messageID, chatJID string) (path, mediaType, filename string, err error)`, e `extractDirectPathFromURL` (659-674). Mudanças:
  - `chatDir := fmt.Sprintf("store/%s", ...)` vira `chatDir := filepath.Join(config.MediaDir(), strings.ReplaceAll(chatJID, ":", "_"))` — mídia baixada vai para `~/.whatsapp-mcp/media/<chat>/`.
  - A consulta fallback `messageStore.db.QueryRow("SELECT media_type, filename ...")` usa `c.st.GetMediaInfo` (que já retorna tudo; se `err != nil`, retornar "failed to find message").

- [ ] **Step 6: Build + testes**

Run: `go build ./... && go test ./internal/wa/`
Expected: build OK, PASS

- [ ] **Step 7: Commit**

```bash
git add internal/wa/ internal/store/queries.go go.mod go.sum
git commit -m "feat: whatsapp client package with background QR auth state"
```

---

### Task 7: `internal/api` — HTTP local do daemon

**Files:**
- Create: `internal/api/server.go`
- Create: `internal/api/rpc.go`
- Test: `internal/api/server_test.go`

**Interfaces:**
- Consumes: `store.Store` (Tasks 2-4), `wa.Client` (via interface local, Task 6), `audio.ConvertToOpusOggTemp` (Task 5).
- Produces: `api.New(deps api.Deps) *api.Server`, `(*Server).ListenAndServe(port int) error`, `(*Server).Shutdown(ctx)`. Rotas:
  - `GET /health` → `200 {"ok":true,"version":...}`
  - `GET /status` → `wa.Status` JSON
  - `POST /shutdown` → `200` e encerra o daemon (via canal)
  - `POST /api/rpc/<tool>` → corpo JSON = args da tool; resposta `200 {"result": ...}` ou `4xx/5xx {"error": "..."}`
  - Compat com bridge antigo: `POST /api/send` e `POST /api/download` (mesmos formatos de `whatsapp-bridge/main.go:679-773`) — usados pelo gate de paridade (Task 12) e por quem já integrava com o bridge.

`api.Deps`:

```go
type WA interface {
	Status() wa.Status
	SendMessage(recipient, message, mediaPath string) (bool, string)
	DownloadMedia(messageID, chatJID string) (path, mediaType, filename string, err error)
}

type Deps struct {
	Store    *store.Store
	WA       WA
	Version  string
	OnShutdown func() // fecha o daemon
}
```

- [ ] **Step 1: Teste falhando (httptest, WA fake)**

`internal/api/server_test.go`:

```go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lncitador/whatsapp-mcp/internal/store"
	"github.com/lncitador/whatsapp-mcp/internal/wa"
)

type fakeWA struct{ sent [][3]string }

func (f *fakeWA) Status() wa.Status { return wa.Status{State: wa.AuthConnected} }
func (f *fakeWA) SendMessage(r, m, p string) (bool, string) {
	f.sent = append(f.sent, [3]string{r, m, p})
	return true, "Message sent to " + r
}
func (f *fakeWA) DownloadMedia(id, jid string) (string, string, string, error) {
	return "/tmp/x.jpg", "image", "x.jpg", nil
}

func newTestServer(t *testing.T) (*httptest.Server, *fakeWA, *store.Store) {
	t.Helper()
	t.Setenv("WHATSAPP_MCP_DIR", t.TempDir())
	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	f := &fakeWA{}
	s := New(Deps{Store: st, WA: f, Version: "test"})
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, f, st
}

func TestHealthAndStatus(t *testing.T) {
	ts, _, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/health")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("health: %v %v", resp, err)
	}
	resp, _ = http.Get(ts.URL + "/status")
	var got wa.Status
	json.NewDecoder(resp.Body).Decode(&got)
	if got.State != wa.AuthConnected {
		t.Fatalf("status = %+v", got)
	}
}

func TestRPCSearchContacts(t *testing.T) {
	ts, _, st := newTestServer(t)
	st.StoreChat("5511999999999@s.whatsapp.net", "Alice", time.Now())
	resp, err := http.Post(ts.URL+"/api/rpc/search_contacts", "application/json",
		strings.NewReader(`{"query":"alice"}`))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("rpc: %v %v", resp.StatusCode, err)
	}
	var body struct {
		Result []store.Contact `json:"result"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Result) != 1 || body.Result[0].Name != "Alice" {
		t.Fatalf("got %+v", body)
	}
}

func TestRPCSendMessage(t *testing.T) {
	ts, f, _ := newTestServer(t)
	resp, _ := http.Post(ts.URL+"/api/rpc/send_message", "application/json",
		strings.NewReader(`{"recipient":"5511999999999","message":"oi"}`))
	if resp.StatusCode != 200 || len(f.sent) != 1 || f.sent[0][1] != "oi" {
		t.Fatalf("status=%d sent=%v", resp.StatusCode, f.sent)
	}
}

func TestRPCUnknownTool(t *testing.T) {
	ts, _, _ := newTestServer(t)
	resp, _ := http.Post(ts.URL+"/api/rpc/nope", "application/json", strings.NewReader(`{}`))
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestLegacySendEndpoint(t *testing.T) {
	ts, f, _ := newTestServer(t)
	resp, _ := http.Post(ts.URL+"/api/send", "application/json",
		strings.NewReader(`{"recipient":"x","message":"legacy"}`))
	if resp.StatusCode != 200 || len(f.sent) != 1 {
		t.Fatalf("legacy send failed: %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/api/`
Expected: FAIL

- [ ] **Step 3: Implementar `internal/api/server.go`**

```go
// Package api is the daemon's local HTTP surface: health/status, a generic
// tool-RPC endpoint consumed by the MCP stdio proxy, and the legacy
// /api/send + /api/download routes for backwards compatibility.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/lncitador/whatsapp-mcp/internal/store"
	"github.com/lncitador/whatsapp-mcp/internal/wa"
)

type WA interface {
	Status() wa.Status
	SendMessage(recipient, message, mediaPath string) (bool, string)
	DownloadMedia(messageID, chatJID string) (path, mediaType, filename string, err error)
}

type Deps struct {
	Store      *store.Store
	WA         WA
	Version    string
	OnShutdown func()
}

type Server struct {
	deps Deps
	mux  *http.ServeMux
	http *http.Server
}

func New(deps Deps) *Server {
	s := &Server{deps: deps, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "version": deps.Version})
	})
	s.mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, deps.WA.Status())
	})
	s.mux.HandleFunc("POST /shutdown", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true})
		if deps.OnShutdown != nil {
			go deps.OnShutdown()
		}
	})
	s.mux.HandleFunc("POST /api/rpc/{tool}", s.handleRPC)
	s.registerLegacy()
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) ListenAndServe(port int) error {
	s.http = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", port), // localhost only: API has no auth
		Handler: s.mux,
	}
	return s.http.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.http == nil {
		return nil
	}
	return s.http.Shutdown(ctx)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeResult(w http.ResponseWriter, v any) {
	writeJSON(w, 200, map[string]any{"result": v})
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}
```

`registerLegacy()` (mesmo arquivo): port dos handlers `/api/send` e `/api/download` de `whatsapp-bridge/main.go:679-773`, com `sendWhatsAppMessage(client, ...)` → `s.deps.WA.SendMessage(...)` e `downloadMedia(...)` → `s.deps.WA.DownloadMedia(...)`; structs `SendMessageRequest`/`SendMessageResponse`/`DownloadMediaRequest`/`DownloadMediaResponse` copiadas de `main.go:193-203` e `473-485`.

- [ ] **Step 4: Implementar `internal/api/rpc.go`**

```go
package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lncitador/whatsapp-mcp/internal/audio"
	"github.com/lncitador/whatsapp-mcp/internal/store"
)

// rpcArgs is the union of every tool's parameters; each handler reads only
// the fields it documents. Field names match the MCP tool schemas (Task 9).
type rpcArgs struct {
	Query              string `json:"query"`
	After              string `json:"after"`
	Before             string `json:"before"`
	SenderPhoneNumber  string `json:"sender_phone_number"`
	ChatJID            string `json:"chat_jid"`
	Limit              int    `json:"limit"`
	Page               int    `json:"page"`
	IncludeContext     *bool  `json:"include_context"`
	ContextBefore      int    `json:"context_before"`
	ContextAfter       int    `json:"context_after"`
	IncludeLastMessage *bool  `json:"include_last_message"`
	SortBy             string `json:"sort_by"`
	JID                string `json:"jid"`
	MessageID          string `json:"message_id"`
	Recipient          string `json:"recipient"`
	Message            string `json:"message"`
	MediaPath          string `json:"media_path"`
}

func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	tool := r.PathValue("tool")
	var a rpcArgs
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		writeError(w, 400, "invalid JSON body: "+err.Error())
		return
	}
	switch tool {
	case "search_contacts":
		res, err := s.deps.Store.SearchContacts(a.Query)
		respond(w, res, err)
	case "list_messages":
		args := store.ListMessagesArgs{
			SenderPhoneNumber: a.SenderPhoneNumber, ChatJID: a.ChatJID,
			Query: a.Query, Limit: a.Limit, Page: a.Page,
		}
		var err error
		if args.After, err = parseISO(a.After); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		if args.Before, err = parseISO(a.Before); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		msgs, err := s.deps.Store.ListMessages(args)
		if err != nil {
			respond(w, nil, err)
			return
		}
		// paridade com Python: include_context expande cada hit
		includeCtx := a.IncludeContext == nil || *a.IncludeContext
		if includeCtx {
			cb, ca := a.ContextBefore, a.ContextAfter
			if cb == 0 {
				cb = 1
			}
			if ca == 0 {
				ca = 1
			}
			var expanded []store.Message
			for _, m := range msgs {
				ctx, err := s.deps.Store.GetMessageContext(m.ID, cb, ca)
				if err != nil {
					continue
				}
				expanded = append(expanded, ctx.Before...)
				expanded = append(expanded, ctx.Message)
				expanded = append(expanded, ctx.After...)
			}
			msgs = expanded
		}
		respond(w, s.formatMessages(msgs), nil)
	case "list_chats":
		ilm := a.IncludeLastMessage == nil || *a.IncludeLastMessage
		sortBy := a.SortBy
		if sortBy == "" {
			sortBy = "last_active"
		}
		res, err := s.deps.Store.ListChats(a.Query, a.Limit, a.Page, ilm, sortBy)
		respond(w, res, err)
	case "get_chat":
		ilm := a.IncludeLastMessage == nil || *a.IncludeLastMessage
		res, err := s.deps.Store.GetChat(a.ChatJID, ilm)
		respond(w, res, err)
	case "get_direct_chat_by_contact":
		res, err := s.deps.Store.GetDirectChatByContact(a.SenderPhoneNumber)
		respond(w, res, err)
	case "get_contact_chats":
		res, err := s.deps.Store.GetContactChats(a.JID, a.Limit, a.Page)
		respond(w, res, err)
	case "get_last_interaction":
		m, err := s.deps.Store.GetLastInteraction(a.JID)
		if err != nil || m == nil {
			respond(w, "", err)
			return
		}
		respond(w, s.formatMessage(*m), nil)
	case "get_message_context":
		cb, ca := a.ContextBefore, a.ContextAfter
		if cb == 0 {
			cb = 5
		}
		if ca == 0 {
			ca = 5
		}
		res, err := s.deps.Store.GetMessageContext(a.MessageID, cb, ca)
		respond(w, res, err)
	case "send_message":
		if a.Recipient == "" {
			writeError(w, 400, "recipient must be provided")
			return
		}
		ok, msg := s.deps.WA.SendMessage(a.Recipient, a.Message, "")
		respond(w, map[string]any{"success": ok, "message": msg}, nil)
	case "send_file":
		if a.Recipient == "" || a.MediaPath == "" {
			writeError(w, 400, "recipient and media_path must be provided")
			return
		}
		if _, err := os.Stat(a.MediaPath); err != nil {
			writeError(w, 400, "media file not found: "+a.MediaPath)
			return
		}
		ok, msg := s.deps.WA.SendMessage(a.Recipient, "", a.MediaPath)
		respond(w, map[string]any{"success": ok, "message": msg}, nil)
	case "send_audio_message":
		if a.Recipient == "" || a.MediaPath == "" {
			writeError(w, 400, "recipient and media_path must be provided")
			return
		}
		path := a.MediaPath
		if !strings.HasSuffix(path, ".ogg") {
			converted, err := audio.ConvertToOpusOggTemp(path)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			defer os.Remove(converted)
			path = converted
		}
		ok, msg := s.deps.WA.SendMessage(a.Recipient, "", path)
		respond(w, map[string]any{"success": ok, "message": msg}, nil)
	case "download_media":
		path, mediaType, filename, err := s.deps.WA.DownloadMedia(a.MessageID, a.ChatJID)
		if err != nil {
			respond(w, nil, err)
			return
		}
		respond(w, map[string]any{"path": path, "media_type": mediaType, "filename": filename}, nil)
	default:
		writeError(w, 404, "unknown tool: "+tool)
	}
}

func respond(w http.ResponseWriter, v any, err error) {
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeResult(w, v)
}

func parseISO(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	for _, l := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, &time.ParseError{Layout: time.RFC3339, Value: s,
		Message: ": invalid date, use ISO-8601 (e.g. 2026-07-08T00:00:00Z)"}
}

// formatMessage mirrors whatsapp.py format_message so agents see the same
// output shape they saw with the Python server.
func (s *Server) formatMessage(m store.Message) string {
	out := "[" + m.Timestamp.Format("2006-01-02 15:04:05") + "] "
	if m.ChatName != "" {
		out = "[" + m.Timestamp.Format("2006-01-02 15:04:05") + "] Chat: " + m.ChatName + " "
	}
	prefix := ""
	if m.MediaType != "" {
		prefix = "[" + m.MediaType + " - Message ID: " + m.ID + " - Chat JID: " + m.ChatJID + "] "
	}
	sender := "Me"
	if !m.IsFromMe {
		sender = s.deps.Store.SenderName(m.Sender)
	}
	return out + "From: " + sender + ": " + prefix + m.Content + "\n"
}

func (s *Server) formatMessages(msgs []store.Message) string {
	if len(msgs) == 0 {
		return "No messages to display."
	}
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(s.formatMessage(m))
	}
	return b.String()
}
```

- [ ] **Step 5: Rodar testes**

Run: `go test ./internal/api/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/api/
git commit -m "feat: daemon HTTP API with generic tool RPC and legacy endpoints"
```

---

### Task 8: Subcomando `serve`

**Files:**
- Create: `cmd/whatsapp-mcp/serve.go`
- Modify: `cmd/whatsapp-mcp/main.go` (case "serve")

**Interfaces:**
- Consumes: `config`, `store.Open`, `wa.New/Start/Stop`, `api.New/ListenAndServe/Shutdown`.
- Produces: `runServe(version string) error` — processo daemon completo. Escreve `config.PIDFile()` com o PID; remove no exit.

- [ ] **Step 1: Implementar `cmd/whatsapp-mcp/serve.go`**

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/lncitador/whatsapp-mcp/internal/api"
	"github.com/lncitador/whatsapp-mcp/internal/config"
	"github.com/lncitador/whatsapp-mcp/internal/store"
	"github.com/lncitador/whatsapp-mcp/internal/wa"
)

func runServe(version string) error {
	if err := config.EnsureDirs(); err != nil {
		return err
	}
	if err := os.WriteFile(config.PIDFile(), []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		return err
	}
	defer os.Remove(config.PIDFile())

	st, err := store.Open()
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer st.Close()

	client, err := wa.New(st)
	if err != nil {
		return fmt.Errorf("whatsapp: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := client.Start(ctx); err != nil {
		return fmt.Errorf("whatsapp start: %w", err)
	}
	defer client.Stop()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	srv := api.New(api.Deps{
		Store:   st,
		WA:      client,
		Version: version,
		OnShutdown: func() {
			quit <- syscall.SIGTERM
		},
	})
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe(config.Port()) }()
	log.Printf("whatsapp-mcp %s serving on 127.0.0.1:%d (data: %s)", version, config.Port(), config.BaseDir())

	select {
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	case <-quit:
	}
	log.Println("shutting down")
	sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer scancel()
	return srv.Shutdown(sctx)
}
```

- [ ] **Step 2: Ligar no dispatch**

Em `cmd/whatsapp-mcp/main.go`, trocar o case `serve`:

```go
	case "serve":
		if err := runServe(version); err != nil {
			fmt.Fprintln(os.Stderr, "serve:", err)
			os.Exit(1)
		}
```

- [ ] **Step 3: Build + smoke manual**

Run: `go build ./... && WHATSAPP_MCP_DIR=$(mktemp -d) go run ./cmd/whatsapp-mcp serve & sleep 3 && curl -s localhost:8080/health && curl -s localhost:8080/status && curl -s -X POST localhost:8080/shutdown`
Expected: health `{"ok":true,...}`, status com `"state":"waiting_qr"` (store vazio → QR), shutdown encerra o processo.

- [ ] **Step 4: Commit**

```bash
git add cmd/whatsapp-mcp/
git commit -m "feat: serve subcommand wires store, whatsapp client and http api"
```

---

### Task 9: `internal/mcpserver` — 12 tools + `auth_status` via go-sdk

**Files:**
- Create: `internal/mcpserver/server.go`
- Create: `internal/mcpserver/tools.go`
- Test: `internal/mcpserver/server_test.go`

**Interfaces:**
- Consumes: `config.BaseURL()`.
- Produces: `mcpserver.New(version string, baseURL string) *mcp.Server` (todas as tools registradas), `mcpserver.Run(ctx, version, baseURL) error` (roda sobre `mcp.StdioTransport`). Toda tool chama `POST <baseURL>/api/rpc/<name>` e devolve o campo `result` serializado como texto; `auth_status` chama `GET /status` e renderiza o QR com `qrterminal` num buffer.

- [ ] **Step 1: Teste falhando — registro e forwarding**

`internal/mcpserver/server_test.go`:

```go
package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCallRPCForwardsArgsAndReturnsResult(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"result":[{"name":"Alice"}]}`))
	}))
	defer ts.Close()

	out, err := callRPC(ts.URL, "search_contacts", map[string]any{"query": "ali"})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/rpc/search_contacts" || gotBody["query"] != "ali" {
		t.Fatalf("path=%q body=%v", gotPath, gotBody)
	}
	if out != `[{"name":"Alice"}]` {
		t.Fatalf("out = %q", out)
	}
}

func TestCallRPCSurfacesDaemonError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"error":"boom"}`))
	}))
	defer ts.Close()
	if _, err := callRPC(ts.URL, "list_chats", map[string]any{}); err == nil || err.Error() != "boom" {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestNewRegistersAllTools(t *testing.T) {
	s := New("test", "http://127.0.0.1:1")
	if s == nil {
		t.Fatal("nil server")
	}
	// smoke: construção não panica com as 13 tools registradas.
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/mcpserver/`
Expected: FAIL

- [ ] **Step 3: Implementar `internal/mcpserver/server.go`**

```go
// Package mcpserver is the MCP stdio server spawned by clients. It holds no
// state: every tool is forwarded to the daemon's local HTTP API.
package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/mdp/qrterminal"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var httpClient = &http.Client{Timeout: 120 * time.Second}

// callRPC posts args to the daemon and returns the raw JSON of "result".
func callRPC(baseURL, tool string, args any) (string, error) {
	body, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	resp, err := httpClient.Post(baseURL+"/api/rpc/"+tool, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("daemon unreachable (%v) — run `whatsapp-mcp status` to diagnose", err)
	}
	defer resp.Body.Close()
	var out struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("bad daemon response: %v", err)
	}
	if out.Error != "" {
		return "", fmt.Errorf("%s", out.Error)
	}
	return string(out.Result), nil
}

func fetchStatus(baseURL string) (state, qr, message string, err error) {
	resp, err := httpClient.Get(baseURL + "/status")
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	var st struct {
		State   string `json:"state"`
		QRCode  string `json:"qr_code"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return "", "", "", err
	}
	return st.State, st.QRCode, st.Message, nil
}

func New(version, baseURL string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "whatsapp", Version: version}, nil)
	registerTools(s, baseURL)

	type authIn struct{}
	mcp.AddTool(s, &mcp.Tool{
		Name: "auth_status",
		Description: "Check the WhatsApp session state. When re-authentication is needed, returns the QR code to scan with the WhatsApp app (Settings > Linked Devices).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in authIn) (*mcp.CallToolResult, any, error) {
		state, qr, message, err := fetchStatus(baseURL)
		if err != nil {
			return nil, nil, fmt.Errorf("daemon unreachable: %v — run `whatsapp-mcp status`", err)
		}
		text := "state: " + state
		if message != "" {
			text += "\n" + message
		}
		if qr != "" {
			var buf bytes.Buffer
			qrterminal.GenerateHalfBlock(qr, qrterminal.L, &buf)
			text += "\n\nScan this QR code with WhatsApp:\n\n" + buf.String()
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, nil, nil
	})
	return s
}

func Run(ctx context.Context, version, baseURL string) error {
	return New(version, baseURL).Run(ctx, &mcp.StdioTransport{})
}
```

- [ ] **Step 4: Implementar `internal/mcpserver/tools.go` (as 12 tools)**

Cada tool: struct de input tipada (gera o schema), forwarding via `callRPC`, resultado como texto. Descrições portadas dos docstrings de `whatsapp-mcp-server/main.py`.

```go
package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// forward builds a handler that proxies the tool to the daemon.
func forward[In any](baseURL, name string) func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		out, err := callRPC(baseURL, name, in)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out}}}, nil, nil
	}
}

type searchContactsIn struct {
	Query string `json:"query" jsonschema:"search term matching contact name or phone number"`
}

type listMessagesIn struct {
	After             string `json:"after,omitempty" jsonschema:"ISO-8601 date, only messages after this"`
	Before            string `json:"before,omitempty" jsonschema:"ISO-8601 date, only messages before this"`
	SenderPhoneNumber string `json:"sender_phone_number,omitempty" jsonschema:"filter by sender phone number"`
	ChatJID           string `json:"chat_jid,omitempty" jsonschema:"filter by chat JID"`
	Query             string `json:"query,omitempty" jsonschema:"search term in message content"`
	Limit             int    `json:"limit,omitempty" jsonschema:"max messages (default 20)"`
	Page              int    `json:"page,omitempty" jsonschema:"page number (default 0)"`
	IncludeContext    *bool  `json:"include_context,omitempty" jsonschema:"include surrounding messages (default true)"`
	ContextBefore     int    `json:"context_before,omitempty" jsonschema:"context messages before each hit (default 1)"`
	ContextAfter      int    `json:"context_after,omitempty" jsonschema:"context messages after each hit (default 1)"`
}

type listChatsIn struct {
	Query              string `json:"query,omitempty" jsonschema:"search term matching chat name or JID"`
	Limit              int    `json:"limit,omitempty" jsonschema:"max chats (default 20)"`
	Page               int    `json:"page,omitempty" jsonschema:"page number (default 0)"`
	IncludeLastMessage *bool  `json:"include_last_message,omitempty" jsonschema:"include last message preview (default true)"`
	SortBy             string `json:"sort_by,omitempty" jsonschema:"last_active or name (default last_active)"`
}

type getChatIn struct {
	ChatJID            string `json:"chat_jid" jsonschema:"the chat JID"`
	IncludeLastMessage *bool  `json:"include_last_message,omitempty" jsonschema:"include last message (default true)"`
}

type getDirectChatIn struct {
	SenderPhoneNumber string `json:"sender_phone_number" jsonschema:"the contact's phone number"`
}

type contactJIDIn struct {
	JID   string `json:"jid" jsonschema:"the contact's JID"`
	Limit int    `json:"limit,omitempty" jsonschema:"max results (default 20)"`
	Page  int    `json:"page,omitempty" jsonschema:"page number (default 0)"`
}

type lastInteractionIn struct {
	JID string `json:"jid" jsonschema:"the contact's JID"`
}

type messageContextIn struct {
	MessageID     string `json:"message_id" jsonschema:"the target message ID"`
	ContextBefore int    `json:"context_before,omitempty" jsonschema:"messages before (default 5)"`
	ContextAfter  int    `json:"context_after,omitempty" jsonschema:"messages after (default 5)"`
}

type sendMessageIn struct {
	Recipient string `json:"recipient" jsonschema:"phone number with country code (no +) or JID"`
	Message   string `json:"message" jsonschema:"the message text to send"`
}

type sendFileIn struct {
	Recipient string `json:"recipient" jsonschema:"phone number with country code (no +) or JID"`
	MediaPath string `json:"media_path" jsonschema:"absolute path of the file to send"`
}

type downloadMediaIn struct {
	MessageID string `json:"message_id" jsonschema:"ID of the message containing media"`
	ChatJID   string `json:"chat_jid" jsonschema:"JID of the chat containing the message"`
}

func registerTools(s *mcp.Server, baseURL string) {
	mcp.AddTool(s, &mcp.Tool{Name: "search_contacts",
		Description: "Search WhatsApp contacts by name or phone number. Matches the phone's contact book, so real names work. Multiple matches are all returned — ask the user to disambiguate."},
		forward[searchContactsIn](baseURL, "search_contacts"))
	mcp.AddTool(s, &mcp.Tool{Name: "list_messages",
		Description: "Get WhatsApp messages matching criteria, with optional surrounding context."},
		forward[listMessagesIn](baseURL, "list_messages"))
	mcp.AddTool(s, &mcp.Tool{Name: "list_chats",
		Description: "Get WhatsApp chats matching criteria."},
		forward[listChatsIn](baseURL, "list_chats"))
	mcp.AddTool(s, &mcp.Tool{Name: "get_chat",
		Description: "Get a WhatsApp chat's metadata by JID."},
		forward[getChatIn](baseURL, "get_chat"))
	mcp.AddTool(s, &mcp.Tool{Name: "get_direct_chat_by_contact",
		Description: "Get the direct chat with a contact by phone number."},
		forward[getDirectChatIn](baseURL, "get_direct_chat_by_contact"))
	mcp.AddTool(s, &mcp.Tool{Name: "get_contact_chats",
		Description: "Get all chats (direct and groups) involving a contact."},
		forward[contactJIDIn](baseURL, "get_contact_chats"))
	mcp.AddTool(s, &mcp.Tool{Name: "get_last_interaction",
		Description: "Get the most recent message involving a contact."},
		forward[lastInteractionIn](baseURL, "get_last_interaction"))
	mcp.AddTool(s, &mcp.Tool{Name: "get_message_context",
		Description: "Get the messages around a specific message."},
		forward[messageContextIn](baseURL, "get_message_context"))
	mcp.AddTool(s, &mcp.Tool{Name: "send_message",
		Description: "Send a WhatsApp text message to a person or group."},
		forward[sendMessageIn](baseURL, "send_message"))
	mcp.AddTool(s, &mcp.Tool{Name: "send_file",
		Description: "Send a file (image, video, document, raw audio) via WhatsApp."},
		forward[sendFileIn](baseURL, "send_file"))
	mcp.AddTool(s, &mcp.Tool{Name: "send_audio_message",
		Description: "Send an audio file as a WhatsApp voice note. Non-.ogg inputs are converted with ffmpeg (must be installed)."},
		forward[sendFileIn](baseURL, "send_audio_message"))
	mcp.AddTool(s, &mcp.Tool{Name: "download_media",
		Description: "Download media from a WhatsApp message; returns the local file path."},
		forward[downloadMediaIn](baseURL, "download_media"))
}
```

- [ ] **Step 5: Rodar testes**

Run: `go test ./internal/mcpserver/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/mcpserver/ go.mod go.sum
git commit -m "feat: MCP stdio server with 12 forwarded tools plus auth_status"
```

---

### Task 10: Subcomando `stdio` com auto-start + lock

**Files:**
- Create: `cmd/whatsapp-mcp/stdio.go`
- Create: `internal/daemonctl/daemonctl.go`
- Test: `internal/daemonctl/daemonctl_test.go`

**Interfaces:**
- Consumes: `config`, `mcpserver.Run`.
- Produces:
  - `daemonctl.Healthy(baseURL string) bool`
  - `daemonctl.EnsureRunning(exe string) error` — health-check; se falhar, adquire flock em `config.LockFile()`, re-checa, spawna `exe serve` detached (stdout/stderr → `logs/daemon.log`), solta o lock e faz poll do health por até 10s. Perdedor do lock só espera o health.
  - `daemonctl.StopDaemon(baseURL string) error` — `POST /shutdown` (usado na Task 11).

- [ ] **Step 1: Teste falhando**

`internal/daemonctl/daemonctl_test.go`:

```go
package daemonctl

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestHealthy(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Write([]byte(`{"ok":true}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer ts.Close()
	if !Healthy(ts.URL) {
		t.Fatal("want healthy")
	}
	if Healthy("http://127.0.0.1:1") {
		t.Fatal("want unhealthy for closed port")
	}
}

// EnsureRunning com daemon fake: binário de teste é um shell script que sobe
// um http server? Não — usamos o truque do processo de teste: o "exe" é um
// script que grava um marker. Aqui validamos apenas o caminho "já saudável"
// (não spawna) e o erro de timeout (spawn de /usr/bin/false).
func TestEnsureRunningAlreadyHealthy(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()
	t.Setenv("WHATSAPP_MCP_DIR", t.TempDir())
	t.Setenv("WHATSAPP_MCP_BASE_URL_OVERRIDE", ts.URL) // hook de teste
	if err := EnsureRunning("/usr/bin/false"); err != nil {
		t.Fatalf("healthy daemon must short-circuit: %v", err)
	}
}

func TestEnsureRunningSpawnFailure(t *testing.T) {
	t.Setenv("WHATSAPP_MCP_DIR", t.TempDir())
	t.Setenv("WHATSAPP_MCP_PORT", "1") // porta fechada
	t.Setenv("WHATSAPP_MCP_BASE_URL_OVERRIDE", "")
	t.Setenv("WHATSAPP_MCP_START_TIMEOUT", "2s")
	err := EnsureRunning("/usr/bin/false") // "daemon" morre na hora
	if err == nil {
		t.Fatal("want timeout error when daemon never becomes healthy")
	}
	if _, statErr := os.Stat(t.TempDir()); statErr != nil {
		t.Fatal(statErr)
	}
}
```

Nota: `TestEnsureRunningSpawnFailure` demora ~10s (poll timeout). Aceitável; se incomodar, o timeout é parametrizável por env `WHATSAPP_MCP_START_TIMEOUT` (implementado abaixo com default `10s`; o teste seta `2s`). Setar no teste: `t.Setenv("WHATSAPP_MCP_START_TIMEOUT", "2s")`.

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/daemonctl/`
Expected: FAIL

- [ ] **Step 3: Implementar `internal/daemonctl/daemonctl.go`**

```go
// Package daemonctl starts, checks and stops the serve daemon from other
// subcommands (stdio proxy, status, stop).
package daemonctl

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/gofrs/flock"

	"github.com/lncitador/whatsapp-mcp/internal/config"
)

var healthClient = &http.Client{Timeout: 2 * time.Second}

func baseURL() string {
	if v := os.Getenv("WHATSAPP_MCP_BASE_URL_OVERRIDE"); v != "" {
		return v
	}
	return config.BaseURL()
}

func Healthy(base string) bool {
	resp, err := healthClient.Get(base + "/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

func startTimeout() time.Duration {
	if v := os.Getenv("WHATSAPP_MCP_START_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 10 * time.Second
}

// EnsureRunning makes sure a daemon answers /health, spawning `exe serve`
// detached when needed. A file lock serialises concurrent proxies: the loser
// just waits for health instead of double-spawning.
func EnsureRunning(exe string) error {
	base := baseURL()
	if Healthy(base) {
		return nil
	}
	if err := config.EnsureDirs(); err != nil {
		return err
	}
	lock := flock.New(config.LockFile())
	locked, err := lock.TryLock()
	if err != nil {
		return fmt.Errorf("daemon lock: %w", err)
	}
	if locked {
		defer lock.Unlock()
		if !Healthy(base) { // re-check under lock
			if err := spawnDetached(exe); err != nil {
				return err
			}
		}
	}
	// winner and loser both wait for health
	deadline := time.Now().Add(startTimeout())
	for time.Now().Before(deadline) {
		if Healthy(base) {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not become healthy within %s — check %s/daemon.log",
		startTimeout(), config.LogsDir())
}

func spawnDetached(exe string) error {
	logf, err := os.OpenFile(
		config.LogsDir()+string(os.PathSeparator)+"daemon.log",
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "serve")
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.Stdin = nil
	setDetached(cmd) // build-tagged: Setsid no unix, CREATE_NEW_PROCESS_GROUP no windows
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn daemon: %w", err)
	}
	return cmd.Process.Release()
}

func StopDaemon(base string) error {
	resp, err := healthClient.Post(base+"/shutdown", "application/json", nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
```

Criar também `internal/daemonctl/detach_unix.go`:

```go
//go:build !windows

package daemonctl

import (
	"os/exec"
	"syscall"
)

func setDetached(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
```

e `internal/daemonctl/detach_windows.go`:

```go
//go:build windows

package daemonctl

import (
	"os/exec"
	"syscall"
)

func setDetached(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x08000000} // DETACHED_PROCESS
}
```

Dependência: `go get github.com/gofrs/flock`.

- [ ] **Step 4: Rodar testes**

Run: `go test ./internal/daemonctl/`
Expected: PASS

- [ ] **Step 5: Implementar `cmd/whatsapp-mcp/stdio.go` + dispatch**

```go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/lncitador/whatsapp-mcp/internal/config"
	"github.com/lncitador/whatsapp-mcp/internal/daemonctl"
	"github.com/lncitador/whatsapp-mcp/internal/mcpserver"
)

func runStdio(version string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	if err := daemonctl.EnsureRunning(exe); err != nil {
		return err
	}
	return mcpserver.Run(context.Background(), version, config.BaseURL())
}
```

Case no `main.go`:

```go
	case "stdio":
		if err := runStdio(version); err != nil {
			fmt.Fprintln(os.Stderr, "stdio:", err)
			os.Exit(1)
		}
```

- [ ] **Step 6: Smoke end-to-end local**

```bash
go build -o /tmp/wmcp ./cmd/whatsapp-mcp
WHATSAPP_MCP_DIR=$(mktemp -d) sh -c 'printf "%s\n" \
  "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2025-06-18\",\"capabilities\":{},\"clientInfo\":{\"name\":\"smoke\",\"version\":\"0\"}}}" \
  "{\"jsonrpc\":\"2.0\",\"method\":\"notifications/initialized\"}" \
  "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\"}" | /tmp/wmcp stdio'
```

Expected: resposta do `initialize` + `tools/list` com 13 tools; daemon auto-iniciado (checar `curl localhost:8080/health`). Depois `curl -X POST localhost:8080/shutdown`.

- [ ] **Step 7: Commit**

```bash
git add cmd/whatsapp-mcp/ internal/daemonctl/ go.mod go.sum
git commit -m "feat: stdio proxy auto-starts daemon with file-lock race protection"
```

---

### Task 11: Subcomandos `status` e `stop`

**Files:**
- Create: `cmd/whatsapp-mcp/status.go`
- Modify: `cmd/whatsapp-mcp/main.go` (cases)

**Interfaces:**
- Consumes: `daemonctl.Healthy/StopDaemon`, `config.BaseURL()`, `qrterminal`.
- Produces: `runStatus() error`, `runStop() error`.

- [ ] **Step 1: Implementar `cmd/whatsapp-mcp/status.go`**

```go
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/mdp/qrterminal"

	"github.com/lncitador/whatsapp-mcp/internal/config"
	"github.com/lncitador/whatsapp-mcp/internal/daemonctl"
)

func runStatus() error {
	base := config.BaseURL()
	if !daemonctl.Healthy(base) {
		fmt.Println("daemon: not running")
		fmt.Printf("data dir: %s\nstart it with: whatsapp-mcp serve (or just use an MCP client — stdio auto-starts it)\n", config.BaseDir())
		return nil
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(base + "/status")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var st struct {
		State   string `json:"state"`
		QRCode  string `json:"qr_code"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return err
	}
	fmt.Printf("daemon: running (%s)\nwhatsapp: %s\n", base, st.State)
	if st.Message != "" {
		fmt.Println(st.Message)
	}
	if st.QRCode != "" {
		fmt.Println("\nScan this QR code with WhatsApp:")
		qrterminal.GenerateHalfBlock(st.QRCode, qrterminal.L, os.Stdout)
	}
	return nil
}

func runStop() error {
	base := config.BaseURL()
	if !daemonctl.Healthy(base) {
		fmt.Println("daemon: not running")
		return nil
	}
	if err := daemonctl.StopDaemon(base); err != nil {
		return err
	}
	fmt.Println("daemon: stopped")
	return nil
}
```

Cases no `main.go`:

```go
	case "status":
		if err := runStatus(); err != nil {
			fmt.Fprintln(os.Stderr, "status:", err)
			os.Exit(1)
		}
	case "stop":
		if err := runStop(); err != nil {
			fmt.Fprintln(os.Stderr, "stop:", err)
			os.Exit(1)
		}
```

- [ ] **Step 2: Smoke**

Run: `go build -o /tmp/wmcp ./cmd/whatsapp-mcp && /tmp/wmcp status`
Expected: `daemon: not running` (ou running se ficou de pé da Task 10). `/tmp/wmcp stop` idem.

- [ ] **Step 3: Commit**

```bash
git add cmd/whatsapp-mcp/
git commit -m "feat: status and stop subcommands"
```

---

### Task 12: Gate de paridade Go vs Python

**Files:**
- Create: `scripts/parity-check.md` (checklist manual preenchível)

Pré-condição: daemon Go rodando com o **store real** (a migração da Task 2 copia `whatsapp-bridge/store/` para `~/.whatsapp-mcp/store/` na primeira subida; sessão preservada). Python MCP server ainda existe no repo.

- [ ] **Step 1: Criar `scripts/parity-check.md`**

```markdown
# Parity check — Go daemon vs Python MCP server

Rode cada item nos dois lados sobre o MESMO store e marque. Python:
`cd whatsapp-mcp-server && uv run python -c "import whatsapp; print(whatsapp.<fn>)"`.
Go: `curl -s -X POST localhost:8080/api/rpc/<tool> -d '<json>'`.

| # | Tool | Chamada | Go == Python (semântica)? |
|---|------|---------|---------------------------|
| 1 | search_contacts | query "carlos" | [ ] Go acha por nome (Python NÃO acha — melhoria esperada, validar JIDs retornados contra whatsmeow_contacts) |
| 2 | search_contacts | query por dígitos do telefone | [ ] mesmos JIDs |
| 3 | list_messages | chat_jid do chat mais ativo, limit 5 | [ ] mesmas mensagens, mesma ordem |
| 4 | list_messages | query "obrigado", include_context true | [ ] mesmos hits + contexto |
| 5 | list_chats | sem filtro, limit 10 | [ ] mesmos chats, mesma ordem |
| 6 | get_chat | jid de grupo conhecido | [ ] mesmos campos |
| 7 | get_direct_chat_by_contact | telefone conhecido | [ ] mesmo chat |
| 8 | get_contact_chats | jid de contato ativo | [ ] mesmos chats |
| 9 | get_last_interaction | mesmo jid | [ ] mesma mensagem |
| 10 | get_message_context | message_id do item 3 | [ ] mesmo contexto |
| 11 | send_message | enviar "teste go" para o próprio número | [ ] entregue |
| 12 | send_file | enviar uma imagem pequena | [ ] entregue |
| 13 | send_audio_message | enviar .mp3 (conversão ffmpeg) | [ ] entregue como voice note |
| 14 | download_media | baixar mídia do item 12 | [ ] arquivo salvo em ~/.whatsapp-mcp/media |
| 15 | auth_status | via tools/call no stdio | [ ] state=connected |

Divergência aceitável: timestamps com fuso formatado diferente; ordem estável
diferente apenas em empates exatos de timestamp. Qualquer outra divergência
bloqueia a Task 13.
```

- [ ] **Step 2: Executar o checklist**

Subir daemon (`go run ./cmd/whatsapp-mcp serve`), rodar os 15 itens, preencher o arquivo. **Todos os itens marcados** = gate passa.

- [ ] **Step 3: Commit**

```bash
git add scripts/parity-check.md
git commit -m "test: record Go vs Python parity gate results"
```

---

### Task 13: Remover Python + bridge antigo; atualizar skill e README

**Files:**
- Delete: `whatsapp-mcp-server/` (inteiro), `whatsapp-bridge/main.go`, `whatsapp-bridge/go.mod`, `whatsapp-bridge/go.sum` (o diretório `whatsapp-bridge/store/` do usuário fica — é dado local, já está no .gitignore)
- Modify: `skills/whatsapp-bridge/SKILL.md`, `skills/whatsapp-bridge/scripts/*`
- Modify: `README.md`
- Modify: `.gitignore`

- [ ] **Step 1: Deletar código antigo**

```bash
git rm -r whatsapp-mcp-server
git rm whatsapp-bridge/main.go whatsapp-bridge/go.mod whatsapp-bridge/go.sum
```

- [ ] **Step 2: Reescrever a skill como wrapper do binário**

`skills/whatsapp-bridge/scripts/start.sh`:

```bash
#!/usr/bin/env bash
# Ensures the whatsapp-mcp daemon is up. The binary self-daemonizes.
set -euo pipefail
if ! command -v whatsapp-mcp >/dev/null 2>&1; then
  echo "whatsapp-mcp not installed. Run: curl -fsSL https://raw.githubusercontent.com/lncitador/whatsapp-mcp/main/install.sh | sh" >&2
  exit 1
fi
whatsapp-mcp status | grep -q "daemon: running" || nohup whatsapp-mcp serve >/dev/null 2>&1 &
sleep 2
whatsapp-mcp status
```

`skills/whatsapp-bridge/scripts/status.sh`: `exec whatsapp-mcp status`.
`skills/whatsapp-bridge/scripts/stop.sh`: `exec whatsapp-mcp stop`.
Deletar `skills/whatsapp-bridge/scripts/monitor.sh`, `lib.sh` e `config.sh.example` (tmux/watchdog e `WHATSAPP_MCP_REPO` não são mais necessários — o binário se auto-gerencia).

`skills/whatsapp-bridge/SKILL.md` — substituir o corpo por:

```markdown
---
name: whatsapp-bridge
description: Ensures the whatsapp-mcp daemon is running before using WhatsApp MCP tools. Use when WhatsApp MCP tools fail to connect or the user asks to start/check the WhatsApp bridge.
---

# WhatsApp MCP daemon

The `whatsapp-mcp` binary self-manages: the `stdio` proxy (spawned by MCP
clients) auto-starts the daemon. You rarely need this skill; it exists for
diagnostics.

- Check: `scripts/status.sh` — shows daemon state, WhatsApp connection, and a
  QR code when re-authentication is pending.
- Start explicitly: `scripts/start.sh`.
- Stop: `scripts/stop.sh`.
- Logs: `~/.whatsapp-mcp/logs/daemon.log`.
- Re-auth: run `scripts/status.sh` and have the user scan the QR, or call the
  `auth_status` MCP tool which returns the QR inline.
```

- [ ] **Step 3: Reescrever README.md**

Substituir o conteúdo por (mantendo `example-use.png` e a seção de licença existente):

```markdown
# WhatsApp MCP Server

Search and send WhatsApp messages (text, media, voice notes) from Claude or
any MCP client. Single binary — no Go, no Python required.

![example](./example-use.png)

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/lncitador/whatsapp-mcp/main/install.sh | sh
```

Then register the MCP server:

```sh
claude mcp add whatsapp -- whatsapp-mcp stdio
```

Other clients: configure a stdio server running `whatsapp-mcp stdio`.

First use: call the `auth_status` tool (or run `whatsapp-mcp status`) and scan
the QR code with WhatsApp (Settings → Linked Devices). The daemon keeps your
session in `~/.whatsapp-mcp/` and stores messages locally in SQLite; nothing
leaves your machine except through the tools you call.

Voice notes: converting non-.ogg audio requires `ffmpeg` on your PATH.

## How it works

`whatsapp-mcp stdio` (spawned by your MCP client) auto-starts a background
daemon (`whatsapp-mcp serve`) that holds the WhatsApp connection (via
[whatsmeow](https://github.com/tulir/whatsmeow)) and a local-only HTTP API on
127.0.0.1:8080. The daemon outlives your MCP client, so messages keep being
received. `whatsapp-mcp status` / `stop` manage it.

Windows: download the binary from
[Releases](https://github.com/lncitador/whatsapp-mcp/releases), put it on
your PATH, and register `whatsapp-mcp stdio` in your MCP client.

## Tools

search_contacts, list_messages, list_chats, get_chat,
get_direct_chat_by_contact, get_contact_chats, get_last_interaction,
get_message_context, send_message, send_file, send_audio_message,
download_media, auth_status.

## Development

```sh
go build ./cmd/whatsapp-mcp
go test ./...
```

Upgrading from the old two-process setup (Go bridge + Python server): the
daemon migrates `whatsapp-bridge/store/` into `~/.whatsapp-mcp/store/` on
first run when started from the repo root — your session and history carry
over, no QR re-scan.
```

- [ ] **Step 4: `.gitignore` — remover entradas do Python/bridge que sobraram, garantir**

```
/whatsapp-mcp
dist/
whatsapp-bridge/store/
```

- [ ] **Step 5: Build + testes completos**

Run: `go build ./... && go test ./...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat!: single Go binary replaces Python MCP server and legacy bridge

BREAKING CHANGE: whatsapp-mcp-server/ (Python) removed; MCP clients must run
'whatsapp-mcp stdio' instead of uv. Data moves to ~/.whatsapp-mcp (migrated
automatically from whatsapp-bridge/store on first run)."
```

---

### Task 14: GoReleaser + GitHub Actions + install.sh

**Files:**
- Create: `.goreleaser.yaml`
- Create: `.github/workflows/release.yml`
- Create: `.github/workflows/ci.yml`
- Create: `install.sh`

- [ ] **Step 1: `.goreleaser.yaml`**

```yaml
version: 2
project_name: whatsapp-mcp

builds:
  - main: ./cmd/whatsapp-mcp
    binary: whatsapp-mcp
    env:
      - CGO_ENABLED=0
    goos: [darwin, linux, windows]
    goarch: [amd64, arm64]
    ignore:
      - goos: windows
        goarch: arm64
    ldflags:
      - -s -w -X main.version={{.Version}}

archives:
  - formats: [tar.gz]
    format_overrides:
      - goos: windows
        formats: [zip]
    name_template: >-
      {{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}

checksum:
  name_template: checksums.txt

release:
  draft: false

changelog:
  use: github-native
```

- [ ] **Step 2: `.github/workflows/ci.yml`**

```yaml
name: ci
on:
  push:
    branches: [main]
  pull_request:

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: go build ./...
        env:
          CGO_ENABLED: "0"
      - run: go test ./...
```

- [ ] **Step 3: `.github/workflows/release.yml`**

```yaml
name: release
on:
  push:
    tags: ["v*"]

permissions:
  contents: write

jobs:
  goreleaser:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - uses: goreleaser/goreleaser-action@v6
        with:
          version: "~> v2"
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

- [ ] **Step 4: `install.sh`**

```sh
#!/bin/sh
# whatsapp-mcp installer: downloads the latest release binary for this
# platform into ~/.local/bin (or /usr/local/bin with sudo).
set -eu

REPO="lncitador/whatsapp-mcp"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac
case "$os" in
  darwin | linux) ;;
  *) echo "unsupported OS: $os (Windows: download from https://github.com/$REPO/releases)" >&2; exit 1 ;;
esac

tag=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
  grep '"tag_name"' | head -1 | cut -d '"' -f 4)
[ -n "$tag" ] || { echo "could not resolve latest release" >&2; exit 1; }
version=${tag#v}

url="https://github.com/$REPO/releases/download/$tag/whatsapp-mcp_${version}_${os}_${arch}.tar.gz"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "downloading whatsapp-mcp $tag ($os/$arch)..."
curl -fsSL "$url" -o "$tmp/wmcp.tar.gz"
tar -xzf "$tmp/wmcp.tar.gz" -C "$tmp"

dest="$HOME/.local/bin"
if [ ! -d "$dest" ] || [ ! -w "$dest" ]; then
  mkdir -p "$dest" 2>/dev/null || dest="/usr/local/bin"
fi
if [ -w "$dest" ]; then
  install -m 0755 "$tmp/whatsapp-mcp" "$dest/whatsapp-mcp"
else
  echo "need sudo to install into $dest"
  sudo install -m 0755 "$tmp/whatsapp-mcp" "$dest/whatsapp-mcp"
fi

echo ""
echo "installed: $dest/whatsapp-mcp ($("$dest/whatsapp-mcp" --version))"
case ":$PATH:" in
  *":$dest:"*) ;;
  *) echo "NOTE: add $dest to your PATH" ;;
esac
echo ""
echo "next steps:"
echo "  claude mcp add whatsapp -- whatsapp-mcp stdio"
echo "  # then call the auth_status tool (or run: whatsapp-mcp status) and scan the QR"
```

- [ ] **Step 5: Validar goreleaser localmente**

Run: `go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean --skip=publish`
Expected: `dist/` com os 5 targets buildados sem erro (confirma CGO_ENABLED=0 + modernc).

- [ ] **Step 6: Commit**

```bash
git add .goreleaser.yaml .github/ install.sh
git commit -m "feat: goreleaser cross-platform releases, CI, and curl installer"
```

---

### Task 15: Fork remoto + primeira release

- [ ] **Step 1: Criar fork e apontar remotes**

```bash
gh repo fork lharries/whatsapp-mcp --clone=false   # cria github.com/lncitador/whatsapp-mcp
git remote rename origin upstream
git remote add origin https://github.com/lncitador/whatsapp-mcp.git
git push -u origin main
```

- [ ] **Step 2: Confirmar com o usuário antes do push/release** (ação externa e pública — parar aqui e perguntar se ainda não confirmado nesta sessão)

- [ ] **Step 3: Tag e release**

```bash
git tag v0.1.0
git push origin v0.1.0
gh run watch --repo lncitador/whatsapp-mcp
```

Expected: workflow `release` verde; Release `v0.1.0` com 5 artefatos + checksums.

- [ ] **Step 4: Validar instalação de ponta a ponta**

```bash
curl -fsSL https://raw.githubusercontent.com/lncitador/whatsapp-mcp/main/install.sh | sh
whatsapp-mcp --version   # => whatsapp-mcp 0.1.0
whatsapp-mcp status
```

Expected: binário instalado, daemon sobe, sessão migrada conecta sem QR novo.

---

## Self-Review (executado na escrita do plano)

- **Spec coverage:** binário único + 4 subcomandos (T1, T8, T10, T11); 12 tools (T7, T9); search_contacts por nome com dedupe (T4); auth_status com QR (T6, T9); ffmpeg opcional (T5, T7); modernc/CGO=0 (T2, T14); store `~/.whatsapp-mcp` + migração (T1, T2); lock/auto-start (T10); bind 127.0.0.1 (T7); gate de paridade antes de deletar Python (T12→T13); skill + README (T13); GoReleaser/Actions/install.sh (T14); fork + release (T15). Fora de escopo respeitado: sem history sync request, sem launchd/systemd, sem PowerShell.
- **Placeholders:** nenhum TBD; código movido referencia intervalos de linha exatos de `whatsapp-bridge/main.go` @ commit `7b1c17d`.
- **Type consistency:** `store.Message/Chat/Contact/MessageContext` definidos em T3/T4 e consumidos em T7; `wa.Status/AuthState` definidos em T6, consumidos em T7/T9; `api.Deps.WA` casa com métodos de `wa.Client`; `forward[In]` de T9 casa com handler do go-sdk (3 retornos).
```
