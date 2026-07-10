# Speech-to-Text Transcription Module Design

## Overview

Add speech-to-text transcription for audio and video WhatsApp messages. Uses whisper.cpp locally with OpenAI API fallback. For videos, extracts frames at Whisper segment timestamps.

## Architecture

New package `internal/transcriber/` with:

- `transcriber.go` — interface `Transcribe(mediaPath string) (*Result, error)`
- `whisper.go` — whisper.cpp CLI integration
- `openai.go` — OpenAI API fallback
- `frames.go` — ffmpeg frame extraction
- `markdown.go` — markdown generation

### Flow

```
Message received (audio/video)
  → download_media (existing)
  → transcriber.Transcribe(path)
    → detect type (audio vs video)
    → call whisper.cpp or OpenAI API
    → if video: extract frames at segment timestamps
    → generate markdown
    → save to ~/.whatsapp-mcp/store/transcripts/
    → return Result{Text, Segments, Frames, MarkdownPath}
  → store transcription reference in messages.db
```

## Components

### 1. Whisper Integration

**whisper.cpp (primary):**
- Executes `whisper-cli` via `os/exec`
- Supports models: tiny, base, small, medium, large
- Configurable via `WHISPER_MODEL` env var (default: base)
- Parses stdout output (JSON or SRT format)

**OpenAI API (fallback):**
- Used when whisper.cpp unavailable or fails
- `POST /v1/audio/transcriptions`
- Requires `OPENAI_API_KEY` env var

**Auto-detection:**
```go
func New() Transcriber {
    if _, err := exec.LookPath("whisper-cli"); err == nil {
        return &WhisperCLI{...}
    }
    if os.Getenv("OPENAI_API_KEY") != "" {
        return &OpenAITranscriber{...}
    }
    return nil // no transcriber available
}
```

### 2. Frame Extraction (Video)

Extract frame at each Whisper segment timestamp:

```go
func ExtractFrame(videoPath string, timestamp time.Duration) (string, error) {
    // ffmpeg -ss <timestamp> -i <video> -frames:v 1 -q:v 2 output.jpg
}
```

Frames saved to: `~/.whatsapp-mcp/store/transcripts/<msg_id>/frame_001.jpg`

### 3. Markdown Generation

```markdown
# Transcrição - 2026-07-10 14:32:25

**Duração:** 2m 35s
**Tipo:** Vídeo

## Transcrição

[00:00] Olá, tudo bem?
[00:15] Estou te enviando esse vídeo...

## Frames

![Frame 00:00](frame_001.jpg)
![Frame 00:15](frame_002.jpg)
```

### 4. MCP Tool

New tool `transcribe_media`:

**Input:**
```json
{
  "message_id": "msg123",
  "chat_jid": "555123456789@s.whatsapp.net",
  "force_reprocess": false
}
```

**Output:**
```json
{
  "text": "Olá, tudo bem?",
  "segments": [
    {"start": 0, "end": 15, "text": "Olá, tudo bem?"},
    {"start": 15, "end": 35, "text": "Estou te enviando esse vídeo..."}
  ],
  "frames": [
    {"timestamp": 0, "path": "/path/to/frame_001.jpg"},
    {"timestamp": 15, "path": "/path/to/frame_002.jpg"}
  ],
  "markdown_path": "/path/to/transcript.md",
  "markdown_content": "# Transcrição - ..."
}
```

### 5. Store Changes

New table `transcriptions` in `messages.db`:

```sql
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

New methods:
- `StoreTranscription(t Transcription) error`
- `GetTranscription(messageID, chatJID string) (*Transcription, error)`

### 6. Background Processing

In `handleMessage`:
- Detect `media_type == "audio" || "video"`
- Call `transcriber.Transcribe()` in goroutine
- Save result to store
- Log transcription status

### 7. Error Handling

- whisper.cpp not found → fallback to OpenAI API
- No API key → return descriptive error
- ffmpeg not found → skip frame extraction, transcribe only
- Processing fails → log warning, don't block message
- Audio > 10min → chunk into 5min parts

## Dependencies

- `whisper-cli` (optional, compile manually)
- `ffmpeg` (already used in project)
- `OPENAI_API_KEY` (optional, for fallback)

## Testing

- Unit tests for each component
- Integration test with real audio file
- Fallback test (whisper fails → openai)

## Files Modified

- `internal/transcriber/` — new package (5 files)
- `internal/store/store.go` — add transcriptions table
- `internal/store/queries.go` — add transcription methods
- `internal/mcpserver/tools.go` — add transcribe_media tool
- `internal/wa/handlers.go` — add background transcription
