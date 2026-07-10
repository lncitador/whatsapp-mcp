package transcriber

import (
	"os"
	"os/exec"
	"strings"
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
	for _, ext := range []string{".mp4", ".avi", ".mov", ".mkv", ".webm", ".flv"} {
		if strings.HasSuffix(strings.ToLower(mediaPath), ext) {
			return true
		}
	}
	return false
}
