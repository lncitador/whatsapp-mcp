// Package config resolves whatsapp-mcp's on-disk layout and local HTTP address.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// ValidateMediaPath validates that a media path is safe to read/write.
// It prevents CWE-22 path traversal attacks.
//
// Rules:
//   - Path must not contain ".." (prevents directory traversal)
//   - If WHATSAPP_MEDIA_ROOTS is set, path must be under one of those roots
//   - Symlinks are resolved before checking (prevents symlink escape)
func ValidateMediaPath(mediaPath string) error {
	if mediaPath == "" {
		return fmt.Errorf("media_path is empty")
	}

	if strings.Contains(mediaPath, "..") {
		return fmt.Errorf("media_path must not contain \"..\"")
	}

	roots := os.Getenv("WHATSAPP_MEDIA_ROOTS")
	if roots == "" {
		return nil
	}

	cleaned, err := filepath.EvalSymlinks(mediaPath)
	if err != nil {
		return fmt.Errorf("cannot resolve media_path: %w", err)
	}

	for _, root := range filepath.SplitList(roots) {
		absRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		resolvedRoot, err := filepath.EvalSymlinks(absRoot)
		if err != nil {
			continue
		}
		if strings.HasPrefix(cleaned, resolvedRoot+string(filepath.Separator)) || cleaned == resolvedRoot {
			return nil
		}
	}

	return fmt.Errorf("media_path %q is outside allowed media roots", mediaPath)
}
