package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/synamcps/synamcps-server/internal/access"
	"github.com/synamcps/synamcps-server/internal/auth"
	"github.com/synamcps/synamcps-server/internal/knowledge"
	"github.com/synamcps/synamcps-server/internal/mcpproxy"
	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/session"
	"github.com/synamcps/synamcps-server/internal/usage"
)

type Server struct {
	sessions  *session.Store
	knowledge *knowledge.Service
	access    *access.Service
	usage     *usage.Service
	proxy     *mcpproxy.Manager
	mcpAccess *mcpproxy.AccessService
}

func NewServer(sessions *session.Store, knowledgeService *knowledge.Service) *Server {
	return &Server{sessions: sessions, knowledge: knowledgeService}
}

func (s *Server) AttachAccess(accessService *access.Service) {
	s.access = accessService
}

func (s *Server) AttachUsage(usageService *usage.Service) {
	s.usage = usageService
}

func (s *Server) AttachProxy(proxy *mcpproxy.Manager, mcpAccess *mcpproxy.AccessService) {
	s.proxy = proxy
	s.mcpAccess = mcpAccess
}

func (s *Server) HandleInitialize(w http.ResponseWriter, p models.Principal) {
	sess := s.sessions.CreateMCPSession(p, 12*time.Hour)
	w.Header().Set("Mcp-Session-Id", sess.SessionID)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      "init",
		"result":  s.initializeResult(context.Background(), p, models.APIAccessContext{}, "2024-11-05", sess.SessionID),
	})
}

func (s *Server) HandleJSONRPC(ctx context.Context, p models.Principal, request map[string]any) (map[string]any, error) {
	start := time.Now()
	method, _ := request["method"].(string)
	params, _ := request["params"].(map[string]any)
	if params == nil {
		params = map[string]any{}
	}
	id := request["id"]
	storageID := asString(params["storageId"])
	accessCtx, _ := auth.AccessContextFromContext(ctx)
	status := "ok"
	defer func() {
		if s.usage != nil {
			s.usage.Record(ctx, models.UsageEvent{
				TokenID:        accessCtx.TokenID,
				UserSubjectKey: models.SubjectKeyForPrincipal(p),
				StorageID:      storageID,
				Tool:           method,
				Operation:      operationForMethod(method),
				Status:         status,
				LatencyMS:      time.Since(start).Milliseconds(),
			})
		}
	}()

	if accessCtx.AccessToken != nil && s.usage != nil {
		ok, err := s.usage.Allow(ctx, *accessCtx.AccessToken, storageID)
		if err != nil {
			status = "error"
			return nil, err
		}
		if !ok {
			status = "rate_limited"
			return nil, errors.New("rate limit exceeded")
		}
	}

	switch method {
	case "initialize":
		sess := s.sessions.CreateMCPSession(p, 12*time.Hour)
		protocolVersion := asString(params["protocolVersion"])
		if protocolVersion == "" {
			protocolVersion = "2024-11-05"
		}
		return map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"result":  s.initializeResult(ctx, p, accessCtx, protocolVersion, sess.SessionID),
		}, nil
	case "notifications/initialized":
		return map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}}, nil
	case "tools/list":
		result, err := s.handleToolsList(ctx, p, accessCtx)
		if err != nil {
			status = "error"
			return nil, err
		}
		return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}, nil
	case "tools/call":
		originalName := asString(params["name"])
		arguments, _ := params["arguments"].(map[string]any)
		if arguments == nil {
			arguments = map[string]any{}
		}
		if s.proxy != nil && s.proxy.Enabled() && s.proxy.HasProxiedTool(originalName) {
			servers, err := s.accessibleMCPServers(ctx, p, accessCtx)
			if err != nil {
				status = statusFromError(err)
				return nil, err
			}
			result, err := s.proxy.CallTool(ctx, originalName, arguments, servers)
			if err != nil {
				status = statusFromError(err)
				return nil, err
			}
			return toolCallResponse(id, result), nil
		}
		name := methodForToolName(originalName)
		callReq := map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"method":  name,
			"params":  arguments,
		}
		resp, err := s.HandleJSONRPC(ctx, p, callReq)
		if err != nil {
			status = statusFromError(err)
			return nil, err
		}
		return toolCallResponse(id, resp["result"]), nil
	case "resources/list":
		result, err := s.handleResourcesList(ctx, p, accessCtx)
		if err != nil {
			status = "error"
			return nil, err
		}
		return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}, nil
	case "resources/read":
		uri := asString(params["uri"])
		servers, err := s.accessibleMCPServers(ctx, p, accessCtx)
		if err != nil {
			status = statusFromError(err)
			return nil, err
		}
		if s.proxy == nil || !s.proxy.Enabled() {
			status = "error"
			return nil, errors.New("unknown method")
		}
		result, err := s.proxy.ReadResource(ctx, uri, servers)
		if err != nil {
			status = statusFromError(err)
			return nil, err
		}
		return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}, nil
	case "prompts/list":
		result, err := s.handlePromptsList(ctx, p, accessCtx)
		if err != nil {
			status = "error"
			return nil, err
		}
		return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}, nil
	case "prompts/get":
		name := asString(params["name"])
		arguments, _ := params["arguments"].(map[string]any)
		servers, err := s.accessibleMCPServers(ctx, p, accessCtx)
		if err != nil {
			status = statusFromError(err)
			return nil, err
		}
		if s.proxy == nil || !s.proxy.Enabled() {
			status = "error"
			return nil, errors.New("unknown method")
		}
		result, err := s.proxy.GetPrompt(ctx, name, arguments, servers)
		if err != nil {
			status = statusFromError(err)
			return nil, err
		}
		return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}, nil
	case "knowledge.save":
		in := knowledge.SaveInput{
			StorageID:  storageID,
			Title:      asString(params["title"]),
			Text:       asString(params["text"]),
			MimeType:   asString(params["mimeType"]),
			Visibility: models.Visibility(asString(params["visibility"])),
			GroupIDs:   asStringSlice(params["groupIds"]),
			Source:     asString(params["source"]),
			SourceURL:  asString(params["sourceUrl"]),
			Channel:    "mcp",
		}
		doc, err := s.knowledge.Save(ctx, p, in)
		if err != nil {
			status = statusFromError(err)
			return nil, err
		}
		return map[string]any{"jsonrpc": "2.0", "id": id, "result": doc}, nil
	case "knowledge.get":
		doc, err := s.knowledge.Get(ctx, p, asString(params["docId"]))
		if err != nil {
			status = statusFromError(err)
			return nil, err
		}
		storageID = doc.StorageID
		return map[string]any{"jsonrpc": "2.0", "id": id, "result": doc}, nil
	case "knowledge.delete":
		if err := s.knowledge.Delete(ctx, p, asString(params["docId"])); err != nil {
			status = statusFromError(err)
			return nil, err
		}
		return map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]string{"status": "deleted"}}, nil
	case "knowledge.search":
		req := models.SearchRequest{
			Query: asString(params["query"]),
			TopK:  asInt(params["topK"]),
			Filters: models.PageRequest{
				StorageID:     storageID,
				Source:        asString(params["source"]),
				SourceURL:     asString(params["sourceUrl"]),
				SourceURLMode: asString(params["sourceUrlMode"]),
			},
		}
		hits, err := s.knowledge.Search(ctx, p, req, true)
		if err != nil {
			status = statusFromError(err)
			return nil, err
		}
		return map[string]any{"jsonrpc": "2.0", "id": id, "result": hits}, nil
	default:
		status = "error"
		return nil, errors.New("unknown method")
	}
}

