package mcp

import (
	"context"

	"github.com/synamcps/synamcps-server/internal/access"
	"github.com/synamcps/synamcps-server/internal/domainerr"
	"github.com/synamcps/synamcps-server/internal/mcpdesc"
	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/strutil"
)

type adminRights struct {
	platformAdmin bool
	storageAdmin  bool
	tokenStorage  map[string]struct{}
	aclStorage    map[string]struct{}
}

func (s *Server) buildAdminTools(ctx context.Context, p models.Principal, accessCtx models.APIAccessContext) ([]map[string]any, error) {
	rights, err := s.adminRights(ctx, p, accessCtx)
	if err != nil || !rights.any() {
		return nil, err
	}
	tools := []map[string]any{
		adminTool("admin_token_create", "Create an access token. The raw token secret is returned once; store it immediately.", map[string]any{
			"name":       stringProp("Token name"),
			"mode":       enumProp("read", "read_write"),
			"storageIds": arrayProp(map[string]any{"type": "string"}),
			"rateLimit":  objectProp(),
		}),
		adminTool("admin_token_list", "List access tokens visible to the caller", map[string]any{"storageId": stringProp("Optional storage filter")}),
		adminTool("admin_token_get", "Get an access token with storage and MCP scopes", map[string]any{"tokenId": stringProp("Token ID")}, "tokenId"),
		adminTool("admin_token_revoke", "Revoke an access token", map[string]any{"tokenId": stringProp("Token ID")}, "tokenId"),
		adminTool("admin_token_update_scopes", "Replace storage and MCP scopes for an access token", map[string]any{
			"tokenId":       stringProp("Token ID"),
			"storageIds":    arrayProp(map[string]any{"type": "string"}),
			"storageScopes": arrayProp(objectProp()),
			"mcpServers":    arrayProp(objectProp()),
		}, "tokenId"),
		adminTool("admin_token_update_rate_limit", "Update an access token rate limit policy", map[string]any{"tokenId": stringProp("Token ID"), "rateLimit": objectProp()}, "tokenId", "rateLimit"),
	}
	if rights.platformAdmin {
		tools = append(tools,
			adminTool("admin_user_list", "List users", nil),
			adminTool("admin_user_get", "Get user by id", map[string]any{"userId": stringProp("User ID")}, "userId"),
			adminTool("admin_user_disable", "Disable user by id", map[string]any{"userId": stringProp("User ID")}, "userId"),
			adminTool("admin_group_list", "List groups", nil),
			adminTool("admin_group_members", "List group members", map[string]any{"groupId": stringProp("Group ID")}, "groupId"),
			adminTool("admin_group_add_member", "Add a user to a group", map[string]any{"groupId": stringProp("Group ID"), "userId": stringProp("User ID")}, "groupId", "userId"),
			adminTool("admin_group_remove_member", "Remove a user from a group", map[string]any{"groupId": stringProp("Group ID"), "userId": stringProp("User ID")}, "groupId", "userId"),
			adminTool("admin_storage_create", "Create a storage", map[string]any{"slug": stringProp("Slug"), "name": stringProp("Name"), "s3Bucket": stringProp("S3 bucket"), "s3Prefix": stringProp("S3 prefix")}, "slug"),
			adminTool("admin_mcp_server_list", "List MCP proxy servers", nil),
			adminTool("admin_mcp_server_test", "Run MCP proxy server connection test", map[string]any{"serverId": stringProp("MCP server ID")}, "serverId"),
			adminTool("admin_mcp_scope_set", "Replace MCP server scopes on a token", map[string]any{"tokenId": stringProp("Token ID"), "mcpServers": arrayProp(objectProp())}, "tokenId"),
		)
	}
	if rights.platformAdmin || rights.storageAdmin {
		tools = append(tools,
			adminTool("admin_acl_list", "List storage ACL bindings", map[string]any{"storageId": stringProp("Storage ID")}, "storageId"),
			adminTool("admin_acl_grant", "Grant storage ACL role", map[string]any{"storageId": stringProp("Storage ID"), "subjectKey": stringProp("Subject key"), "role": enumProp("storage_owner", "storage_admin", "storage_writer", "storage_reader")}, "storageId", "subjectKey", "role"),
			adminTool("admin_acl_revoke", "Revoke storage ACL role", map[string]any{"storageId": stringProp("Storage ID"), "subjectKey": stringProp("Subject key"), "role": enumProp("storage_owner", "storage_admin", "storage_writer", "storage_reader")}, "storageId", "subjectKey", "role"),
			adminTool("admin_storage_archive", "Archive a storage", map[string]any{"storageId": stringProp("Storage ID")}, "storageId"),
		)
	}
	return tools, nil
}

