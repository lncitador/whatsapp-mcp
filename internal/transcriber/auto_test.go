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
