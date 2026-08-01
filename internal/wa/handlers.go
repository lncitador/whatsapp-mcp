package wa

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/lncitador/whatsapp-mcp/internal/config"
	"github.com/lncitador/whatsapp-mcp/internal/store"
	"github.com/lncitador/whatsapp-mcp/internal/stream"
	"github.com/lncitador/whatsapp-mcp/internal/transcriber"
)

func extractTextContent(msg *waProto.Message) string {
	if msg == nil {
		return ""
	}
	if text := msg.GetConversation(); text != "" {
		return text
	}
	if extendedText := msg.GetExtendedTextMessage(); extendedText != nil && extendedText.GetText() != "" {
		return extendedText.GetText()
	}
	// A caption is the only text a media message carries. Ignoring it stored
	// the row with empty content and threw away what the person actually said.
	if caption := msg.GetImageMessage().GetCaption(); caption != "" {
		return caption
	}
	if caption := msg.GetVideoMessage().GetCaption(); caption != "" {
		return caption
	}
	if caption := msg.GetPtvMessage().GetCaption(); caption != "" {
		return caption
	}
	if caption := msg.GetDocumentMessage().GetCaption(); caption != "" {
		return caption
	}
	return ""
}

// synthesizeContent builds a short human-readable stand-in for message types
// that carry no free text of their own. Without it those messages end up with
// both content and mediaType empty and are dropped, which used to happen
// without a single log line (docs/drain-diagnosis.md C4).
func synthesizeContent(msg *waProto.Message) string {
	if msg == nil {
		return ""
	}
	if loc := msg.GetLocationMessage(); loc != nil {
		if place := strings.TrimSpace(strings.TrimSpace(loc.GetName()) + " " + strings.TrimSpace(loc.GetAddress())); place != "" {
			return fmt.Sprintf("[localização: %s]", place)
		}
		return fmt.Sprintf("[localização: %.6f, %.6f]", loc.GetDegreesLatitude(), loc.GetDegreesLongitude())
	}
	if live := msg.GetLiveLocationMessage(); live != nil {
		if caption := live.GetCaption(); caption != "" {
			return fmt.Sprintf("[localização ao vivo: %s]", caption)
		}
		return fmt.Sprintf("[localização ao vivo: %.6f, %.6f]", live.GetDegreesLatitude(), live.GetDegreesLongitude())
	}
	if contact := msg.GetContactMessage(); contact != nil {
		return fmt.Sprintf("[contato: %s]", contact.GetDisplayName())
	}
	if contacts := msg.GetContactsArrayMessage(); contacts != nil {
		return fmt.Sprintf("[contatos (%d): %s]", len(contacts.GetContacts()), contacts.GetDisplayName())
	}
	if poll := msg.GetPollCreationMessage(); poll != nil {
		options := make([]string, 0, len(poll.GetOptions()))
		for _, opt := range poll.GetOptions() {
			options = append(options, opt.GetOptionName())
		}
		if len(options) > 0 {
			return fmt.Sprintf("[enquete: %s | %s]", poll.GetName(), strings.Join(options, " / "))
		}
		return fmt.Sprintf("[enquete: %s]", poll.GetName())
	}
	if reaction := msg.GetReactionMessage(); reaction != nil {
		return fmt.Sprintf("[reação: %s a %s]", reaction.GetText(), reaction.GetKey().GetID())
	}
	if btn := msg.GetButtonsResponseMessage(); btn != nil {
		if text := btn.GetSelectedDisplayText(); text != "" {
			return fmt.Sprintf("[botão: %s]", text)
		}
		return fmt.Sprintf("[botão: %s]", btn.GetSelectedButtonID())
	}
	if reply := msg.GetTemplateButtonReplyMessage(); reply != nil {
		if text := reply.GetSelectedDisplayText(); text != "" {
			return fmt.Sprintf("[botão: %s]", text)
		}
		return fmt.Sprintf("[botão: %s]", reply.GetSelectedID())
	}
	if list := msg.GetListResponseMessage(); list != nil {
		if title := list.GetTitle(); title != "" {
			return fmt.Sprintf("[lista: %s]", title)
		}
		return fmt.Sprintf("[lista: %s]", list.GetSingleSelectReply().GetSelectedRowID())
	}
	if event := msg.GetEventMessage(); event != nil {
		return fmt.Sprintf("[evento: %s]", event.GetName())
	}
	if invite := msg.GetGroupInviteMessage(); invite != nil {
		return fmt.Sprintf("[convite de grupo: %s]", invite.GetGroupName())
	}
	// Sticker and PTV already produce a mediaType below; the label only exists
	// so the stored row reads as something in a message list.
	if sticker := msg.GetStickerMessage(); sticker != nil {
		if sticker.GetIsAnimated() {
			return "[sticker animado]"
		}
		return "[sticker]"
	}
	if msg.GetPtvMessage() != nil {
		return "[vídeo-recado]"
	}
	return ""
}

