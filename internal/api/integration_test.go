package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestFullApprovalFlow(t *testing.T) {
	ts, f, st := newTestServer(t)

	// 1. Send message -> pending approval
	resp, err := http.Post(ts.URL+"/api/rpc/send_message", "application/json",
		strings.NewReader(`{"recipient":"5511999999999","message":"hello"}`))
	if err != nil {
		t.Fatalf("step 1: request failed: %v", err)
	}
	var pending struct {
		Result struct {
			Status    string `json:"status"`
			RequestID string `json:"request_id"`
		} `json:"result"`
	}
	json.NewDecoder(resp.Body).Decode(&pending)
	resp.Body.Close()
	if pending.Result.Status != "pending_approval" {
		t.Fatalf("step 1: want pending_approval, got %s", pending.Result.Status)
	}
	if pending.Result.RequestID == "" {
		t.Fatal("step 1: want non-empty request_id")
	}

	// 2. Approve
	resp2, err := http.Post(ts.URL+"/api/approve/"+pending.Result.RequestID, "application/json",
		strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("step 2: request failed: %v", err)
	}
	var approved struct {
		Result struct {
			Success bool `json:"success"`
		} `json:"result"`
	}
	json.NewDecoder(resp2.Body).Decode(&approved)
	resp2.Body.Close()
	if !approved.Result.Success {
		t.Fatal("step 2: approval should succeed")
	}
	if len(f.sent) != 1 || f.sent[0][1] != "hello" {
		t.Fatalf("step 2: want message sent with 'hello', got %v", f.sent)
	}

	// 3. Rate limiting: exhaust 10 requests on search_contacts
	st.StoreChat("5511999999999@s.whatsapp.net", "Alice", time.Now())
	for i := 0; i < 10; i++ {
		r, _ := http.Post(ts.URL+"/api/rpc/search_contacts", "application/json",
			strings.NewReader(`{"query":"test"}`))
		r.Body.Close()
	}

	// 4. Next request should be rate limited
	resp3, _ := http.Post(ts.URL+"/api/rpc/search_contacts", "application/json",
		strings.NewReader(`{"query":"test"}`))
	resp3.Body.Close()
	if resp3.StatusCode != 429 {
		t.Fatalf("step 4: want 429, got %d", resp3.StatusCode)
	}

	// 5. Path traversal blocked on send_file
	resp4, _ := http.Post(ts.URL+"/api/rpc/send_file", "application/json",
		strings.NewReader(`{"recipient":"5511999999999","media_path":"../../etc/passwd"}`))
	resp4.Body.Close()
	if resp4.StatusCode != 400 {
		t.Fatalf("step 5: want 400, got %d", resp4.StatusCode)
	}
}

func TestRejectFlow(t *testing.T) {
	ts, f, _ := newTestServer(t)

	// Send -> pending
	resp, err := http.Post(ts.URL+"/api/rpc/send_message", "application/json",
		strings.NewReader(`{"recipient":"5511999999999","message":"test"}`))
	if err != nil {
		t.Fatalf("send: request failed: %v", err)
	}
	var pending struct {
		Result struct {
			RequestID string `json:"request_id"`
		} `json:"result"`
	}
	json.NewDecoder(resp.Body).Decode(&pending)
	resp.Body.Close()
	if pending.Result.RequestID == "" {
		t.Fatal("want non-empty request_id")
	}

	// Reject
	resp2, _ := http.Post(ts.URL+"/api/reject/"+pending.Result.RequestID, "application/json",
		strings.NewReader(`{}`))
	resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("reject: want 200, got %d", resp2.StatusCode)
	}
	if len(f.sent) != 0 {
		t.Fatalf("should not have sent after rejection: %v", f.sent)
	}

	// Approving a rejected request should 404
	resp3, _ := http.Post(ts.URL+"/api/approve/"+pending.Result.RequestID, "application/json",
		strings.NewReader(`{}`))
	resp3.Body.Close()
	if resp3.StatusCode != 404 {
		t.Fatalf("re-approve: want 404, got %d", resp3.StatusCode)
	}
}
