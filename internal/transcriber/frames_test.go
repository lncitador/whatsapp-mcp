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
