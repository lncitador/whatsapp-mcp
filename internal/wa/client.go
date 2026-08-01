// Package wa owns the whatsmeow session: connection lifecycle, QR-based
// authentication state, and message send/receive.
package wa

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	_ "modernc.org/sqlite"

	"github.com/lncitador/whatsapp-mcp/internal/config"
	"github.com/lncitador/whatsapp-mcp/internal/store"
)

type AuthState string

const (
	AuthConnected  AuthState = "connected"
	AuthWaitingQR  AuthState = "waiting_qr"
	AuthLoggedOut  AuthState = "logged_out"
	AuthConnecting AuthState = "connecting"
)

type Status struct {
	State   AuthState `json:"state"`
	QRCode  string    `json:"qr_code,omitempty"`
	Message string    `json:"message,omitempty"`

	// Last history-resync outcome, so an operator (or an agent) can tell
	// whether the offline backlog is actually draining without reading logs.
	LastResyncAt          *time.Time `json:"last_resync_at,omitempty"`
	LastResyncReason      string     `json:"last_resync_reason,omitempty"`
	LastResyncChats       int        `json:"last_resync_chats,omitempty"`
	LastResyncNewMessages int        `json:"last_resync_new_messages,omitempty"`

	// Connection health. On the reference production log the socket dropped
	// every ~50 minutes with a <stream:error> node and none of it was visible
	// outside whatsmeow's own logging: 22 reconnects in 23.5h, zero offline
	// syncs completed, nothing in the status. LastStreamError holds the last
	// transport-level failure whatsmeow reported (stream error, keepalive
	// timeout, connect failure, ban) so a flaky link is diagnosable from
	// /api/status instead of by grepping the daemon log.
	LastStreamError        string     `json:"last_stream_error,omitempty"`
	LastStreamErrorAt      *time.Time `json:"last_stream_error_at,omitempty"`
	ConsecutiveDisconnects int        `json:"consecutive_disconnects,omitempty"`
	LastDisconnectAt       *time.Time `json:"last_disconnect_at,omitempty"`
	LastConnectedAt        *time.Time `json:"last_connected_at,omitempty"`
}

// connHealth is the transport-level history behind Status. It lives outside
// Status because setState replaces the whole struct on every state change.
type connHealth struct {
	lastStreamError        string
	lastStreamErrorAt      time.Time
	consecutiveDisconnects int
	lastDisconnectAt       time.Time
	lastConnectedAt        time.Time
}

// resyncResult is the measured outcome of one history resync.
type resyncResult struct {
	at          time.Time
	reason      string
	chats       int
	newMessages int
}

type Client struct {
	wm     *whatsmeow.Client
	st     *store.Store
	logger waLog.Logger

	mu               sync.RWMutex
	status           Status
	lastResyncResult resyncResult
	health           connHealth

	// noAutoResync mirrors WHATSAPP_MCP_DISABLE_AUTO_RESYNC: event- and
	// timer-driven backfills are off, manual ones still work.
	noAutoResync bool

	resyncMu   sync.Mutex
	lastResync time.Time

	// disconnectedAt is when the current outage started (zero while online),
	// used to force a resync after a long one — see autoResync.
	discMu         sync.Mutex
	disconnectedAt time.Time

	stopOnce sync.Once
	stopCh   chan struct{}
}

// Resync timings live in vars rather than consts so tests can shrink them.
var (
	// autoResyncDebounce is the minimum gap between automatic history resyncs.
	// On a flaky link the daemon reconnects repeatedly (keepalive timeouts,
	// stream drops); without a debounce every reconnect would fire a resync.
	autoResyncDebounce = 60 * time.Second

	// longDisconnectThreshold is how long an outage has to last before the
	// debounce is overridden: after ten minutes offline there is a real
	// backlog waiting, and skipping the resync means those messages stay
	// missing until the next event happens to get through.
	longDisconnectThreshold = 10 * time.Minute

	// periodicResyncInterval is the safety-net sweep. Both Connected and
	// OfflineSyncCompleted can be missed on a bad link, so the drain must not
	// depend on events alone.
	periodicResyncInterval = 15 * time.Minute

	// resyncInitialDelay gives WhatsApp's own offline delivery a head start
	// before we ask for history explicitly.
	resyncInitialDelay = 5 * time.Second

	// resyncBackoff is used when we wake up already disconnected again:
	// reschedule instead of silently dropping the resync on the floor.
	resyncBackoff = []time.Duration{15 * time.Second, 45 * time.Second, 2 * time.Minute}

	// resyncSettleDelay is how long requested history gets to arrive before
	// we measure how many messages it added — it comes back asynchronously as
	// events.HistorySync, not in the send response.
	resyncSettleDelay = 30 * time.Second

	// resyncActiveWindow: every chat active within this window is resynced,
	// regardless of its rank. See store.ListChatsForResync.
	resyncActiveWindow = 48 * time.Hour
)

