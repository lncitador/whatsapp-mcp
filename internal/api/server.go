// Package api is the daemon's local HTTP surface: health/status, a generic
// tool-RPC endpoint consumed by the MCP stdio proxy, and the legacy
// /api/send + /api/download routes for backwards compatibility.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lncitador/whatsapp-mcp/internal/audio"
	"github.com/lncitador/whatsapp-mcp/internal/store"
	"github.com/lncitador/whatsapp-mcp/internal/wa"
)

type WA interface {
	Status() wa.Status
	SendMessage(recipient, message, mediaPath string) (bool, string)
	DownloadMedia(messageID, chatJID string) (path, mediaType, filename string, err error)
}

type Deps struct {
	Store      *store.Store
	WA         WA
	Version    string
	OnShutdown func()
}

type pendingRequest struct {
	ID        string
	Tool      string
	Recipient string
	Message   string
	MediaPath string
	CreatedAt time.Time
}

type approvalSystem struct {
	mu       sync.Mutex
	requests map[string]*pendingRequest
}

func newApprovalSystem() *approvalSystem {
	return &approvalSystem{requests: make(map[string]*pendingRequest)}
}

func (a *approvalSystem) Create(tool, recipient, message, mediaPath string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := fmt.Sprintf("req_%d", time.Now().UnixNano())
	a.requests[id] = &pendingRequest{
		ID: id, Tool: tool, Recipient: recipient,
		Message: message, MediaPath: mediaPath,
		CreatedAt: time.Now(),
	}
	return id
}

func (a *approvalSystem) Get(id string) (*pendingRequest, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.requests[id]
	if !ok {
		return nil, false
	}
	if time.Since(r.CreatedAt) > 5*time.Minute {
		delete(a.requests, id)
		return nil, false
	}
	return r, true
}

func (a *approvalSystem) Remove(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.requests, id)
}

func (a *approvalSystem) GetAndRemove(id string) (*pendingRequest, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.requests[id]
	if !ok {
		return nil, false
	}
	if time.Since(r.CreatedAt) > 5*time.Minute {
		delete(a.requests, id)
		return nil, false
	}
	delete(a.requests, id)
	return r, true
}

type Server struct {
	deps      Deps
	mux       *http.ServeMux
	http      *http.Server
	rateLim   *rateLimiter
	approvals *approvalSystem
}

func New(deps Deps) *Server {
	s := &Server{
		deps:      deps,
		mux:       http.NewServeMux(),
		rateLim:   newRateLimiter(10, time.Minute),
		approvals: newApprovalSystem(),
	}
	s.mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "version": deps.Version})
	})
	s.mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, deps.WA.Status())
	})
	s.mux.HandleFunc("POST /shutdown", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true})
		if deps.OnShutdown != nil {
			go deps.OnShutdown()
		}
	})
	s.mux.HandleFunc("POST /api/approve/{request_id}", s.handleApprove)
	s.mux.HandleFunc("POST /api/reject/{request_id}", s.handleReject)
	s.mux.HandleFunc("POST /api/rpc/{tool}", s.rateLimitMiddleware(s.handleRPC))
	s.registerLegacy()
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) ListenAndServe(port int) error {
	s.http = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", port),
		Handler: s.mux,
	}
	return s.http.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.http == nil {
		return nil
	}
	return s.http.Shutdown(ctx)
}

func (s *Server) rateLimitMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tool := r.PathValue("tool")
		if !s.rateLim.Allow(tool) {
			writeError(w, 429, "rate limit exceeded for tool: "+tool)
			return
		}
		next(w, r)
	}
}

// Legacy struct types ported from whatsapp-bridge/main.go:193-203, 473-485.

type SendMessageRequest struct {
	Recipient string `json:"recipient"`
	Message   string `json:"message"`
	MediaPath string `json:"media_path,omitempty"`
}

type SendMessageResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type DownloadMediaRequest struct {
	MessageID string `json:"message_id"`
	ChatJID   string `json:"chat_jid"`
}

type DownloadMediaResponse struct {
	Success  bool   `json:"success"`
	Message  string `json:"message"`
	Filename string `json:"filename,omitempty"`
	Path     string `json:"path,omitempty"`
}

func (s *Server) registerLegacy() {
	s.mux.HandleFunc("POST /api/send", func(w http.ResponseWriter, r *http.Request) {
		var req SendMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request format", http.StatusBadRequest)
			return
		}
		if req.Recipient == "" {
			http.Error(w, "Recipient is required", http.StatusBadRequest)
			return
		}
		if req.Message == "" && req.MediaPath == "" {
			http.Error(w, "Message or media path is required", http.StatusBadRequest)
			return
		}
		success, message := s.deps.WA.SendMessage(req.Recipient, req.Message, req.MediaPath)
		w.Header().Set("Content-Type", "application/json")
		if !success {
			w.WriteHeader(http.StatusInternalServerError)
		}
		json.NewEncoder(w).Encode(SendMessageResponse{Success: success, Message: message})
	})

	s.mux.HandleFunc("POST /api/download", func(w http.ResponseWriter, r *http.Request) {
		var req DownloadMediaRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request format", http.StatusBadRequest)
			return
		}
		if req.MessageID == "" || req.ChatJID == "" {
			http.Error(w, "Message ID and Chat JID are required", http.StatusBadRequest)
			return
		}
		path, mediaType, filename, err := s.deps.WA.DownloadMedia(req.MessageID, req.ChatJID)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(DownloadMediaResponse{
				Success: false,
				Message: fmt.Sprintf("Failed to download media: %s", err.Error()),
			})
			return
		}
		json.NewEncoder(w).Encode(DownloadMediaResponse{
			Success:  true,
			Message:  fmt.Sprintf("Successfully downloaded %s media", mediaType),
			Filename: filename,
			Path:     path,
		})
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeResult(w http.ResponseWriter, v any) {
	writeJSON(w, 200, map[string]any{"result": v})
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("request_id")
	req, ok := s.approvals.GetAndRemove(id)
	if !ok {
		writeError(w, 404, "approval request not found or expired")
		return
	}

	mediaPath := req.MediaPath
	if req.Tool == "send_audio_message" && mediaPath != "" && !strings.HasSuffix(mediaPath, ".ogg") {
		converted, err := audio.ConvertToOpusOggTemp(mediaPath)
		if err != nil {
			writeError(w, 500, "audio conversion failed: "+err.Error())
			return
		}
		defer os.Remove(converted)
		mediaPath = converted
	}

	success, msg := s.deps.WA.SendMessage(req.Recipient, req.Message, mediaPath)
	if !success {
		writeError(w, 502, msg)
		return
	}
	respond(w, map[string]any{"success": true, "message": msg}, nil)
}

func (s *Server) handleReject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("request_id")
	if _, ok := s.approvals.Get(id); !ok {
		writeError(w, 404, "approval request not found or expired")
		return
	}
	s.approvals.Remove(id)
	respond(w, map[string]any{"success": true, "message": "request rejected"}, nil)
}
