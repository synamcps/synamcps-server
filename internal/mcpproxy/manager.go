package mcpproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/zmiishe/synamcps/internal/config"
	"github.com/zmiishe/synamcps/internal/models"
)

type Manager struct {
	store   *Store
	cfg     config.MCPProxyConfig
	sessions map[string]*upstreamClient
	mu      sync.Mutex
}

func NewManager(cfg config.MCPProxyConfig, store *Store) *Manager {
	return &Manager{
		store:    store,
		cfg:      cfg,
		sessions: map[string]*upstreamClient{},
	}
}

func (m *Manager) Store() *Store { return m.store }

func (m *Manager) Enabled() bool { return m.cfg.Enabled && m.store != nil }

func (m *Manager) ConnectTest(ctx context.Context, serverID string) (models.MCPServerCapabilities, error) {
	srv, err := m.store.GetServer(ctx, serverID)
	if err != nil {
		return models.MCPServerCapabilities{}, err
	}
	client, err := m.newClient(ctx, srv)
	if err != nil {
		_ = m.store.SetServerStatus(ctx, serverID, models.MCPServerStatusError, err.Error(), false)
		return models.MCPServerCapabilities{}, err
	}
	now := time.Now().UTC()
	discovered, err := client.discover(ctx, serverID, now)
	if err != nil {
		_ = m.store.SetServerStatus(ctx, serverID, models.MCPServerStatusError, err.Error(), false)
		return models.MCPServerCapabilities{}, err
	}
	if err := m.store.UpsertDiscovery(ctx, serverID, discovered.Tools, discovered.Resources, discovered.Prompts); err != nil {
		return models.MCPServerCapabilities{}, err
	}
	_ = m.store.SetServerStatus(ctx, serverID, models.MCPServerStatusActive, "", true)
	m.mu.Lock()
	m.sessions[serverID] = client
	m.mu.Unlock()
	return m.store.GetCapabilities(ctx, serverID)
}