func (r adminRights) any() bool {
	return r.platformAdmin || r.storageAdmin || r.tokenStorage != nil
}

func (s *Server) adminRights(ctx context.Context, p models.Principal, accessCtx models.APIAccessContext) (adminRights, error) {
	if s.access == nil || accessCtx.AccessToken != nil {
		return adminRights{}, nil
	}
	rights := adminRights{
		platformAdmin: strutil.Contains(p.Scopes, "platform_admin") || strutil.Contains(p.Scopes, "admin"),
		tokenStorage:  map[string]struct{}{},
		aclStorage:    map[string]struct{}{},
	}
	storages, effective, err := s.access.AvailableStorages(ctx, p, nil, nil)
	if err != nil {
		return adminRights{}, err
	}
	for _, st := range storages {
		for _, perm := range effective[st.ID].Permissions {
			switch perm {
			case models.PermissionTokenManage:
				rights.storageAdmin = true
				rights.tokenStorage[st.ID] = struct{}{}
			case models.PermissionACLManage, models.PermissionStorageDelete:
				rights.storageAdmin = true
				rights.aclStorage[st.ID] = struct{}{}
			}
		}
	}
	return rights, nil
}

func (s *Server) handleAdminTool(ctx context.Context, p models.Principal, accessCtx models.APIAccessContext, method string, params map[string]any) (any, error) {
	rights, err := s.adminRights(ctx, p, accessCtx)
	if err != nil {
		return nil, err
	}
	if !rights.any() {
		return nil, domainerr.ErrForbidden
	}
	switch method {
	case "admin_token_create":
		return s.adminTokenCreate(ctx, p, rights, params)
	case "admin_token_list":
		return s.adminTokenList(ctx, p, rights, params)
	case "admin_token_get":
		return s.adminTokenGet(ctx, p, rights, params)
	case "admin_token_revoke":
		tokenID := strutil.AsString(params["tokenId"])
		if err := s.requireTokenAccess(ctx, p, rights, tokenID); err != nil {
			return nil, err
		}
		if err := s.access.Store().RevokeToken(ctx, tokenID); err != nil {
			return nil, err
		}
		_ = s.audit(ctx, p, method, "access_token", tokenID, "")
		return map[string]any{"status": "revoked", "tokenId": tokenID}, nil
	case "admin_token_update_scopes":
		return s.adminTokenUpdateScopes(ctx, p, rights, params)
	case "admin_token_update_rate_limit":
		tokenID := strutil.AsString(params["tokenId"])
		if err := s.requireTokenAccess(ctx, p, rights, tokenID); err != nil {
			return nil, err
		}
		if err := s.access.Store().UpdateTokenRateLimit(ctx, tokenID, rateLimitFrom(params["rateLimit"])); err != nil {
			return nil, err
		}
		_ = s.audit(ctx, p, method, "access_token", tokenID, "")
		return map[string]any{"status": "updated", "tokenId": tokenID}, nil
	case "admin_user_list":
		if !rights.platformAdmin {
			return nil, domainerr.ErrForbidden
		}
		return s.access.Store().ListUsers(ctx)
	case "admin_user_get":
		if !rights.platformAdmin {
			return nil, domainerr.ErrForbidden
		}
		user, ok, err := s.access.Store().GetUser(ctx, strutil.AsString(params["userId"]))
		if err != nil || !ok {
			return nil, notFoundIfMissing(err, ok)
		}
		return user, nil
	case "admin_user_disable":
		if !rights.platformAdmin {
			return nil, domainerr.ErrForbidden
		}
		userID := strutil.AsString(params["userId"])
		user, err := s.access.Store().UpdateUser(ctx, userID, models.User{Status: "disabled"})
		if err != nil {
			return nil, err
		}
		_ = s.audit(ctx, p, method, "user", userID, "")
		return user, nil
	case "admin_group_list":
		if !rights.platformAdmin {
			return nil, domainerr.ErrForbidden
		}
		return s.access.Store().ListGroups(ctx)
	case "admin_group_members":
		if !rights.platformAdmin {
			return nil, domainerr.ErrForbidden
		}
		return s.access.Store().ListGroupMembers(ctx, strutil.AsString(params["groupId"]))
	case "admin_group_add_member":
		if !rights.platformAdmin {
			return nil, domainerr.ErrForbidden
		}
		groupID, userID := strutil.AsString(params["groupId"]), strutil.AsString(params["userId"])
		if err := s.access.Store().AddGroupMember(ctx, groupID, userID, "mcp"); err != nil {
			return nil, err
		}
		_ = s.audit(ctx, p, method, "group", groupID, "")
		return map[string]any{"status": "added", "groupId": groupID, "userId": userID}, nil
	case "admin_group_remove_member":
		if !rights.platformAdmin {
			return nil, domainerr.ErrForbidden
		}
		groupID, userID := strutil.AsString(params["groupId"]), strutil.AsString(params["userId"])
		if err := s.access.Store().RemoveGroupMember(ctx, groupID, userID); err != nil {
			return nil, err
		}
		_ = s.audit(ctx, p, method, "group", groupID, "")
		return map[string]any{"status": "removed", "groupId": groupID, "userId": userID}, nil
	case "admin_acl_list":
		storageID := strutil.AsString(params["storageId"])
		if err := s.requireStorageRight(rights, storageID, models.PermissionACLManage); err != nil {
			return nil, err
		}
		return s.access.Store().ACLForStorage(ctx, storageID)
	case "admin_acl_grant":
		return s.adminACLGrant(ctx, p, rights, params)
	case "admin_acl_revoke":
		return s.adminACLRevoke(ctx, p, rights, params)
	case "admin_storage_create":
		if !rights.platformAdmin {
			return nil, domainerr.ErrForbidden
		}
		return s.adminStorageCreate(ctx, p, params)
	case "admin_storage_archive":
		storageID := strutil.AsString(params["storageId"])
		if err := s.requireStorageRight(rights, storageID, models.PermissionStorageDelete); err != nil {
			return nil, err
		}
		st, err := s.access.Store().ArchiveStorage(ctx, storageID)
		if err != nil {
			return nil, err
		}
		_ = s.audit(ctx, p, method, "storage", storageID, storageID)
		return st, nil
	case "admin_mcp_server_list":
		if !rights.platformAdmin || s.proxy == nil || s.proxy.Store() == nil {
			return nil, domainerr.ErrForbidden
		}
		return s.proxy.Store().ListServers(ctx)
	case "admin_mcp_server_test":
		if !rights.platformAdmin || s.proxy == nil {
			return nil, domainerr.ErrForbidden
		}
		serverID := strutil.AsString(params["serverId"])
		out, err := s.proxy.ConnectTest(ctx, serverID)
		if err == nil {
			_ = s.audit(ctx, p, method, "mcp_server", serverID, "")
		}
		return out, err
	case "admin_mcp_scope_set":
		tokenID := strutil.AsString(params["tokenId"])
		if err := s.requireTokenAccess(ctx, p, rights, tokenID); err != nil {
			return nil, err
		}
		if s.proxy == nil || s.proxy.Store() == nil {
			return nil, domainerr.ErrForbidden
		}
		scopes := mcpScopesFrom(params["mcpServers"])
		if err := s.proxy.Store().ReplaceTokenMCPServers(ctx, tokenID, scopes); err != nil {
			return nil, err
		}
		_ = s.audit(ctx, p, method, "access_token", tokenID, "")
		return map[string]any{"status": "updated", "tokenId": tokenID}, nil
	default:
		return nil, domainerr.ErrUnknownMethod
	}
}

