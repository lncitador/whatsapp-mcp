package transcriber

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// synthesizeOgg generates a short Ogg Opus tone, mirroring the format
// WhatsApp voice notes actually arrive in (see internal/wa/send.go's
// audio/ogg;codecs=opus mime type).
func synthesizeOgg(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	path := filepath.Join(t.TempDir(), "voice.ogg")
	cmd := exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-c:a", "libopus",
		path,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("synthesize ogg: %v: %s", err, out)
	}
	return path
}

func TestNormalizeToWAV_ProducesPlayableWAV(t *testing.T) {
	oggPath := synthesizeOgg(t)

	wavPath, err := NormalizeToWAV(oggPath)
	if err != nil {
		t.Fatalf("NormalizeToWAV: %v", err)
	}
	defer os.Remove(wavPath)

	data, err := os.ReadFile(wavPath)
	if err != nil {
		t.Fatalf("read wav: %v", err)
	}
	if len(data) < 44 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		t.Fatalf("not a valid WAV file (missing RIFF/WAVE header)")
	}

	channels := binary.LittleEndian.Uint16(data[22:24])
	sampleRate := binary.LittleEndian.Uint32(data[24:28])
	bitsPerSample := binary.LittleEndian.Uint16(data[34:36])

	if channels != 1 {
		t.Errorf("expected mono (1 channel), got %d", channels)
	}
	if sampleRate != 16000 {
		t.Errorf("expected 16000 Hz, got %d", sampleRate)
	}
	if bitsPerSample != 16 {
		t.Errorf("expected 16-bit PCM, got %d", bitsPerSample)
	}

	duration, err := probeDuration(wavPath)
	if err != nil {
		t.Fatalf("probe duration: %v", err)
	}
	if duration <= 0 {
		t.Fatalf("expected non-zero duration, got %v", duration)
	}
}

func TestNormalizeToWAV_MissingFile(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := NormalizeToWAV("/nonexistent/path.ogg"); err == nil {
		t.Fatal("expected error for missing input file")
	}
}
