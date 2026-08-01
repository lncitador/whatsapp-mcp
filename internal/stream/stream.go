// Package stream is an in-process broadcast hub for live WhatsApp message
// events. The WhatsApp handlers publish into it right after a message is
// persisted; the daemon's SSE endpoint (GET /api/events) subscribes and fans
// events out to connected agents, so a client can tail new messages without
// touching the SQLite store.
//
// Publishing never blocks: a subscriber that stops draining its channel loses
// its oldest queued events instead of stalling the WhatsApp connection.
package stream

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// bufferSize is how many events a subscriber may fall behind before the hub
// starts discarding its oldest ones.
const bufferSize = 256

// Event is one persisted WhatsApp message, flattened for JSON delivery.
type Event struct {
	ID         string `json:"id"`
	ChatJID    string `json:"chat_jid"`
	ChatName   string `json:"chat_name"`
	Sender     string `json:"sender"`
	SenderName string `json:"sender_name"`
	IsFromMe   bool   `json:"is_from_me"`
	Timestamp  string `json:"timestamp"`
	Text       string `json:"text"`
	MediaType  string `json:"media_type"`
	Filename   string `json:"filename"`
}

// Subscription is a live feed of events. Read from C; call Unsubscribe (or
// Close) exactly once when done — both are safe to call more than once.
type Subscription struct {
	// C delivers events until the subscription is closed.
	C <-chan Event

	ch   chan Event
	h    *hub
	once sync.Once
}

// Close detaches the subscription from the hub and closes C.
func (s *Subscription) Close() {
	s.once.Do(func() { s.h.remove(s) })
}

type hub struct {
	mu      sync.Mutex
	subs    map[*Subscription]struct{}
	dropped atomic.Uint64
}

var defaultHub = &hub{subs: make(map[*Subscription]struct{})}

// Subscribe registers a new subscriber and returns its feed.
func Subscribe() *Subscription { return defaultHub.subscribe() }

// Unsubscribe detaches sub from the hub. Calling it twice, or with a nil
// subscription, is a no-op.
func Unsubscribe(sub *Subscription) {
	if sub == nil {
		return
	}
	sub.Close()
}

// Publish broadcasts evt to every current subscriber. It never blocks and
// never panics, even with no subscribers at all.
func Publish(evt Event) { defaultHub.publish(evt) }

// SubscriberCount reports how many subscriptions are currently attached.
func SubscriberCount() int { return defaultHub.count() }

// Dropped reports how many events have been discarded because a subscriber
// was not draining fast enough.
func Dropped() uint64 { return defaultHub.dropped.Load() }

// PublishMessage builds an Event from the values the WhatsApp handlers have on
// hand and broadcasts it. Kept as a single call so the handlers only need one
// line at each persist site.
//
// sender may arrive as a bare user part ("5511999999999") on the live path or
// as a full JID ("5511999999999@s.whatsapp.net") on the history-sync path; it
// is normalized to the bare user part here so both look the same to clients.
func PublishMessage(id, chatJID, chatName, sender string, isFromMe bool, ts time.Time, text, mediaType, filename string) {
	Publish(Event{
		ID:         id,
		ChatJID:    chatJID,
		ChatName:   chatName,
		Sender:     bareUser(sender),
		SenderName: senderNameFor(chatJID, chatName, isFromMe),
		IsFromMe:   isFromMe,
		Timestamp:  ts.Format(time.RFC3339),
		Text:       text,
		MediaType:  mediaType,
		Filename:   filename,
	})
}

func bareUser(sender string) string {
	if i := strings.Index(sender, "@"); i >= 0 {
		return sender[:i]
	}
	return sender
}

// senderNameFor derives a display name for the sender from what the handlers
// already resolved. In a 1:1 chat the chat name IS the other party's contact
// name, so it doubles as the sender name; in a group the chat name is the
// group's, and an outgoing message's chat name is the recipient's — neither
// identifies the sender, so those return "" and clients fall back to Sender.
func senderNameFor(chatJID, chatName string, isFromMe bool) string {
	if isFromMe || strings.Contains(chatJID, "@g.us") {
		return ""
	}
	return chatName
}

func (h *hub) subscribe() *Subscription {
	sub := &Subscription{ch: make(chan Event, bufferSize), h: h}
	sub.C = sub.ch
	h.mu.Lock()
	h.subs[sub] = struct{}{}
	h.mu.Unlock()
	return sub
}

func (h *hub) remove(sub *Subscription) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[sub]; !ok {
		return
	}
	delete(h.subs, sub)
	// Safe to close: publish only sends while holding h.mu, and this
	// subscription is no longer in the map.
	close(sub.ch)
}

func (h *hub) publish(evt Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs {
		select {
		case sub.ch <- evt:
		default:
			// Full: drop the oldest queued event so a slow subscriber
			// keeps the most recent messages instead of blocking us.
			select {
			case <-sub.ch:
				h.dropped.Add(1)
			default:
			}
			select {
			case sub.ch <- evt:
			default:
				h.dropped.Add(1)
			}
		}
	}
}

func (h *hub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}
