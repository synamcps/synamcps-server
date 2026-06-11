package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/zmiishe/synamcps/internal/mcpproxy"
	"github.com/zmiishe/synamcps/internal/models"
)

func (h *AdminHandler) handleMCPServers(w http.ResponseWriter, r *http.Request, path string, p models.Principal) {
	if h.mcpStore == nil {
		http.Error(w, "mcp proxy is not configured", http.StatusServiceUnavailable)
		return
	}
	switch {
	case r.Method == http.MethodGet && path == "/mcp-servers":
		h.listMCPServers(w, r, p)
	case r.Method == http.MethodPost && path == "/mcp-servers":
		h.createMCPServer(w, r, p)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/connect-test"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/mcp-servers/"), "/connect-test")
		h.connectTestMCPServer(w, r, id, p)
	case r.Method == http.MethodPut && strings.HasSuffix(path, "/capabilities"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/mcp-servers/"), "/capabilities")
		h.setMCPServerCapabilities(w, r, id, p)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/acl"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/mcp-servers/"), "/acl")
		h.getMCPServerACL(w, r, id)
	case r.Method == http.MethodPut && strings.HasSuffix(path, "/acl"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/mcp-servers/"), "/acl")
		h.putMCPServerACL(w, r, id, p)
	case r.Method == http.MethodDelete && strings.HasSuffix(path, "/auth-secret"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/mcp-servers/"), "/auth-secret")
		h.clearMCPServerSecret(w, r, id, p)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/mcp-servers/") && mcpServerID(path) != "":
		h.getMCPServer(w, r, mcpServerID(path), p)
	case r.Method == http.MethodPatch && strings.HasPrefix(path, "/mcp-servers/") && mcpServerID(path) != "":
		h.patchMCPServer(w, r, mcpServerID(path), p)
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/mcp-servers/") && mcpServerID(path) != "":
		h.deleteMCPServer(w, r, mcpServerID(path), p)
	default:
		http.NotFound(w, r)
	}
}

func mcpServerID(path string) string {
	rest := strings.TrimPrefix(path, "/mcp-servers/")
	if rest == "" || strings.Contains(rest, "/") {
		return ""
	}
	return rest
}

