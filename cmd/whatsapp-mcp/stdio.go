package main

import (
	"context"
	"fmt"
	"os"

	"github.com/lncitador/whatsapp-mcp/internal/config"
	"github.com/lncitador/whatsapp-mcp/internal/daemonctl"
	"github.com/lncitador/whatsapp-mcp/internal/mcpserver"
)

func runStdio(version string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	if err := daemonctl.EnsureRunning(exe); err != nil {
		return err
	}
	return mcpserver.Run(context.Background(), version, config.BaseURL())
}
