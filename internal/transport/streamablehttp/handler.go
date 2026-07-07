package streamablehttp

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/synamcps/synamcps-server/internal/auth"
	"github.com/synamcps/synamcps-server/internal/domainerr"
	"github.com/synamcps/synamcps-server/internal/mcp"
	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/session"
)

type Handler struct {
	server   *mcp.Server
	gateway  *auth.Gateway
	sessions *session.Store
}

func NewHandler(server *mcp.Server, gateway *auth.Gateway, sessions *session.Store) *Handler {
	return &Handler{server: server, gateway: gateway, sessions: sessions}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("POST /mcp", h.gateway.Middleware(http.HandlerFunc(h.post)))
	mux.Handle("GET /mcp", h.gateway.Middleware(http.HandlerFunc(h.get)))
	mux.Handle("DELETE /mcp", h.gateway.Middleware(http.HandlerFunc(h.deleteSession)))
}

func (h *Handler) post(w http.ResponseWriter, r *http.Request) {
	p, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !accepts(r, "application/json") && !accepts(r, "*/*") {
		http.Error(w, "Accept must allow application/json", http.StatusNotAcceptable)
		return
	}
	if sessionID := r.Header.Get("Mcp-Session-Id"); sessionID != "" {
		if !h.authorizedSession(w, p, sessionID) {
			return
		}
	}

	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, mcp.NewErrorResponse(nil, -32700, "parse error"), http.StatusBadRequest)
		return
	}
	responses, status := h.handleJSONRPCPayload(w, r, p, raw)
	if len(responses) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, responsesPayload(raw, responses), status)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	p, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !accepts(r, "text/event-stream") && !accepts(r, "*/*") {
		http.Error(w, "Accept must allow text/event-stream", http.StatusNotAcceptable)
		return
	}
	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		http.Error(w, "missing Mcp-Session-Id", http.StatusBadRequest)
		return
	}
	if !h.authorizedSession(w, p, sessionID) {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	streamID := "default"
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	for _, replay := range h.sessions.ReplayFrom(sessionID, streamID, r.Header.Get("Last-Event-ID")) {
		_, _ = w.Write([]byte(replay))
	}
	flusher.Flush()

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			_, _ = w.Write([]byte(": keepalive\n\n"))
			flusher.Flush()
		}
	}
}

func (h *Handler) deleteSession(w http.ResponseWriter, r *http.Request) {
	p, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		http.Error(w, "missing Mcp-Session-Id", http.StatusBadRequest)
		return
	}
	if !h.authorizedSession(w, p, sessionID) {
		return
	}
	h.sessions.DeleteMCPSession(sessionID)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleJSONRPCPayload(w http.ResponseWriter, r *http.Request, p models.Principal, raw []byte) ([]mcp.JSONRPCResponse, int) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return []mcp.JSONRPCResponse{mcp.NewErrorResponse(nil, -32700, "parse error")}, http.StatusBadRequest
	}
	if strings.HasPrefix(trimmed, "[") {
		var batch []mcp.JSONRPCRequest
		if err := json.Unmarshal(raw, &batch); err != nil || len(batch) == 0 {
			return []mcp.JSONRPCResponse{mcp.NewErrorResponse(nil, -32600, "invalid request")}, http.StatusBadRequest
		}
		out := make([]mcp.JSONRPCResponse, 0, len(batch))
		status := http.StatusOK
		for _, req := range batch {
			resp, code := h.handleOne(w, r, p, req)
			if resp != nil {
				out = append(out, *resp)
			}
			if code >= 400 {
				status = code
			}
		}
		return out, status
	}
	var req mcp.JSONRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return []mcp.JSONRPCResponse{mcp.NewErrorResponse(nil, -32700, "parse error")}, http.StatusBadRequest
	}
	resp, status := h.handleOne(w, r, p, req)
	if resp == nil {
		return nil, status
	}
	return []mcp.JSONRPCResponse{*resp}, status
}

func (h *Handler) handleOne(w http.ResponseWriter, r *http.Request, p models.Principal, req mcp.JSONRPCRequest) (*mcp.JSONRPCResponse, int) {
	resp, err := h.server.HandleRequest(r.Context(), p, req)
	if err != nil {
		if req.IsNotification() {
			return nil, http.StatusAccepted
		}
		return ptr(mcp.NewErrorResponse(req.ID, domainerr.JSONRPCCode(err), err.Error())), http.StatusOK
	}
	if resp == nil {
		return nil, http.StatusAccepted
	}
	if req.Method == "initialize" {
		if result, ok := resp.Result.(map[string]any); ok {
			if sessionID, _ := result["sessionId"].(string); sessionID != "" {
				// MCP clients expect the streamable HTTP session id in this header.
				// The result keeps sessionId for compatibility with older clients.
				w.Header().Set("Mcp-Session-Id", sessionID)
			}
		}
	}
	return resp, http.StatusOK
}

func (h *Handler) authorizedSession(w http.ResponseWriter, p models.Principal, sessionID string) bool {
	sess, ok := h.sessions.GetMCPSession(sessionID)
	if !ok {
		http.Error(w, "invalid session", http.StatusNotFound)
		return false
	}
	if models.SubjectKeyForPrincipal(sess.Principal) != models.SubjectKeyForPrincipal(p) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}

func accepts(r *http.Request, want string) bool {
	values := r.Header.Values("Accept")
	if len(values) == 0 {
		return true
	}
	for _, raw := range values {
		for _, item := range strings.Split(raw, ",") {
			mediaType := strings.TrimSpace(strings.Split(item, ";")[0])
			if mediaType == want || mediaType == "*/*" {
				return true
			}
		}
	}
	return false
}

func responsesPayload(raw []byte, responses []mcp.JSONRPCResponse) any {
	if strings.HasPrefix(strings.TrimSpace(string(raw)), "[") {
		return responses
	}
	return responses[0]
}

func writeJSON(w http.ResponseWriter, payload any, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func ptr[T any](v T) *T {
	return &v
}