const (
	// resyncRecentChats is the top-N slice of the chat list an automatic
	// resync covers, on top of everything inside resyncActiveWindow.
	resyncRecentChats = 25
	// resyncMaxChats caps the union: each chat costs one peer message.
	resyncMaxChats = 60
	// historySyncCount is the number of messages requested per chat; 50 is
	// whatsmeow's recommended batch size.
	historySyncCount = 50

	// disableAutoResyncEnv switches off the event- and timer-driven history
	// backfill only; POST /api/resync keeps working. It exists to test the
	// standing suspicion that the burst of peer messages we send seconds
	// after authenticating is what stops the server from delivering the
	// offline queue (zero "offline sync completed" in 22 reconnects, while
	// the pending count grew from 76 to 374). Running a session with this set
	// is the cheapest way to confirm or kill that hypothesis.
	disableAutoResyncEnv = "WHATSAPP_MCP_DISABLE_AUTO_RESYNC"
)

// autoResyncDisabled reads the knob above. Read once at New so the daemon's
// behaviour can't change under it mid-session.
func autoResyncDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(disableAutoResyncEnv))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func (c *Client) setState(s AuthState, qr, msg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status = Status{State: s, QRCode: qr, Message: msg}
}

func (c *Client) Status() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s := c.status
	// Resync info is kept outside c.status because setState replaces the
	// whole struct on every connection change and would otherwise wipe it.
	if r := c.lastResyncResult; !r.at.IsZero() {
		at := r.at
		s.LastResyncAt = &at
		s.LastResyncReason = r.reason
		s.LastResyncChats = r.chats
		s.LastResyncNewMessages = r.newMessages
	}
	h := c.health
	s.ConsecutiveDisconnects = h.consecutiveDisconnects
	if h.lastStreamError != "" {
		s.LastStreamError = h.lastStreamError
		at := h.lastStreamErrorAt
		s.LastStreamErrorAt = &at
	}
	if !h.lastDisconnectAt.IsZero() {
		at := h.lastDisconnectAt
		s.LastDisconnectAt = &at
	}
	if !h.lastConnectedAt.IsZero() {
		at := h.lastConnectedAt
		s.LastConnectedAt = &at
	}
	return s
}

func (c *Client) recordResync(r resyncResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastResyncResult = r
}

// noteStreamError records the last transport-level failure. Every one of
// these is a plausible reason for the offline queue never being delivered, so
// they must outlive the event that carried them.
func (c *Client) noteStreamError(desc string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.health.lastStreamError = desc
	c.health.lastStreamErrorAt = time.Now()
}

// noteConnectedHealth resets the disconnect streak: the link came back.
func (c *Client) noteConnectedHealth() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.health.consecutiveDisconnects = 0
	c.health.lastConnectedAt = time.Now()
}

// noteDisconnectedHealth counts the drop and returns the streak length plus
// the last error reported before it, which is the closest thing to a reason:
// events.Disconnected itself carries no payload at all.
func (c *Client) noteDisconnectedHealth() (streak int, lastError string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.health.consecutiveDisconnects++
	c.health.lastDisconnectAt = time.Now()
	return c.health.consecutiveDisconnects, c.health.lastStreamError
}

