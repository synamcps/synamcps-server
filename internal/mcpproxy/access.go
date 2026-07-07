package mcpproxy

import (
	"context"
	"slices"

	"github.com/synamcps/synamcps-server/internal/access"
	"github.com/synamcps/synamcps-server/internal/models"
)

func MCPServerRolePermissions(role models.MCPServerRole) []models.MCPServerPermission {
	switch role {
	case models.RoleMCPServerOwner, models.RoleMCPServerAdmin:
		return []models.MCPServerPermission{
			models.PermissionMCPServerUse,
			models.PermissionMCPServerManage,
			models.PermissionMCPServerDelete,
		}
	case models.RoleMCPServerUser:
		return []models.MCPServerPermission{models.PermissionMCPServerUse}
	default:
		return nil
	}
}

type AccessService struct {
	store *Store
}

func NewAccessService(store *Store) *AccessService {
	return &AccessService{store: store}
}

func (s *AccessService) CanAccessMCPServer(ctx context.Context, p models.Principal, token *models.AccessToken, tokenScopes []models.AccessTokenMCPServer, serverID string, permission models.MCPServerPermission) (bool, error) {
	if s == nil || s.store == nil {
		return false, nil
	}
	acl, err := s.store.ACLForSubject(ctx, access.SubjectKeys(p))
	if err != nil {
		return false, err
	}
	return evaluateMCPServerAccess(p, token, tokenScopes, acl, serverID, permission), nil
}

// evaluateMCPServerAccess computes MCP server access from a pre-loaded ACL set.
func evaluateMCPServerAccess(p models.Principal, token *models.AccessToken, tokenScopes []models.AccessTokenMCPServer, acl []models.MCPServerACLBinding, serverID string, permission models.MCPServerPermission) bool {
	if token != nil {
		allowed := false
		for _, scope := range tokenScopes {
			if scope.ServerID == serverID {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	perms := map[models.MCPServerPermission]struct{}{}
	for _, b := range acl {
		if b.ServerID != serverID {
			continue
		}
		for _, perm := range MCPServerRolePermissions(b.Role) {
			perms[perm] = struct{}{}
		}
	}
	if len(perms) == 0 && hasPlatformAdmin(p) {
		perms[permission] = struct{}{}
	}
	_, ok := perms[permission]
	return ok
}

func (s *AccessService) AvailableMCPServers(ctx context.Context, p models.Principal, token *models.AccessToken, tokenScopes []models.AccessTokenMCPServer) ([]AccessibleServer, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	acl, err := s.store.ACLForSubject(ctx, access.SubjectKeys(p))
	if err != nil {
		return nil, err
	}

	if token != nil {
		if len(tokenScopes) == 0 {
			return nil, nil
		}
		serversByID := map[string]models.MCPServer{}
		all, err := s.store.ListServers(ctx)
		if err != nil {
			return nil, err
		}
		for _, srv := range all {
			serversByID[srv.ID] = srv
		}
		out := make([]AccessibleServer, 0, len(tokenScopes))
		for _, scope := range tokenScopes {
			srv, ok := serversByID[scope.ServerID]
			if !ok {
				continue
			}
			if !evaluateMCPServerAccess(p, token, tokenScopes, acl, srv.ID, models.PermissionMCPServerUse) {
				continue
			}
			out = append(out, AccessibleServer{Server: srv, Scope: scope})
		}
		return out, nil
	}

	all, err := s.store.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AccessibleServer, 0, len(all))
	for _, srv := range all {
		if !evaluateMCPServerAccess(p, nil, nil, acl, srv.ID, models.PermissionMCPServerUse) {
			continue
		}
		out = append(out, AccessibleServer{
			Server: srv,
			Scope:  models.AccessTokenMCPServer{ServerID: srv.ID},
		})
	}
	return out, nil
}

func hasPlatformAdmin(p models.Principal) bool {
	return slices.Contains(p.Scopes, "platform_admin")
}
