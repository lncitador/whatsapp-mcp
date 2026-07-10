package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func forward[In any](baseURL, name string) func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		out, err := callRPC(baseURL, name, in)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out}}}, nil, nil
	}
}

type searchContactsIn struct {
	Query string `json:"query" jsonschema:"search term matching contact name or phone number"`
}

type listMessagesIn struct {
	After             string `json:"after,omitempty" jsonschema:"ISO-8601 date, only messages after this"`
	Before            string `json:"before,omitempty" jsonschema:"ISO-8601 date, only messages before this"`
	SenderPhoneNumber string `json:"sender_phone_number,omitempty" jsonschema:"filter by sender phone number"`
	ChatJID           string `json:"chat_jid,omitempty" jsonschema:"filter by chat JID"`
	Query             string `json:"query,omitempty" jsonschema:"search term in message content"`
	Limit             int    `json:"limit,omitempty" jsonschema:"max messages (default 20)"`
	Page              int    `json:"page,omitempty" jsonschema:"page number (default 0)"`
	IncludeContext    *bool  `json:"include_context,omitempty" jsonschema:"include surrounding messages (default true)"`
	ContextBefore     int    `json:"context_before,omitempty" jsonschema:"context messages before each hit (default 1)"`
	ContextAfter      int    `json:"context_after,omitempty" jsonschema:"context messages after each hit (default 1)"`
}

type listChatsIn struct {
	Query              string `json:"query,omitempty" jsonschema:"search term matching chat name or JID"`
	Limit              int    `json:"limit,omitempty" jsonschema:"max chats (default 20)"`
	Page               int    `json:"page,omitempty" jsonschema:"page number (default 0)"`
	IncludeLastMessage *bool  `json:"include_last_message,omitempty" jsonschema:"include last message preview (default true)"`
	SortBy             string `json:"sort_by,omitempty" jsonschema:"last_active or name (default last_active)"`
}

type getChatIn struct {
	ChatJID            string `json:"chat_jid" jsonschema:"the chat JID"`
	IncludeLastMessage *bool  `json:"include_last_message,omitempty" jsonschema:"include last message (default true)"`
}

type getDirectChatIn struct {
	SenderPhoneNumber string `json:"sender_phone_number" jsonschema:"the contact's phone number"`
}

type contactJIDIn struct {
	JID   string `json:"jid" jsonschema:"the contact's JID"`
	Limit int    `json:"limit,omitempty" jsonschema:"max results (default 20)"`
	Page  int    `json:"page,omitempty" jsonschema:"page number (default 0)"`
}

type lastInteractionIn struct {
	JID string `json:"jid" jsonschema:"the contact's JID"`
}

type messageContextIn struct {
	MessageID     string `json:"message_id" jsonschema:"the target message ID"`
	ContextBefore int    `json:"context_before,omitempty" jsonschema:"messages before (default 5)"`
	ContextAfter  int    `json:"context_after,omitempty" jsonschema:"messages after (default 5)"`
}

type sendMessageIn struct {
	Recipient        string `json:"recipient" jsonschema:"phone number with country code (no +) or JID"`
	Message          string `json:"message" jsonschema:"the message text to send"`
	ReplyToMessageID string `json:"reply_to_message_id,omitempty" jsonschema:"message ID to quote/reply to (from list_messages or get_message_context)"`
	ReplyToSenderJID string `json:"reply_to_sender_jid,omitempty" jsonschema:"original sender JID (required for group replies, optional for direct chats)"`
}

type sendFileIn struct {
	Recipient string `json:"recipient" jsonschema:"phone number with country code (no +) or JID"`
	MediaPath string `json:"media_path" jsonschema:"absolute path of the file to send"`
}

type downloadMediaIn struct {
	MessageID string `json:"message_id" jsonschema:"ID of the message containing media"`
	ChatJID   string `json:"chat_jid" jsonschema:"JID of the chat containing the message"`
}

type createGroupIn struct {
	Name               string   `json:"name" jsonschema:"group name (max 25 chars)"`
	Participants       []string `json:"participants" jsonschema:"list of phone numbers or JIDs to add"`
	IsCommunity        bool     `json:"is_community,omitempty" jsonschema:"create as community group"`
	CommunityParentJID string   `json:"community_parent_jid,omitempty" jsonschema:"attach as sub-group of this community"`
}

type leaveGroupIn struct {
	JID string `json:"jid" jsonschema:"group JID (must end with @g.us)"`
}

