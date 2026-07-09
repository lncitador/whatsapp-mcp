package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCallRPCForwardsArgsAndReturnsResult(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"result":[{"name":"Alice"}]}`))
	}))
	defer ts.Close()

	out, err := callRPC(ts.URL, "search_contacts", map[string]any{"query": "ali"})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/rpc/search_contacts" || gotBody["query"] != "ali" {
		t.Fatalf("path=%q body=%v", gotPath, gotBody)
	}
	if out != `[{"name":"Alice"}]` {
		t.Fatalf("out = %q", out)
	}
}

func TestCallRPCSurfacesDaemonError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"error":"boom"}`))
	}))
	defer ts.Close()
	if _, err := callRPC(ts.URL, "list_chats", map[string]any{}); err == nil || err.Error() != "boom" {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestNewRegistersAllTools(t *testing.T) {
	s := New("test", "http://127.0.0.1:1")
	if s == nil {
		t.Fatal("nil server")
	}
}
