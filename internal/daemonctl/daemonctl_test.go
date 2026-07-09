package daemonctl

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
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
