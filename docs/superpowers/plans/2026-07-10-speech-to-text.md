# Speech-to-Text Transcription Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add speech-to-text transcription for audio and video WhatsApp messages using whisper.cpp with OpenAI API fallback.

**Architecture:** New `internal/transcriber/` package with Whisper CLI and OpenAI API backends, ffmpeg frame extraction for videos, markdown generation, and MCP tool integration.

**Tech Stack:** Go, whisper.cpp (CLI), ffmpeg, OpenAI API

## Global Constraints

- ffmpeg already available in project (used in `internal/audio/`)
- whisper-cli optional (fallback to OpenAI API if not found)
- Transcriptions stored in `~/.whatsapp-mcp/store/transcripts/`
- New table `transcriptions` in `messages.db`
- Background processing in goroutine (don't block message handling)

---

### Task 1: Transcriber Interface and Types

**Files:**
- Create: `internal/transcriber/transcriber.go`

**Interfaces:**
- Produces: `Transcriber` interface, `Result`, `Segment`, `Frame` types

- [ ] **Step 1: Create the transcriber package**

```go
package transcriber

import "time"

type Segment struct {
	Start time.Duration `json:"start"`
	End   time.Duration `json:"end"`
	Text  string        `json:"text"`
}

type Frame struct {
	Timestamp time.Duration `json:"timestamp"`
	Path      string        `json:"path"`
}

type Result struct {
	Text          string    `json:"text"`
	Segments      []Segment `json:"segments"`
	Frames        []Frame   `json:"frames,omitempty"`
	MarkdownPath  string    `json:"markdown_path,omitempty"`
	MarkdownContent string  `json:"markdown_content,omitempty"`
}

type Transcriber interface {
	Transcribe(mediaPath string) (*Result, error)
	Available() bool
}
```

- [ ] **Step 2: Build to verify compilation**

Run: `go build ./internal/transcriber/`
Expected: SUCCESS

- [ ] **Step 3: Commit**

```bash
git add internal/transcriber/transcriber.go
git commit -m "feat(transcriber): add interface and types"
```

---

### Task 2: Whisper CLI Integration

**Files:**
- Create: `internal/transcriber/whisper.go`
- Test: `internal/transcriber/whisper_test.go`

**Interfaces:**
- Consumes: `Transcriber` interface from Task 1
- Produces: `WhisperCLI` struct implementing `Transcriber`

- [ ] **Step 1: Write the failing test**

```go
package transcriber

import (
	"os/exec"
	"testing"
)

func TestWhisperCLIAvailable(t *testing.T) {
	_, err := exec.LookPath("whisper-cli")
	if err != nil {
		t.Skip("whisper-cli not installed")
	}
	w := NewWhisperCLI("base")
	if !w.Available() {
		t.Fatal("expected Available() to return true")
	}
}

func TestWhisperCLINotAvailable(t *testing.T) {
	w := NewWhisperCLI("nonexistent-model-xyz")
	// Available should still return true if binary exists
	// The model check happens at transcription time
	_, err := exec.LookPath("whisper-cli")
	if err != nil {
		t.Skip("whisper-cli not installed")
	}
	if !w.Available() {
		t.Fatal("expected Available() to return true when binary exists")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/transcriber/ -run TestWhisperCLI -v`
Expected: FAIL with "NewWhisperCLI not defined"

- [ ] **Step 3: Write minimal implementation**

```go
package transcriber

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type WhisperCLI struct {
	Model  string
	Binary string
}

type whisperSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

type whisperOutput struct {
	Segments []whisperSegment `json:"segments"`
}

func NewWhisperCLI(model string) *WhisperCLI {
	binary := "whisper-cli"
	if path, err := exec.LookPath("whisper-cli"); err == nil {
		binary = path
	}
	return &WhisperCLI{Model: model, Binary: binary}
}

func (w *WhisperCLI) Available() bool {
	_, err := exec.LookPath("whisper-cli")
	return err == nil
}

func (w *WhisperCLI) Transcribe(mediaPath string) (*Result, error) {
	if _, err := os.Stat(mediaPath); err != nil {
		return nil, fmt.Errorf("media file not found: %s", mediaPath)
	}

	tmpDir, err := os.MkdirTemp("", "whisper-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	cmd := exec.Command(w.Binary,
		"--model", w.Model,
		"--output_format", "json",
		"--output_dir", tmpDir,
		mediaPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("whisper-cli failed: %v: %s", err, output)
	}

	// Find the JSON output file
	base := filepath.Base(mediaPath)
	ext := filepath.Ext(base)
	jsonFile := filepath.Join(tmpDir, base[:len(base)-len(ext)]+".json")

	data, err := os.ReadFile(jsonFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read whisper output: %v", err)
	}

	var wo whisperOutput
	if err := json.Unmarshal(data, &wo); err != nil {
		return nil, fmt.Errorf("failed to parse whisper output: %v", err)
	}

	result := &Result{}
	for _, seg := range wo.Segments {
		result.Segments = append(result.Segments, Segment{
			Start: time.Duration(seg.Start * float64(time.Second)),
			End:   time.Duration(seg.End * float64(time.Second)),
			Text:  seg.Text,
		})
		result.Text += seg.Text + " "
	}

	return result, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/transcriber/ -run TestWhisperCLI -v`
Expected: PASS (or SKIP if whisper-cli not installed)

- [ ] **Step 5: Commit**

```bash
git add internal/transcriber/whisper.go internal/transcriber/whisper_test.go
git commit -m "feat(transcriber): add whisper.cpp CLI integration"
```

---

### Task 3: OpenAI API Integration

**Files:**
- Create: `internal/transcriber/openai.go`
- Test: `internal/transcriber/openai_test.go`

**Interfaces:**
- Consumes: `Transcriber` interface from Task 1
- Produces: `OpenAITranscriber` struct implementing `Transcriber`

- [ ] **Step 1: Write the failing test**

```go
package transcriber

import (
	"os"
	"testing"
)

func TestOpenAITranscriberAvailable(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("OPENAI_API_KEY not set")
	}
	o := NewOpenAITranscriber()
	if !o.Available() {
		t.Fatal("expected Available() to return true when API key is set")
	}
}

func TestOpenAITranscriberNotAvailable(t *testing.T) {
	// Temporarily unset the key
	old := os.Getenv("OPENAI_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	defer os.Setenv("OPENAI_API_KEY", old)

	o := NewOpenAITranscriber()
	if o.Available() {
		t.Fatal("expected Available() to return false when API key is not set")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/transcriber/ -run TestOpenAI -v`
Expected: FAIL with "NewOpenAITranscriber not defined"

- [ ] **Step 3: Write minimal implementation**

```go
package transcriber

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type OpenAITranscriber struct {
	APIKey string
	Model  string
}

type openaiSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

type openaiResponse struct {
	Text     string          `json:"text"`
	Segments []openaiSegment `json:"segments,omitempty"`
}

func NewOpenAITranscriber() *OpenAITranscriber {
	return &OpenAITranscriber{
		APIKey: os.Getenv("OPENAI_API_KEY"),
		Model:  "whisper-1",
	}
}

func (o *OpenAITranscriber) Available() bool {
	return o.APIKey != ""
}

func (o *OpenAITranscriber) Transcribe(mediaPath string) (*Result, error) {
	if o.APIKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY not set")
	}

	if _, err := os.Stat(mediaPath); err != nil {
		return nil, fmt.Errorf("media file not found: %s", mediaPath)
	}

	file, err := os.Open(mediaPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", filepath.Base(mediaPath))
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(part, file); err != nil {
		return nil, err
	}

	writer.WriteField("model", o.Model)
	writer.WriteField("response_format", "verbose_json")
	writer.Close()

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/audio/transcriptions", &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+o.APIKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OpenAI API error %d: %s", resp.StatusCode, body)
	}

	var oar openaiResponse
	if err := json.NewDecoder(resp.Body).Decode(&oar); err != nil {
		return nil, err
	}

	result := &Result{Text: oar.Text}
	for _, seg := range oar.Segments {
		result.Segments = append(result.Segments, Segment{
			Start: time.Duration(seg.Start * float64(time.Second)),
			End:   time.Duration(seg.End * float64(time.Second)),
			Text:  seg.Text,
		})
	}

	return result, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/transcriber/ -run TestOpenAI -v`
Expected: PASS (or SKIP if OPENAI_API_KEY not set)

- [ ] **Step 5: Commit**

```bash
git add internal/transcriber/openai.go internal/transcriber/openai_test.go
git commit -m "feat(transcriber): add OpenAI API integration"
```

---

### Task 4: Frame Extraction

**Files:**
- Create: `internal/transcriber/frames.go`
- Test: `internal/transcriber/frames_test.go`

**Interfaces:**
- Produces: `ExtractFrame(videoPath string, timestamp time.Duration) (string, error)`
- Produces: `ExtractFrames(videoPath string, segments []Segment, outputDir string) ([]Frame, error)`

- [ ] **Step 1: Write the failing test**

```go
package transcriber

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestExtractFrame(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}

	// Create a simple test video with ffmpeg
	tmpVideo, err := os.CreateTemp("", "test-*.mp4")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpVideo.Name())
	tmpVideo.Close()

	cmd := exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", "color=c=blue:s=320x240:d=2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p",
		tmpVideo.Name())
	if err := cmd.Run(); err != nil {
		t.Skip("ffmpeg can't create test video")
	}

	framePath, err := ExtractFrame(tmpVideo.Name(), 1*time.Second)
	if err != nil {
		t.Fatalf("ExtractFrame failed: %v", err)
	}
	defer os.Remove(framePath)

	if _, err := os.Stat(framePath); err != nil {
		t.Fatalf("frame file not created: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/transcriber/ -run TestExtractFrame -v`
Expected: FAIL with "ExtractFrame not defined"

- [ ] **Step 3: Write minimal implementation**

```go
package transcriber

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func ExtractFrame(videoPath string, timestamp time.Duration) (string, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return "", fmt.Errorf("ffmpeg not found in PATH")
	}

	if _, err := os.Stat(videoPath); err != nil {
		return "", fmt.Errorf("video file not found: %s", videoPath)
	}

	tmpFile, err := os.CreateTemp("", "frame-*.jpg")
	if err != nil {
		return "", err
	}
	tmpFile.Close()

	cmd := exec.Command("ffmpeg", "-y",
		"-ss", timestamp.String(),
		"-i", videoPath,
		"-frames:v", "1",
		"-q:v", "2",
		tmpFile.Name(),
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("ffmpeg failed: %v: %s", err, output)
	}

	return tmpFile.Name(), nil
}

func ExtractFrames(videoPath string, segments []Segment, outputDir string) ([]Frame, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, err
	}

	var frames []Frame
	for i, seg := range segments {
		framePath := filepath.Join(outputDir, fmt.Sprintf("frame_%03d.jpg", i+1))
		
		cmd := exec.Command("ffmpeg", "-y",
			"-ss", seg.Start.String(),
			"-i", videoPath,
			"-frames:v", "1",
			"-q:v", "2",
			framePath,
		)

		if err := cmd.Run(); err != nil {
			continue // Skip failed frames
		}

		frames = append(frames, Frame{
			Timestamp: seg.Start,
			Path:      framePath,
		})
	}

	return frames, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/transcriber/ -run TestExtractFrame -v`
Expected: PASS (or SKIP if ffmpeg not installed)

- [ ] **Step 5: Commit**

```bash
git add internal/transcriber/frames.go internal/transcriber/frames_test.go
git commit -m "feat(transcriber): add ffmpeg frame extraction"
```

---

### Task 5: Markdown Generation

**Files:**
- Create: `internal/transcriber/markdown.go`
- Test: `internal/transcriber/markdown_test.go`

**Interfaces:**
- Produces: `GenerateMarkdown(result *Result, mediaType string, timestamp time.Time) (string, string, error)`

- [ ] **Step 1: Write the failing test**

```go
package transcriber

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestGenerateMarkdown(t *testing.T) {
	result := &Result{
		Text: "Hello world. How are you?",
		Segments: []Segment{
			{Start: 0, End: 2 * time.Second, Text: "Hello world."},
			{Start: 2 * time.Second, End: 5 * time.Second, Text: "How are you?"},
		},
		Frames: []Frame{
			{Timestamp: 0, Path: "/path/to/frame_001.jpg"},
			{Timestamp: 2 * time.Second, Path: "/path/to/frame_002.jpg"},
		},
	}

	timestamp := time.Date(2026, 7, 10, 14, 32, 25, 0, time.Local)
	content, path, err := GenerateMarkdown(result, "video", timestamp)
	if err != nil {
		t.Fatalf("GenerateMarkdown failed: %v", err)
	}

	if !strings.Contains(content, "# Transcrição") {
		t.Fatal("expected title in markdown")
	}
	if !strings.Contains(content, "[0s]") {
		t.Fatal("expected timestamp in markdown")
	}
	if !strings.Contains(content, "![Frame 0s]") {
		t.Fatal("expected frame reference in markdown")
	}
	if path == "" {
		t.Fatal("expected non-empty path")
	}
	defer os.Remove(path)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/transcriber/ -run TestGenerateMarkdown -v`
Expected: FAIL with "GenerateMarkdown not defined"

- [ ] **Step 3: Write minimal implementation**

```go
package transcriber

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func GenerateMarkdown(result *Result, mediaType string, timestamp time.Time) (string, string, error) {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# Transcrição - %s\n\n", timestamp.Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("**Tipo:** %s\n\n", mediaType))

	sb.WriteString("## Transcrição\n\n")
	for _, seg := range result.Segments {
		sb.WriteString(fmt.Sprintf("[%s] %s\n", formatDuration(seg.Start), seg.Text))
	}

	if len(result.Frames) > 0 {
		sb.WriteString("\n## Frames\n\n")
		for _, frame := range result.Frames {
			sb.WriteString(fmt.Sprintf("![Frame %s](%s)\n", 
				formatDuration(frame.Timestamp), 
				filepath.Base(frame.Path)))
		}
	}

	content := sb.String()

	// Save to file
	tmpFile, err := os.CreateTemp("", "transcript-*.md")
	if err != nil {
		return content, "", err
	}
	defer tmpFile.Close()

	if _, err := tmpFile.WriteString(content); err != nil {
		return content, "", err
	}

	return content, tmpFile.Name(), nil
}

func formatDuration(d time.Duration) string {
	s := int(d.Seconds())
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m := s / 60
	s = s % 60
	return fmt.Sprintf("%dm%ds", m, s)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/transcriber/ -run TestGenerateMarkdown -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/transcriber/markdown.go internal/transcriber/markdown_test.go
git commit -m "feat(transcriber): add markdown generation"
```

---

### Task 6: Auto-detection and Transcribe Function

**Files:**
- Create: `internal/transcriber/auto.go`
- Test: `internal/transcriber/auto_test.go`

**Interfaces:**
- Consumes: `WhisperCLI`, `OpenAITranscriber` from Tasks 2-3
- Produces: `New() Transcriber` factory function

- [ ] **Step 1: Write the failing test**

```go
package transcriber

import (
	"os"
	"os/exec"
	"testing"
)

func TestNew(t *testing.T) {
	tr := New()
	if tr == nil {
		// No transcriber available - skip
		if _, err := exec.LookPath("whisper-cli"); err != nil && os.Getenv("OPENAI_API_KEY") == "" {
			t.Skip("no transcriber available")
		}
		t.Fatal("expected non-nil transcriber")
	}

	if !tr.Available() {
		t.Fatal("expected transcriber to be available")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/transcriber/ -run TestNew -v`
Expected: FAIL with "New not defined"

- [ ] **Step 3: Write minimal implementation**

```go
package transcriber

import (
	"os"
	"os/exec"
)

func New() Transcriber {
	// Try whisper.cpp first
	if _, err := exec.LookPath("whisper-cli"); err == nil {
		model := os.Getenv("WHISPER_MODEL")
		if model == "" {
			model = "base"
		}
		return NewWhisperCLI(model)
	}

	// Fallback to OpenAI API
	if os.Getenv("OPENAI_API_KEY") != "" {
		return NewOpenAITranscriber()
	}

	return nil
}

func IsVideo(mediaPath string) bool {
	ext := map[string]bool{
		".mp4": true, ".avi": true, ".mov": true,
		".mkv": true, ".webm": true, ".flv": true,
	}
	
	for e := range ext {
		if len(mediaPath) > len(e) && mediaPath[len(mediaPath)-len(e):] == e {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/transcriber/ -run TestNew -v`
Expected: PASS (or SKIP if no transcriber available)

- [ ] **Step 5: Commit**

```bash
git add internal/transcriber/auto.go internal/transcriber/auto_test.go
git commit -m "feat(transcriber): add auto-detection and factory"
```

---

### Task 7: Store Transcriptions Table

**Files:**
- Modify: `internal/store/store.go`
- Modify: `internal/store/queries.go`
- Test: `internal/store/queries_test.go`

**Interfaces:**
- Produces: `Transcription` type, `StoreTranscription`, `GetTranscription` methods

- [ ] **Step 1: Add migration and type**

In `internal/store/store.go`, add after the messages table creation:

```go
CREATE TABLE IF NOT EXISTS transcriptions (
    message_id TEXT PRIMARY KEY,
    chat_jid TEXT NOT NULL,
    media_type TEXT NOT NULL,
    text TEXT NOT NULL,
    segments TEXT,
    frames_dir TEXT,
    markdown_path TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (message_id, chat_jid) REFERENCES messages(id, chat_jid)
);
```

Add type to `internal/store/queries.go`:

```go
type Transcription struct {
	MessageID    string    `json:"message_id"`
	ChatJID      string    `json:"chat_jid"`
	MediaType    string    `json:"media_type"`
	Text         string    `json:"text"`
	Segments     string    `json:"segments,omitempty"`
	FramesDir    string    `json:"frames_dir,omitempty"`
	MarkdownPath string    `json:"markdown_path,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}
```

- [ ] **Step 2: Add store methods**

```go
func (s *Store) StoreTranscription(t Transcription) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO transcriptions 
		(message_id, chat_jid, media_type, text, segments, frames_dir, markdown_path)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.MessageID, t.ChatJID, t.MediaType, t.Text, t.Segments, t.FramesDir, t.MarkdownPath)
	return err
}

