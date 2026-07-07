package mcpproxy

import (
	"context"
	"testing"
	"time"

	"github.com/synamcps/synamcps-server/internal/models"
)

func testMCPStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(context.Background(), "", nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

func seedMCPServer(t *testing.T, store *Store, id, slug string) models.MCPServer {
	t.Helper()
	ctx := context.Background()
	srv, err := store.CreateServer(ctx, CreateServerInput{
		Slug:            slug,
		Name:            slug,
		OwnerSubjectKey: "user:oauth:owner",
		Transport:       models.MCPTransportAuto,
		URL:             "http://example/mcp",
		AuthType:        models.MCPAuthTypeBearer,
	})
	if err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	// CreateServer generates its own id; re-seed with fixed id for tests.
	store.servers[id] = models.MCPServer{
		ID:              id,
		Slug:            slug,
		Name:            slug,
		OwnerSubjectKey: "user:oauth:owner",
		Transport:       models.MCPTransportAuto,
		URL:             "http://example/mcp",
		AuthType:        models.MCPAuthTypeBearer,
		Status:          models.MCPServerStatusActive,
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}
	delete(store.servers, srv.ID)
	return store.servers[id]
}

func TestCanAccessMCPServer(t *testing.T) {
	ctx := context.Background()
	store := testMCPStore(t)
	svc := NewAccessService(store)

	srvAllowed := seedMCPServer(t, store, "srv-1", "github")
	srvDenied := seedMCPServer(t, store, "srv-2", "gitlab")

	subject := "user:oauth:alice"
	p := models.Principal{UserID: "alice", SubjectKey: subject, AuthSource: "oauth"}
	if _, err := store.UpsertACL(ctx, models.MCPServerACLBinding{
		ServerID:   srvAllowed.ID,
		SubjectKey: subject,
		Role:       models.RoleMCPServerUser,
	}); err != nil {
		t.Fatalf("UpsertACL: %v", err)
	}

	token := &models.AccessToken{ID: "tok-1", Mode: models.AccessModeReadWrite}
	tokenScopes := []models.AccessTokenMCPServer{{TokenID: "tok-1", ServerID: srvAllowed.ID}}

	tests := []struct {
		name       string
		p          models.Principal
		token      *models.AccessToken
		scopes     []models.AccessTokenMCPServer
		serverID   string
		permission models.MCPServerPermission
		want       bool
	}{
		{
			name:       "acl user can use allowed server",
			p:          p,
			serverID:   srvAllowed.ID,
			permission: models.PermissionMCPServerUse,
			want:       true,
		},
		{
			name:       "no acl denied",
			p:          p,
			serverID:   srvDenied.ID,
			permission: models.PermissionMCPServerUse,
			want:       false,
		},
		{
			name:       "platform_admin without acl",
			p:          models.Principal{UserID: "admin", SubjectKey: "user:oauth:admin", Scopes: []string{"platform_admin"}},
			serverID:   srvDenied.ID,
			permission: models.PermissionMCPServerUse,
			want:       true,
		},
		{
			name:       "token without server scope denied",
			p:          p,
			token:      token,
			scopes:     tokenScopes,
			serverID:   srvDenied.ID,
			permission: models.PermissionMCPServerUse,
			want:       false,
		},
		{
			name:       "token with server scope allowed",
			p:          p,
			token:      token,
			scopes:     tokenScopes,
			serverID:   srvAllowed.ID,
			permission: models.PermissionMCPServerUse,
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.CanAccessMCPServer(ctx, tt.p, tt.token, tt.scopes, tt.serverID, tt.permission)
			if err != nil {
				t.Fatalf("CanAccessMCPServer: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAvailableMCPServers(t *testing.T) {
	ctx := context.Background()
	store := testMCPStore(t)
	svc := NewAccessService(store)

	srv1 := seedMCPServer(t, store, "srv-a", "alpha")
	srv2 := seedMCPServer(t, store, "srv-b", "beta")
	subject := "user:oauth:bob"
	p := models.Principal{UserID: "bob", SubjectKey: subject, AuthSource: "oauth"}
	if _, err := store.UpsertACL(ctx, models.MCPServerACLBinding{
		ServerID: srv1.ID, SubjectKey: subject, Role: models.RoleMCPServerUser,
	}); err != nil {
		t.Fatalf("UpsertACL: %v", err)
	}
	if _, err := store.UpsertACL(ctx, models.MCPServerACLBinding{
		ServerID: srv2.ID, SubjectKey: subject, Role: models.RoleMCPServerUser,
	}); err != nil {
		t.Fatalf("UpsertACL: %v", err)
	}

	t.Run("without token returns all acl servers", func(t *testing.T) {
		got, err := svc.AvailableMCPServers(ctx, p, nil, nil)
		if err != nil {
			t.Fatalf("AvailableMCPServers: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}
	})

	t.Run("token scopes filter servers", func(t *testing.T) {
		token := &models.AccessToken{ID: "tok-2"}
		scopes := []models.AccessTokenMCPServer{{TokenID: "tok-2", ServerID: srv1.ID, ToolAllowlist: []string{"alpha__search"}}}
		got, err := svc.AvailableMCPServers(ctx, p, token, scopes)
		if err != nil {
			t.Fatalf("AvailableMCPServers: %v", err)
		}
		if len(got) != 1 || got[0].Server.ID != srv1.ID {
			t.Fatalf("got %+v", got)
		}
		if len(got[0].Scope.ToolAllowlist) != 1 {
			t.Fatalf("scope not propagated: %+v", got[0].Scope)
		}
	})
}
