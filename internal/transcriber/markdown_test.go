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
