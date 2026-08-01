package api

import (
	"bytes"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lncitador/whatsapp-mcp/internal/stream"
)

// TestWatchScriptEndToEnd runs skills/whatsapp/scripts/watch.sh against a real
// HTTP server and asserts it prints an event published while it was listening.
// This is the only check that the script's SSE parsing, port resolution and
// --timeout exit actually line up with the endpoint.
func TestWatchScriptEndToEnd(t *testing.T) {
	for _, bin := range []string{"bash", "curl"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available", bin)
		}
	}
	script, err := filepath.Abs("../../skills/whatsapp/scripts/watch.sh")
	if err != nil {
		t.Fatal(err)
	}

	ts, _, _ := newTestServer(t)
	_, port, err := net.SplitHostPort(strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", script, "--timeout", "3", "--json")
	cmd.Env = append(cmd.Environ(), "WHATSAPP_MCP_PORT="+port, "WHATSAPP_MCP_DIR="+t.TempDir())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	// Publish only once the script's stream is attached, so the event cannot
	// be broadcast before it subscribes.
	waitForSubscribers(t, 1)
	stream.PublishMessage("WATCHED", "5511999999999@s.whatsapp.net", "Alice", "5511999999999",
		false, time.Now(), "from the script", "", "")

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("watch.sh exited with %v\nstderr:\n%s", err, stderr.String())
		}
	case <-time.After(20 * time.Second):
		cmd.Process.Kill()
		t.Fatalf("watch.sh did not exit after --timeout\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}

	out := stdout.String()
	if !strings.Contains(out, `"id":"WATCHED"`) || !strings.Contains(out, "from the script") {
		t.Fatalf("event missing from watch.sh output:\n%s\nstderr:\n%s", out, stderr.String())
	}
}

// TestWatchScriptFailsWithoutDaemon covers the "daemon not running" path: the
// script must tell the caller to run start.sh and exit non-zero.
func TestWatchScriptFailsWithoutDaemon(t *testing.T) {
	for _, bin := range []string{"bash", "curl"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available", bin)
		}
	}
	script, err := filepath.Abs("../../skills/whatsapp/scripts/watch.sh")
	if err != nil {
		t.Fatal(err)
	}

	// Bind a port, then release it, so nothing is listening there.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(l.Addr().String())
	l.Close()

	cmd := exec.Command("bash", script, "--timeout", "3")
	cmd.Env = append(cmd.Environ(), "WHATSAPP_MCP_PORT="+port, "WHATSAPP_MCP_DIR="+t.TempDir())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err == nil {
		t.Fatal("watch.sh exited 0 with no daemon running")
	}
	if !strings.Contains(stderr.String(), "start.sh") {
		t.Fatalf("stderr does not point at start.sh:\n%s", stderr.String())
	}
}
