package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	var body struct {
		Result struct {
			Success   bool   `json:"success"`
			Message   string `json:"message"`
			RequestID string `json:"request_id,omitempty"`
			Status    string `json:"status,omitempty"`
		} `json:"result"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if resp.StatusCode != 200 || body.Result.Status != "pending_approval" || body.Result.RequestID == "" {
		t.Fatalf("status=%d body=%+v", resp.StatusCode, body.Result)
	}
	if len(f.sent) != 0 {
		t.Fatalf("should not have sent yet: %v", f.sent)
	}

	// Approve the request
	resp2, _ := http.Post(ts.URL+"/api/approve/"+body.Result.RequestID, "application/json",
		strings.NewReader(`{}`))
	if resp2.StatusCode != 200 {
		t.Fatalf("approve: want 200, got %d", resp2.StatusCode)
	}
	if len(f.sent) != 1 || f.sent[0][1] != "oi" {
		t.Fatalf("want 1 sent with 'oi', got %v", f.sent)
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

func TestSendFileRejectsPathTraversal(t *testing.T) {
	ts, f, _ := newTestServer(t)
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.txt")
	os.WriteFile(outsideFile, []byte("secret"), 0644)

	resp, _ := http.Post(ts.URL+"/api/rpc/send_file", "application/json",
		strings.NewReader(fmt.Sprintf(`{"recipient":"5511999999999","media_path":"%s"}`, outsideFile)))
	if resp.StatusCode != 400 {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
	if len(f.sent) != 0 {
		t.Fatalf("message should not have been sent: %v", f.sent)
	}
}

func TestSendFileAcceptsValidPath(t *testing.T) {
	ts, f, _ := newTestServer(t)
	dir := os.Getenv("WHATSAPP_MCP_DIR")
	mediaDir := filepath.Join(dir, "media")
	os.MkdirAll(mediaDir, 0755)
	photo := filepath.Join(mediaDir, "photo.jpg")
	os.WriteFile(photo, []byte("fake"), 0644)

	resp, _ := http.Post(ts.URL+"/api/rpc/send_file", "application/json",
		strings.NewReader(fmt.Sprintf(`{"recipient":"5511999999999","media_path":"%s"}`, photo)))
	var body struct {
		Result struct {
			Success   bool   `json:"success"`
			Status    string `json:"status"`
			RequestID string `json:"request_id"`
		} `json:"result"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if resp.StatusCode != 200 || body.Result.Status != "pending_approval" || body.Result.RequestID == "" {
		t.Fatalf("want pending_approval, got status=%d body=%+v", resp.StatusCode, body.Result)
	}
	if len(f.sent) != 0 {
		t.Fatalf("should not have sent yet: %v", f.sent)
	}

	// Approve
	resp2, _ := http.Post(ts.URL+"/api/approve/"+body.Result.RequestID, "application/json",
		strings.NewReader(`{}`))
	if resp2.StatusCode != 200 {
		t.Fatalf("approve: want 200, got %d", resp2.StatusCode)
	}
	if len(f.sent) != 1 {
		t.Fatalf("want 1 sent after approval, got %d", len(f.sent))
	}
}