func (s *Server) adminTokenCreate(ctx context.Context, p models.Principal, rights adminRights, params map[string]any) (any, error) {
	owner := strutil.AsString(params["ownerSubjectKey"])
	if owner == "" {
		owner = models.SubjectKeyForPrincipal(p)
	}
	if owner != models.SubjectKeyForPrincipal(p) && !rights.platformAdmin {
		return nil, domainerr.ErrForbidden
	}
	mode := models.AccessMode(strutil.AsString(params["mode"]))
	if mode == "" {
		mode = models.AccessModeRead
	}
	scopes := storageScopesFrom(params, mode)
	for _, scope := range scopes {
		if err := s.requireStorageRight(rights, scope.StorageID, models.PermissionTokenManage); err != nil && !rights.platformAdmin {
			return nil, err
		}
	}
	token, raw, err := s.access.Store().CreateToken(ctx, access.CreateTokenInput{
		OwnerSubjectKey: owner,
		Name:            strutil.AsString(params["name"]),
		Mode:            mode,
		StorageScopes:   scopes,
		RateLimit:       rateLimitFrom(params["rateLimit"]),
		CreatedBy:       models.SubjectKeyForPrincipal(p),
	})
	if err != nil {
		return nil, err
	}
	if s.proxy != nil && s.proxy.Store() != nil {
		_ = s.proxy.Store().ReplaceTokenMCPServers(ctx, token.ID, mcpScopesFrom(params["mcpServers"]))
	}
	_ = s.audit(ctx, p, "admin_token_create", "access_token", token.ID, "")
	return map[string]any{"token": token, "rawToken": raw}, nil
}