func New(st *store.Store) (*Client, error) {
	logger := waLog.Stdout("wa", "INFO", false)
	dbLog := waLog.Stdout("db", "WARN", false)
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
		filepath.Join(config.StoreDir(), "whatsapp.db"))
	container, err := sqlstore.New(context.Background(), "sqlite", dsn, dbLog)
	if err != nil {
		return nil, fmt.Errorf("open whatsapp.db: %w", err)
	}
	device, err := container.GetFirstDevice(context.Background())
	if err != nil {
		if err == sql.ErrNoRows {
			device = container.NewDevice()
		} else {
			return nil, fmt.Errorf("get device: %w", err)
		}
	}
	c := &Client{
		wm:           whatsmeow.NewClient(device, logger),
		st:           st,
		logger:       logger,
		stopCh:       make(chan struct{}),
		noAutoResync: autoResyncDisabled(),
	}
	if c.noAutoResync {
		logger.Infof("%s is set: automatic history backfill disabled (POST /api/resync still works)", disableAutoResyncEnv)
	}
	c.setState(AuthConnecting, "", "starting")
	c.wm.AddEventHandler(func(evt any) {
		switch v := evt.(type) {
		case *events.Message:
			c.handleMessage(v)
		case *events.HistorySync:
			c.handleHistorySync(v)
		case *events.OfflineSyncPreview:
			c.logger.Infof("Offline sync preview: %d total (%d messages, %d notifications, %d receipts)",
				v.Total, v.Messages, v.Notifications, v.Receipts)
		case *events.OfflineSyncCompleted:
			c.logger.Infof("Offline sync completed: %d events processed", v.Count)
			go c.autoResync("offline sync completed", false)
		case *events.Connected:
			c.setState(AuthConnected, "", "")
			c.noteConnectedHealth()
			// OfflineSyncCompleted often never fires on a flaky link, so the
			// offline backlog never drains and afternoon messages go missing
			// until a manual daemon restart. Resync on every (re)connect too,
			// debounced, so recovered messages are pulled once the net returns.
			// A long outage overrides the debounce: the longer we were away,
			// the more certain it is that something is waiting for us.
			down := c.takeDowntime()
			reason, force := "connected", down >= longDisconnectThreshold
			if force {
				reason = fmt.Sprintf("connected after %s offline", down.Round(time.Second))
			}
			go c.autoResync(reason, force)
		case *events.Disconnected:
			c.setState(AuthConnecting, "", "disconnected, reconnecting")
			c.noteDisconnected()
			// events.Disconnected is an empty struct: the reason, when there
			// is one, arrived moments earlier as StreamError/KeepAliveTimeout/
			// ConnectFailure. Pair them here so one log line explains the drop.
			streak, lastErr := c.noteDisconnectedHealth()
			if lastErr == "" {
				lastErr = "none reported"
			}
			c.logger.Warnf("Disconnected from WhatsApp (consecutive: %d, last reported cause: %s)", streak, lastErr)
		case *events.LoggedOut:
			c.setState(AuthLoggedOut, "", "device logged out — re-pair via auth_status QR")
		case *events.StreamError:
			// The reference outage was `<stream:error><ack class="status"
			// type="media"/></stream:error>` 146 times in one day; the raw
			// node is the only thing that identifies which one it is.
			desc := fmt.Sprintf("stream error (code %q)", v.Code)
			if v.Raw != nil {
				desc = fmt.Sprintf("stream error (code %q): %s", v.Code, v.Raw.String())
			}
			c.logger.Errorf("%s", desc)
			c.noteStreamError(desc)
		case *events.KeepAliveTimeout:
			desc := fmt.Sprintf("keepalive timeout (%d consecutive, last success %s)",
				v.ErrorCount, v.LastSuccess.Format(time.RFC3339))
			c.logger.Warnf("%s", desc)
			c.noteStreamError(desc)
		case *events.KeepAliveRestored:
			c.logger.Infof("Keepalive restored")
		case *events.ConnectFailure:
			desc := fmt.Sprintf("connect failure: %s (%s)", v.Reason, v.Message)
			c.logger.Errorf("%s", desc)
			c.noteStreamError(desc)
			c.setState(AuthConnecting, "", "connect failure: "+v.Reason.String())
		// The three below implement events.PermanentDisconnect: whatsmeow does
		// not reconnect on its own after them, so the daemon sits there
		// looking merely "disconnected" while nothing will ever arrive again.
		// Say so in the status message — the state enum has no word for it.
		case *events.TemporaryBan:
			desc := v.String()
			c.logger.Errorf("%s", desc)
			c.noteStreamError(desc)
			c.setState(AuthConnecting, "", desc+" (no automatic reconnect)")
		case *events.ClientOutdated:
			desc := "client outdated — WhatsApp rejected the connection, the whatsmeow dependency needs updating"
			c.logger.Errorf("%s", desc)
			c.noteStreamError(desc)
			c.setState(AuthConnecting, "", desc+" (no automatic reconnect)")
		case *events.StreamReplaced:
			// Two daemons on the same session: the socket dies for good and
			// nothing arrives again, which looks exactly like the drain bug.
			desc := "stream replaced — another client connected with this session"
			c.logger.Errorf("%s", desc)
			c.noteStreamError(desc)
			c.setState(AuthConnecting, "", desc+" (no automatic reconnect, restart the daemon)")
		}
	})
	return c, nil
}