type approvalIn struct {
	RequestID string `json:"request_id" jsonschema:"request_id returned by send_message/send_file/send_audio_message"`
}

func forwardApproval(baseURL, action string) func(context.Context, *mcp.CallToolRequest, approvalIn) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, in approvalIn) (*mcp.CallToolResult, any, error) {
		out, err := callApproval(baseURL, action, in.RequestID)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out}}}, nil, nil
	}
}

func registerTools(s *mcp.Server, baseURL string) {
	mcp.AddTool(s, &mcp.Tool{Name: "search_contacts",
		Description: "Find a WhatsApp contact by name or phone number — ALWAYS use this first when the user mentions a person. Matches the phone's contact book, so real names work even without a recent chat. Returns each match's JID for use with the other tools. Multiple matches are all returned — ask the user to disambiguate."},
		forward[searchContactsIn](baseURL, "search_contacts"))
	mcp.AddTool(s, &mcp.Tool{Name: "list_messages",
		Description: "Get WhatsApp messages matching criteria, with optional surrounding context."},
		forward[listMessagesIn](baseURL, "list_messages"))
	mcp.AddTool(s, &mcp.Tool{Name: "list_chats",
		Description: "List WhatsApp conversations (recent first). NOT for finding a person — use search_contacts for that; a contact without a recent conversation will not appear here."},
		forward[listChatsIn](baseURL, "list_chats"))
	mcp.AddTool(s, &mcp.Tool{Name: "get_chat",
		Description: "Get a WhatsApp chat's metadata by JID."},
		forward[getChatIn](baseURL, "get_chat"))
	mcp.AddTool(s, &mcp.Tool{Name: "get_direct_chat_by_contact",
		Description: "Get the direct chat with a contact by phone number."},
		forward[getDirectChatIn](baseURL, "get_direct_chat_by_contact"))
	mcp.AddTool(s, &mcp.Tool{Name: "get_contact_chats",
		Description: "Get all chats (direct and groups) involving a contact."},
		forward[contactJIDIn](baseURL, "get_contact_chats"))
	mcp.AddTool(s, &mcp.Tool{Name: "get_last_interaction",
		Description: "Get the most recent message involving a contact."},
		forward[lastInteractionIn](baseURL, "get_last_interaction"))
	mcp.AddTool(s, &mcp.Tool{Name: "get_message_context",
		Description: "Get the messages around a specific message."},
		forward[messageContextIn](baseURL, "get_message_context"))
	mcp.AddTool(s, &mcp.Tool{Name: "send_message",
		Description: "Send a WhatsApp text message to a person or group. Optionally reply to a specific message using reply_to_message_id (obtainable from list_messages or get_message_context). For group replies, also provide reply_to_sender_jid."},
		forward[sendMessageIn](baseURL, "send_message"))
	mcp.AddTool(s, &mcp.Tool{Name: "send_file",
		Description: "Send a file (image, video, document, raw audio) via WhatsApp."},
		forward[sendFileIn](baseURL, "send_file"))
	mcp.AddTool(s, &mcp.Tool{Name: "send_audio_message",
		Description: "Send an audio file as a WhatsApp voice note. Non-.ogg inputs are converted with ffmpeg (must be installed)."},
		forward[sendFileIn](baseURL, "send_audio_message"))
	mcp.AddTool(s, &mcp.Tool{Name: "approve_send",
		Description: "Confirm a pending send. The send tools return status pending_approval with a request_id; ask the user for confirmation, then call this with that request_id to actually send. Requests expire after 5 minutes."},
		forwardApproval(baseURL, "approve"))
	mcp.AddTool(s, &mcp.Tool{Name: "reject_send",
		Description: "Cancel a pending send by request_id (from send_message/send_file/send_audio_message). Nothing is sent."},
		forwardApproval(baseURL, "reject"))
	mcp.AddTool(s, &mcp.Tool{Name: "download_media",
		Description: "Download media from a WhatsApp message; returns the local file path."},
		forward[downloadMediaIn](baseURL, "download_media"))
	mcp.AddTool(s, &mcp.Tool{Name: "create_group",
		Description: "Create a new WhatsApp group. Participants can be phone numbers or JIDs. Optionally create as community or attach to existing community."},
		forward[createGroupIn](baseURL, "create_group"))
	mcp.AddTool(s, &mcp.Tool{Name: "leave_group",
		Description: "Leave a WhatsApp group. Other members will continue to see the group."},
		forward[leaveGroupIn](baseURL, "leave_group"))
}