// storable is everything we persist about one message. Both the live path and
// the history-sync path go through describeMessage, so the two cannot drift.
type storable struct {
	content       string
	mediaType     string
	filename      string
	url           string
	mediaKey      []byte
	fileSHA256    []byte
	fileEncSHA256 []byte
	fileLength    uint64
}

// describeMessage extracts what we store from an already-unwrapped
// waE2E.Message. ok is false when there is genuinely nothing to store; the
// caller must log that, never drop it in silence.
func describeMessage(msg *waProto.Message) (storable, bool) {
	if msg == nil {
		return storable{}, false
	}
	var s storable
	s.content = extractTextContent(msg)
	s.mediaType, s.filename, s.url, s.mediaKey, s.fileSHA256, s.fileEncSHA256, s.fileLength = extractMediaInfo(msg)
	if s.content == "" {
		s.content = synthesizeContent(msg)
	}
	if s.content == "" && s.mediaType == "" {
		return storable{}, false
	}
	return s, true
}

// controlOnlyMessage reports whether msg is a protocol/control payload that by
// design carries nothing user-visible, so not storing it is correct and does
// not deserve a warning (it is still counted, see recordDrop).
func controlOnlyMessage(msg *waProto.Message) bool {
	if msg == nil {
		return false
	}
	switch {
	case msg.GetProtocolMessage() != nil,
		msg.GetSenderKeyDistributionMessage() != nil,
		msg.GetFastRatchetKeySenderKeyDistributionMessage() != nil,
		msg.GetKeepInChatMessage() != nil,
		// The vote in a poll update is encrypted; there is no plaintext to store.
		msg.GetPollUpdateMessage() != nil,
		msg.GetEncReactionMessage() != nil,
		msg.GetEncCommentMessage() != nil,
		msg.GetEncEventResponseMessage() != nil,
		msg.GetPinInChatMessage() != nil,
		msg.GetPlaceholderMessage() != nil,
		msg.GetStickerSyncRmrMessage() != nil,
		msg.GetSecretEncryptedMessage() != nil,
		msg.GetRootSecretDistributeMessage() != nil,
		msg.GetLimitSharingMessage() != nil:
		return true
	}
	return false
}

// Drop accounting. The diagnosis could not tell "the message never arrived"
// from "the message arrived and we threw it away" because a dropped message
// left no trace at all. The counters live in the package rather than on Client
// so both handlers (and later the API) can read them without a new field.
var (
	dropMu     sync.Mutex
	dropCounts = map[string]int{}
)

func recordDrop(reason string) int {
	dropMu.Lock()
	defer dropMu.Unlock()
	dropCounts[reason]++
	return dropCounts[reason]
}

// DroppedMessages returns a snapshot of how many messages were not persisted,
// keyed by reason ("unhandled:live", "control:history", "parse_error:history",
// ...). Exported so the drop rate is observable without parsing logs.
func DroppedMessages() map[string]int {
	dropMu.Lock()
	defer dropMu.Unlock()
	out := make(map[string]int, len(dropCounts))
	for reason, n := range dropCounts {
		out[reason] = n
	}
	return out
}

// populatedMessageFields names the waE2E.Message fields that actually came
// filled. Reflection instead of a hand-written switch: the struct has 100+
// message fields and any list would drift with the next whatsmeow bump.
func populatedMessageFields(msg *waProto.Message) []string {
	if msg == nil {
		return []string{"nil"}
	}
	v := reflect.ValueOf(msg).Elem()
	t := v.Type()
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		// MessageContextInfo rides along with almost everything and says
		// nothing about the message type, so it is noise here.
		if !f.IsExported() || f.Name == "MessageContextInfo" {
			continue
		}
		if v.Field(i).IsZero() {
			continue
		}
		out = append(out, f.Name)
	}
	if len(out) == 0 {
		return []string{"none"}
	}
	return out
}

