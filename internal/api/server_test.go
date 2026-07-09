package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lncitador/whatsapp-mcp/internal/store"
	"github.com/lncitador/whatsapp-mcp/internal/wa"
)

type fakeWA struct{ sent [][3]string }

func (f *fakeWA) Status() wa.Status { return wa.Status{State: wa.AuthConnected} }
func (f *fakeWA) SendMessage(r, m, p string) (bool, string) {
	f.sent = append(f.sent, [3]string{r, m, p})
	return true, "Message sent to " + r
}
func (f *fakeWA) DownloadMedia(id, jid string) (string, string, string, error) {
	return "/tmp/x.jpg", "image", "x.jpg", nil
}

func newTestServer(t *testing.T) (*httptest.Server, *fakeWA, *store.Store) {
	t.Helper()
	t.Setenv("WHATSAPP_MCP_DIR", t.TempDir())
	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	f := &fakeWA{}
	s := New(Deps{Store: st, WA: f, Version: "test"})
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, f, st
}

func TestHealthAndStatus(t *testing.T) {
	ts, _, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/health")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("health: %v %v", resp, err)
	}
	resp, _ = http.Get(ts.URL + "/status")
	var got wa.Status
	json.NewDecoder(resp.Body).Decode(&got)
	if got.State != wa.AuthConnected {
		t.Fatalf("status = %+v", got)
	}
}

func TestRPCSearchContacts(t *testing.T) {
	ts, _, st := newTestServer(t)
	st.StoreChat("5511999999999@s.whatsapp.net", "Alice", time.Now())
	resp, err := http.Post(ts.URL+"/api/rpc/search_contacts", "application/json",
		strings.NewReader(`{"query":"alice"}`))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("rpc: %v %v", resp.StatusCode, err)
	}
	var body struct {
		Result []store.Contact `json:"result"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Result) != 1 || body.Result[0].Name != "Alice" {
		t.Fatalf("got %+v", body)
	}
}

func TestRPCSendMessage(t *testing.T) {
	ts, f, _ := newTestServer(t)
	resp, _ := http.Post(ts.URL+"/api/rpc/send_message", "application/json",
		strings.NewReader(`{"recipient":"5511999999999","message":"oi"}`))
	if resp.StatusCode != 200 || len(f.sent) != 1 || f.sent[0][1] != "oi" {
		t.Fatalf("status=%d sent=%v", resp.StatusCode, f.sent)
	}
}

func TestRPCUnknownTool(t *testing.T) {
	ts, _, _ := newTestServer(t)
	resp, _ := http.Post(ts.URL+"/api/rpc/nope", "application/json", strings.NewReader(`{}`))
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestFormatMessageSanitizesContent(t *testing.T) {
	ts, _, st := newTestServer(t)
	st.StoreChat("5511999999999@s.whatsapp.net", "Alice", time.Now())
	st.StoreMessage(store.NewMessage{
		ID: "san1", ChatJID: "5511999999999@s.whatsapp.net",
		Sender: "5511999999999", Content: "hello\u200Bworld\u202Emalicious",
		Timestamp: time.Now(), IsFromMe: false,
	})

	resp, _ := http.Post(ts.URL+"/api/rpc/list_messages", "application/json",
		strings.NewReader(`{"query":"hello","limit":1}`))
	var body struct {
		Result string `json:"result"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if strings.Contains(body.Result, "\u200B") || strings.Contains(body.Result, "\u202E") {
		t.Fatalf("content not sanitized: %q", body.Result)
	}
	if !strings.Contains(body.Result, "helloworldmalicious") {
		t.Fatalf("sanitized content missing: %q", body.Result)
	}
}

func TestLegacySendEndpoint(t *testing.T) {
	ts, f, _ := newTestServer(t)
	resp, _ := http.Post(ts.URL+"/api/send", "application/json",
		strings.NewReader(`{"recipient":"x","message":"legacy"}`))
	if resp.StatusCode != 200 || len(f.sent) != 1 {
		t.Fatalf("legacy send failed: %d", resp.StatusCode)
	}
}
