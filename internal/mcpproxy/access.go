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
	subjectKeys := access.SubjectKeys(p)
	acl, err := s.store.ACLForSubject(ctx, subjectKeys)
	if err != nil {
		return false, err
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
	if token != nil {
		allowed := false
		for _, scope := range tokenScopes {
			if scope.ServerID == serverID {
				allowed = true
				break
			}
		}
		if !allowed {
			return false, nil
		}
	}
	_, ok := perms[permission]
	return ok, nil
}

func (s *AccessService) AvailableMCPServers(ctx context.Context, p models.Principal, token *models.AccessToken, tokenScopes []models.AccessTokenMCPServer) ([]AccessibleServer, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	all, err := s.store.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	var out []AccessibleServer
	for _, srv := range all {
		ok, err := s.CanAccessMCPServer(ctx, p, token, tokenScopes, srv.ID, models.PermissionMCPServerUse)
		if err != nil || !ok {
			continue
		}
		scope := models.AccessTokenMCPServer{ServerID: srv.ID}
		if token != nil {
			found := false
			for _, sc := range tokenScopes {
				if sc.ServerID == srv.ID {
					scope = sc
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		out = append(out, AccessibleServer{Server: srv, Scope: scope})
	}
	return out, nil
}

func hasPlatformAdmin(p models.Principal) bool {
	return slices.Contains(p.Scopes, "platform_admin")
}
