package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/synamcps/synamcps-server/internal/access"
	"github.com/synamcps/synamcps-server/internal/auth"
	"github.com/synamcps/synamcps-server/internal/domainerr"
	"github.com/synamcps/synamcps-server/internal/knowledge"
	"github.com/synamcps/synamcps-server/internal/mcpdesc"
	"github.com/synamcps/synamcps-server/internal/mcpproxy"
	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/session"
	"github.com/synamcps/synamcps-server/internal/strutil"
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

type ServerDeps struct {
	Sessions  *session.Store
	Knowledge *knowledge.Service
	Access    *access.Service
	Usage     *usage.Service
	Proxy     *mcpproxy.Manager
	MCPAccess *mcpproxy.AccessService
}

func NewServer(deps ServerDeps) *Server {
	return &Server{
		sessions:  deps.Sessions,
		knowledge: deps.Knowledge,
		access:    deps.Access,
		usage:     deps.Usage,
		proxy:     deps.Proxy,
		mcpAccess: deps.MCPAccess,
	}
}

func (s *Server) HandleInitialize(ctx context.Context, w http.ResponseWriter, p models.Principal, id json.RawMessage) {
	sess := s.sessions.CreateMCPSession(p, 12*time.Hour)
	w.Header().Set("Mcp-Session-Id", sess.SessionID)
	w.Header().Set("Content-Type", "application/json")
	accessCtx, _ := auth.AccessContextFromContext(ctx)
	_ = json.NewEncoder(w).Encode(NewResultResponse(id, s.initializeResult(ctx, p, accessCtx, "2024-11-05", sess.SessionID)))
}

func (s *Server) HandleJSONRPC(ctx context.Context, p models.Principal, request map[string]any) (map[string]any, error) {
	id, _ := json.Marshal(request["id"])
	if _, ok := request["id"]; !ok {
		id = nil
	}
	start := time.Now()
	method, _ := request["method"].(string)
	params, _ := request["params"].(map[string]any)
	if params == nil {
		params = map[string]any{}
	}
	resp, err := s.handleRequest(ctx, p, JSONRPCRequest{JSONRPC: jsonrpcVersion, ID: id, Method: method, Params: params}, start)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Server) HandleRequest(ctx context.Context, p models.Principal, request JSONRPCRequest) (*JSONRPCResponse, error) {
	if request.IsNotification() {
		_, err := s.handleRequest(ctx, p, request, time.Now())
		return nil, err
	}
	resp, err := s.handleRequest(ctx, p, request, time.Now())
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (s *Server) handleRequest(ctx context.Context, p models.Principal, request JSONRPCRequest, start time.Time) (JSONRPCResponse, error) {
	method := request.Method
	params := request.Params
	if params == nil {
		params = map[string]any{}
	}
	storageID := strutil.AsString(params["storageId"])
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
			return JSONRPCResponse{}, err
		}
		if !ok {
			status = "rate_limited"
			return JSONRPCResponse{}, domainerr.ErrRateLimited
		}
	}

	result, err := s.dispatch(ctx, p, accessCtx, method, params, request.ID, &status, &storageID)
	if err != nil {
		return JSONRPCResponse{}, err
	}
	return NewResultResponse(request.ID, result), nil
}

