package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lncitador/whatsapp-mcp/internal/audio"
	"github.com/lncitador/whatsapp-mcp/internal/store"
)

type rpcArgs struct {
	Query              string `json:"query"`
	After              string `json:"after"`
	Before             string `json:"before"`
	SenderPhoneNumber  string `json:"sender_phone_number"`
	ChatJID            string `json:"chat_jid"`
	Limit              int    `json:"limit"`
	Page               int    `json:"page"`
	IncludeContext     *bool  `json:"include_context"`
	ContextBefore      int    `json:"context_before"`
	ContextAfter       int    `json:"context_after"`
	IncludeLastMessage *bool  `json:"include_last_message"`
	SortBy             string `json:"sort_by"`
	JID                string `json:"jid"`
	MessageID          string `json:"message_id"`
	Recipient          string `json:"recipient"`
	Message            string `json:"message"`
	MediaPath          string `json:"media_path"`
}

func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	tool := r.PathValue("tool")
	var a rpcArgs
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		writeError(w, 400, "invalid JSON body: "+err.Error())
		return
	}
	switch tool {
	case "search_contacts":
		res, err := s.deps.Store.SearchContacts(a.Query)
		respond(w, res, err)
	case "list_messages":
		args := store.ListMessagesArgs{
			SenderPhoneNumber: a.SenderPhoneNumber, ChatJID: a.ChatJID,
			Query: a.Query, Limit: a.Limit, Page: a.Page,
		}
		var err error
		if args.After, err = parseISO(a.After); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		if args.Before, err = parseISO(a.Before); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		msgs, err := s.deps.Store.ListMessages(args)
		if err != nil {
			respond(w, nil, err)
			return
		}
		includeCtx := a.IncludeContext == nil || *a.IncludeContext
		if includeCtx {
			cb, ca := a.ContextBefore, a.ContextAfter
			if cb == 0 {
				cb = 1
			}
			if ca == 0 {
				ca = 1
			}
			var expanded []store.Message
			for _, m := range msgs {
				ctx, err := s.deps.Store.GetMessageContext(m.ID, cb, ca)
				if err != nil {
					continue
				}
				expanded = append(expanded, ctx.Before...)
				expanded = append(expanded, ctx.Message)
				expanded = append(expanded, ctx.After...)
			}
			msgs = expanded
		}
		respond(w, s.formatMessages(msgs), nil)
	case "list_chats":
		ilm := a.IncludeLastMessage == nil || *a.IncludeLastMessage
		sortBy := a.SortBy
		if sortBy == "" {
			sortBy = "last_active"
		}
		res, err := s.deps.Store.ListChats(a.Query, a.Limit, a.Page, ilm, sortBy)
		respond(w, res, err)
	case "get_chat":
		ilm := a.IncludeLastMessage == nil || *a.IncludeLastMessage
		res, err := s.deps.Store.GetChat(a.ChatJID, ilm)
		respond(w, res, err)
	case "get_direct_chat_by_contact":
		res, err := s.deps.Store.GetDirectChatByContact(a.SenderPhoneNumber)
		respond(w, res, err)
	case "get_contact_chats":
		res, err := s.deps.Store.GetContactChats(a.JID, a.Limit, a.Page)
		respond(w, res, err)
	case "get_last_interaction":
		m, err := s.deps.Store.GetLastInteraction(a.JID)
		if err != nil || m == nil {
			respond(w, "", err)
			return
		}
		respond(w, s.formatMessage(*m), nil)
	case "get_message_context":
		cb, ca := a.ContextBefore, a.ContextAfter
		if cb == 0 {
			cb = 5
		}
		if ca == 0 {
			ca = 5
		}
		res, err := s.deps.Store.GetMessageContext(a.MessageID, cb, ca)
		respond(w, res, err)
	case "send_message":
		if a.Recipient == "" {
			writeError(w, 400, "recipient must be provided")
			return
		}
		ok, msg := s.deps.WA.SendMessage(a.Recipient, a.Message, "")
		respond(w, map[string]any{"success": ok, "message": msg}, nil)
	case "send_file":
		if a.Recipient == "" || a.MediaPath == "" {
			writeError(w, 400, "recipient and media_path must be provided")
			return
		}
		if _, err := os.Stat(a.MediaPath); err != nil {
			writeError(w, 400, "media file not found: "+a.MediaPath)
			return
		}
		ok, msg := s.deps.WA.SendMessage(a.Recipient, "", a.MediaPath)
		respond(w, map[string]any{"success": ok, "message": msg}, nil)
	case "send_audio_message":
		if a.Recipient == "" || a.MediaPath == "" {
			writeError(w, 400, "recipient and media_path must be provided")
			return
		}
		path := a.MediaPath
		if !strings.HasSuffix(path, ".ogg") {
			converted, err := audio.ConvertToOpusOggTemp(path)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			defer os.Remove(converted)
			path = converted
		}
		ok, msg := s.deps.WA.SendMessage(a.Recipient, "", path)
		respond(w, map[string]any{"success": ok, "message": msg}, nil)
	case "download_media":
		path, mediaType, filename, err := s.deps.WA.DownloadMedia(a.MessageID, a.ChatJID)
		if err != nil {
			respond(w, nil, err)
			return
		}
		respond(w, map[string]any{"path": path, "media_type": mediaType, "filename": filename}, nil)
	default:
		writeError(w, 404, "unknown tool: "+tool)
	}
}

func respond(w http.ResponseWriter, v any, err error) {
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeResult(w, v)
}

func parseISO(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	for _, l := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, &time.ParseError{Layout: time.RFC3339, Value: s,
		Message: ": invalid date, use ISO-8601 (e.g. 2026-07-08T00:00:00Z)"}
}

func (s *Server) formatMessage(m store.Message) string {
	out := "[" + m.Timestamp.Format("2006-01-02 15:04:05") + "] "
	if m.ChatName != "" {
		out = "[" + m.Timestamp.Format("2006-01-02 15:04:05") + "] Chat: " + m.ChatName + " "
	}
	prefix := ""
	if m.MediaType != "" {
		prefix = "[" + m.MediaType + " - Message ID: " + m.ID + " - Chat JID: " + m.ChatJID + "] "
	}
	sender := "Me"
	if !m.IsFromMe {
		sender = s.deps.Store.SenderName(m.Sender)
	}
	return out + "From: " + sender + ": " + prefix + sanitizeContent(m.Content) + "\n"
}

func (s *Server) formatMessages(msgs []store.Message) string {
	if len(msgs) == 0 {
		return "No messages to display."
	}
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(s.formatMessage(m))
	}
	return b.String()
}
