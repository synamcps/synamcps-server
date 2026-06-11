package access

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"

	"github.com/synamcps/synamcps-server/internal/models"
)

type MCPScopeLoader interface {
	TokenMCPServers(ctx context.Context, tokenID string) ([]models.AccessTokenMCPServer, error)
}

type Service struct {
	store    *Store
	mcpStore MCPScopeLoader
}

func NewService(store *Store) *Service {
	return &Service{store: store}
}

func (s *Service) AttachMCPStore(loader MCPScopeLoader) {
	s.mcpStore = loader
}

func (s *Service) Store() *Store { return s.store }

func (s *Service) EnsurePrincipal(ctx context.Context, p models.Principal, defaultBucket string) (models.User, models.Storage, error) {
	user, err := s.store.UpsertUserFromPrincipal(ctx, p)
	if err != nil {
		return models.User{}, models.Storage{}, err
	}
	for _, groupKey := range models.GroupSubjectKeysForPrincipal(p) {
		parts := strings.Split(groupKey, ":")
		name := groupKey
		if len(parts) > 0 {
			name = parts[len(parts)-1]
		}
		_, _ = s.store.CreateGroup(ctx, models.Group{
			ID:              subjectIDForService(groupKey),
			SubjectKey:      groupKey,
			Source:          p.AuthSource,
			Issuer:          p.Issuer,
			ExternalGroupID: name,
			Name:            name,
			ManagedBy:       "external",
			SyncStatus:      "claims",
		})
	}
	storage, err := s.store.EnsurePersonalStorage(ctx, p, defaultBucket)
	if err != nil {
		return models.User{}, models.Storage{}, err
	}
	return user, storage, nil
}

func (s *Service) ResolveBearer(ctx context.Context, raw string) (models.APIAccessContext, bool, error) {
	token, scopes, found, err := s.store.ResolveToken(ctx, raw)
	if err != nil || !found {
		return models.APIAccessContext{}, found, err
	}
	p := models.Principal{
		UserID:     token.OwnerSubjectKey,
		SubjectKey: token.OwnerSubjectKey,
		AuthSource: "access_token",
		Scopes:     []string{"mcp.token"},
	}
	accessCtx := models.APIAccessContext{
		Principal:      p,
		AuthMode:       "access_token",
		TokenID:        token.ID,
		AccessToken:    &token,
		AllowedStorage: scopes,
	}
	if s.mcpStore != nil {
		mcpScopes, err := s.mcpStore.TokenMCPServers(ctx, token.ID)
		if err != nil {
			return models.APIAccessContext{}, true, err
		}
		accessCtx.AllowedMCPServers = mcpScopes
	}
	return accessCtx, true, nil
}

func (s *Service) CanAccessStorage(ctx context.Context, p models.Principal, token *models.AccessToken, tokenScopes []models.AccessTokenStorage, storageID string, permission models.StoragePermission) (models.EffectiveAccess, bool, error) {
	if storageID == "" {
		return models.EffectiveAccess{}, false, errors.New("storage id is required")
	}
	acl, err := s.store.ACLForSubject(ctx, SubjectKeys(p))
	if err != nil {
		return models.EffectiveAccess{}, false, err
	}
	eff, ok := evaluateStorageAccess(p, token, tokenScopes, acl, storageID, permission)
	return eff, ok, nil
}

// evaluateStorageAccess computes the caller's effective access to a single
// storage from a pre-loaded ACL set (no DB access), so callers that need many
// storages can load the ACL once and avoid N+1 queries.
func evaluateStorageAccess(p models.Principal, token *models.AccessToken, tokenScopes []models.AccessTokenStorage, acl []models.ACLBinding, storageID string, permission models.StoragePermission) (models.EffectiveAccess, bool) {
	userPerms := map[models.StoragePermission]struct{}{}
	for _, b := range acl {
		if b.StorageID != storageID {
			continue
		}
		for _, perm := range RolePermissions(b.Role) {
			userPerms[perm] = struct{}{}
		}
	}

	mode := models.AccessModeReadWrite
	if token != nil {
		mode = models.AccessModeNone
		for _, scope := range tokenScopes {
			if scope.StorageID == storageID {
				mode = intersectMode(token.Mode, scope.MaxMode)
				break
			}
		}
	} else {
		if len(userPerms) == 0 && hasScope(p.Scopes, "platform_admin") {
			userPerms[permission] = struct{}{}
		}
	}

	effective := map[models.StoragePermission]struct{}{}
	for perm := range userPerms {
		if token != nil && !slices.Contains(ModePermissions(mode), perm) {
			continue
		}
		effective[perm] = struct{}{}
	}
	if token != nil && len(token.AllowedPermissions) > 0 {
		allowed := map[models.StoragePermission]struct{}{}
		for _, perm := range token.AllowedPermissions {
			allowed[perm] = struct{}{}
		}
		for perm := range effective {
			if _, ok := allowed[perm]; !ok {
				delete(effective, perm)
			}
		}
	}
	out := models.EffectiveAccess{
		StorageID:  storageID,
		SubjectKey: models.SubjectKeyForPrincipal(p),
		Mode:       mode,
	}
	if token != nil {
		out.TokenID = token.ID
	}
	for perm := range effective {
		out.Permissions = append(out.Permissions, perm)
	}
	_, ok := effective[permission]
	return out, ok
}

func (s *Service) AvailableStorages(ctx context.Context, p models.Principal, token *models.AccessToken, tokenScopes []models.AccessTokenStorage) ([]models.Storage, map[string]models.EffectiveAccess, error) {
	all, err := s.store.ListStorages(ctx)
	if err != nil {
		return nil, nil, err
	}
	acl, err := s.store.ACLForSubject(ctx, SubjectKeys(p))
	if err != nil {
		return nil, nil, err
	}
	out := []models.Storage{}
	accessByStorage := map[string]models.EffectiveAccess{}
	for _, st := range all {
		eff, ok := evaluateStorageAccess(p, token, tokenScopes, acl, st.ID, models.PermissionStorageRead)
		if ok {
			out = append(out, st)
			accessByStorage[st.ID] = eff
		}
	}
	return out, accessByStorage, nil
}

// ReadableStorageIDs returns the set of storage IDs the caller can read
// documents from, computed with a single ACL load (no per-storage query).
func (s *Service) ReadableStorageIDs(ctx context.Context, p models.Principal, token *models.AccessToken, tokenScopes []models.AccessTokenStorage) (map[string]struct{}, error) {
	all, err := s.store.ListStorages(ctx)
	if err != nil {
		return nil, err
	}
	acl, err := s.store.ACLForSubject(ctx, SubjectKeys(p))
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(all))
	for _, st := range all {
		if _, ok := evaluateStorageAccess(p, token, tokenScopes, acl, st.ID, models.PermissionDocumentRead); ok {
			out[st.ID] = struct{}{}
		}
	}
	return out, nil
}

func intersectMode(a, b models.AccessMode) models.AccessMode {
	if a == models.AccessModeNone || b == models.AccessModeNone {
		return models.AccessModeNone
	}
	if a == models.AccessModeRead || b == models.AccessModeRead {
		return models.AccessModeRead
	}
	return models.AccessModeReadWrite
}

func hasScope(scopes []string, scope string) bool {
	for _, s := range scopes {
		if s == scope {
			return true
		}
	}
	return false
}

func subjectIDForService(subject string) string {
	sum := sha256.Sum256([]byte(subject))
	return hex.EncodeToString(sum[:])[:24]
}