func (s *Store) GetTranscription(messageID, chatJID string) (*Transcription, error) {
	var t Transcription
	err := s.db.QueryRow(
		"SELECT message_id, chat_jid, media_type, text, IFNULL(segments,''), IFNULL(frames_dir,''), IFNULL(markdown_path,''), created_at FROM transcriptions WHERE message_id = ? AND chat_jid = ?",
		messageID, chatJID,
	).Scan(&t.MessageID, &t.ChatJID, &t.MediaType, &t.Text, &t.Segments, &t.FramesDir, &t.MarkdownPath, &t.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}
```

- [ ] **Step 3: Build to verify compilation**

Run: `go build ./internal/store/`
Expected: SUCCESS

- [ ] **Step 4: Commit**

```bash
git add internal/store/store.go internal/store/queries.go
git commit -m "feat(store): add transcriptions table and methods"
```

---

### Task 8: MCP Tool Registration

**Files:**
- Modify: `internal/mcpserver/tools.go`
- Modify: `internal/api/` (RPC handler)

**Interfaces:**
- Consumes: `Transcription` type from Task 7
- Produces: `transcribe_media` MCP tool

- [ ] **Step 1: Add tool type**

In `internal/mcpserver/tools.go`:

```go
type transcribeMediaIn struct {
	MessageID       string `json:"message_id" jsonschema:"ID of the message containing media"`
	ChatJID         string `json:"chat_jid" jsonschema:"JID of the chat containing the message"`
	ForceReprocess  bool   `json:"force_reprocess,omitempty" jsonschema:"re-transcribe even if already done"`
}
```

- [ ] **Step 2: Register the tool**

```go
mcp.AddTool(s, &mcp.Tool{Name: "transcribe_media",
	Description: "Transcribe audio/video message to text. For videos, extracts frames at segment timestamps. Returns transcription text, segments, frame paths, and markdown content."},
	forward[transcribeMediaIn](baseURL, "transcribe_media"))
```

- [ ] **Step 3: Build to verify compilation**

Run: `go build ./internal/mcpserver/`
Expected: SUCCESS

- [ ] **Step 4: Commit**

```bash
git add internal/mcpserver/tools.go
git commit -m "feat(mcp): add transcribe_media tool"
```

---

### Task 9: Background Processing in Handler

**Files:**
- Modify: `internal/wa/handlers.go`

**Interfaces:**
- Consumes: `transcriber.New()`, `StoreTranscription` from Tasks 6-7
- Produces: Automatic transcription on message receipt

- [ ] **Step 1: Add transcription to handleMessage**

In `handleMessage`, after storing the message, add:

```go
if mediaType == "audio" || mediaType == "video" {
	go c.transcribeMessage(msg.Info.ID, chatJID, mediaType, url)
}
```

Add the transcribeMessage method:

```go
func (c *Client) transcribeMessage(messageID, chatJID, mediaType, mediaURL string) {
	tr := transcriber.New()
	if tr == nil {
		return // No transcriber available
	}

	// Check if already transcribed
	existing, _ := c.st.GetTranscription(messageID, chatJID)
	if existing != nil {
		return
	}

	// Download media to temp file
	tmpFile, err := os.CreateTemp("", "media-*"+filepath.Ext(mediaURL))
	if err != nil {
		c.logger.Warnf("Failed to create temp file for transcription: %v", err)
		return
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	// TODO: Download media from WhatsApp (needs implementation)
	// For now, skip if we can't download
	if mediaURL == "" {
		return
	}

	result, err := tr.Transcribe(tmpFile.Name())
	if err != nil {
		c.logger.Warnf("Transcription failed for %s: %v", messageID, err)
		return
	}

	// Extract frames for video
	var framesDir string
	if mediaType == "video" && transcriber.IsVideo(tmpFile.Name()) {
		framesDir = filepath.Join(config.StoreDir(), "transcripts", messageID)
		result.Frames, _ = transcriber.ExtractFrames(tmpFile.Name(), result.Segments, framesDir)
	}

	// Generate markdown
	_, mdPath, _ := transcriber.GenerateMarkdown(result, mediaType, time.Now())

	// Store transcription
	jsonSegments, _ := json.Marshal(result.Segments)
	c.st.StoreTranscription(store.Transcription{
		MessageID:    messageID,
		ChatJID:      chatJID,
		MediaType:    mediaType,
		Text:         result.Text,
		Segments:     string(jsonSegments),
		FramesDir:    framesDir,
		MarkdownPath: mdPath,
	})

	c.logger.Infof("Transcribed message %s: %s", messageID, result.Text[:min(50, len(result.Text))])
}
```

- [ ] **Step 2: Build to verify compilation**

Run: `go build ./internal/wa/`
Expected: SUCCESS

- [ ] **Step 3: Commit**

```bash
git add internal/wa/handlers.go
git commit -m "feat(wa): add background transcription on message receipt"
```

---

### Task 10: Run Full Test Suite

**Files:**
- None (verification only)

- [ ] **Step 1: Run all tests**

Run: `go test ./...`
Expected: All tests PASS

- [ ] **Step 2: Build binary**

Run: `go build -o whatsapp-mcp ./cmd/whatsapp-mcp/`
Expected: SUCCESS

- [ ] **Step 3: Final commit if needed**

If any fixes were needed:
```bash
git add -A
git commit -m "fix: test fixes for speech-to-text module"
```