// logDrop makes every discarded message visible: a warning plus the field
// names that were populated when we do not know how to handle the type, and a
// counted debug line for control payloads that legitimately store nothing.
func (c *Client) logDrop(path, msgID, chatJID string, msg *waProto.Message) {
	if controlOnlyMessage(msg) {
		n := recordDrop("control:" + path)
		c.logger.Debugf("Skipped %s control message %s in %s (fields: %s, control skips: %d)",
			path, msgID, chatJID, strings.Join(populatedMessageFields(msg), ", "), n)
		return
	}
	n := recordDrop("unhandled:" + path)
	c.logger.Warnf("Dropped %s message %s in %s: no storable content, populated waE2E fields: [%s] (unhandled drops on this path: %d)",
		path, msgID, chatJID, strings.Join(populatedMessageFields(msg), ", "), n)
}

func extractMediaInfo(msg *waProto.Message) (mediaType, filename, url string, mediaKey, fileSHA256, fileEncSHA256 []byte, fileLength uint64) {
	if msg == nil {
		return "", "", "", nil, nil, nil, 0
	}
	if img := msg.GetImageMessage(); img != nil {
		sha := hex.EncodeToString(img.GetFileSHA256())
		if len(sha) > 16 {
			sha = sha[:16]
		}
		return "image", "image_" + sha + ".jpg",
			img.GetURL(), img.GetMediaKey(), img.GetFileSHA256(), img.GetFileEncSHA256(), img.GetFileLength()
	}
	if vid := msg.GetVideoMessage(); vid != nil {
		sha := hex.EncodeToString(vid.GetFileSHA256())
		if len(sha) > 16 {
			sha = sha[:16]
		}
		return "video", "video_" + sha + ".mp4",
			vid.GetURL(), vid.GetMediaKey(), vid.GetFileSHA256(), vid.GetFileEncSHA256(), vid.GetFileLength()
	}
	if aud := msg.GetAudioMessage(); aud != nil {
		sha := hex.EncodeToString(aud.GetFileSHA256())
		if len(sha) > 16 {
			sha = sha[:16]
		}
		return "audio", "audio_" + sha + ".ogg",
			aud.GetURL(), aud.GetMediaKey(), aud.GetFileSHA256(), aud.GetFileEncSHA256(), aud.GetFileLength()
	}
	// PtvMessage (the round "video note") is a VideoMessage in disguise, so it
	// keeps mediaType "video" and stays downloadable.
	if ptv := msg.GetPtvMessage(); ptv != nil {
		sha := hex.EncodeToString(ptv.GetFileSHA256())
		if len(sha) > 16 {
			sha = sha[:16]
		}
		return "video", "ptv_" + sha + ".mp4",
			ptv.GetURL(), ptv.GetMediaKey(), ptv.GetFileSHA256(), ptv.GetFileEncSHA256(), ptv.GetFileLength()
	}
	// Stickers get their own mediaType because the extension differs; note that
	// download.go has no case for it yet, so download fails with an explicit
	// error instead of the message being absent from the database entirely.
	if sticker := msg.GetStickerMessage(); sticker != nil {
		sha := hex.EncodeToString(sticker.GetFileSHA256())
		if len(sha) > 16 {
			sha = sha[:16]
		}
		return "sticker", "sticker_" + sha + ".webp",
			sticker.GetURL(), sticker.GetMediaKey(), sticker.GetFileSHA256(), sticker.GetFileEncSHA256(), sticker.GetFileLength()
	}
	if doc := msg.GetDocumentMessage(); doc != nil {
		fn := doc.GetFileName()
		if fn == "" {
			sha := hex.EncodeToString(doc.GetFileSHA256())
			if len(sha) > 16 {
				sha = sha[:16]
			}
			fn = "document_" + sha
		}
		return "document", fn,
			doc.GetURL(), doc.GetMediaKey(), doc.GetFileSHA256(), doc.GetFileEncSHA256(), doc.GetFileLength()
	}
	return "", "", "", nil, nil, nil, 0
}

