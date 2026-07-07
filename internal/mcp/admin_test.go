package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/synamcps/synamcps-server/internal/access"
	"github.com/synamcps/synamcps-server/internal/auth"
	"github.com/synamcps/synamcps-server/internal/models"
)

func newAdminTestServer(t *testing.T) (*Server, *access.Store) {
	t.Helper()
	store := access.NewInMemoryStore()
	svc := access.NewService(store)
	return NewServer(ServerDeps{Access: svc}), store
}

func TestAdminToolVisibility(t *testing.T) {
	ctx := context.Background()
	srv, _ := newAdminTestServer(t)

	admin := models.Principal{UserID: "admin", SubjectKey: "user:oauth:admin", Scopes: []string{"platform_admin"}}
	ordinary := models.Principal{UserID: "alice", SubjectKey: "user:oauth:alice"}

	adminTools := listToolNames(t, srv, ctx, admin)
	if !adminTools["admin_user_disable"] || !adminTools["admin_token_create"] {
		t.Fatalf("admin tools missing: %+v", adminTools)
	}

	userTools := listToolNames(t, srv, ctx, ordinary)
	if !userTools["admin_token_create"] {
		t.Fatalf("ordinary user should see own token tools: %+v", userTools)
	}
	if userTools["admin_user_disable"] {
		t.Fatalf("ordinary user should not see platform admin tools: %+v", userTools)
	}

	tokenCtx := context.WithValue(ctx, auth.AccessContextKey, models.APIAccessContext{
		AccessToken: &models.AccessToken{ID: "tok"},
	})
	tokenTools := listToolNames(t, srv, tokenCtx, admin)
	if tokenTools["admin_token_create"] || tokenTools["admin_user_disable"] {
		t.Fatalf("access-token auth should not expose admin tools: %+v", tokenTools)
	}
}

func TestAdminToolRejectsAccessTokenAuth(t *testing.T) {
	srv, _ := newAdminTestServer(t)
	ctx := context.WithValue(context.Background(), auth.AccessContextKey, models.APIAccessContext{
		AccessToken: &models.AccessToken{ID: "tok"},
	})
	_, err := srv.HandleRequest(ctx, models.Principal{UserID: "admin", Scopes: []string{"platform_admin"}}, JSONRPCRequest{
		JSONRPC: jsonrpcVersion,
		ID:      json.RawMessage(`1`),
		Method:  "admin_token_list",
	})
	if err == nil {
		t.Fatal("expected forbidden for admin tool with access-token auth")
	}
}

func TestAdminTokenLifecycleAndAudit(t *testing.T) {
	ctx := context.Background()
	srv, store := newAdminTestServer(t)
	admin := models.Principal{UserID: "admin", SubjectKey: "user:oauth:admin", Scopes: []string{"platform_admin"}}
	storage, err := store.CreateStorage(ctx, models.Storage{
		ID:              "st-admin",
		Slug:            "admin",
		Name:            "Admin",
		OwnerSubjectKey: admin.SubjectKey,
		Visibility:      models.VisibilityPersonal,
	}, admin.SubjectKey)
	if err != nil {
		t.Fatalf("CreateStorage: %v", err)
	}

	createResp, err := srv.HandleRequest(ctx, admin, JSONRPCRequest{
		JSONRPC: jsonrpcVersion,
		ID:      json.RawMessage(`1`),
		Method:  "admin_token_create",
		Params: map[string]any{
			"name":       "Claude token",
			"mode":       string(models.AccessModeReadWrite),
			"storageIds": []any{storage.ID},
		},
	})
	if err != nil {
		t.Fatalf("admin_token_create: %v", err)
	}
	result := createResp.Result.(map[string]any)
	token := result["token"].(models.AccessToken)
	if result["rawToken"].(string) == "" {
		t.Fatal("raw token secret must be returned once")
	}

	_, err = srv.HandleRequest(ctx, admin, JSONRPCRequest{
		JSONRPC: jsonrpcVersion,
		ID:      json.RawMessage(`2`),
		Method:  "admin_token_update_scopes",
		Params: map[string]any{
			"tokenId": token.ID,
			"storageScopes": []any{map[string]any{
				"storageId": storage.ID,
				"maxMode":   string(models.AccessModeRead),
			}},
		},
	})
	if err != nil {
		t.Fatalf("admin_token_update_scopes: %v", err)
	}
	scopes, err := store.TokenStorages(ctx, token.ID)
	if err != nil {
		t.Fatalf("TokenStorages: %v", err)
	}
	if len(scopes) != 1 || scopes[0].MaxMode != models.AccessModeRead {
		t.Fatalf("scopes = %+v", scopes)
	}

	_, err = srv.HandleRequest(ctx, admin, JSONRPCRequest{
		JSONRPC: jsonrpcVersion,
		ID:      json.RawMessage(`3`),
		Method:  "admin_token_revoke",
		Params:  map[string]any{"tokenId": token.ID},
	})
	if err != nil {
		t.Fatalf("admin_token_revoke: %v", err)
	}
	updated, ok, err := store.GetToken(ctx, token.ID)
	if err != nil || !ok {
		t.Fatalf("GetToken: ok=%v err=%v", ok, err)
	}
	if updated.RevokedAt == nil {
		t.Fatal("token should be revoked")
	}

	audit, err := store.ListAudit(ctx)
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if len(audit) < 3 {
		t.Fatalf("expected audit events for lifecycle, got %+v", audit)
	}
}

func listToolNames(t *testing.T, srv *Server, ctx context.Context, p models.Principal) map[string]bool {
	t.Helper()
	resp, err := srv.HandleRequest(ctx, p, JSONRPCRequest{
		JSONRPC: jsonrpcVersion,
		ID:      json.RawMessage(`1`),
		Method:  "tools/list",
	})
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	result := resp.Result.(map[string]any)
	rawTools := result["tools"].([]map[string]any)
	out := map[string]bool{}
	for _, tool := range rawTools {
		name, _ := tool["name"].(string)
		out[name] = true
	}
	return out
}
