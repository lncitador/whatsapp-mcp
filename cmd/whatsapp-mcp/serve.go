package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lncitador/whatsapp-mcp/internal/api"
	"github.com/lncitador/whatsapp-mcp/internal/config"
	"github.com/lncitador/whatsapp-mcp/internal/daemonctl"
	"github.com/lncitador/whatsapp-mcp/internal/store"
	"github.com/lncitador/whatsapp-mcp/internal/wa"
)

func runServe(version string) error {
	if err := config.EnsureDirs(); err != nil {
		return fmt.Errorf("ensure dirs: %w", err)
	}

	port := config.Port()
	if err := daemonctl.TakeoverPort(port); err != nil {
		return fmt.Errorf("takeover port: %w", err)
	}

	pidPath := config.PIDFile()
	ownPID := fmt.Sprintf("%d", os.Getpid())
	if err := os.WriteFile(pidPath, []byte(ownPID), 0644); err != nil {
		return fmt.Errorf("write pid: %w", err)
	}
	// Only remove the PID file if it still holds our PID — a takeover may
	// have rewritten it while this process was shutting down.
	defer func() {
		if data, err := os.ReadFile(pidPath); err == nil && string(data) == ownPID {
			os.Remove(pidPath)
		}
	}()

	st, err := store.Open()
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	client, err := wa.New(st)
	if err != nil {
		return fmt.Errorf("new wa client: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := client.Start(ctx); err != nil {
		return fmt.Errorf("start wa client: %w", err)
	}
	defer client.Stop()

	srv := api.New(api.Deps{
		Store:   st,
		WA:      client,
		Version: version,
		OnShutdown: func() {
			cancel()
		},
	})

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe(port) }()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	log.Printf("whatsapp-mcp %s listening on 127.0.0.1:%d", version, port)

	select {
	case sig := <-sigCh:
		log.Printf("received %s, shutting down", sig)
	case <-ctx.Done():
		log.Printf("shutdown requested via API")
	case err := <-errCh:
		log.Printf("server error: %v", err)
	}

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()
	return srv.Shutdown(shutCtx)
}