func (c *Client) handleMessage(msg *events.Message) {
	chatJID := msg.Info.Chat.String()
	sender := msg.Info.Sender.User

	chatJID = c.resolveToPN(chatJID)
	senderJID := c.resolveToPN(msg.Info.Sender.String())
	if i := strings.Index(senderJID, "@"); i >= 0 {
		sender = senderJID[:i]
	}

	s, ok := describeMessage(msg.Message)
	if !ok {
		// Deliberately BEFORE StoreChat: bumping last_message_time for a
		// message we are not storing is what produced 90 chats with zero rows
		// in messages. Such a chat has no resync anchor, so it can never be
		// repaired — the gap perpetuates itself (docs/drain-diagnosis.md C5).
		c.logDrop("live", msg.Info.ID, chatJID, msg.Message)
		return
	}

	// Resolved only for messages we keep: chatName can hit the network for
	// groups, and a dropped message should not pay for that.
	name := c.chatName(msg.Info.Chat, chatJID, nil, sender)

	// The chat row must exist before the message: messages.chat_jid is a
	// foreign key on chats.jid and the database runs with foreign_keys=ON.
	if err := c.st.StoreChat(chatJID, name, msg.Info.Timestamp); err != nil {
		c.logger.Warnf("Failed to store chat: %v", err)
	}

	err := c.st.StoreMessage(store.NewMessage{
		ID:            msg.Info.ID,
		ChatJID:       chatJID,
		Sender:        sender,
		Content:       s.content,
		Timestamp:     msg.Info.Timestamp,
		IsFromMe:      msg.Info.IsFromMe,
		MediaType:     s.mediaType,
		Filename:      s.filename,
		URL:           s.url,
		MediaKey:      s.mediaKey,
		FileSHA256:    s.fileSHA256,
		FileEncSHA256: s.fileEncSHA256,
		FileLength:    s.fileLength,
	})
	if err != nil {
		c.logger.Warnf("Failed to store message: %v", err)
		recordDrop("store_error:live")
	} else {
		stream.PublishMessage(msg.Info.ID, chatJID, name, sender, msg.Info.IsFromMe, msg.Info.Timestamp, s.content, s.mediaType, s.filename)

		timestamp := msg.Info.Timestamp.Format("2006-01-02 15:04:05")
		direction := "←"
		if msg.Info.IsFromMe {
			direction = "→"
		}
		if s.mediaType != "" {
			c.logger.Infof("[%s] %s %s: [%s: %s] %s", timestamp, direction, sender, s.mediaType, s.filename, s.content)
		} else {
			c.logger.Infof("[%s] %s %s: %s", timestamp, direction, sender, s.content)
		}

		if s.mediaType == "audio" || s.mediaType == "video" {
			go c.transcribeMessage(msg.Info.ID, chatJID, s.mediaType)
		}
	}
}

func (c *Client) TranscribeMedia(messageID, chatJID string, forceReprocess bool) (any, error) {
	existing, _ := c.st.GetTranscription(messageID, chatJID)
	if existing != nil && !forceReprocess {
		var mdContent string
		if existing.MarkdownPath != "" {
			data, err := os.ReadFile(existing.MarkdownPath)
			if err == nil {
				mdContent = string(data)
			}
		}
		return map[string]any{
			"success":          true,
			"already_done":     true,
			"text":             existing.Text,
			"markdown_path":    existing.MarkdownPath,
			"markdown_content": mdContent,
			"message_id":       messageID,
			"chat_jid":         chatJID,
		}, nil
	}

	tr, mdContent, err := c.doTranscribe(messageID, chatJID, "", forceReprocess)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"success":          true,
		"text":             tr.Text,
		"markdown_path":    tr.MarkdownPath,
		"markdown_content": mdContent,
		"message_id":       messageID,
		"chat_jid":         chatJID,
	}, nil
}

func (c *Client) transcribeMessage(messageID, chatJID, mediaType string) {
	c.doTranscribe(messageID, chatJID, mediaType, false)
}

type transcriptionOutput struct {
	Text         string
	MarkdownPath string
}

