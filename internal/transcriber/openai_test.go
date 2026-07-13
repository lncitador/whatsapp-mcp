package transcriber

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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
	old := os.Getenv("OPENAI_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	defer os.Setenv("OPENAI_API_KEY", old)

	o := NewOpenAITranscriber()
	if o.Available() {
		t.Fatal("expected Available() to return false when API key is not set")
	}
}

// TestOpenAITranscriber_UploadsNormalizedWAV is the regression test for the
// real bug: WhatsApp voice notes are Ogg Opus, which OpenAI's
// /v1/audio/transcriptions endpoint does not accept. Before NormalizeToWAV
// was wired in, this backend uploaded the raw .ogg file as-is. Assert the
// uploaded part is actually a WAV file.
func TestOpenAITranscriber_UploadsNormalizedWAV(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	oggPath := synthesizeOgg(t)

	var gotFilename string
	var gotFirstBytes [4]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mr, err := r.MultipartReader()
		if err != nil {
			t.Fatalf("multipart reader: %v", err)
		}
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			if part.FormName() == "file" {
				gotFilename = part.FileName()
				part.Read(gotFirstBytes[:])
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"text": "ola"})
	}))
	defer server.Close()

	o := &OpenAITranscriber{APIKey: "test", Model: "whisper-1", BaseURL: server.URL}
	result, err := o.Transcribe(oggPath)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if result.Text != "ola" {
		t.Fatalf("expected 'ola', got %q", result.Text)
	}

	if !hasWavExt(gotFilename) {
		t.Fatalf("expected uploaded file to have a .wav name, got %q", gotFilename)
	}
	if string(gotFirstBytes[:]) != "RIFF" {
		t.Fatalf("expected uploaded bytes to start with RIFF (WAV header), got %q", gotFirstBytes)
	}
}

func hasWavExt(filename string) bool {
	return len(filename) >= 4 && filename[len(filename)-4:] == ".wav"
}