func (c *Client) Start(ctx context.Context) error {
	go c.resyncLoop(ctx)
	if c.wm.Store.ID == nil {
		qrChan, err := c.wm.GetQRChannel(ctx)
		if err != nil {
			return fmt.Errorf("qr channel: %w", err)
		}
		if err := c.wm.Connect(); err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		go func() {
			for evt := range qrChan {
				switch evt.Event {
				case "code":
					c.setState(AuthWaitingQR, evt.Code, "scan the QR code with WhatsApp")
				case "success":
					c.setState(AuthConnected, "", "")
					return
				case "timeout":
					c.setState(AuthLoggedOut, "", "QR timed out — restart daemon to get a new code")
					return
				}
			}
		}()
		return nil
	}
	if err := c.wm.Connect(); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	return nil
}

// Stop disconnects and releases the background resync goroutines, which may
// be parked in a retry/settle sleep of several minutes.
func (c *Client) Stop() {
	c.stopOnce.Do(func() {
		if c.stopCh != nil {
			close(c.stopCh)
		}
	})
	if c.wm != nil {
		c.wm.Disconnect()
	}
}

func (c *Client) CreateGroup(name string, participants []string, isCommunity bool, communityParentJID string) (string, error) {
	if !c.wm.IsConnected() {
		return "", fmt.Errorf("not connected to WhatsApp")
	}

	if len(name) > 25 {
		return "", fmt.Errorf("group name too long (max 25 chars, got %d)", len(name))
	}

	if len(participants) == 0 {
		return "", fmt.Errorf("at least one participant is required")
	}

	var participantJIDs []types.JID
	for _, p := range participants {
		if strings.Contains(p, "@") {
			jid, err := types.ParseJID(p)
			if err != nil {
				return "", fmt.Errorf("invalid participant JID %q: %w", p, err)
			}
			participantJIDs = append(participantJIDs, jid)
		} else {
			participantJIDs = append(participantJIDs, types.JID{
				User:   p,
				Server: "s.whatsapp.net",
			})
		}
	}

	req := whatsmeow.ReqCreateGroup{
		Name:         name,
		Participants: participantJIDs,
	}

	if isCommunity {
		req.IsParent = true
	}

	if communityParentJID != "" {
		parentJID, err := types.ParseJID(communityParentJID)
		if err != nil {
			return "", fmt.Errorf("invalid community parent JID: %w", err)
		}
		req.LinkedParentJID = parentJID
	}

	groupInfo, err := c.wm.CreateGroup(context.Background(), req)
	if err != nil {
		return "", fmt.Errorf("create group: %w", err)
	}

	groupJID := groupInfo.JID.String()
	if err := c.st.StoreChat(groupJID, name, time.Now()); err != nil {
		c.logger.Warnf("Failed to store new group chat: %v", err)
	}

	return groupJID, nil
}

func (c *Client) LeaveGroup(jid string) error {
	if !c.wm.IsConnected() {
		return fmt.Errorf("not connected to WhatsApp")
	}

	groupJID, err := types.ParseJID(jid)
	if err != nil {
		return fmt.Errorf("invalid group JID: %w", err)
	}

	if groupJID.Server != "g.us" {
		return fmt.Errorf("not a group JID (must end with @g.us)")
	}

	return c.wm.LeaveGroup(context.Background(), groupJID)
}

// noteDisconnected marks the start of an outage. Only the first Disconnected
// of a streak counts: whatsmeow emits one per failed reconnect attempt, and
// we want the total time offline, not the time since the last retry.
func (c *Client) noteDisconnected() {
	c.discMu.Lock()
	defer c.discMu.Unlock()
	if c.disconnectedAt.IsZero() {
		c.disconnectedAt = time.Now()
	}
}

