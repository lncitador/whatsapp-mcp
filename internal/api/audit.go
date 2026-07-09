package api

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

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
	fmt.Fprintf(f, "%s | %s | %s | %s\n",
		time.Now().UTC().Format(time.RFC3339),
		tool,
		recipient,
		ip,
	)
}
