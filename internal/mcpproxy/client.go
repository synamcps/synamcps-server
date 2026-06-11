package mcpproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/synamcps/synamcps-server/internal/models"
)

type upstreamClient struct {
	httpClient *http.Client
	url        string
	headers    map[string]string

	mu        sync.Mutex // guards sessionID against concurrent calls reusing this client
	sessionID string

	reqID     atomic.Int64
	createdAt time.Time
}

func newUpstreamClient(url string, headers map[string]string, timeout time.Duration) *upstreamClient {
	return &upstreamClient{
		httpClient: &http.Client{Timeout: timeout},
		url:        url,
		headers:    headers,
		createdAt:  time.Now(),
	}
}

func (c *upstreamClient) getSessionID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionID
}

func (c *upstreamClient) setSessionID(id string) {
	if id == "" {
		return
	}
	c.mu.Lock()
	c.sessionID = id
	c.mu.Unlock()
}

func (c *upstreamClient) call(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	id := c.reqID.Add(1)
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		body["params"] = params
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	if sid := c.getSessionID(); sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	res, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	respBody, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("upstream http %d: %s", res.StatusCode, string(respBody))
	}
	c.setSessionID(res.Header.Get("Mcp-Session-Id"))
	var resp map[string]any
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("decode upstream response: %w", err)
	}
	if errObj, ok := resp["error"].(map[string]any); ok {
		msg, _ := errObj["message"].(string)
		if msg == "" {
			msg = "upstream error"
		}
		return nil, fmt.Errorf("%s", msg)
	}
	result, _ := resp["result"].(map[string]any)
	return result, nil
}

func (c *upstreamClient) initialize(ctx context.Context) error {
	result, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "syna-mcp-proxy",
			"version": "0.1.0",
		},
	})
	if err != nil {
		return err
	}
	if sid, _ := result["sessionId"].(string); sid != "" {
		c.setSessionID(sid)
	}
	_, _ = c.call(ctx, "notifications/initialized", nil)
	return nil
}

type discoveryResult struct {
	Tools     []models.MCPServerTool
	Resources []models.MCPServerResource
	Prompts   []models.MCPServerPrompt
}

func (c *upstreamClient) discover(ctx context.Context, serverID string, now time.Time) (discoveryResult, error) {
	var out discoveryResult
	if err := c.initialize(ctx); err != nil {
		return out, err
	}
	if tools, err := c.listTools(ctx, serverID, now); err == nil {
		out.Tools = tools
	}
	if resources, err := c.listResources(ctx, serverID, now); err == nil {
		out.Resources = resources
	}
	if prompts, err := c.listPrompts(ctx, serverID, now); err == nil {
		out.Prompts = prompts
	}
	return out, nil
}

func (c *upstreamClient) listTools(ctx context.Context, serverID string, now time.Time) ([]models.MCPServerTool, error) {
	result, err := c.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	rawTools, _ := result["tools"].([]any)
	out := make([]models.MCPServerTool, 0, len(rawTools))
	for _, item := range rawTools {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		name, _ := m["name"].(string)
		if name == "" {
			continue
		}
		schemaJSON := ""
		if schema, ok := m["inputSchema"]; ok {
			b, _ := json.Marshal(schema)
			schemaJSON = string(b)
		}
		out = append(out, models.MCPServerTool{
			ServerID:        serverID,
			ToolName:        name,
			Description:     asString(m["description"]),
			InputSchemaJSON: schemaJSON,
			Enabled:         false,
			DiscoveredAt:    now,
		})
	}
	return out, nil
}

func (c *upstreamClient) listResources(ctx context.Context, serverID string, now time.Time) ([]models.MCPServerResource, error) {
	result, err := c.call(ctx, "resources/list", nil)
	if err != nil {
		return nil, err
	}
	rawItems, _ := result["resources"].([]any)
	out := make([]models.MCPServerResource, 0, len(rawItems))
	for _, item := range rawItems {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		uri, _ := m["uri"].(string)
		if uri == "" {
			continue
		}
		out = append(out, models.MCPServerResource{
			ServerID:     serverID,
			URI:          uri,
			Name:         asString(m["name"]),
			MimeType:     asString(m["mimeType"]),
			Description:  asString(m["description"]),
			Enabled:      false,
			DiscoveredAt: now,
		})
	}
	return out, nil
}

func (c *upstreamClient) listPrompts(ctx context.Context, serverID string, now time.Time) ([]models.MCPServerPrompt, error) {
	result, err := c.call(ctx, "prompts/list", nil)
	if err != nil {
		return nil, err
	}
	rawItems, _ := result["prompts"].([]any)
	out := make([]models.MCPServerPrompt, 0, len(rawItems))
	for _, item := range rawItems {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		name, _ := m["name"].(string)
		if name == "" {
			continue
		}
		argsJSON := ""
		if args, ok := m["arguments"]; ok {
			b, _ := json.Marshal(args)
			argsJSON = string(b)
		}
		out = append(out, models.MCPServerPrompt{
			ServerID:            serverID,
			PromptName:          name,
			Description:         asString(m["description"]),
			ArgumentsSchemaJSON: argsJSON,
			Enabled:             false,
			DiscoveredAt:        now,
		})
	}
	return out, nil
}

func (c *upstreamClient) callTool(ctx context.Context, name string, arguments map[string]any) (any, error) {
	result, err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": arguments})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (c *upstreamClient) readResource(ctx context.Context, uri string) (any, error) {
	return c.call(ctx, "resources/read", map[string]any{"uri": uri})
}

func (c *upstreamClient) getPrompt(ctx context.Context, name string, arguments map[string]any) (any, error) {
	params := map[string]any{"name": name}
	if arguments != nil {
		params["arguments"] = arguments
	}
	return c.call(ctx, "prompts/get", params)
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
