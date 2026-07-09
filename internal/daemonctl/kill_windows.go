//go:build windows

package daemonctl

import "os"

// Windows has no SIGTERM; Kill is the only portable termination.
func terminate(proc *os.Process) error {
	return proc.Kill()
}

// pidOnPort is unix-only (lsof); on Windows the PID file is the only source.
func pidOnPort(port int) int { return 0 }
