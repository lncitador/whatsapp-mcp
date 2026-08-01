package wa

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"github.com/lncitador/whatsapp-mcp/internal/audio"
	"github.com/lncitador/whatsapp-mcp/internal/store"
	"github.com/lncitador/whatsapp-mcp/internal/stream"
)

func (c *Client) SendMessage(recipient, message, mediaPath, replyToMessageID, replyToSenderJID string) (bool, string) {
	if !c.wm.IsConnected() {
		return false, "Not connected to WhatsApp"
	}

	var recipientJID types.JID
	var err error

	isJID := strings.Contains(recipient, "@")
	if isJID {
		recipientJID, err = types.ParseJID(recipient)
		if err != nil {
			return false, fmt.Sprintf("Error parsing JID: %v", err)
		}
	} else {
		recipientJID = types.JID{
			User:   recipient,
			Server: "s.whatsapp.net",
		}
	}

	msg := &waProto.Message{}
	var contextInfo *waProto.ContextInfo

	if replyToMessageID != "" {
		contextInfo = &waProto.ContextInfo{
			StanzaID: &replyToMessageID,
		}
		if replyToSenderJID != "" {
			contextInfo.Participant = &replyToSenderJID
		}
	}

	if mediaPath != "" {
		mediaData, err := os.ReadFile(mediaPath)
		if err != nil {
			return false, fmt.Sprintf("Error reading media file: %v", err)
		}

		fileExt := strings.ToLower(mediaPath[strings.LastIndex(mediaPath, ".")+1:])
		var mediaType whatsmeow.MediaType
		var mimeType string

		switch fileExt {
		case "jpg", "jpeg":
			mediaType = whatsmeow.MediaImage
			mimeType = "image/jpeg"
		case "png":
			mediaType = whatsmeow.MediaImage
			mimeType = "image/png"
		case "gif":
			mediaType = whatsmeow.MediaImage
			mimeType = "image/gif"
		case "webp":
			mediaType = whatsmeow.MediaImage
			mimeType = "image/webp"
		case "ogg":
			mediaType = whatsmeow.MediaAudio
			mimeType = "audio/ogg; codecs=opus"
		case "mp4":
			mediaType = whatsmeow.MediaVideo
			mimeType = "video/mp4"
		case "avi":
			mediaType = whatsmeow.MediaVideo
			mimeType = "video/avi"
		case "mov":
			mediaType = whatsmeow.MediaVideo
			mimeType = "video/quicktime"
		default:
			mediaType = whatsmeow.MediaDocument
			mimeType = "application/octet-stream"
		}

		resp, err := c.wm.Upload(context.Background(), mediaData, mediaType)
		if err != nil {
			return false, fmt.Sprintf("Error uploading media: %v", err)
		}

		c.logger.Debugf("Media uploaded %+v", resp)

		switch mediaType {
		case whatsmeow.MediaImage:
			msg.ImageMessage = &waProto.ImageMessage{
				Caption:       proto.String(message),
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &resp.FileLength,
				ContextInfo:   contextInfo,
			}
		case whatsmeow.MediaAudio:
			var seconds uint32 = 30
			var waveform []byte = nil

			if strings.Contains(mimeType, "ogg") {
				analyzedSeconds, analyzedWaveform, err := audio.AnalyzeOggOpus(mediaData)
				if err == nil {
					seconds = analyzedSeconds
					waveform = analyzedWaveform
				} else {
					return false, fmt.Sprintf("Failed to analyze Ogg Opus file: %v", err)
				}
			} else {
				c.logger.Debugf("Not an Ogg Opus file: %s", mimeType)
			}

			msg.AudioMessage = &waProto.AudioMessage{
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &resp.FileLength,
				Seconds:       proto.Uint32(seconds),
				PTT:           proto.Bool(true),
				Waveform:      waveform,
				ContextInfo:   contextInfo,
			}
		case whatsmeow.MediaVideo:
			msg.VideoMessage = &waProto.VideoMessage{
				Caption:       proto.String(message),
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &resp.FileLength,
				ContextInfo:   contextInfo,
			}
		case whatsmeow.MediaDocument:
			msg.DocumentMessage = &waProto.DocumentMessage{
				Title:         proto.String(mediaPath[strings.LastIndex(mediaPath, "/")+1:]),
				Caption:       proto.String(message),
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &resp.FileLength,
				ContextInfo:   contextInfo,
			}
		}
	} else {
		if contextInfo != nil {
			msg.ExtendedTextMessage = &waProto.ExtendedTextMessage{
				Text:        proto.String(message),
				ContextInfo: contextInfo,
			}
		} else {
			msg.Conversation = proto.String(message)
		}
	}

	resp, err := c.wm.SendMessage(context.Background(), recipientJID, msg)
	if err != nil {
		return false, fmt.Sprintf("Error sending message: %v", err)
	}

	c.storeOutgoing(recipientJID, resp, msg)

	return true, fmt.Sprintf("Message sent to %s", recipient)
}

// storeOutgoing records a message this daemon just sent. WhatsApp does not
// echo a message back to the session that sent it, so handleMessage never
// runs for our own sends — without this the local store and the /api/events
// stream only ever see one side of a conversation.
//
// Failures here are logged, never returned: the message is already delivered,
// and reporting a send as failed because the local copy did not persist would
// invite a duplicate send.
func (c *Client) storeOutgoing(recipient types.JID, resp whatsmeow.SendResponse, msg *waProto.Message) {
	defer c.recoverPanic("store outgoing message")

	chatJID := c.resolveToPN(recipient.String())
	s, ok := describeMessage(msg)
	if !ok {
		c.logger.Warnf("Sent message %s to %s has no storable content", resp.ID, chatJID)
		return
	}

	ts := resp.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	var sender string
	if c.wm != nil && c.wm.Store != nil {
		sender = c.wm.Store.GetJID().User
	}

	name := c.chatName(recipient, chatJID, nil, sender)
	if err := c.st.StoreChat(chatJID, name, ts); err != nil {
		c.logger.Warnf("Failed to store chat for sent message: %v", err)
		return
	}

	if err := c.st.StoreMessage(store.NewMessage{
		ID:            string(resp.ID),
		ChatJID:       chatJID,
		Sender:        sender,
		Content:       s.content,
		Timestamp:     ts,
		IsFromMe:      true,
		MediaType:     s.mediaType,
		Filename:      s.filename,
		URL:           s.url,
		MediaKey:      s.mediaKey,
		FileSHA256:    s.fileSHA256,
		FileEncSHA256: s.fileEncSHA256,
		FileLength:    s.fileLength,
	}); err != nil {
		c.logger.Warnf("Failed to store sent message %s: %v", resp.ID, err)
		return
	}

	stream.PublishMessage(string(resp.ID), chatJID, name, sender, true, ts, s.content, s.mediaType, s.filename)
	c.logger.Infof("[%s] → %s: %s", ts.Format("2006-01-02 15:04:05"), chatJID, s.content)
}