func (c *Client) doTranscribe(messageID, chatJID, mediaType string, force bool) (*transcriptionOutput, string, error) {
	tr := transcriber.New()
	if tr == nil {
		return nil, "", fmt.Errorf("no transcriber available (install whisper-cli or set OPENAI_API_KEY)")
	}

	if !force {
		existing, _ := c.st.GetTranscription(messageID, chatJID)
		if existing != nil {
			return &transcriptionOutput{Text: existing.Text, MarkdownPath: existing.MarkdownPath}, "", nil
		}
	}

	localPath, downloadedType, _, err := c.DownloadMedia(messageID, chatJID)
	if err != nil {
		c.logger.Warnf("Failed to download media for transcription %s: %v", messageID, err)
		return nil, "", fmt.Errorf("failed to download media: %v", err)
	}
	if mediaType == "" {
		mediaType = downloadedType
	}

	result, err := tr.Transcribe(localPath)
	if err != nil {
		c.logger.Warnf("Transcription failed for %s: %v", messageID, err)
		return nil, "", fmt.Errorf("transcription failed: %v", err)
	}

	var framesDir string
	if mediaType == "video" && transcriber.IsVideo(localPath) {
		framesDir = filepath.Join(config.StoreDir(), "transcripts", messageID)
		result.Frames, _ = transcriber.ExtractFrames(localPath, result.Segments, framesDir)
	}

	mdContent, mdPath, _ := transcriber.GenerateMarkdown(result, mediaType, time.Now())

	jsonSegments, _ := json.Marshal(result.Segments)
	if err := c.st.StoreTranscription(store.Transcription{
		MessageID:    messageID,
		ChatJID:      chatJID,
		MediaType:    mediaType,
		Text:         result.Text,
		Segments:     string(jsonSegments),
		FramesDir:    framesDir,
		MarkdownPath: mdPath,
	}); err != nil {
		c.logger.Warnf("Failed to store transcription for %s: %v", messageID, err)
	}

	if result.Text != "" {
		c.logger.Infof("Transcribed message %s: %s", messageID, result.Text[:min(50, len(result.Text))])
	} else {
		c.logger.Infof("Transcribed message %s: (empty)", messageID)
	}

	return &transcriptionOutput{Text: result.Text, MarkdownPath: mdPath}, mdContent, nil
}

func (c *Client) chatName(jid types.JID, chatJID string, conversation any, sender string) string {
	existingName := c.st.ChatName(chatJID)
	if existingName != "" {
		return existingName
	}

	var name string

	if jid.Server == "g.us" {
		if conversation != nil {
			v := reflect.ValueOf(conversation)
			if v.Kind() == reflect.Ptr && !v.IsNil() {
				v = v.Elem()

				if displayNameField := v.FieldByName("DisplayName"); displayNameField.IsValid() && displayNameField.Kind() == reflect.Ptr && !displayNameField.IsNil() {
					dn := displayNameField.Elem().String()
					if dn != "" {
						name = dn
					}
				}

				if name == "" {
					if nameField := v.FieldByName("Name"); nameField.IsValid() && nameField.Kind() == reflect.Ptr && !nameField.IsNil() {
						n := nameField.Elem().String()
						if n != "" {
							name = n
						}
					}
				}
			}
		}

		if name == "" {
			groupInfo, err := c.wm.GetGroupInfo(context.Background(), jid)
			if err == nil && groupInfo.Name != "" {
				name = groupInfo.Name
			} else {
				name = fmt.Sprintf("Group %s", jid.User)
			}
		}
	} else {
		contact, err := c.wm.Store.Contacts.GetContact(context.Background(), jid)
		if err == nil && contact.FullName != "" {
			name = contact.FullName
		} else if sender != "" {
			name = sender
		} else {
			name = jid.User
		}
	}

	return name
}

