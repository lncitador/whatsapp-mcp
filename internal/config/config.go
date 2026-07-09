// Package config resolves whatsapp-mcp's on-disk layout and local HTTP address.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

func BaseDir() string {
	if dir := os.Getenv("WHATSAPP_MCP_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".whatsapp-mcp"
	}
	return filepath.Join(home, ".whatsapp-mcp")
}

func StoreDir() string { return filepath.Join(BaseDir(), "store") }
func LogsDir() string  { return filepath.Join(BaseDir(), "logs") }
func MediaDir() string { return filepath.Join(BaseDir(), "media") }

func PIDFile() string  { return filepath.Join(BaseDir(), "daemon.pid") }
func LockFile() string { return filepath.Join(BaseDir(), "daemon.lock") }

type fileConfig struct {
	Port int `json:"port"`
}

// Port precedence: WHATSAPP_MCP_PORT env > config.json > 8080.
func Port() int {
	if v := os.Getenv("WHATSAPP_MCP_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			return p
		}
	}
	data, err := os.ReadFile(filepath.Join(BaseDir(), "config.json"))
	if err == nil {
		var fc fileConfig
		if json.Unmarshal(data, &fc) == nil && fc.Port > 0 {
			return fc.Port
		}
	}
	return 8080
}

func BaseURL() string { return fmt.Sprintf("http://127.0.0.1:%d", Port()) }

func EnsureDirs() error {
	for _, d := range []string{BaseDir(), StoreDir(), LogsDir(), MediaDir()} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return err
		}
	}
	return nil
}