func (s *Server) adminTokenList(ctx context.Context, p models.Principal, rights adminRights, params map[string]any) (any, error) {
	storageID := strutil.AsString(params["storageId"])
	if storageID != "" {
		if err := s.requireStorageRight(rights, storageID, models.PermissionTokenManage); err != nil {
			return nil, err
		}
		return s.access.Store().TokenAccessByStorage(ctx, storageID)
	}
	tokens, err := s.access.Store().ListTokens(ctx)
	if err != nil || rights.platformAdmin {
		return tokens, err
	}
	subject := models.SubjectKeyForPrincipal(p)
	out := make([]models.AccessToken, 0, len(tokens))
	for _, token := range tokens {
		if token.OwnerSubjectKey == subject {
			out = append(out, token)
		}
	}
	return out, nil
}

func (s *Server) adminTokenGet(ctx context.Context, p models.Principal, rights adminRights, params map[string]any) (any, error) {
	tokenID := strutil.AsString(params["tokenId"])
	if err := s.requireTokenAccess(ctx, p, rights, tokenID); err != nil {
		return nil, err
	}
	token, ok, err := s.access.Store().GetToken(ctx, tokenID)
	if err != nil || !ok {
		return nil, notFoundIfMissing(err, ok)
	}
	scopes, _ := s.access.Store().TokenStorages(ctx, tokenID)
	var mcpScopes []models.AccessTokenMCPServer
	if s.proxy != nil && s.proxy.Store() != nil {
		mcpScopes, _ = s.proxy.Store().TokenMCPServers(ctx, tokenID)
	}
	return map[string]any{"token": token, "storageScopes": scopes, "mcpServers": mcpScopes}, nil
}