func TestSendAudioRejectsPathTraversal(t *testing.T) {
	ts, f, _ := newTestServer(t)
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.ogg")
	os.WriteFile(outsideFile, []byte("secret"), 0644)

	resp, _ := http.Post(ts.URL+"/api/rpc/send_audio_message", "application/json",
		strings.NewReader(fmt.Sprintf(`{"recipient":"5511999999999","media_path":"%s"}`, outsideFile)))
	if resp.StatusCode != 400 {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
	if len(f.sent) != 0 {
		t.Fatalf("message should not have been sent: %v", f.sent)
	}
}

func TestRateLimiting(t *testing.T) {
	ts, _, _ := newTestServer(t)

	for i := 0; i < 10; i++ {
		resp, _ := http.Post(ts.URL+"/api/rpc/search_contacts", "application/json",
			strings.NewReader(`{"query":"test"}`))
		resp.Body.Close()
		if resp.StatusCode == 429 {
			t.Fatalf("rate limited too early at request %d", i)
		}
	}

	resp, _ := http.Post(ts.URL+"/api/rpc/search_contacts", "application/json",
		strings.NewReader(`{"query":"test"}`))
	resp.Body.Close()
	if resp.StatusCode != 429 {
		t.Fatalf("want 429, got %d", resp.StatusCode)
	}
}

func TestSendAudioAcceptsValidPath(t *testing.T) {
	ts, f, _ := newTestServer(t)
	dir := os.Getenv("WHATSAPP_MCP_DIR")
	mediaDir := filepath.Join(dir, "media")
	os.MkdirAll(mediaDir, 0755)
	audio := filepath.Join(mediaDir, "voice.ogg")
	os.WriteFile(audio, []byte("fake"), 0644)

	resp, _ := http.Post(ts.URL+"/api/rpc/send_audio_message", "application/json",
		strings.NewReader(fmt.Sprintf(`{"recipient":"5511999999999","media_path":"%s"}`, audio)))
	var body struct {
		Result struct {
			Success   bool   `json:"success"`
			Status    string `json:"status"`
			RequestID string `json:"request_id"`
		} `json:"result"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if resp.StatusCode != 200 || body.Result.Status != "pending_approval" || body.Result.RequestID == "" {
		t.Fatalf("want pending_approval, got status=%d body=%+v", resp.StatusCode, body.Result)
	}
	if len(f.sent) != 0 {
		t.Fatalf("should not have sent yet: %v", f.sent)
	}

	// Approve
	resp2, _ := http.Post(ts.URL+"/api/approve/"+body.Result.RequestID, "application/json",
		strings.NewReader(`{}`))
	if resp2.StatusCode != 200 {
		t.Fatalf("approve: want 200, got %d", resp2.StatusCode)
	}
	if len(f.sent) != 1 {
		t.Fatalf("want 1 sent after approval, got %d", len(f.sent))
	}
}

func TestSendMessageRequiresApproval(t *testing.T) {
	ts, f, _ := newTestServer(t)

	resp, _ := http.Post(ts.URL+"/api/rpc/send_message", "application/json",
		strings.NewReader(`{"recipient":"5511999999999","message":"test"}`))
	var body struct {
		Result struct {
			Success   bool   `json:"success"`
			Message   string `json:"message"`
			RequestID string `json:"request_id,omitempty"`
			Status    string `json:"status,omitempty"`
		} `json:"result"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if body.Result.Status != "pending_approval" {
		t.Fatalf("want pending_approval, got %+v", body.Result)
	}
	if body.Result.RequestID == "" {
		t.Fatal("want non-empty request_id")
	}
	if len(f.sent) != 0 {
		t.Fatalf("should not have sent yet: %v", f.sent)
	}

	// Approve the request
	resp2, _ := http.Post(ts.URL+"/api/approve/"+body.Result.RequestID, "application/json",
		strings.NewReader(`{}`))
	if resp2.StatusCode != 200 {
		t.Fatalf("approve: want 200, got %d", resp2.StatusCode)
	}
	if len(f.sent) != 1 {
		t.Fatalf("want 1 sent after approval, got %d", len(f.sent))
	}
}

func TestSendMessageReject(t *testing.T) {
	ts, f, _ := newTestServer(t)

	resp, _ := http.Post(ts.URL+"/api/rpc/send_message", "application/json",
		strings.NewReader(`{"recipient":"5511999999999","message":"test"}`))
	var body struct {
		Result struct {
			RequestID string `json:"request_id,omitempty"`
		} `json:"result"`
	}
	json.NewDecoder(resp.Body).Decode(&body)

	// Reject the request
	resp2, _ := http.Post(ts.URL+"/api/reject/"+body.Result.RequestID, "application/json",
		strings.NewReader(`{}`))
	if resp2.StatusCode != 200 {
		t.Fatalf("reject: want 200, got %d", resp2.StatusCode)
	}
	if len(f.sent) != 0 {
		t.Fatalf("should not have sent after rejection: %v", f.sent)
	}
}

func TestApproveNotFound(t *testing.T) {
	ts, _, _ := newTestServer(t)
	resp, _ := http.Post(ts.URL+"/api/approve/req_nonexistent", "application/json",
		strings.NewReader(`{}`))
	if resp.StatusCode != 404 {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

func TestRejectNotFound(t *testing.T) {
	ts, _, _ := newTestServer(t)
	resp, _ := http.Post(ts.URL+"/api/reject/req_nonexistent", "application/json",
		strings.NewReader(`{}`))
	if resp.StatusCode != 404 {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}
