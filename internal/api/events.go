package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/lncitador/whatsapp-mcp/internal/stream"
)

// heartbeatInterval is how often an idle stream emits an SSE comment. Proxies
// and clients commonly drop connections that stay silent for ~30-60s.
const heartbeatInterval = 15 * time.Second

// eventFilter narrows the stream to the events a client asked for.
type eventFilter struct {
	chatJID      string
	fromMe       *bool
	includeMedia bool
}

// parseEventFilter reads the query string. Defaults are permissive: no
// chat filter, both directions, media messages included.
//
//	?chat_jid=...        only this chat
//	?from_me=false       only incoming (true: only outgoing)
//	?include_media=false text-only messages
func parseEventFilter(q url.Values) eventFilter {
	f := eventFilter{chatJID: q.Get("chat_jid"), includeMedia: true}
	if v := q.Get("from_me"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			f.fromMe = &b
		}
	}
	if v := q.Get("include_media"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			f.includeMedia = b
		}
	}
	return f
}

func (f eventFilter) match(evt stream.Event) bool {
	if f.chatJID != "" && evt.ChatJID != f.chatJID {
		return false
	}
	if f.fromMe != nil && evt.IsFromMe != *f.fromMe {
		return false
	}
	if !f.includeMedia && evt.MediaType != "" {
		return false
	}
	return true
}

// handleEvents streams new WhatsApp messages as Server-Sent Events. It is
// deliberately not behind rateLimitMiddleware: that limiter is per-tool and
// meant for RPC bursts, and a long-lived stream must not be cut off by it.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, 500, "streaming unsupported by this connection")
		return
	}
	filter := parseEventFilter(r.URL.Query())

	// Subscribe before announcing readiness so nothing published between the
	// header write and the subscription is missed.
	sub := stream.Subscribe()
	defer stream.Unsubscribe(sub)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			// Client disconnected; the deferred Unsubscribe releases the slot.
			return
		case <-ticker.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case evt, open := <-sub.C:
			if !open {
				return
			}
			if !filter.match(evt) {
				continue
			}
			// Message content is untrusted; strip the same control characters
			// the RPC path strips before handing it to an LLM client.
			evt.Text = sanitizeContent(evt.Text)
			evt.ChatName = sanitizeContent(evt.ChatName)
			evt.SenderName = sanitizeContent(evt.SenderName)
			payload, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", payload)
			flusher.Flush()
		}
	}
}
