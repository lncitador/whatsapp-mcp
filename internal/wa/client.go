// Package wa owns the whatsmeow session: connection lifecycle, QR-based
// authentication state, and message send/receive.
package wa

import (
	"context"
	"database/sql"
	"fmt"
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
)

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
	return s
}

func (c *Client) recordResync(r resyncResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastResyncResult = r
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
		wm:     whatsmeow.NewClient(device, logger),
		st:     st,
		logger: logger,
		stopCh: make(chan struct{}),
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
		case *events.LoggedOut:
			c.setState(AuthLoggedOut, "", "device logged out — re-pair via auth_status QR")
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

// autoResync pulls recent history after a (re)connect, debounced. It waits a
// few seconds first to let normal offline delivery try, then fills any gap;
// if the link dropped again while waiting it retries with backoff rather than
// giving up silently, because "we reconnected and immediately fell over" is
// precisely the case where messages go missing.
func (c *Client) autoResync(reason string, force bool) {
	defer c.recoverPanic("auto resync (" + reason + ")")
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
			c.logger.Warnf("Auto history resync (%s) gave up: still disconnected after %d retries", reason, attempt)
			return
		}
		delay = resyncBackoff[attempt]
		c.logger.Infof("Auto history resync (%s): not connected, retrying in %s", reason, delay)
	}
	c.logger.Infof("Auto history resync (%s)", reason)
	if _, err := c.requestHistorySync(reason, resyncRecentChats); err != nil {
		c.logger.Warnf("Auto history resync failed: %v", err)
	}
}

// RequestHistorySync asks WhatsApp to redeliver recent history, anchored on
// each chat's last known message. Normally triggered automatically on
// OfflineSyncCompleted, but that event doesn't always fire on a flaky
// connection (repeated stream drops prevent the offline-sync backlog from
// ever fully draining) — exposed here so it can also be triggered on demand
// via POST /api/resync.
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
		c.logger.Warnf("Resync %s: cannot count messages before sync: %v", reason, countErr)
	}

	skipped := 0
	for _, chat := range chats {
		lastMsg, err := c.st.GetLastMessageForChat(chat.JID)
		if err != nil {
			c.logger.Warnf("Resync %s: last message lookup failed for %s: %v", reason, chat.JID, err)
			skipped++
			continue
		}
		anchor, err := historySyncAnchor(chat.JID, lastMsg)
		if err != nil {
			// Not fatal for the pass: a chat with no usable anchor simply
			// can't be asked about, the others still can.
			c.logger.Debugf("Resync %s: skipping %s: %v", reason, chat.JID, err)
			skipped++
			continue
		}
		req := c.wm.BuildHistorySyncRequest(anchor, historySyncCount)
		if req == nil {
			// Documented as always non-nil, but a nil here would panic deep
			// inside SendPeerMessage and take the daemon with it.
			c.logger.Warnf("Resync %s: nil history sync request built for %s", reason, chat.JID)
			skipped++
			continue
		}
		if _, err := c.wm.SendPeerMessage(context.Background(), req); err != nil {
			c.logger.Warnf("Failed to request history sync for %s: %v", chat.JID, err)
			skipped++
			continue
		}
		sent++
		c.logger.Infof("Requested history sync for %s (anchor: %s)", chat.JID, anchor.ID)
	}

	c.logger.Infof("Resync %s: requested history for %d chats (%d skipped of %d selected)",
		reason, sent, skipped, len(chats))
	if sent > 0 && countErr == nil {
		go c.reportResync(reason, sent, before)
	}
	return sent, nil
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
		c.logger.Warnf("Resync %s: cannot count messages after sync: %v", reason, err)
		return
	}
	newMessages := int(after - before)
	if newMessages < 0 {
		newMessages = 0
	}
	c.logger.Infof("Resync %s: %d new messages in %d chats", reason, newMessages, chats)
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
// an on-demand history request on. BuildHistorySyncRequest dereferences the
// info and reads Chat, ID, IsFromMe and Timestamp without a single nil or
// zero check, so validate here: a nil info panics, and a zero timestamp or
// empty ID produces a request WhatsApp silently answers nothing for.
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
