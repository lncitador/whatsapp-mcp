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
