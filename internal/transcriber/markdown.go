package transcriber

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func GenerateMarkdown(result *Result, mediaType string, timestamp time.Time) (string, string, error) {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# Transcrição - %s\n\n", timestamp.Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("**Tipo:** %s\n\n", mediaType))

	sb.WriteString("## Transcrição\n\n")
	for _, seg := range result.Segments {
		sb.WriteString(fmt.Sprintf("[%s] %s\n", formatDuration(seg.Start), seg.Text))
	}

	if len(result.Frames) > 0 {
		sb.WriteString("\n## Frames\n\n")
		for _, frame := range result.Frames {
			sb.WriteString(fmt.Sprintf("![Frame %s](%s)\n",
				formatDuration(frame.Timestamp),
				filepath.Base(frame.Path)))
		}
	}

	content := sb.String()

	storeDir := filepath.Join(os.Getenv("HOME"), ".whatsapp-mcp", "store", "transcripts")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		return content, "", err
	}

	tmpFile, err := os.CreateTemp(storeDir, "transcript-*.md")
	if err != nil {
		return content, "", err
	}
	defer tmpFile.Close()

	if _, err := tmpFile.WriteString(content); err != nil {
		return content, "", err
	}

	return content, tmpFile.Name(), nil
}

func formatDuration(d time.Duration) string {
	s := int(d.Seconds())
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m := s / 60
	s = s % 60
	return fmt.Sprintf("%dm%ds", m, s)
}