func (s *Server) accessibleMCPServers(ctx context.Context, p models.Principal, accessCtx models.APIAccessContext) ([]mcpproxy.AccessibleServer, error) {
	if s.mcpAccess == nil {
		return nil, nil
	}
	return s.mcpAccess.AvailableMCPServers(ctx, p, accessCtx.AccessToken, accessCtx.AllowedMCPServers)
}

func (s *Server) exposedProxy(ctx context.Context, p models.Principal, accessCtx models.APIAccessContext) (mcpproxy.ExposedCapabilities, error) {
	if s.proxy == nil || !s.proxy.Enabled() {
		return mcpproxy.ExposedCapabilities{}, nil
	}
	servers, err := s.accessibleMCPServers(ctx, p, accessCtx)
	if err != nil {
		return mcpproxy.ExposedCapabilities{}, err
	}
	return s.proxy.ExposedForAccess(ctx, servers)
}

func toolCallResponse(id any, result any) map[string]any {
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		raw, _ = json.Marshal(result)
	}
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result": map[string]any{
			"content": []map[string]any{
				{
					"type": "text",
					"text": string(raw),
				},
			},
		},
	}
}

func (s *Server) initializeResult(ctx context.Context, p models.Principal, accessCtx models.APIAccessContext, protocolVersion, sessionID string) map[string]any {
	caps := map[string]any{
		"tools": map[string]any{},
	}
	exposed, _ := s.exposedProxy(ctx, p, accessCtx)
	if s.proxy != nil {
		for k, v := range s.proxy.CapabilityFlags(exposed) {
			caps[k] = v
		}
	}
	knowledgeTools, _, _ := s.buildKnowledgeTools(ctx, p, accessCtx)
	if len(knowledgeTools) > 0 {
		caps["tools"] = map[string]any{}
	}
	return map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    caps,
		"serverInfo": map[string]any{
			"name":    "syna-knowledge-mcp",
			"version": "0.2.0",
		},
		"sessionId": sessionID,
	}
}