func (h *AdminHandler) listMCPServers(w http.ResponseWriter, r *http.Request, p models.Principal) {
	all, err := h.mcpStore.ListServers(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var out []models.MCPServer
	for _, srv := range all {
		ok, _ := h.mcpAccess.CanAccessMCPServer(r.Context(), p, nil, nil, srv.ID, models.PermissionMCPServerUse)
		if ok {
			out = append(out, srv)
		}
	}
	writeJSON(w, out, http.StatusOK)
}

type createMCPServerRequest struct {
	Slug           string                 `json:"slug"`
	Name           string                 `json:"name"`
	Transport      models.MCPTransportKind `json:"transport"`
	URL            string                 `json:"url"`
	HeadersJSON    string                 `json:"headersJson"`
	AuthType       models.MCPAuthType     `json:"authType"`
	AuthHeaderName string                 `json:"authHeaderName"`
	AuthSecret     string                 `json:"authSecret"`
}

func (h *AdminHandler) createMCPServer(w http.ResponseWriter, r *http.Request, p models.Principal) {
	var req createMCPServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	srv, err := h.mcpStore.CreateServer(r.Context(), mcpproxy.CreateServerInput{
		Slug:            req.Slug,
		Name:            req.Name,
		OwnerSubjectKey: models.SubjectKeyForPrincipal(p),
		Transport:       req.Transport,
		URL:             req.URL,
		HeadersJSON:     req.HeadersJSON,
		AuthType:        req.AuthType,
		AuthHeaderName:  req.AuthHeaderName,
		AuthSecret:      req.AuthSecret,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, srv, http.StatusCreated)
}

func (h *AdminHandler) getMCPServer(w http.ResponseWriter, r *http.Request, id string, p models.Principal) {
	ok, _ := h.mcpAccess.CanAccessMCPServer(r.Context(), p, nil, nil, id, models.PermissionMCPServerUse)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	caps, err := h.mcpStore.GetCapabilities(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	acl, _ := h.mcpStore.ACLForServer(r.Context(), id)
	writeJSON(w, map[string]any{
		"server":    caps.Server,
		"tools":     caps.Tools,
		"resources": caps.Resources,
		"prompts":   caps.Prompts,
		"acl":       acl,
	}, http.StatusOK)
}

func (h *AdminHandler) patchMCPServer(w http.ResponseWriter, r *http.Request, id string, p models.Principal) {
	ok, _ := h.mcpAccess.CanAccessMCPServer(r.Context(), p, nil, nil, id, models.PermissionMCPServerManage)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var req struct {
		Name           *string                 `json:"name"`
		Transport      *models.MCPTransportKind `json:"transport"`
		URL            *string                 `json:"url"`
		HeadersJSON    *string                 `json:"headersJson"`
		AuthType       *models.MCPAuthType     `json:"authType"`
		AuthHeaderName *string                 `json:"authHeaderName"`
		AuthSecret     *string                 `json:"authSecret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	in := mcpproxy.UpdateServerInput{
		Name: req.Name, Transport: req.Transport, URL: req.URL, HeadersJSON: req.HeadersJSON,
		AuthType: req.AuthType, AuthHeaderName: req.AuthHeaderName,
	}
	if req.AuthSecret != nil {
		if *req.AuthSecret == "__clear__" {
			in.ClearAuthSecret = true
		} else if *req.AuthSecret != "" {
			in.AuthSecret = req.AuthSecret
		}
	}
	srv, err := h.mcpStore.UpdateServer(r.Context(), id, in)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, srv, http.StatusOK)
}

func (h *AdminHandler) clearMCPServerSecret(w http.ResponseWriter, r *http.Request, id string, p models.Principal) {
	ok, _ := h.mcpAccess.CanAccessMCPServer(r.Context(), p, nil, nil, id, models.PermissionMCPServerManage)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := h.mcpStore.ClearAuthSecret(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) deleteMCPServer(w http.ResponseWriter, r *http.Request, id string, p models.Principal) {
	ok, _ := h.mcpAccess.CanAccessMCPServer(r.Context(), p, nil, nil, id, models.PermissionMCPServerDelete)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := h.mcpStore.DeleteServer(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) connectTestMCPServer(w http.ResponseWriter, r *http.Request, id string, p models.Principal) {
	ok, _ := h.mcpAccess.CanAccessMCPServer(r.Context(), p, nil, nil, id, models.PermissionMCPServerManage)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if h.mcpManager == nil {
		http.Error(w, "mcp proxy is not configured", http.StatusServiceUnavailable)
		return
	}
	caps, err := h.mcpManager.ConnectTest(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, caps, http.StatusOK)
}

func (h *AdminHandler) setMCPServerCapabilities(w http.ResponseWriter, r *http.Request, id string, p models.Principal) {
	ok, _ := h.mcpAccess.CanAccessMCPServer(r.Context(), p, nil, nil, id, models.PermissionMCPServerManage)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var req struct {
		EnabledTools     []string `json:"enabledTools"`
		EnabledResources []string `json:"enabledResources"`
		EnabledPrompts   []string `json:"enabledPrompts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.mcpStore.SetEnabledCapabilities(r.Context(), id, req.EnabledTools, req.EnabledResources, req.EnabledPrompts); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	caps, err := h.mcpStore.GetCapabilities(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, caps, http.StatusOK)
}

func (h *AdminHandler) getMCPServerACL(w http.ResponseWriter, r *http.Request, id string) {
	acl, err := h.mcpStore.ACLForServer(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, acl, http.StatusOK)
}

func (h *AdminHandler) putMCPServerACL(w http.ResponseWriter, r *http.Request, id string, p models.Principal) {
	ok, _ := h.mcpAccess.CanAccessMCPServer(r.Context(), p, nil, nil, id, models.PermissionMCPServerManage)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var req models.MCPServerACLBinding
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.ServerID = id
	req.GrantedBy = models.SubjectKeyForPrincipal(p)
	b, err := h.mcpStore.UpsertACL(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, b, http.StatusOK)
}

func (h *AdminHandler) patchTokenMCPScopes(w http.ResponseWriter, r *http.Request, tokenID string, p models.Principal) {
	if h.mcpStore == nil {
		http.Error(w, "mcp proxy is not configured", http.StatusServiceUnavailable)
		return
	}
	if !h.requireTokenOwner(w, r, p, tokenID) {
		return
	}
	var req struct {
		Scopes []models.AccessTokenMCPServer `json:"mcpServers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	for i := range req.Scopes {
		req.Scopes[i].TokenID = tokenID
		if h.mcpManager != nil {
			if err := h.mcpManager.ValidateScopeNames(r.Context(), req.Scopes[i].ServerID, req.Scopes[i].ToolAllowlist, req.Scopes[i].ResourceAllowlist, req.Scopes[i].PromptAllowlist); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
	}
	if err := h.mcpStore.ReplaceTokenMCPServers(r.Context(), tokenID, req.Scopes); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"status": "updated"}, http.StatusOK)
}