// takeDowntime returns how long the just-ended outage lasted and clears it.
func (c *Client) takeDowntime() time.Duration {
	c.discMu.Lock()
	defer c.discMu.Unlock()
	if c.disconnectedAt.IsZero() {
		return 0
	}
	d := time.Since(c.disconnectedAt)
	c.disconnectedAt = time.Time{}
	return d
}

// recoverPanic keeps a panic inside a background goroutine from taking the
// whole daemon down with it. The resync paths run in `go` calls, and an
// unrecovered panic there kills the process — and the WhatsApp session with
// it — which is exactly the manual-restart loop this code exists to avoid.
func (c *Client) recoverPanic(what string) {
	if r := recover(); r != nil {
		if c.logger != nil {
			c.logger.Errorf("Recovered panic in %s: %v\n%s", what, r, debug.Stack())
		}
	}
}

// wait sleeps for d, returning false if the client was stopped meanwhile so
// callers bail out instead of holding shutdown up for minutes.
func (c *Client) wait(d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-c.stopCh:
		return false
	case <-t.C:
		return true
	}
}

func (c *Client) isConnected() bool { return c.wm != nil && c.wm.IsConnected() }

// claimResync enforces the debounce so a reconnect storm doesn't spam
// WhatsApp with sync requests. force skips it: after a long outage there is a
// real backlog and it is worth asking again even if a resync just ran.
func (c *Client) claimResync(force bool) bool {
	c.resyncMu.Lock()
	defer c.resyncMu.Unlock()
	if !force && !c.lastResync.IsZero() && time.Since(c.lastResync) < autoResyncDebounce {
		return false
	}
	c.lastResync = time.Now()
	return true
}

// resyncLoop is the safety net: a resync driven only by events stops
// happening the moment an event is missed, and both Connected and
// OfflineSyncCompleted go missing on a bad link. Sweep on a timer too.
func (c *Client) resyncLoop(ctx context.Context) {
	defer c.recoverPanic("periodic resync loop")
	if c.noAutoResync {
		return
	}
	t := time.NewTicker(periodicResyncInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stopCh:
			return
		case <-t.C:
			if c.isConnected() {
				c.autoResync("periodic", false)
			}
		}
	}
}

// autoResync backfills history after a (re)connect, debounced. It waits a few
// seconds first to let normal offline delivery try, then asks for the history
// preceding what we already have; if the link dropped again while waiting it
// retries with backoff rather than giving up silently, because "we reconnected
// and immediately fell over" is precisely the case where messages go missing.
//
// This is a backfill, not a drain: see RequestHistorySync for why the two are
// not the same thing.
func (c *Client) autoResync(reason string, force bool) {
	defer c.recoverPanic("auto resync (" + reason + ")")
	if c.noAutoResync {
		return
	}
	if !c.claimResync(force) {
		return
	}
	delay := resyncInitialDelay
	for attempt := 0; ; attempt++ {
		if !c.wait(delay) {
			return // daemon shutting down
		}
		if c.isConnected() {
			break
		}
		if attempt >= len(resyncBackoff) {
			c.logger.Warnf("Auto history backfill (%s) gave up: still disconnected after %d retries", reason, attempt)
			return
		}
		delay = resyncBackoff[attempt]
		c.logger.Infof("Auto history backfill (%s): not connected, retrying in %s", reason, delay)
	}
	c.logger.Infof("Auto history backfill (%s)", reason)
	if _, err := c.requestHistorySync(reason, resyncRecentChats); err != nil {
		c.logger.Warnf("Auto history backfill failed: %v", err)
	}
}

