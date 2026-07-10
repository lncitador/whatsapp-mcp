//go:build !windows

package daemonctl

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

func terminate(proc *os.Process) error {
	return proc.Signal(syscall.SIGTERM)
}

// pidOnPort finds the PID listening on the port when the PID file is
// missing or stale. Returns 0 if it can't be determined.
func pidOnPort(port int) int {
	out, err := exec.Command("lsof", "-ti", fmt.Sprintf("tcp:%d", port), "-sTCP:LISTEN").Output()
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(strings.Split(string(out), "\n")[0]))
	if err != nil {
		return 0
	}
	return pid
}
