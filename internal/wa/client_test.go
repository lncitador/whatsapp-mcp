package wa

import (
	"context"
	"strings"
	"testing"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/lncitador/whatsapp-mcp/internal/store"
)

func TestStatusTransitions(t *testing.T) {
	c := &Client{}
	c.setState(AuthConnecting, "", "")
	if s := c.Status(); s.State != AuthConnecting {
		t.Fatalf("state = %s", s.State)
	}
	c.setState(AuthWaitingQR, "QRDATA", "scan me")
	s := c.Status()
	if s.State != AuthWaitingQR || s.QRCode != "QRDATA" || s.Message != "scan me" {
		t.Fatalf("got %+v", s)
	}
	c.setState(AuthConnected, "", "")
	if s := c.Status(); s.QRCode != "" {
		t.Fatalf("QR must be cleared on connect: %+v", s)
	}
}

func TestStatusKeepsResyncResultAcrossStateChanges(t *testing.T) {
	c := &Client{}
	c.setState(AuthConnected, "", "")
	if s := c.Status(); s.LastResyncAt != nil {
		t.Fatalf("want no resync info before any resync, got %+v", s)
	}

	at := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	c.recordResync(resyncResult{at: at, reason: "connected", chats: 3, newMessages: 7})

	// A reconnect replaces the auth status; the resync evidence must survive.
	c.setState(AuthConnecting, "", "disconnected, reconnecting")
	s := c.Status()
	if s.LastResyncAt == nil || !s.LastResyncAt.Equal(at) {
		t.Fatalf("LastResyncAt = %v", s.LastResyncAt)
	}
	if s.LastResyncNewMessages != 7 || s.LastResyncChats != 3 || s.LastResyncReason != "connected" {
		t.Fatalf("got %+v", s)
	}
}

func TestHistorySyncAnchor(t *testing.T) {
	ts := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	ok := &store.Message{ID: "ABC", Timestamp: ts, IsFromMe: true}

	cases := []struct {
		name    string
		jid     string
		msg     *store.Message
		wantErr string
	}{
		{"nil message", "5511999999999@s.whatsapp.net", nil, "no stored message"},
		{"empty id", "5511999999999@s.whatsapp.net", &store.Message{Timestamp: ts}, "no ID"},
		{"zero timestamp", "5511999999999@s.whatsapp.net", &store.Message{ID: "ABC"}, "no timestamp"},
		{"empty jid", "", ok, "chat JID"},
		{"garbage jid", "not a jid", ok, "chat JID"},
		{"ok", "5511999999999@s.whatsapp.net", ok, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info, err := historySyncAnchor(tc.jid, tc.msg)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("want error containing %q, got info %+v", tc.wantErr, info)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want it to mention %q", err, tc.wantErr)
				}
				if info != nil {
					t.Fatalf("want nil info on error, got %+v", info)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// These are exactly the fields BuildHistorySyncRequest dereferences.
			if info.Chat.String() != tc.jid || string(info.ID) != "ABC" || !info.IsFromMe {
				t.Fatalf("got %+v", info)
			}
			if !info.Timestamp.Equal(ts) {
				t.Fatalf("timestamp = %v", info.Timestamp)
			}
		})
	}
}

// A zero-value Client has no whatsmeow client and no store: the resync must
// report an error instead of panicking on a nil dereference.
func TestRequestHistorySyncOnUninitialisedClient(t *testing.T) {
	c := &Client{logger: waLog.Noop}
	if err := c.RequestHistorySync(10); err == nil {
		t.Fatal("want error for uninitialised client")
	}
}

func TestRecoverPanicKeepsDaemonAlive(t *testing.T) {
	c := &Client{logger: waLog.Noop}
	func() {
		defer c.recoverPanic("test")
		panic("boom")
	}()
	// Reaching here means the panic did not escape the goroutine helper.
	c.recoverPanicValue("test", "boom")
}

// requestHistorySync must convert a panic into an error, since it also runs
// from the HTTP handler where a panic would kill the request.
func TestRequestHistorySyncRecoversPanic(t *testing.T) {
	var c *Client
	_, err := c.requestHistorySync("manual", 10)
	if err == nil {
		t.Fatal("want error from nil client")
	}
}

