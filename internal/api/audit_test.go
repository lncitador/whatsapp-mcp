package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditLog(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")

	logToolCall(logPath, "send_message", "5511999999999", "127.0.0.1")

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read audit log: %v", err)
	}
	entry := string(data)
	if !strings.Contains(entry, "send_message") {
		t.Fatalf("log missing tool name: %q", entry)
	}
	if !strings.Contains(entry, "5511999999999") {
		t.Fatalf("log missing recipient: %q", entry)
	}
	if !strings.Contains(entry, "127.0.0.1") {
		t.Fatalf("log missing IP: %q", entry)
	}
}
