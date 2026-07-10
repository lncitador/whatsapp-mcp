// Package wa owns the whatsmeow session: connection lifecycle, QR-based
// authentication state, and message send/receive.
package wa

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
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
}

type Client struct {
	wm     *whatsmeow.Client
	st     *store.Store
	logger waLog.Logger

	mu     sync.RWMutex
	status Status
}

func (c *Client) setState(s AuthState, qr, msg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status = Status{State: s, QRCode: qr, Message: msg}
}

func (c *Client) Status() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status
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
			go c.requestHistorySyncForRecentChats()
		case *events.Connected:
			c.setState(AuthConnected, "", "")
		case *events.Disconnected:
			c.setState(AuthConnecting, "", "disconnected, reconnecting")
		case *events.LoggedOut:
			c.setState(AuthLoggedOut, "", "device logged out — re-pair via auth_status QR")
		}
	})
	return c, nil
}

func (c *Client) Start(ctx context.Context) error {
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

func (c *Client) Stop() { c.wm.Disconnect() }

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

func (c *Client) requestHistorySyncForRecentChats() {
	chats, err := c.st.ListChats("", 10, 0, false, "")
	if err != nil {
		c.logger.Warnf("Failed to list chats for history sync: %v", err)
		return
	}

	for _, chat := range chats {
		lastMsg, err := c.st.GetLastMessageForChat(chat.JID)
		if err != nil || lastMsg == nil {
			continue
		}

		jid, err := types.ParseJID(chat.JID)
		if err != nil {
			continue
		}

		msgInfo := &types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:     jid,
				IsFromMe: lastMsg.IsFromMe,
			},
			ID:        types.MessageID(lastMsg.ID),
			Timestamp: lastMsg.Timestamp,
		}

		req := c.wm.BuildHistorySyncRequest(msgInfo, 50)
		_, err = c.wm.SendPeerMessage(context.Background(), req)
		if err != nil {
			c.logger.Warnf("Failed to request history sync for %s: %v", chat.JID, err)
		} else {
			c.logger.Infof("Requested on-demand history sync for %s (last msg: %s)", chat.JID, lastMsg.ID)
		}
	}
}
