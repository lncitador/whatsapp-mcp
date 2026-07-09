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
	if !strings.Contains(entry, "tool=send_message") {
		t.Fatalf("log missing tool key: %q", entry)
	}
	if !strings.Contains(entry, "recipient=5511999999999") {
		t.Fatalf("log missing recipient key: %q", entry)
	}
	if !strings.Contains(entry, "ip=127.0.0.1") {
		t.Fatalf("log missing ip key: %q", entry)
	}
	if !strings.Contains(entry, "msg=\"tool invocation\"") {
		t.Fatalf("log missing msg key: %q", entry)
	}
}

func TestAuditLogMultiWrite(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")

	logToolCall(logPath, "send_message", "5511999999999", "127.0.0.1")
	logToolCall(logPath, "read_message", "5511888888888", "10.0.0.1")

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read audit log: %v", err)
	}
	entry := string(data)
	if !strings.Contains(entry, "tool=send_message") {
		t.Fatalf("log missing first tool entry: %q", entry)
	}
	if !strings.Contains(entry, "tool=read_message") {
		t.Fatalf("log missing second tool entry: %q", entry)
	}
	if !strings.Contains(entry, "recipient=5511999999999") {
		t.Fatalf("log missing first recipient: %q", entry)
	}
	if !strings.Contains(entry, "recipient=5511888888888") {
		t.Fatalf("log missing second recipient: %q", entry)
	}
}
