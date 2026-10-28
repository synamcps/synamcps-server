package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/synamcps/synamcps-server/internal/agentkeys"
	"github.com/synamcps/synamcps-server/internal/models"
)

type AgentKeysHandler struct {
	Store *agentkeys.Store
}

func (h *AgentKeysHandler) Register(mux *http.ServeMux, auth func(http.Handler) http.Handler) {
	mux.Handle("POST /api/agent-keys/challenge", auth(http.HandlerFunc(h.challenge)))
	mux.Handle("POST /api/agent-keys", auth(http.HandlerFunc(h.register)))
	mux.Handle("GET /api/agent-keys", auth(http.HandlerFunc(h.list)))
	mux.Handle("DELETE /api/agent-keys/{id}", auth(http.HandlerFunc(h.revoke)))
}

func (h *AgentKeysHandler) binder(r *http.Request, p models.Principal, ac models.APIAccessContext) string {
	if ac.AccessToken != nil {
		return "token:" + ac.AccessToken.ID
	}
	if c, err := r.Cookie("session_id"); err == nil && c.Value != "" {
		return "session:" + c.Value
	}
	return "subject:" + models.SubjectKeyForPrincipal(p)
}

func (h *AgentKeysHandler) challenge(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ac := accessContextFromRequest(r)
	ch, err := h.Store.CreateChallenge(r.Context(), h.binder(r, p, ac), 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"nonce": ch.Nonce, "expiresAt": ch.ExpiresAt}, http.StatusOK)
}

func (h *AgentKeysHandler) register(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ac := accessContextFromRequest(r)
	var req struct {
		Alg       string `json:"alg"`
		PublicKey string `json:"publicKey"`
		Nonce     string `json:"nonce"`
		Signature string `json:"signature"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	pub, err := decodeKeyBytes(req.PublicKey)
	if err != nil {
		http.Error(w, "invalid publicKey", http.StatusBadRequest)
		return
	}
	sig, err := decodeKeyBytes(req.Signature)
	if err != nil {
		http.Error(w, "invalid signature", http.StatusBadRequest)
		return
	}
	tokenID := ""
	if ac.AccessToken != nil {
		tokenID = ac.AccessToken.ID
	}
	key, err := h.Store.Register(r.Context(), agentkeys.RegisterInput{
		SubjectKey: models.SubjectKeyForPrincipal(p),
		TokenID:    tokenID,
		Algorithm:  req.Alg,
		PublicKey:  pub,
		Nonce:      req.Nonce,
		Signature:  sig,
		Binder:     h.binder(r, p, ac),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, key, http.StatusCreated)
}

func (h *AgentKeysHandler) list(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	keys, err := h.Store.ListForSubject(r.Context(), models.SubjectKeyForPrincipal(p))
	if err != nil {
		http.Error(w, err.Error(), statusFromErr(err))
		return
	}
	writeJSON(w, map[string]any{"keys": keys}, http.StatusOK)
}

func (h *AgentKeysHandler) revoke(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id := r.PathValue("id")
	if err := h.Store.Revoke(r.Context(), id, models.SubjectKeyForPrincipal(p)); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"status": "revoked"}, http.StatusOK)
}

func decodeKeyBytes(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if out, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return out, nil
	}
	return base64.StdEncoding.DecodeString(s)
}
