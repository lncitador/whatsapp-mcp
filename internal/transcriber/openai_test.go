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
	old := os.Getenv("OPENAI_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	defer os.Setenv("OPENAI_API_KEY", old)

	o := NewOpenAITranscriber()
	if o.Available() {
		t.Fatal("expected Available() to return false when API key is not set")
	}
}