// RequestHistorySync backfills history BACKWARDS: for each selected chat it
// asks WhatsApp for the messages immediately BEFORE the oldest message we
// have in that part of the timeline.
//
// This direction is not a choice. whatsmeow's BuildHistorySyncRequest fills
// OldestMsgID/OldestMsgFromMe/OldestMsgTimestampMS and is documented as
// returning "count messages immediately before the given message"
// (whatsmeow/send.go:558-582). An earlier version of this code anchored on
// each chat's NEWEST message, which asked the server for 50 messages we
// already had — 337 "Stored 0 messages" responses in one production day.
//
// The honest consequence: this is a backfill mechanism, NOT a way to recover
// messages that arrived while the daemon was offline. Those come through the
// offline queue (events.Message on reconnect) or the server's own RECENT
// history sync. POST /api/resync therefore fills gaps *behind* the timeline;
// it will not conjure up this afternoon's missing messages.
//
// limit sizes the "most recently active" slice of the selection; chats active
// in the last 48h are always included on top of it (see
// store.ListChatsForResync), so the number of chats actually synced can be
// larger than limit.
func (c *Client) RequestHistorySync(limit int) error {
	_, err := c.requestHistorySync("manual", limit)
	return err
}

// requestHistorySync does the work and reports how many chats it asked for.
// Every step is nil-guarded: this runs from goroutines, and whatsmeow
// dereferences the anchor it is given without checking anything.
func (c *Client) requestHistorySync(reason string, limit int) (sent int, err error) {
	defer func() {
		if r := recover(); r != nil {
			c.recoverPanicValue("history resync ("+reason+")", r)
			err = fmt.Errorf("panic during history resync: %v", r)
		}
	}()

	if c == nil || c.wm == nil || c.st == nil {
		return 0, fmt.Errorf("history resync: client not initialised")
	}
	if !c.wm.IsConnected() {
		return 0, fmt.Errorf("not connected to WhatsApp")
	}
	// SendPeerMessage addresses our own JID, so without a paired device every
	// send below can only fail with ErrNotLoggedIn — say so once instead.
	if c.wm.Store == nil || c.wm.Store.GetJID().IsEmpty() {
		return 0, fmt.Errorf("not logged in to WhatsApp")
	}
	if limit <= 0 {
		limit = resyncRecentChats
	}
	maxChats := resyncMaxChats
	if limit > maxChats {
		maxChats = limit
	}

	chats, err := c.st.ListChatsForResync(limit, time.Now().Add(-resyncActiveWindow), maxChats)
	if err != nil {
		return 0, fmt.Errorf("list chats for history sync: %w", err)
	}

	// Counted before the requests go out; the matching count happens after the
	// history had time to land (see reportResync).
	before, countErr := c.st.CountMessages()
	if countErr != nil {
		c.logger.Warnf("Backfill %s: cannot count messages before sync: %v", reason, countErr)
	}

	skipped, noAnchor := 0, 0
	var noAnchorSample []string
	for _, chat := range chats {
		oldest, err := c.backfillAnchorMessage(chat.JID)
		if err != nil {
			c.logger.Warnf("Backfill %s: anchor lookup failed for %s: %v", reason, chat.JID, err)
			skipped++
			continue
		}
		anchor, err := historySyncAnchor(chat.JID, oldest)
		if err != nil {
			// Not fatal for the pass: a chat with no usable anchor simply
			// can't be asked about, the others still can. Counted rather than
			// logged per chat — this used to be a Debugf, and the daemon logs
			// at INFO, so in production the reason was never visible at all
			// even though it hit 90 of 215 chats.
			skipped++
			noAnchor++
			if len(noAnchorSample) < 5 {
				noAnchorSample = append(noAnchorSample, fmt.Sprintf("%s (%v)", chat.JID, err))
			}
			continue
		}
		req := c.wm.BuildHistorySyncRequest(anchor, historySyncCount)
		if req == nil {
			// Documented as always non-nil, but a nil here would panic deep
			// inside SendPeerMessage and take the daemon with it.
			c.logger.Warnf("Backfill %s: nil history sync request built for %s", reason, chat.JID)
			skipped++
			continue
		}
		if _, err := c.wm.SendPeerMessage(context.Background(), req); err != nil {
			c.logger.Warnf("Failed to request history backfill for %s: %v", chat.JID, err)
			skipped++
			continue
		}
		sent++
		c.logger.Infof("Requested history backfill for %s: %d messages before %s (%s)",
			chat.JID, historySyncCount, anchor.ID, anchor.Timestamp.Format(time.RFC3339))
	}

	c.logger.Infof("Backfill %s: requested history for %d chats (%d skipped of %d selected)",
		reason, sent, skipped, len(chats))
	if noAnchor > 0 {
		c.logger.Infof("Backfill %s: %d chats skipped for lack of an anchor (no stored message to ask before): %s",
			reason, noAnchor, strings.Join(noAnchorSample, "; "))
	}
	if sent > 0 && countErr == nil {
		go c.reportResync(reason, sent, before)
	}
	return sent, nil
}

