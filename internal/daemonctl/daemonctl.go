package daemonctl

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/gofrs/flock"

	"github.com/lncitador/whatsapp-mcp/internal/config"
)

var healthClient = &http.Client{Timeout: 2 * time.Second}

func baseURL() string {
	if v := os.Getenv("WHATSAPP_MCP_BASE_URL_OVERRIDE"); v != "" {
		return v
	}
	return config.BaseURL()
}

func Healthy(base string) bool {
	resp, err := healthClient.Get(base + "/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

func startTimeout() time.Duration {
	if v := os.Getenv("WHATSAPP_MCP_START_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 10 * time.Second
}

func EnsureRunning(exe string) error {
	base := baseURL()
	if Healthy(base) {
		return nil
	}
	if err := config.EnsureDirs(); err != nil {
		return err
	}
	lock := flock.New(config.LockFile())
	locked, err := lock.TryLock()
	if err != nil {
		return fmt.Errorf("daemon lock: %w", err)
	}
	if locked {
		defer lock.Unlock()
		if !Healthy(base) {
			if err := spawnDetached(exe); err != nil {
				return err
			}
		}
	}
	deadline := time.Now().Add(startTimeout())
	for time.Now().Before(deadline) {
		if Healthy(base) {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not become healthy within %s — check %s/daemon.log",
		startTimeout(), config.LogsDir())
}

func spawnDetached(exe string) error {
	logf, err := os.OpenFile(
		config.LogsDir()+string(os.PathSeparator)+"daemon.log",
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "serve")
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.Stdin = nil
	setDetached(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn daemon: %w", err)
	}
	return cmd.Process.Release()
}

func StopDaemon(base string) error {
	resp, err := healthClient.Post(base+"/shutdown", "application/json", nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
