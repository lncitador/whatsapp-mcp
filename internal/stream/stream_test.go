package stream

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestPublishWithoutSubscribersDoesNotPanic(t *testing.T) {
	Publish(Event{ID: "nobody-listening"})
	if got := SubscriberCount(); got != 0 {
		t.Fatalf("SubscriberCount() = %d, want 0", got)
	}
}

func TestSubscribeReceivesEvent(t *testing.T) {
	sub := Subscribe()
	defer Unsubscribe(sub)

	Publish(Event{ID: "abc", ChatJID: "5511@s.whatsapp.net", Text: "hi"})

	select {
	case evt := <-sub.C:
		if evt.ID != "abc" || evt.Text != "hi" {
			t.Fatalf("got %+v", evt)
		}
	case <-time.After(time.Second):
		t.Fatal("no event delivered")
	}
}

func TestUnsubscribeClosesChannelAndIsIdempotent(t *testing.T) {
	sub := Subscribe()
	if got := SubscriberCount(); got != 1 {
		t.Fatalf("SubscriberCount() = %d, want 1", got)
	}

	Unsubscribe(sub)
	Unsubscribe(sub) // must not panic on a double close
	sub.Close()

	if got := SubscriberCount(); got != 0 {
		t.Fatalf("SubscriberCount() after unsubscribe = %d, want 0", got)
	}
	if _, open := <-sub.C; open {
		t.Fatal("channel still open after Unsubscribe")
	}
}

// A subscriber that never reads must not be able to stall Publish — that
// caller is the WhatsApp event handler.
func TestPublishDoesNotBlockOnSlowSubscriber(t *testing.T) {
	slow := Subscribe()
	defer Unsubscribe(slow)

	const n = bufferSize * 4
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < n; i++ {
			Publish(Event{ID: "flood"})
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish blocked on a subscriber that never reads")
	}

	if Dropped() == 0 {
		t.Fatal("expected dropped events after flooding a slow subscriber")
	}
	// Drop-oldest keeps the buffer full rather than empty.
	if len(slow.ch) != bufferSize {
		t.Fatalf("buffered events = %d, want %d", len(slow.ch), bufferSize)
	}
}

// A subscriber whose buffer is permanently full must not starve one that
// keeps draining. Publishing and reading in lockstep keeps this deterministic:
// the fast subscriber never queues more than one event, so it must see every
// one, however far past bufferSize the slow subscriber has fallen behind.
func TestSlowSubscriberDoesNotStarveFastSubscriber(t *testing.T) {
	slow := Subscribe()
	defer Unsubscribe(slow)
	fast := Subscribe()
	defer Unsubscribe(fast)

	const n = bufferSize * 2
	for i := 0; i < n; i++ {
		want := strconv.Itoa(i)
		Publish(Event{ID: want})
		select {
		case evt := <-fast.C:
			if evt.ID != want {
				t.Fatalf("event %d: fast subscriber got %q, want %q", i, evt.ID, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("fast subscriber starved at event %d while the slow one was full", i)
		}
	}

	if len(slow.ch) != bufferSize {
		t.Fatalf("slow subscriber buffered %d events, want it pinned at %d", len(slow.ch), bufferSize)
	}
}

func TestConcurrentSubscribeUnsubscribePublish(t *testing.T) {
	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				Publish(Event{ID: "race"})
			}
		}
	}()

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				sub := Subscribe()
				select {
				case <-sub.C:
				default:
				}
				Unsubscribe(sub)
			}
		}()
	}

	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()

	if got := SubscriberCount(); got != 0 {
		t.Fatalf("SubscriberCount() = %d, want 0", got)
	}
}

func TestPublishMessageNormalizesSenderAndName(t *testing.T) {
	ts := time.Date(2026, 7, 31, 23, 12, 3, 0, time.UTC)

	tests := []struct {
		name           string
		chatJID        string
		chatName       string
		sender         string
		isFromMe       bool
		wantSender     string
		wantSenderName string
	}{
		{
			name: "direct chat uses chat name as sender name", chatJID: "5511999999999@s.whatsapp.net",
			chatName: "Alice", sender: "5511999999999",
			wantSender: "5511999999999", wantSenderName: "Alice",
		},
		{
			name: "history sync full JID is reduced to the user part", chatJID: "5511999999999@s.whatsapp.net",
			chatName: "Alice", sender: "5511999999999@s.whatsapp.net",
			wantSender: "5511999999999", wantSenderName: "Alice",
		},
		{
			name: "group chat name does not identify the sender", chatJID: "120363000000000000@g.us",
			chatName: "Team", sender: "5511888888888@s.whatsapp.net",
			wantSender: "5511888888888", wantSenderName: "",
		},
		{
			name: "outgoing message chat name is the recipient", chatJID: "5511999999999@s.whatsapp.net",
			chatName: "Alice", sender: "5511777777777", isFromMe: true,
			wantSender: "5511777777777", wantSenderName: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub := Subscribe()
			defer Unsubscribe(sub)

			PublishMessage("id1", tt.chatJID, tt.chatName, tt.sender, tt.isFromMe, ts, "hello", "image", "x.jpg")

			select {
			case evt := <-sub.C:
				if evt.Sender != tt.wantSender {
					t.Errorf("Sender = %q, want %q", evt.Sender, tt.wantSender)
				}
				if evt.SenderName != tt.wantSenderName {
					t.Errorf("SenderName = %q, want %q", evt.SenderName, tt.wantSenderName)
				}
				if evt.Timestamp != "2026-07-31T23:12:03Z" {
					t.Errorf("Timestamp = %q, want RFC3339", evt.Timestamp)
				}
				if evt.MediaType != "image" || evt.Filename != "x.jpg" {
					t.Errorf("media = %q/%q", evt.MediaType, evt.Filename)
				}
			case <-time.After(time.Second):
				t.Fatal("no event delivered")
			}
		})
	}
}
