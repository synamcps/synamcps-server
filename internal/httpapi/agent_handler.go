package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/synamcps/synamcps-server/internal/agent"
)

type AgentHandler struct {
	service *agent.Service
}

func NewAgentHandler(service *agent.Service) *AgentHandler {
	return &AgentHandler{service: service}
}

func (h *AgentHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		http.NotFound(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/agent")
	switch {
	case r.Method == http.MethodGet && path == "/conversations":
		h.listConversations(w, r)
	case r.Method == http.MethodPost && path == "/conversations":
		h.createConversation(w, r)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/messages"):
		h.listMessages(w, r, conversationIDFromAgentPath(path))
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/messages"):
		h.sendMessage(w, r, conversationIDFromAgentPath(path))
	default:
		http.NotFound(w, r)
	}
}

func (h *AgentHandler) listConversations(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	out, err := h.service.ListConversations(r.Context(), p)
	if err != nil {
		http.Error(w, err.Error(), statusFromErr(err))
		return
	}
	writeJSON(w, map[string]any{"conversations": out}, http.StatusOK)
}

func (h *AgentHandler) createConversation(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req agent.CreateConversationInput
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	conv, err := h.service.CreateConversation(r.Context(), p, accessContextFromRequest(r), req)
	if err != nil {
		http.Error(w, err.Error(), statusFromErr(err))
		return
	}
	writeJSON(w, conv, http.StatusCreated)
}

func (h *AgentHandler) listMessages(w http.ResponseWriter, r *http.Request, conversationID string) {
	p, ok := principalFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	out, err := h.service.Messages(r.Context(), p, conversationID)
	if err != nil {
		http.Error(w, err.Error(), statusFromErr(err))
		return
	}
	writeJSON(w, map[string]any{"messages": out}, http.StatusOK)
}

func (h *AgentHandler) sendMessage(w http.ResponseWriter, r *http.Request, conversationID string) {
	p, ok := principalFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req agent.SendMessageInput
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	reply, err := h.service.SendMessage(r.Context(), p, accessContextFromRequest(r), conversationID, req)
	if err != nil {
		http.Error(w, err.Error(), statusFromErr(err))
		return
	}
	writeAgentSSE(w, reply)
}

func writeAgentSSE(w http.ResponseWriter, reply agent.Reply) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	writeSSE(w, "conversation", reply.Conversation)
	writeSSE(w, "documents", reply.DocumentRefs)
	if reply.SavedMemory != nil {
		writeSSE(w, "saved_memory", reply.SavedMemory)
	}
	writeSSE(w, "message", reply.Message)
	writeSSE(w, "done", map[string]bool{"ok": true})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func writeSSE(w http.ResponseWriter, event string, payload any) {
	b, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
}

func conversationIDFromAgentPath(path string) string {
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) >= 2 && parts[0] == "conversations" {
		return parts[1]
	}
	return ""
}
