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

// A drop with no preceding error is indistinguishable in the log from one
// caused by a stream error unless the two are correlated, so Status has to
// carry the last transport failure and the disconnect streak.
func TestConnectionHealthInStatus(t *testing.T) {
	c := &Client{logger: waLog.Noop}
	c.setState(AuthConnected, "", "")
	if s := c.Status(); s.LastStreamError != "" || s.LastStreamErrorAt != nil || s.ConsecutiveDisconnects != 0 {
		t.Fatalf("clean client must report no connection trouble: %+v", s)
	}

	c.noteStreamError(`stream error (code "unknown"): <stream:error><ack class="status" type="media"/></stream:error>`)
	streak, lastErr := c.noteDisconnectedHealth()
	if streak != 1 || !strings.Contains(lastErr, "ack class") {
		t.Fatalf("streak=%d lastErr=%q", streak, lastErr)
	}
	if streak, _ := c.noteDisconnectedHealth(); streak != 2 {
		t.Fatalf("consecutive drops must accumulate, got %d", streak)
	}

	// A reconnect replaces the auth status; the diagnosis must survive it.
	c.setState(AuthConnecting, "", "disconnected, reconnecting")
	s := c.Status()
	if !strings.Contains(s.LastStreamError, "ack class") {
		t.Fatalf("LastStreamError = %q", s.LastStreamError)
	}
	if s.LastStreamErrorAt == nil || s.LastDisconnectAt == nil {
		t.Fatalf("want timestamps, got %+v", s)
	}
	if s.ConsecutiveDisconnects != 2 {
		t.Fatalf("ConsecutiveDisconnects = %d, want 2", s.ConsecutiveDisconnects)
	}

	// Reconnecting clears the streak but keeps the error for post-mortem.
	c.noteConnectedHealth()
	s = c.Status()
	if s.ConsecutiveDisconnects != 0 || s.LastConnectedAt == nil {
		t.Fatalf("after reconnect: %+v", s)
	}
	if s.LastStreamError == "" {
		t.Fatal("the last stream error must outlive the reconnect")
	}
}

func TestAutoResyncDisabledEnv(t *testing.T) {
	for _, v := range []string{"1", "true", "YES", " on "} {
		t.Setenv(disableAutoResyncEnv, v)
		if !autoResyncDisabled() {
			t.Fatalf("%q must disable the automatic backfill", v)
		}
	}
	for _, v := range []string{"", "0", "false", "no"} {
		t.Setenv(disableAutoResyncEnv, v)
		if autoResyncDisabled() {
			t.Fatalf("%q must leave the automatic backfill on", v)
		}
	}
}

// The knob only silences the automatic paths: the manual one behind
// POST /api/resync has to keep working, because it is what an operator uses
// while running the experiment the knob exists for.
func TestAutoResyncDisabledSkipsAutomaticPathsOnly(t *testing.T) {
	restore := shrinkResyncTimings(t)
	defer restore()

	c := &Client{logger: waLog.Noop, stopCh: make(chan struct{}), noAutoResync: true}
	c.autoResync("connected", true)
	if !c.lastResync.IsZero() {
		t.Fatal("a disabled auto backfill must not even claim the debounce")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		c.resyncLoop(context.Background())
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("resyncLoop must return immediately when auto backfill is disabled")
	}

	// Manual path still runs (and fails here only for lack of a session).
	if err := c.RequestHistorySync(10); err == nil {
		t.Fatal("want the manual path to run and report the missing client")
	}
}

// The anchor decides which direction history is requested in. WhatsApp
// answers with the messages BEFORE it, so anchoring on the newest message
// re-requests what we already have — that was the original bug.
func TestBackfillAnchorMessageUsesOldest(t *testing.T) {
	t.Setenv("WHATSAPP_MCP_DIR", t.TempDir())
	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	const jid = "5511999999999@s.whatsapp.net"
	now := time.Now()
	if err := st.StoreChat(jid, "Alice", now); err != nil {
		t.Fatal(err)
	}
	msgs := []store.NewMessage{
		{ID: "OLD", ChatJID: jid, Sender: "5511999999999", Content: "antiga", Timestamp: now.Add(-30 * 24 * time.Hour)},
		{ID: "GAPEDGE", ChatJID: jid, Sender: "5511999999999", Content: "depois do buraco", Timestamp: now.Add(-2 * time.Hour)},
		{ID: "NEW", ChatJID: jid, Sender: "5511999999999", Content: "recente", Timestamp: now},
	}
	for _, m := range msgs {
		if err := st.StoreMessage(m); err != nil {
			t.Fatal(err)
		}
	}

	c := &Client{logger: waLog.Noop, st: st}
	got, err := c.backfillAnchorMessage(jid)
	if err != nil {
		t.Fatal(err)
	}
	// Oldest inside the 48h window: the gap sits right before it.
	if got == nil || got.ID != "GAPEDGE" {
		t.Fatalf("anchor = %+v, want GAPEDGE", got)
	}

	// Silent chat: fall back to the chat's absolute oldest message and walk
	// history backwards instead.
	const quiet = "5511888888888@s.whatsapp.net"
	if err := st.StoreChat(quiet, "Bob", now.Add(-90*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, m := range []store.NewMessage{
		{ID: "Q1", ChatJID: quiet, Sender: "5511888888888", Content: "a", Timestamp: now.Add(-100 * 24 * time.Hour)},
		{ID: "Q2", ChatJID: quiet, Sender: "5511888888888", Content: "b", Timestamp: now.Add(-90 * 24 * time.Hour)},
	} {
		if err := st.StoreMessage(m); err != nil {
			t.Fatal(err)
		}
	}
	got, err = c.backfillAnchorMessage(quiet)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != "Q1" {
		t.Fatalf("anchor = %+v, want Q1", got)
	}

	// A chat with no messages has no anchor at all — the caller counts it as
	// skipped rather than sending a request that can't mean anything.
	const empty = "5511777777777@s.whatsapp.net"
	if err := st.StoreChat(empty, "Carol", now); err != nil {
		t.Fatal(err)
	}
	got, err = c.backfillAnchorMessage(empty)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("want nil anchor for an empty chat, got %+v", got)
	}
	if _, err := historySyncAnchor(empty, got); err == nil {
		t.Fatal("want historySyncAnchor to reject a nil message")
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
