package wa

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/lncitador/whatsapp-mcp/internal/config"
	"github.com/lncitador/whatsapp-mcp/internal/store"
	"github.com/lncitador/whatsapp-mcp/internal/transcriber"
)

func extractTextContent(msg *waProto.Message) string {
	if msg == nil {
		return ""
	}
	if text := msg.GetConversation(); text != "" {
		return text
	} else if extendedText := msg.GetExtendedTextMessage(); extendedText != nil {
		return extendedText.GetText()
	}
	return ""
}

func extractMediaInfo(msg *waProto.Message) (mediaType, filename, url string, mediaKey, fileSHA256, fileEncSHA256 []byte, fileLength uint64) {
	if msg == nil {
		return "", "", "", nil, nil, nil, 0
	}
	if img := msg.GetImageMessage(); img != nil {
		return "image", "image_" + time.Now().Format("20060102_150405") + ".jpg",
			img.GetURL(), img.GetMediaKey(), img.GetFileSHA256(), img.GetFileEncSHA256(), img.GetFileLength()
	}
	if vid := msg.GetVideoMessage(); vid != nil {
		return "video", "video_" + time.Now().Format("20060102_150405") + ".mp4",
			vid.GetURL(), vid.GetMediaKey(), vid.GetFileSHA256(), vid.GetFileEncSHA256(), vid.GetFileLength()
	}
	if aud := msg.GetAudioMessage(); aud != nil {
		return "audio", "audio_" + time.Now().Format("20060102_150405") + ".ogg",
			aud.GetURL(), aud.GetMediaKey(), aud.GetFileSHA256(), aud.GetFileEncSHA256(), aud.GetFileLength()
	}
	if doc := msg.GetDocumentMessage(); doc != nil {
		fn := doc.GetFileName()
		if fn == "" {
			fn = "document_" + time.Now().Format("20060102_150405")
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

	name := c.chatName(msg.Info.Chat, chatJID, nil, sender)

	if err := c.st.StoreChat(chatJID, name, msg.Info.Timestamp); err != nil {
		c.logger.Warnf("Failed to store chat: %v", err)
	}

	content := extractTextContent(msg.Message)
	mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength := extractMediaInfo(msg.Message)

	if content == "" && mediaType == "" {
		return
	}

	err := c.st.StoreMessage(store.NewMessage{
		ID:            msg.Info.ID,
		ChatJID:       chatJID,
		Sender:        sender,
		Content:       content,
		Timestamp:     msg.Info.Timestamp,
		IsFromMe:      msg.Info.IsFromMe,
		MediaType:     mediaType,
		Filename:      filename,
		URL:           url,
		MediaKey:      mediaKey,
		FileSHA256:    fileSHA256,
		FileEncSHA256: fileEncSHA256,
		FileLength:    fileLength,
	})
	if err != nil {
		c.logger.Warnf("Failed to store message: %v", err)
	} else {
		timestamp := msg.Info.Timestamp.Format("2006-01-02 15:04:05")
		direction := "←"
		if msg.Info.IsFromMe {
			direction = "→"
		}
		if mediaType != "" {
			c.logger.Infof("[%s] %s %s: [%s: %s] %s", timestamp, direction, sender, mediaType, filename, content)
		} else if content != "" {
			c.logger.Infof("[%s] %s %s: %s", timestamp, direction, sender, content)
		}

		if mediaType == "audio" || mediaType == "video" {
			go c.transcribeMessage(msg.Info.ID, chatJID, mediaType, url)
		}
	}
}

func (c *Client) transcribeMessage(messageID, chatJID, mediaType, mediaURL string) {
	tr := transcriber.New()
	if tr == nil {
		return
	}

	existing, _ := c.st.GetTranscription(messageID, chatJID)
	if existing != nil {
		return
	}

	tmpFile, err := os.CreateTemp("", "media-*"+filepath.Ext(mediaURL))
	if err != nil {
		c.logger.Warnf("Failed to create temp file for transcription: %v", err)
		return
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	if mediaURL == "" {
		return
	}

	result, err := tr.Transcribe(tmpFile.Name())
	if err != nil {
		c.logger.Warnf("Transcription failed for %s: %v", messageID, err)
		return
	}

	var framesDir string
	if mediaType == "video" && transcriber.IsVideo(tmpFile.Name()) {
		framesDir = filepath.Join(config.StoreDir(), "transcripts", messageID)
		result.Frames, _ = transcriber.ExtractFrames(tmpFile.Name(), result.Segments, framesDir)
	}

	_, mdPath, _ := transcriber.GenerateMarkdown(result, mediaType, time.Now())

	jsonSegments, _ := json.Marshal(result.Segments)
	c.st.StoreTranscription(store.Transcription{
		MessageID:    messageID,
		ChatJID:      chatJID,
		MediaType:    mediaType,
		Text:         result.Text,
		Segments:     string(jsonSegments),
		FramesDir:    framesDir,
		MarkdownPath: mdPath,
	})

	c.logger.Infof("Transcribed message %s: %s", messageID, result.Text[:min(50, len(result.Text))])
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

		chatJID := *conversation.ID
		chatJID = c.resolveToPN(chatJID)

		jid, err := types.ParseJID(chatJID)
		if err != nil {
			c.logger.Warnf("Failed to parse JID %s: %v", chatJID, err)
			continue
		}

		name := c.chatName(jid, chatJID, conversation, "")

		messages := conversation.Messages
		if len(messages) > 0 {
			latestMsg := messages[0]
			if latestMsg == nil || latestMsg.Message == nil {
				continue
			}

			timestamp := time.Time{}
			if ts := latestMsg.Message.GetMessageTimestamp(); ts != 0 {
				timestamp = time.Unix(int64(ts), 0)
			} else {
				continue
			}

			c.st.StoreChat(chatJID, name, timestamp)

			for _, msg := range messages {
				if msg == nil || msg.Message == nil {
					continue
				}

				var content string
				if msg.Message.Message != nil {
					if conv := msg.Message.Message.GetConversation(); conv != "" {
						content = conv
					} else if ext := msg.Message.Message.GetExtendedTextMessage(); ext != nil {
						content = ext.GetText()
					}
				}

				var mediaType, filename, url string
				var mediaKey, fileSHA256, fileEncSHA256 []byte
				var fileLength uint64

				if msg.Message.Message != nil {
					mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength = extractMediaInfo(msg.Message.Message)
				}

				c.logger.Infof("Message content: %v, Media Type: %v", content, mediaType)

				if content == "" && mediaType == "" {
					continue
				}

				var sender string
				isFromMe := false
				if msg.Message.Key != nil {
					if msg.Message.Key.FromMe != nil {
						isFromMe = *msg.Message.Key.FromMe
					}
					if !isFromMe && msg.Message.Key.Participant != nil && *msg.Message.Key.Participant != "" {
						sender = *msg.Message.Key.Participant
					} else if isFromMe {
						sender = c.wm.Store.ID.User
					} else {
						sender = jid.User
					}
				} else {
					sender = jid.User
				}

				msgID := ""
				if msg.Message.Key != nil && msg.Message.Key.ID != nil {
					msgID = *msg.Message.Key.ID
				}

				timestamp := time.Time{}
				if ts := msg.Message.GetMessageTimestamp(); ts != 0 {
					timestamp = time.Unix(int64(ts), 0)
				} else {
					continue
				}

				err = c.st.StoreMessage(store.NewMessage{
					ID:            msgID,
					ChatJID:       chatJID,
					Sender:        sender,
					Content:       content,
					Timestamp:     timestamp,
					IsFromMe:      isFromMe,
					MediaType:     mediaType,
					Filename:      filename,
					URL:           url,
					MediaKey:      mediaKey,
					FileSHA256:    fileSHA256,
					FileEncSHA256: fileEncSHA256,
					FileLength:    fileLength,
				})
				if err != nil {
					c.logger.Warnf("Failed to store history message: %v", err)
				} else {
					syncedCount++
					if mediaType != "" {
						c.logger.Infof("Stored message: [%s] %s -> %s: [%s: %s] %s",
							timestamp.Format("2006-01-02 15:04:05"), sender, chatJID, mediaType, filename, content)
					} else {
						c.logger.Infof("Stored message: [%s] %s -> %s: %s",
							timestamp.Format("2006-01-02 15:04:05"), sender, chatJID, content)
					}
				}
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