// backfillAnchorMessage picks the message the history request anchors on.
// The server returns what came immediately BEFORE it, so the anchor has to be
// the oldest message of the stretch we want filled — never the newest.
//
// Preference order:
//  1. the oldest message inside the recent window. A gap opened by an outage
//     yesterday sits just before whatever did make it in since; anchoring
//     there asks for exactly that gap.
//  2. otherwise the oldest message in the chat, which walks the chat's
//     history further back one batch per pass.
func (c *Client) backfillAnchorMessage(chatJID string) (*store.Message, error) {
	m, err := c.st.GetOldestMessageSince(chatJID, time.Now().Add(-resyncActiveWindow))
	if err != nil {
		return nil, fmt.Errorf("oldest recent message for %s: %w", chatJID, err)
	}
	if m != nil {
		return m, nil
	}
	m, err = c.st.GetOldestMessageForChat(chatJID)
	if err != nil {
		return nil, fmt.Errorf("oldest message for %s: %w", chatJID, err)
	}
	return m, nil
}

// reportResync measures the drain. Requested history comes back
// asynchronously as events.HistorySync some seconds later, so counting right
// after the send would always report zero; wait for it to land, then diff the
// message count. The delta is the only hard evidence the drain worked (it is
// approximate: live messages arriving in the same window count too).
func (c *Client) reportResync(reason string, chats int, before int64) {
	defer c.recoverPanic("resync report (" + reason + ")")
	if !c.wait(resyncSettleDelay) {
		return // daemon shutting down
	}
	after, err := c.st.CountMessages()
	if err != nil {
		c.logger.Warnf("Backfill %s: cannot count messages after sync: %v", reason, err)
		return
	}
	newMessages := int(after - before)
	if newMessages < 0 {
		newMessages = 0
	}
	c.logger.Infof("Backfill %s: %d new messages in %d chats", reason, newMessages, chats)
	c.recordResync(resyncResult{at: time.Now(), reason: reason, chats: chats, newMessages: newMessages})
}

// recoverPanicValue logs an already-recovered panic; recoverPanic is the
// deferred form used where the value doesn't need to become an error.
func (c *Client) recoverPanicValue(what string, r any) {
	if c != nil && c.logger != nil {
		c.logger.Errorf("Recovered panic in %s: %v\n%s", what, r, debug.Stack())
	}
}

// historySyncAnchor builds the "oldest known message" that whatsmeow anchors
// an on-demand history request on — the protobuf fields are literally
// OldestMsgID/OldestMsgTimestampMS, and the server answers with the messages
// before it. Callers must pass the oldest message of the range they want
// filled (see backfillAnchorMessage), not the newest.
//
// BuildHistorySyncRequest dereferences the info and reads Chat, ID, IsFromMe
// and Timestamp without a single nil or zero check, so validate here: a nil
// info panics, and a zero timestamp or empty ID produces a request WhatsApp
// silently answers nothing for.
func historySyncAnchor(chatJID string, last *store.Message) (*types.MessageInfo, error) {
	if last == nil {
		return nil, fmt.Errorf("chat has no stored message to anchor on")
	}
	if last.ID == "" {
		return nil, fmt.Errorf("stored message has no ID")
	}
	if last.Timestamp.IsZero() {
		return nil, fmt.Errorf("stored message %s has no timestamp", last.ID)
	}
	jid, err := types.ParseJID(chatJID)
	if err != nil {
		return nil, fmt.Errorf("invalid chat JID %q: %w", chatJID, err)
	}
	// ParseJID accepts a bare string as a server-only JID ("not a jid" parses
	// fine), which would address the history request at nothing. A real chat
	// JID always has both halves.
	if jid.User == "" || jid.Server == "" {
		return nil, fmt.Errorf("incomplete chat JID %q", chatJID)
	}
	return &types.MessageInfo{
		MessageSource: types.MessageSource{
			Chat:     jid,
			IsFromMe: last.IsFromMe,
		},
		ID:        types.MessageID(last.ID),
		Timestamp: last.Timestamp,
	}, nil
}