func (m *Manager) newClient(ctx context.Context, srv models.MCPServer) (*upstreamClient, error) {
	headers, err := m.store.AuthHeaders(ctx, srv)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(m.cfg.ConnectTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return newUpstreamClient(srv.URL, headers, timeout), nil
}

func (m *Manager) getClient(ctx context.Context, serverID string) (*upstreamClient, error) {
	m.mu.Lock()
	if c, ok := m.sessions[serverID]; ok {
		m.mu.Unlock()
		return c, nil
	}
	m.mu.Unlock()
	srv, err := m.store.GetServer(ctx, serverID)
	if err != nil {
		return nil, err
	}
	client, err := m.newClient(ctx, srv)
	if err != nil {
		return nil, err
	}
	if err := client.initialize(ctx); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.sessions[serverID] = client
	m.mu.Unlock()
	return client, nil
}

type ExposedCapabilities struct {
	Tools     []map[string]any
	Resources []map[string]any
	Prompts   []map[string]any
}

func (m *Manager) ExposedForAccess(ctx context.Context, servers []AccessibleServer) (ExposedCapabilities, error) {
	var out ExposedCapabilities
	for _, item := range servers {
		caps, err := m.store.GetCapabilities(ctx, item.Server.ID)
		if err != nil {
			continue
		}
		for _, t := range caps.Tools {
			if !t.Enabled {
				continue
			}
			name := NamespacedTool(item.Server.Slug, t.ToolName)
			if !allow(item.Scope.ToolAllowlist, name) {
				continue
			}
			out.Tools = append(out.Tools, toolDescriptor(name, t))
		}
		for _, r := range caps.Resources {
			if !r.Enabled {
				continue
			}
			uri := NamespacedResourceURI(item.Server.Slug, r.URI)
			if !allow(item.Scope.ResourceAllowlist, uri) {
				continue
			}
			out.Resources = append(out.Resources, resourceDescriptor(uri, r))
		}
		for _, p := range caps.Prompts {
			if !p.Enabled {
				continue
			}
			name := NamespacedPrompt(item.Server.Slug, p.PromptName)
			if !allow(item.Scope.PromptAllowlist, name) {
				continue
			}
			out.Prompts = append(out.Prompts, promptDescriptor(name, p))
		}
	}
	return out, nil
}

type AccessibleServer struct {
	Server models.MCPServer
	Scope  models.AccessTokenMCPServer
}

func allow(allowlist []string, name string) bool {
	if len(allowlist) == 0 {
		return true
	}
	for _, v := range allowlist {
		if v == name {
			return true
		}
	}
	return false
}

func toolDescriptor(name string, t models.MCPServerTool) map[string]any {
	schema := map[string]any{"type": "object", "properties": map[string]any{}}
	if t.InputSchemaJSON != "" {
		_ = json.Unmarshal([]byte(t.InputSchemaJSON), &schema)
	}
	return map[string]any{
		"name":        name,
		"description": t.Description,
		"inputSchema": schema,
	}
}

func resourceDescriptor(uri string, r models.MCPServerResource) map[string]any {
	return map[string]any{
		"uri":         uri,
		"name":        r.Name,
		"description": r.Description,
		"mimeType":    r.MimeType,
	}
}

func promptDescriptor(name string, p models.MCPServerPrompt) map[string]any {
	out := map[string]any{
		"name":        name,
		"description": p.Description,
	}
	if p.ArgumentsSchemaJSON != "" {
		var args any
		_ = json.Unmarshal([]byte(p.ArgumentsSchemaJSON), &args)
		out["arguments"] = args
	}
	return out
}

func (m *Manager) CallTool(ctx context.Context, namespaced string, arguments map[string]any, servers []AccessibleServer) (any, error) {
	slug, tool, ok := ParseNamespacedTool(namespaced)
	if !ok {
		return nil, ErrUnknownProxyTarget
	}
	srv, err := m.findServerBySlug(ctx, slug, servers)
	if err != nil {
		return nil, err
	}
	if !allow(findScope(servers, srv.ID).ToolAllowlist, namespaced) {
		return nil, errors.New("forbidden")
	}
	client, err := m.getClient(ctx, srv.ID)
	if err != nil {
		return nil, err
	}
	callTimeout := time.Duration(m.cfg.CallTimeoutSeconds) * time.Second
	if callTimeout <= 0 {
		callTimeout = 120 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return client.callTool(cctx, tool, arguments)
}

func (m *Manager) ReadResource(ctx context.Context, uri string, servers []AccessibleServer) (any, error) {
	slug, upstream, ok := ParseNamespacedResourceURI(uri)
	if !ok {
		return nil, ErrUnknownProxyTarget
	}
	srv, err := m.findServerBySlug(ctx, slug, servers)
	if err != nil {
		return nil, err
	}
	if !allow(findScope(servers, srv.ID).ResourceAllowlist, uri) {
		return nil, errors.New("forbidden")
	}
	client, err := m.getClient(ctx, srv.ID)
	if err != nil {
		return nil, err
	}
	return client.readResource(ctx, upstream)
}

func (m *Manager) GetPrompt(ctx context.Context, namespaced string, arguments map[string]any, servers []AccessibleServer) (any, error) {
	slug, prompt, ok := ParseNamespacedPrompt(namespaced)
	if !ok {
		return nil, ErrUnknownProxyTarget
	}
	srv, err := m.findServerBySlug(ctx, slug, servers)
	if err != nil {
		return nil, err
	}
	if !allow(findScope(servers, srv.ID).PromptAllowlist, namespaced) {
		return nil, errors.New("forbidden")
	}
	client, err := m.getClient(ctx, srv.ID)
	if err != nil {
		return nil, err
	}
	return client.getPrompt(ctx, prompt, arguments)
}

func (m *Manager) findServerBySlug(ctx context.Context, slug string, servers []AccessibleServer) (models.MCPServer, error) {
	for _, item := range servers {
		if item.Server.Slug == slug {
			return item.Server, nil
		}
	}
	return m.store.GetServerBySlug(ctx, slug)
}

func findScope(servers []AccessibleServer, serverID string) models.AccessTokenMCPServer {
	for _, item := range servers {
		if item.Server.ID == serverID {
			return item.Scope
		}
	}
	return models.AccessTokenMCPServer{}
}

func (m *Manager) HasProxiedTool(name string) bool {
	_, _, ok := ParseNamespacedTool(name)
	return ok
}

func (m *Manager) HasProxiedPrompt(name string) bool {
	_, _, ok := ParseNamespacedPrompt(name)
	return ok
}

func (m *Manager) HasProxiedResource(uri string) bool {
	return stringsHasPrefix(uri, resourcePrefix)
}

func stringsHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func (m *Manager) CapabilityFlags(exposed ExposedCapabilities) map[string]any {
	caps := map[string]any{}
	if len(exposed.Tools) > 0 {
		caps["tools"] = map[string]any{}
	}
	if len(exposed.Resources) > 0 {
		caps["resources"] = map[string]any{}
	}
	if len(exposed.Prompts) > 0 {
		caps["prompts"] = map[string]any{}
	}
	return caps
}

func (m *Manager) ValidateScopeNames(ctx context.Context, serverID string, tools, resources, prompts []string) error {
	caps, err := m.store.GetCapabilities(ctx, serverID)
	if err != nil {
		return err
	}
	srv := caps.Server
	for _, name := range tools {
		if !stringsHasPrefix(name, srv.Slug+"__") {
			return fmt.Errorf("invalid tool %q for server %s", name, srv.Slug)
		}
	}
	for _, uri := range resources {
		if !stringsHasPrefix(uri, resourcePrefix+srv.Slug+"/") {
			return fmt.Errorf("invalid resource %q for server %s", uri, srv.Slug)
		}
	}
	for _, name := range prompts {
		if !stringsHasPrefix(name, srv.Slug+"__") {
			return fmt.Errorf("invalid prompt %q for server %s", name, srv.Slug)
		}
	}
	return nil
}