func (c *Client) handleHistorySync(hs *events.HistorySync) {
	c.logger.Infof("Received history sync event with %d conversations", len(hs.Data.Conversations))

	syncedCount := 0
	for _, conversation := range hs.Data.Conversations {
		if conversation.ID == nil {
			continue
		}

		chatJID := c.resolveToPN(*conversation.ID)

		jid, err := types.ParseJID(chatJID)
		if err != nil {
			c.logger.Warnf("Failed to parse JID %s: %v", chatJID, err)
			continue
		}

		messages := conversation.Messages
		if len(messages) == 0 {
			// Nothing to store. Writing the chat row here would create a chat
			// with zero messages — no resync anchor, invisible forever (C5).
			c.logger.Debugf("History sync: conversation %s carries no messages", chatJID)
			continue
		}

		// last_message_time comes from the newest message the server sent
		// (messages[0]), but the chat row itself is only written once a message
		// of this conversation actually lands — see ensureChat.
		var latestTS time.Time
		if ts := messages[0].GetMessage().GetMessageTimestamp(); ts != 0 {
			latestTS = time.Unix(int64(ts), 0)
		}

		chatStored := false
		resolvedName := ""
		ensureChat := func(fallbackTS time.Time) {
			if chatStored {
				return
			}
			chatStored = true
			// Resolved lazily because chatName can hit the network for groups,
			// and a conversation whose messages are all dropped must not pay
			// for that (nor get a chat row).
			resolvedName = c.chatName(jid, chatJID, conversation, "")
			ts := latestTS
			if ts.IsZero() {
				ts = fallbackTS
			}
			if err := c.st.StoreChat(chatJID, resolvedName, ts); err != nil {
				c.logger.Warnf("Failed to store chat %s: %v", chatJID, err)
			}
		}

		for _, msg := range messages {
			if msg == nil || msg.Message == nil {
				continue
			}

			// ParseWebMessage is what the live path gets for free: it unwraps
			// DeviceSent/Ephemeral/ViewOnce/LottieSticker/DocumentWithCaption/
			// Edited/BotInvoke and resolves the sender the same way. Reading
			// msg.Message.Message directly meant every chat with disappearing
			// messages lost 100% of its synced history (C3).
			evt, err := c.wm.ParseWebMessage(jid, msg.Message)
			if err != nil {
				recordDrop("parse_error:history")
				c.logger.Warnf("History sync: failed to parse message in %s: %v", chatJID, err)
				continue
			}

			if msg.Message.GetMessageTimestamp() == 0 {
				recordDrop("no_timestamp:history")
				c.logger.Warnf("History sync: message %s in %s has no timestamp, skipping", evt.Info.ID, chatJID)
				continue
			}

			s, ok := describeMessage(evt.Message)
			if !ok {
				c.logDrop("history", evt.Info.ID, chatJID, evt.Message)
				continue
			}

			// Same shape as the live path: the bare number, not the full JID.
			sender := evt.Info.Sender.User
			senderJID := c.resolveToPN(evt.Info.Sender.String())
			if i := strings.Index(senderJID, "@"); i > 0 {
				sender = senderJID[:i]
			}

			ensureChat(evt.Info.Timestamp)

			if err := c.st.StoreMessage(store.NewMessage{
				ID:            evt.Info.ID,
				ChatJID:       chatJID,
				Sender:        sender,
				Content:       s.content,
				Timestamp:     evt.Info.Timestamp,
				IsFromMe:      evt.Info.IsFromMe,
				MediaType:     s.mediaType,
				Filename:      s.filename,
				URL:           s.url,
				MediaKey:      s.mediaKey,
				FileSHA256:    s.fileSHA256,
				FileEncSHA256: s.fileEncSHA256,
				FileLength:    s.fileLength,
			}); err != nil {
				recordDrop("store_error:history")
				c.logger.Warnf("Failed to store history message %s in %s: %v", evt.Info.ID, chatJID, err)
				continue
			}

			syncedCount++
			stream.PublishMessage(evt.Info.ID, chatJID, resolvedName, sender, evt.Info.IsFromMe, evt.Info.Timestamp, s.content, s.mediaType, s.filename)

			if s.mediaType != "" {
				c.logger.Infof("Stored message: [%s] %s -> %s: [%s: %s] %s",
					evt.Info.Timestamp.Format("2006-01-02 15:04:05"), sender, chatJID, s.mediaType, s.filename, s.content)
			} else {
				c.logger.Infof("Stored message: [%s] %s -> %s: %s",
					evt.Info.Timestamp.Format("2006-01-02 15:04:05"), sender, chatJID, s.content)
			}
		}
	}

	c.logger.Infof("History sync complete. Stored %d messages.", syncedCount)
}

func (c *Client) resolveToPN(jidStr string) string {
	jid, err := types.ParseJID(jidStr)
	if err != nil {
		return jidStr
	}
	if jid.Server != "lid" {
		return jidStr
	}
	pn, err := c.wm.Store.LIDs.GetPNForLID(context.Background(), jid)
	if err != nil || pn.IsEmpty() {
		return jidStr
	}
	return pn.String()
}