func (s *Server) adminTokenUpdateScopes(ctx context.Context, p models.Principal, rights adminRights, params map[string]any) (any, error) {
	tokenID := strutil.AsString(params["tokenId"])
	if err := s.requireTokenAccess(ctx, p, rights, tokenID); err != nil {
		return nil, err
	}
	token, ok, err := s.access.Store().GetToken(ctx, tokenID)
	if err != nil || !ok {
		return nil, notFoundIfMissing(err, ok)
	}
	scopes := storageScopesFrom(params, token.Mode)
	for _, scope := range scopes {
		if err := s.requireStorageRight(rights, scope.StorageID, models.PermissionTokenManage); err != nil && !rights.platformAdmin {
			return nil, err
		}
	}
	if err := s.access.Store().ReplaceTokenStorages(ctx, tokenID, scopes); err != nil {
		return nil, err
	}
	if s.proxy != nil && s.proxy.Store() != nil {
		if err := s.proxy.Store().ReplaceTokenMCPServers(ctx, tokenID, mcpScopesFrom(params["mcpServers"])); err != nil {
			return nil, err
		}
	}
	_ = s.audit(ctx, p, "admin_token_update_scopes", "access_token", tokenID, "")
	return map[string]any{"status": "updated", "tokenId": tokenID}, nil
}

func (s *Server) adminACLGrant(ctx context.Context, p models.Principal, rights adminRights, params map[string]any) (any, error) {
	storageID := strutil.AsString(params["storageId"])
	if err := s.requireStorageRight(rights, storageID, models.PermissionACLManage); err != nil {
		return nil, err
	}
	b, err := s.access.Store().UpsertACL(ctx, models.ACLBinding{
		StorageID:  storageID,
		SubjectKey: strutil.AsString(params["subjectKey"]),
		Role:       models.StorageRole(strutil.AsString(params["role"])),
		GrantedBy:  models.SubjectKeyForPrincipal(p),
	})
	if err == nil {
		_ = s.audit(ctx, p, "admin_acl_grant", "storage_acl", b.ID, storageID)
	}
	return b, err
}

func (s *Server) adminACLRevoke(ctx context.Context, p models.Principal, rights adminRights, params map[string]any) (any, error) {
	storageID := strutil.AsString(params["storageId"])
	if err := s.requireStorageRight(rights, storageID, models.PermissionACLManage); err != nil {
		return nil, err
	}
	subjectKey := strutil.AsString(params["subjectKey"])
	role := models.StorageRole(strutil.AsString(params["role"]))
	if err := s.access.Store().DeleteACL(ctx, storageID, subjectKey, role); err != nil {
		return nil, err
	}
	_ = s.audit(ctx, p, "admin_acl_revoke", "storage_acl", subjectKey+":"+string(role), storageID)
	return map[string]any{"status": "revoked", "storageId": storageID, "subjectKey": subjectKey, "role": role}, nil
}

func (s *Server) adminStorageCreate(ctx context.Context, p models.Principal, params map[string]any) (any, error) {
	subject := models.SubjectKeyForPrincipal(p)
	st, err := s.access.Store().CreateStorage(ctx, models.Storage{
		Slug:            strutil.AsString(params["slug"]),
		Name:            strutil.AsString(params["name"]),
		OwnerSubjectKey: valueOr(strutil.AsString(params["ownerSubjectKey"]), subject),
		Visibility:      models.Visibility(valueOr(strutil.AsString(params["visibility"]), string(models.VisibilityPersonal))),
		DefaultAccess:   models.AccessMode(valueOr(strutil.AsString(params["defaultAccess"]), string(models.AccessModeNone))),
		Kind:            models.StorageKind(valueOr(strutil.AsString(params["kind"]), string(models.StorageKindKnowledge))),
		S3Bucket:        strutil.AsString(params["s3Bucket"]),
		S3Prefix:        strutil.AsString(params["s3Prefix"]),
	}, subject)
	if err == nil {
		_ = s.audit(ctx, p, "admin_storage_create", "storage", st.ID, st.ID)
	}
	return st, err
}

func (s *Server) requireTokenAccess(ctx context.Context, p models.Principal, rights adminRights, tokenID string) error {
	if rights.platformAdmin {
		return nil
	}
	token, ok, err := s.access.Store().GetToken(ctx, tokenID)
	if err != nil {
		return err
	}
	if !ok {
		return domainerr.ErrNotFound
	}
	if token.OwnerSubjectKey == models.SubjectKeyForPrincipal(p) {
		return nil
	}
	scopes, _ := s.access.Store().TokenStorages(ctx, tokenID)
	for _, scope := range scopes {
		if _, ok := rights.tokenStorage[scope.StorageID]; ok {
			return nil
		}
	}
	return domainerr.ErrForbidden
}

