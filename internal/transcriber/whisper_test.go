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
	_, err := exec.LookPath("whisper-cli")
	if err != nil {
		t.Skip("whisper-cli not installed")
	}
	if !w.Available() {
		t.Fatal("expected Available() to return true when binary exists")
	}
}
