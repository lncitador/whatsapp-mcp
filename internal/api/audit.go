package api

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/lncitador/whatsapp-mcp/internal/config"
)

func auditLogPath() string {
	return filepath.Join(config.LogsDir(), "audit.log")
}

func logToolCall(logPath, tool, recipient, ip string) {
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	logger := slog.New(slog.NewTextHandler(f, nil))
	logger.Info("tool invocation",
		slog.String("tool", tool),
		slog.String("recipient", recipient),
		slog.String("ip", ip),
	)
}