func (s *Server) requireStorageRight(rights adminRights, storageID string, permission models.StoragePermission) error {
	if rights.platformAdmin {
		return nil
	}
	var set map[string]struct{}
	switch permission {
	case models.PermissionACLManage, models.PermissionStorageDelete:
		set = rights.aclStorage
	default:
		set = rights.tokenStorage
	}
	if _, ok := set[storageID]; ok {
		return nil
	}
	return domainerr.ErrForbidden
}

func (s *Server) audit(ctx context.Context, p models.Principal, action, resourceType, resourceID, storageID string) error {
	if s.access == nil {
		return nil
	}
	return s.access.Store().RecordAudit(ctx, models.AuditEvent{
		ActorSubjectKey: models.SubjectKeyForPrincipal(p),
		Action:          "mcp." + action,
		ResourceType:    resourceType,
		ResourceID:      resourceID,
		StorageID:       storageID,
		Metadata:        map[string]any{"channel": "mcp"},
	})
}

func adminTool(name, description string, props map[string]any, required ...string) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	return mcpdesc.Tool(name, description, map[string]any{
		"type":       "object",
		"properties": props,
		"required":   required,
	})
}

func stringProp(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func enumProp(values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}

func arrayProp(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}

func objectProp() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": true}
}

func storageScopesFrom(params map[string]any, defaultMode models.AccessMode) []models.AccessTokenStorage {
	if raw, ok := params["storageScopes"].([]any); ok {
		out := make([]models.AccessTokenStorage, 0, len(raw))
		for _, item := range raw {
			m, _ := item.(map[string]any)
			if m == nil {
				continue
			}
			mode := models.AccessMode(strutil.AsString(m["maxMode"]))
			if mode == "" {
				mode = defaultMode
			}
			out = append(out, models.AccessTokenStorage{
				StorageID:     strutil.AsString(m["storageId"]),
				MaxMode:       mode,
				ToolAllowlist: strutil.AsStringSlice(m["toolAllowlist"]),
			})
		}
		return out
	}
	ids := strutil.AsStringSlice(params["storageIds"])
	out := make([]models.AccessTokenStorage, 0, len(ids))
	for _, id := range ids {
		out = append(out, models.AccessTokenStorage{StorageID: id, MaxMode: defaultMode})
	}
	return out
}

func mcpScopesFrom(v any) []models.AccessTokenMCPServer {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]models.AccessTokenMCPServer, 0, len(raw))
	for _, item := range raw {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		out = append(out, models.AccessTokenMCPServer{
			ServerID:          strutil.AsString(m["serverId"]),
			ToolAllowlist:     strutil.AsStringSlice(m["toolAllowlist"]),
			ResourceAllowlist: strutil.AsStringSlice(m["resourceAllowlist"]),
			PromptAllowlist:   strutil.AsStringSlice(m["promptAllowlist"]),
		})
	}
	return out
}

func rateLimitFrom(v any) models.RateLimitPolicy {
	m, _ := v.(map[string]any)
	if m == nil {
		return models.RateLimitPolicy{}
	}
	return models.RateLimitPolicy{
		Enabled:           boolFrom(m["enabled"]),
		RequestsPerMinute: intFrom(m["requestsPerMinute"]),
		RequestsPerHour:   intFrom(m["requestsPerHour"]),
		RequestsPerDay:    intFrom(m["requestsPerDay"]),
		Burst:             intFrom(m["burst"]),
	}
}

func boolFrom(v any) bool {
	b, _ := v.(bool)
	return b
}

func intFrom(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	default:
		return 0
	}
}

func valueOr(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func notFoundIfMissing(err error, ok bool) error {
	if err != nil {
		return err
	}
	if !ok {
		return domainerr.ErrNotFound
	}
	return nil
}
