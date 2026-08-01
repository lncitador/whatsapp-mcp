package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lncitador/whatsapp-mcp/internal/stream"
)

// openEventStream connects to /api/events and returns a reader positioned
// after the ": connected" preamble, so the caller only sees real events.
func openEventStream(t *testing.T, baseURL, query string) (*bufio.Reader, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/api/events"+query, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })

	if resp.StatusCode != 200 {
		cancel()
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		cancel()
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	br := bufio.NewReader(resp.Body)
	line, err := br.ReadString('\n')
	if err != nil {
		cancel()
		t.Fatalf("reading preamble: %v", err)
	}
	if !strings.HasPrefix(line, ": connected") {
		cancel()
		t.Fatalf("preamble = %q", line)
	}
	// The preamble arriving proves the handler flushed; the subscription is
	// registered before that write.
	return br, cancel
}

// readEvent reads SSE frames until one carries a data payload, then decodes
// it. Comment lines (heartbeats) and blank separators are skipped.
func readEvent(t *testing.T, br *bufio.Reader) stream.Event {
	t.Helper()
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("reading event: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var evt stream.Event
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &evt); err != nil {
			t.Fatalf("decoding %q: %v", line, err)
		}
		return evt
	}
}

// waitForSubscribers polls until the hub reaches want, so tests never race
// against the handler goroutine.
func waitForSubscribers(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if stream.SubscriberCount() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("SubscriberCount() = %d, want %d", stream.SubscriberCount(), want)
}

func TestEventsStreamDeliversPublishedMessage(t *testing.T) {
	ts, _, _ := newTestServer(t)
	br, cancel := openEventStream(t, ts.URL, "")
	defer cancel()
	waitForSubscribers(t, 1)

	stream.PublishMessage("MSG1", "5511999999999@s.whatsapp.net", "Alice", "5511999999999",
		false, time.Date(2026, 7, 31, 23, 12, 3, 0, time.UTC), "hello there", "", "")

	evt := readEvent(t, br)
	if evt.ID != "MSG1" || evt.Text != "hello there" {
		t.Fatalf("got %+v", evt)
	}
	if evt.ChatName != "Alice" || evt.SenderName != "Alice" || evt.Sender != "5511999999999" {
		t.Fatalf("names not resolved: %+v", evt)
	}
	if evt.Timestamp != "2026-07-31T23:12:03Z" {
		t.Fatalf("Timestamp = %q, want RFC3339", evt.Timestamp)
	}
}

func TestEventsStreamUnsubscribesOnDisconnect(t *testing.T) {
	ts, _, _ := newTestServer(t)
	_, cancel := openEventStream(t, ts.URL, "")
	waitForSubscribers(t, 1)

	cancel()

	// The handler must notice r.Context() cancellation and release its slot.
	waitForSubscribers(t, 0)
}

func TestEventsStreamFilters(t *testing.T) {
	const alice = "5511999999999@s.whatsapp.net"
	const group = "120363000000000000@g.us"

	tests := []struct {
		name    string
		query   string
		want    string // ID of the event expected to arrive first
		publish func()
	}{
		{
			name: "chat_jid drops other chats", query: "?chat_jid=" + alice, want: "WANTED",
			publish: func() {
				stream.PublishMessage("OTHER", group, "Team", "5511888888888", false, time.Now(), "noise", "", "")
				stream.PublishMessage("WANTED", alice, "Alice", "5511999999999", false, time.Now(), "hi", "", "")
			},
		},
		{
			name: "from_me=false drops outgoing", query: "?from_me=false", want: "WANTED",
			publish: func() {
				stream.PublishMessage("OTHER", alice, "Alice", "me", true, time.Now(), "sent", "", "")
				stream.PublishMessage("WANTED", alice, "Alice", "5511999999999", false, time.Now(), "hi", "", "")
			},
		},
		{
			name: "from_me=true drops incoming", query: "?from_me=true", want: "WANTED",
			publish: func() {
				stream.PublishMessage("OTHER", alice, "Alice", "5511999999999", false, time.Now(), "in", "", "")
				stream.PublishMessage("WANTED", alice, "Alice", "me", true, time.Now(), "out", "", "")
			},
		},
		{
			name: "include_media=false drops media", query: "?include_media=false", want: "WANTED",
			publish: func() {
				stream.PublishMessage("OTHER", alice, "Alice", "5511999999999", false, time.Now(), "", "image", "x.jpg")
				stream.PublishMessage("WANTED", alice, "Alice", "5511999999999", false, time.Now(), "text only", "", "")
			},
		},
		{
			name: "include_media=true keeps media", query: "?include_media=true", want: "WANTED",
			publish: func() {
				stream.PublishMessage("WANTED", alice, "Alice", "5511999999999", false, time.Now(), "", "image", "x.jpg")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, _, _ := newTestServer(t)
			br, cancel := openEventStream(t, ts.URL, tt.query)
			defer cancel()
			waitForSubscribers(t, 1)

			tt.publish()

			if evt := readEvent(t, br); evt.ID != tt.want {
				t.Fatalf("first delivered event = %q, want %q (filter not applied)", evt.ID, tt.want)
			}
		})
	}
}

func TestEventsStreamSanitizesContent(t *testing.T) {
	ts, _, _ := newTestServer(t)
	br, cancel := openEventStream(t, ts.URL, "")
	defer cancel()
	waitForSubscribers(t, 1)

	// Zero-width space and a right-to-left override, as stripped by the RPC path.
	stream.PublishMessage("MSG1", "5511999999999@s.whatsapp.net", "Al​ice", "5511999999999",
		false, time.Now(), "he‮llo", "", "")

	evt := readEvent(t, br)
	if evt.Text != "hello" {
		t.Errorf("Text = %q, want sanitized %q", evt.Text, "hello")
	}
	if evt.ChatName != "Alice" || evt.SenderName != "Alice" {
		t.Errorf("names not sanitized: %q / %q", evt.ChatName, evt.SenderName)
	}
}
