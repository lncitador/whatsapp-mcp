package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/mdp/qrterminal"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var httpClient = &http.Client{Timeout: 120 * time.Second}

func callRPC(baseURL, tool string, args any) (string, error) {
	body, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	resp, err := httpClient.Post(baseURL+"/api/rpc/"+tool, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("daemon unreachable (%v) — run `whatsapp-mcp status` to diagnose", err)
	}
	defer resp.Body.Close()
	var out struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("bad daemon response: %v", err)
	}
	if out.Error != "" {
		return "", fmt.Errorf("%s", out.Error)
	}
	return string(out.Result), nil
}

func fetchStatus(baseURL string) (state, qr, message string, err error) {
	resp, err := httpClient.Get(baseURL + "/status")
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	var st struct {
		State   string `json:"state"`
		QRCode  string `json:"qr_code"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return "", "", "", err
	}
	return st.State, st.QRCode, st.Message, nil
}

func New(version, baseURL string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "whatsapp", Version: version}, nil)
	registerTools(s, baseURL)

	type authIn struct{}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "auth_status",
		Description: "Check the WhatsApp session state. When re-authentication is needed, returns the QR code to scan with the WhatsApp app (Settings > Linked Devices).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in authIn) (*mcp.CallToolResult, any, error) {
		state, qr, message, err := fetchStatus(baseURL)
		if err != nil {
			return nil, nil, fmt.Errorf("daemon unreachable: %v — run `whatsapp-mcp status`", err)
		}
		text := "state: " + state
		if message != "" {
			text += "\n" + message
		}
		if qr != "" {
			var buf bytes.Buffer
			qrterminal.GenerateHalfBlock(qr, qrterminal.L, &buf)
			text += "\n\nScan this QR code with WhatsApp:\n\n" + buf.String()
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, nil, nil
	})
	return s
}

func Run(ctx context.Context, version, baseURL string) error {
	return New(version, baseURL).Run(ctx, &mcp.StdioTransport{})
}