func (s *Server) dispatch(ctx context.Context, p models.Principal, accessCtx models.APIAccessContext, method string, params map[string]any, id json.RawMessage, status *string, storageID *string) (any, error) {
	switch method {
	case "initialize":
		sess := s.sessions.CreateMCPSession(p, 12*time.Hour)
		protocolVersion := strutil.AsString(params["protocolVersion"])
		if protocolVersion == "" {
			protocolVersion = "2024-11-05"
		}
		return s.initializeResult(ctx, p, accessCtx, protocolVersion, sess.SessionID), nil
	case "notifications/initialized":
		return map[string]any{}, nil
	case "tools/list":
		result, err := s.handleToolsList(ctx, p, accessCtx)
		if err != nil {
			*status = "error"
			return nil, err
		}
		return result, nil
	case "tools/call":
		originalName := strutil.AsString(params["name"])
		arguments, _ := params["arguments"].(map[string]any)
		if arguments == nil {
			arguments = map[string]any{}
		}
		if s.proxy != nil && s.proxy.Enabled() && s.proxy.HasProxiedTool(originalName) {
			servers, err := s.accessibleMCPServers(ctx, p, accessCtx)
			if err != nil {
				*status = statusFromError(err)
				return nil, err
			}
			result, err := s.proxy.CallTool(ctx, originalName, arguments, servers)
			if err != nil {
				*status = statusFromError(err)
				return nil, err
			}
			return toolCallResult(result), nil
		}
		name := methodForToolName(originalName)
		result, err := s.dispatch(ctx, p, accessCtx, name, arguments, id, status, storageID)
		if err != nil {
			*status = statusFromError(err)
			return nil, err
		}
		return toolCallResult(result), nil
	case "resources/list":
		result, err := s.handleResourcesList(ctx, p, accessCtx)
		if err != nil {
			*status = "error"
			return nil, err
		}
		return result, nil
	case "resources/read":
		uri := strutil.AsString(params["uri"])
		servers, err := s.accessibleMCPServers(ctx, p, accessCtx)
		if err != nil {
			*status = statusFromError(err)
			return nil, err
		}
		if s.proxy == nil || !s.proxy.Enabled() {
			*status = "error"
			return nil, domainerr.ErrUnknownMethod
		}
		result, err := s.proxy.ReadResource(ctx, uri, servers)
		if err != nil {
			*status = statusFromError(err)
			return nil, err
		}
		return result, nil
	case "prompts/list":
		result, err := s.handlePromptsList(ctx, p, accessCtx)
		if err != nil {
			*status = "error"
			return nil, err
		}
		return result, nil
	case "prompts/get":
		name := strutil.AsString(params["name"])
		arguments, _ := params["arguments"].(map[string]any)
		servers, err := s.accessibleMCPServers(ctx, p, accessCtx)
		if err != nil {
			*status = statusFromError(err)
			return nil, err
		}
		if s.proxy == nil || !s.proxy.Enabled() {
			*status = "error"
			return nil, domainerr.ErrUnknownMethod
		}
		result, err := s.proxy.GetPrompt(ctx, name, arguments, servers)
		if err != nil {
			*status = statusFromError(err)
			return nil, err
		}
		return result, nil
	case "knowledge.save":
		in := knowledge.SaveInput{
			StorageID:  *storageID,
			Title:      strutil.AsString(params["title"]),
			Text:       strutil.AsString(params["text"]),
			MimeType:   strutil.AsString(params["mimeType"]),
			Visibility: models.Visibility(strutil.AsString(params["visibility"])),
			GroupIDs:   strutil.AsStringSlice(params["groupIds"]),
			Source:     strutil.AsString(params["source"]),
			SourceURL:  strutil.AsString(params["sourceUrl"]),
			Channel:    "mcp",
		}
		doc, err := s.knowledge.Save(ctx, p, accessCtx, in)
		if err != nil {
			*status = statusFromError(err)
			return nil, err
		}
		return doc, nil
	case "knowledge.get":
		doc, err := s.knowledge.Get(ctx, p, accessCtx, strutil.AsString(params["docId"]))
		if err != nil {
			*status = statusFromError(err)
			return nil, err
		}
		*storageID = doc.StorageID
		return doc, nil
	case "knowledge.delete":
		if err := s.knowledge.Delete(ctx, p, accessCtx, strutil.AsString(params["docId"])); err != nil {
			*status = statusFromError(err)
			return nil, err
		}
		return map[string]string{"status": "deleted"}, nil
	case "knowledge.search":
		req := models.SearchRequest{
			Query: strutil.AsString(params["query"]),
			TopK:  asInt(params["topK"]),
			Filters: models.PageRequest{
				StorageID:     *storageID,
				Source:        strutil.AsString(params["source"]),
				SourceURL:     strutil.AsString(params["sourceUrl"]),
				SourceURLMode: strutil.AsString(params["sourceUrlMode"]),
			},
		}
		hits, err := s.knowledge.Search(ctx, p, accessCtx, req, true)
		if err != nil {
			*status = statusFromError(err)
			return nil, err
		}
		return hits, nil
	case "admin_token_create", "admin_token_list", "admin_token_get", "admin_token_revoke", "admin_token_update_scopes", "admin_token_update_rate_limit",
		"admin_user_list", "admin_user_get", "admin_user_disable", "admin_group_list", "admin_group_members", "admin_group_add_member", "admin_group_remove_member",
		"admin_acl_list", "admin_acl_grant", "admin_acl_revoke", "admin_storage_create", "admin_storage_archive",
		"admin_mcp_server_list", "admin_mcp_server_test", "admin_mcp_scope_set":
		result, err := s.handleAdminTool(ctx, p, accessCtx, method, params)
		if err != nil {
			*status = statusFromError(err)
			return nil, err
		}
		return result, nil
	default:
		*status = "error"
		return nil, domainerr.ErrUnknownMethod
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

func toolCallResult(result any) map[string]any {
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		raw, _ = json.Marshal(result)
	}
	return map[string]any{
		"content": []map[string]any{
			{
				"type": "text",
				"text": string(raw),
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
	adminTools, err := s.buildAdminTools(ctx, p, accessCtx)
	if err != nil {
		return nil, err
	}
	tools = append(tools, adminTools...)
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
		return nil, nil, domainerr.ErrForbidden
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
		knowledgeToolDescriptor("knowledge_search", "Search knowledge in an allowed storage", storageProperty, map[string]any{"query": map[string]any{"type": "string"}, "topK": map[string]any{"type": "integer"}}),
		knowledgeToolDescriptor("knowledge_get", "Get a document by id", storageProperty, map[string]any{"docId": map[string]any{"type": "string"}}),
	}
	if writeAllowed {
		tools = append(tools,
			knowledgeToolDescriptor("knowledge_save", "Save knowledge into an allowed storage", storageProperty, map[string]any{"title": map[string]any{"type": "string"}, "text": map[string]any{"type": "string"}, "mimeType": map[string]any{"type": "string"}}),
			knowledgeToolDescriptor("knowledge_delete", "Delete a document from an allowed storage", storageProperty, map[string]any{"docId": map[string]any{"type": "string"}}),
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

func knowledgeToolDescriptor(name, description string, storageProperty map[string]any, extra map[string]any) map[string]any {
	props := map[string]any{"storageId": storageProperty}
	required := []string{"storageId"}
	for k, v := range extra {
		props[k] = v
		required = append(required, k)
	}
	return mcpdesc.Tool(name, description, map[string]any{
		"type":       "object",
		"properties": props,
		"required":   required,
	})
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
	if errors.Is(err, domainerr.ErrForbidden) {
		return "forbidden"
	}
	return "error"
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
