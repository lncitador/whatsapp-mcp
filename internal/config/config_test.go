package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBaseDirRespectsOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WHATSAPP_MCP_DIR", dir)
	if got := BaseDir(); got != dir {
		t.Fatalf("BaseDir() = %q, want %q", got, dir)
	}
	if got := StoreDir(); got != filepath.Join(dir, "store") {
		t.Fatalf("StoreDir() = %q", got)
	}
}

func TestBaseDirDefaultsToHome(t *testing.T) {
	t.Setenv("WHATSAPP_MCP_DIR", "")
	home, _ := os.UserHomeDir()
	if got := BaseDir(); got != filepath.Join(home, ".whatsapp-mcp") {
		t.Fatalf("BaseDir() = %q", got)
	}
}

func TestPortPrecedence(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WHATSAPP_MCP_DIR", dir)

	t.Setenv("WHATSAPP_MCP_PORT", "")
	if got := Port(); got != 8080 {
		t.Fatalf("default Port() = %d, want 8080", got)
	}

	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"port": 9001}`), 0644)
	if got := Port(); got != 9001 {
		t.Fatalf("config.json Port() = %d, want 9001", got)
	}

	t.Setenv("WHATSAPP_MCP_PORT", "9002")
	if got := Port(); got != 9002 {
		t.Fatalf("env Port() = %d, want 9002", got)
	}
}

func TestEnsureDirs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WHATSAPP_MCP_DIR", dir)
	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{StoreDir(), LogsDir(), MediaDir()} {
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			t.Fatalf("missing dir %s: %v", d, err)
		}
	}
}