func TestClaimResyncDebounceAndForce(t *testing.T) {
	c := &Client{logger: waLog.Noop}
	if !c.claimResync(false) {
		t.Fatal("first resync must be allowed")
	}
	if c.claimResync(false) {
		t.Fatal("second resync inside the debounce window must be refused")
	}
	if !c.claimResync(true) {
		t.Fatal("forced resync must ignore the debounce")
	}
}

func TestDowntimeTracking(t *testing.T) {
	c := &Client{logger: waLog.Noop}
	if d := c.takeDowntime(); d != 0 {
		t.Fatalf("no outage yet, got %s", d)
	}

	c.noteDisconnected()
	first := c.disconnectedAt
	// whatsmeow emits Disconnected once per failed reconnect; only the first
	// one may count or the measured outage resets on every retry.
	time.Sleep(2 * time.Millisecond)
	c.noteDisconnected()
	if !c.disconnectedAt.Equal(first) {
		t.Fatal("repeated Disconnected must not restart the outage clock")
	}

	if d := c.takeDowntime(); d <= 0 {
		t.Fatalf("downtime = %s, want > 0", d)
	}
	if d := c.takeDowntime(); d != 0 {
		t.Fatalf("downtime must be cleared after being taken, got %s", d)
	}
}

func TestLongOutageForcesResyncThroughDebounce(t *testing.T) {
	c := &Client{logger: waLog.Noop}
	c.claimResync(false) // debounce now hot

	c.noteDisconnected()
	c.disconnectedAt = time.Now().Add(-30 * time.Minute)
	down := c.takeDowntime()
	if down < longDisconnectThreshold {
		t.Fatalf("downtime = %s, want >= %s", down, longDisconnectThreshold)
	}
	if !c.claimResync(down >= longDisconnectThreshold) {
		t.Fatal("resync after a long outage must run despite the hot debounce")
	}
}

func TestWaitReturnsFalseWhenStopped(t *testing.T) {
	c := &Client{logger: waLog.Noop, stopCh: make(chan struct{})}
	if !c.wait(time.Millisecond) {
		t.Fatal("wait must return true when it sleeps to completion")
	}
	close(c.stopCh)
	start := time.Now()
	if c.wait(time.Hour) {
		t.Fatal("wait must return false once the client is stopped")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("wait blocked for %s after stop", elapsed)
	}
}

// Waking up already disconnected must retry with backoff and then give up
// cleanly — never panic, never block shutdown.
func TestAutoResyncRetriesWhileDisconnectedThenGivesUp(t *testing.T) {
	restore := shrinkResyncTimings(t)
	defer restore()

	c := &Client{logger: waLog.Noop, stopCh: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.autoResync("connected", false)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("autoResync did not give up while disconnected")
	}
	if s := c.Status(); s.LastResyncAt != nil {
		t.Fatalf("a resync that never ran must not be reported: %+v", s)
	}
}

func TestAutoResyncStopsOnShutdown(t *testing.T) {
	restore := shrinkResyncTimings(t)
	defer restore()
	resyncInitialDelay = time.Hour // parked in the initial wait

	c := &Client{logger: waLog.Noop, stopCh: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.autoResync("connected", false)
	}()
	c.Stop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("autoResync ignored shutdown")
	}
}

func TestResyncLoopExitsOnContextCancel(t *testing.T) {
	restore := shrinkResyncTimings(t)
	defer restore()

	c := &Client{logger: waLog.Noop, stopCh: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.resyncLoop(ctx)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("resyncLoop leaked after context cancel")
	}
}

// shrinkResyncTimings makes the resync delays test-sized. Tests using it must
// not run in parallel: the timings are package-level vars.
func shrinkResyncTimings(t *testing.T) func() {
	t.Helper()
	oldInitial, oldBackoff, oldSettle, oldTicker := resyncInitialDelay, resyncBackoff, resyncSettleDelay, periodicResyncInterval
	resyncInitialDelay = time.Millisecond
	resyncBackoff = []time.Duration{time.Millisecond, time.Millisecond}
	resyncSettleDelay = time.Millisecond
	periodicResyncInterval = 10 * time.Millisecond
	return func() {
		resyncInitialDelay, resyncBackoff, resyncSettleDelay, periodicResyncInterval = oldInitial, oldBackoff, oldSettle, oldTicker
	}
}