func (s *Server) handleToolsList(ctx context.Context, p models.Principal, accessCtx models.APIAccessContext) (map[string]any, error) {
	tools, storages, err := s.buildKnowledgeTools(ctx, p, accessCtx)
	if err != nil {
		return nil, err
	}
	exposed, err := s.exposedProxy(ctx, p, accessCtx)
	if err != nil {
		return nil, err
	}
	tools = append(tools, exposed.Tools...)
	result := map[string]any{"tools": tools}
	if len(storages) > 0 {
		result["storages"] = storages
	}
	return result, nil
}

func (s *Server) handleResourcesList(ctx context.Context, p models.Principal, accessCtx models.APIAccessContext) (map[string]any, error) {
	exposed, err := s.exposedProxy(ctx, p, accessCtx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"resources": exposed.Resources}, nil
}

func (s *Server) handlePromptsList(ctx context.Context, p models.Principal, accessCtx models.APIAccessContext) (map[string]any, error) {
	exposed, err := s.exposedProxy(ctx, p, accessCtx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"prompts": exposed.Prompts}, nil
}

func (s *Server) buildKnowledgeTools(ctx context.Context, p models.Principal, accessCtx models.APIAccessContext) ([]map[string]any, []models.Storage, error) {
	if s.access == nil {
		return defaultTools(nil, true), nil, nil
	}
	storages, effective, err := s.access.AvailableStorages(ctx, p, accessCtx.AccessToken, accessCtx.AllowedStorage)
	if err != nil {
		return nil, nil, err
	}
	storageEnums := make([]string, 0, len(storages))
	writeAllowed := false
	for _, st := range storages {
		storageEnums = append(storageEnums, st.ID)
		for _, perm := range effective[st.ID].Permissions {
			if perm == models.PermissionDocumentCreate {
				writeAllowed = true
			}
		}
	}
	tools := defaultTools(storageEnums, writeAllowed)
	return filterKnowledgeTools(tools, accessCtx.AllowedStorage), storages, nil
}

func filterKnowledgeTools(tools []map[string]any, scopes []models.AccessTokenStorage) []map[string]any {
	if len(scopes) == 0 {
		return tools
	}
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		if knowledgeToolAllowed(name, scopes) {
			out = append(out, tool)
		}
	}
	return out
}

func knowledgeToolAllowed(name string, scopes []models.AccessTokenStorage) bool {
	for _, scope := range scopes {
		if len(scope.ToolAllowlist) == 0 {
			return true
		}
		for _, allowed := range scope.ToolAllowlist {
			if allowed == name {
				return true
			}
		}
	}
	return false
}

func defaultTools(storageEnums []string, writeAllowed bool) []map[string]any {
	storageProperty := map[string]any{"type": "string", "description": "Storage ID"}
	if len(storageEnums) > 0 {
		storageProperty["enum"] = storageEnums
	}
	tools := []map[string]any{
		toolDescriptor("knowledge_search", "Search knowledge in an allowed storage", storageProperty, map[string]any{"query": map[string]any{"type": "string"}, "topK": map[string]any{"type": "integer"}}),
		toolDescriptor("knowledge_get", "Get a document by id", storageProperty, map[string]any{"docId": map[string]any{"type": "string"}}),
	}
	if writeAllowed {
		tools = append(tools,
			toolDescriptor("knowledge_save", "Save knowledge into an allowed storage", storageProperty, map[string]any{"title": map[string]any{"type": "string"}, "text": map[string]any{"type": "string"}, "mimeType": map[string]any{"type": "string"}}),
			toolDescriptor("knowledge_delete", "Delete a document from an allowed storage", storageProperty, map[string]any{"docId": map[string]any{"type": "string"}}),
		)
	}
	return tools
}

func methodForToolName(name string) string {
	switch name {
	case "knowledge_search":
		return "knowledge.search"
	case "knowledge_get":
		return "knowledge.get"
	case "knowledge_save":
		return "knowledge.save"
	case "knowledge_delete":
		return "knowledge.delete"
	default:
		return name
	}
}

func toolDescriptor(name, description string, storageProperty map[string]any, extra map[string]any) map[string]any {
	props := map[string]any{"storageId": storageProperty}
	required := []string{"storageId"}
	for k, v := range extra {
		props[k] = v
		required = append(required, k)
	}
	return map[string]any{
		"name":        name,
		"description": description,
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": props,
			"required":   required,
		},
	}
}

func operationForMethod(method string) string {
	switch method {
	case "knowledge.save":
		return "write"
	case "knowledge.delete":
		return "delete"
	case "tools/list", "resources/list", "prompts/list":
		return "tools_list"
	case "initialize":
		return "initialize"
	default:
		return "read"
	}
}

func statusFromError(err error) string {
	if err == nil {
		return "ok"
	}
	if errors.Is(err, context.Canceled) {
		return "error"
	}
	msg := err.Error()
	if msg == "forbidden" {
		return "forbidden"
	}
	return "error"
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asStringSlice(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func asInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	default:
		return 0
	}
}
