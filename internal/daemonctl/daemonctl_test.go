package daemonctl

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

func TestHealthy(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Write([]byte(`{"ok":true}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer ts.Close()
	if !Healthy(ts.URL) {
		t.Fatal("want healthy")
	}
	if Healthy("http://127.0.0.1:1") {
		t.Fatal("want unhealthy for closed port")
	}
}

func TestEnsureRunningAlreadyHealthy(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()
	t.Setenv("WHATSAPP_MCP_DIR", t.TempDir())
	t.Setenv("WHATSAPP_MCP_BASE_URL_OVERRIDE", ts.URL)
	if err := EnsureRunning("/usr/bin/false"); err != nil {
		t.Fatalf("healthy daemon must short-circuit: %v", err)
	}
}

func TestEnsureRunningSpawnFailure(t *testing.T) {
	t.Setenv("WHATSAPP_MCP_DIR", t.TempDir())
	t.Setenv("WHATSAPP_MCP_PORT", "1")
	t.Setenv("WHATSAPP_MCP_BASE_URL_OVERRIDE", "")
	t.Setenv("WHATSAPP_MCP_START_TIMEOUT", "2s")
	err := EnsureRunning("/usr/bin/false")
	if err == nil {
		t.Fatal("want timeout error when daemon never becomes healthy")
	}
	if _, statErr := os.Stat(t.TempDir()); statErr != nil {
		t.Fatal(statErr)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestTakeoverPortAlreadyFree(t *testing.T) {
	t.Setenv("WHATSAPP_MCP_DIR", t.TempDir())
	if err := TakeoverPort(freePort(t)); err != nil {
		t.Fatalf("free port must be a no-op: %v", err)
	}
}

func TestTakeoverPortGracefulShutdown(t *testing.T) {
	t.Setenv("WHATSAPP_MCP_DIR", t.TempDir())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	mux := http.NewServeMux()
	srv := &http.Server{Handler: mux}
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("POST /shutdown", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
		go srv.Close()
	})
	go srv.Serve(ln)
	defer srv.Close()

	if err := TakeoverPort(port); err != nil {
		t.Fatalf("takeover of healthy daemon: %v", err)
	}
	probe, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("port still in use after takeover: %v", err)
	}
	probe.Close()
}

// TestHelperProcess is not a real test — it is re-executed as a subprocess
// by TestTakeoverPortKillsStaleProcess to squat a port without answering HTTP.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_LISTENER") != "1" {
		return
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.Exit(1)
	}
	fmt.Println(ln.Addr().(*net.TCPAddr).Port)
	os.Stdout.Close()
	for {
		conn, err := ln.Accept()
		if err != nil {
			os.Exit(0)
		}
		conn.Close()
	}
}

// spawnPortSquatter starts a subprocess that listens on an ephemeral port
// without answering HTTP, and returns the port it holds.
func spawnPortSquatter(t *testing.T) (*exec.Cmd, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_LISTENER=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("helper did not report port: %v", err)
	}
	port, err := strconv.Atoi(line[:len(line)-1])
	if err != nil {
		t.Fatal(err)
	}
	return cmd, port
}

func waitProbe(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		probe, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			probe.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("port still in use after takeover")
}

func TestTakeoverPortKillsStaleProcess(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WHATSAPP_MCP_DIR", dir)
	cmd, port := spawnPortSquatter(t)

	pidPath := filepath.Join(dir, "daemon.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0644); err != nil {
		t.Fatal(err)
	}

	if err := TakeoverPort(port); err != nil {
		t.Fatalf("takeover of stale process: %v", err)
	}
	waitProbe(t, port)
}

func TestTakeoverPortWithoutPIDFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("lsof fallback is unix-only")
	}
	t.Setenv("WHATSAPP_MCP_DIR", t.TempDir())
	_, port := spawnPortSquatter(t)

	if err := TakeoverPort(port); err != nil {
		t.Fatalf("takeover without pid file: %v", err)
	}
	waitProbe(t, port)
}
