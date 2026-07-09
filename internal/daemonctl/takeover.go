package daemonctl

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lncitador/whatsapp-mcp/internal/config"
)

func portFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

func waitPortFree(port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if portFree(port) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func readPIDFile() (int, error) {
	data, err := os.ReadFile(config.PIDFile())
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// TakeoverPort frees the daemon port so a new serve can bind it. A healthy
// daemon is asked to shut down via its API; anything else holding the port
// is terminated through the PID file (SIGTERM, then SIGKILL).
func TakeoverPort(port int) error {
	if portFree(port) {
		return nil
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	if Healthy(base) {
		_ = StopDaemon(base)
		if waitPortFree(port, 5*time.Second) {
			return nil
		}
	}
	pid, err := readPIDFile()
	if err != nil || pid <= 0 {
		pid = pidOnPort(port)
	}
	if pid > 0 && pid != os.Getpid() {
		if proc, err := os.FindProcess(pid); err == nil {
			_ = terminate(proc)
			if waitPortFree(port, 5*time.Second) {
				return nil
			}
			_ = proc.Kill()
			if waitPortFree(port, 2*time.Second) {
				return nil
			}
		}
	}
	return fmt.Errorf("port %d still in use after takeover attempt — free it manually (lsof -i :%d)", port, port)
}
