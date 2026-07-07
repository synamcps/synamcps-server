package mcpproxy

import (
	"context"
	"testing"

	"github.com/synamcps/synamcps-server/internal/config"
	"github.com/synamcps/synamcps-server/internal/domainerr"
	"github.com/synamcps/synamcps-server/internal/models"
)

func TestFindServerBySlugNoFallback(t *testing.T) {
	ctx := context.Background()
	store := testMCPStore(t)
	mgr := NewManager(config.MCPProxyConfig{Enabled: true}, store)
	seedMCPServer(t, store, "hidden", "secret")

	_, err := mgr.findServerBySlug(ctx, "secret", nil)
	if err != domainerr.ErrForbidden {
		t.Fatalf("expected forbidden, got %v", err)
	}
}

func TestCallToolWithoutACLForbidden(t *testing.T) {
	ctx := context.Background()
	store := testMCPStore(t)
	mgr := NewManager(config.MCPProxyConfig{Enabled: true}, store)
	seedMCPServer(t, store, "hidden", "secret")

	_, err := mgr.CallTool(ctx, "secret__do_thing", nil, nil)
	if err != domainerr.ErrForbidden {
		t.Fatalf("expected forbidden, got %v", err)
	}
}

func TestFindScope(t *testing.T) {
	scope := models.AccessTokenMCPServer{ServerID: "s1", ToolAllowlist: []string{"a__b"}}
	servers := []AccessibleServer{{
		Server: models.MCPServer{ID: "s1", Slug: "a"},
		Scope:  scope,
	}}
	got, ok := findScope(servers, "s1")
	if !ok || got.ServerID != "s1" {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
	_, ok = findScope(servers, "missing")
	if ok {
		t.Fatal("expected missing scope")
	}
}

func TestAllowEmptyAllowlistPermitsAll(t *testing.T) {
	if !allow(nil, "anything") {
		t.Fatal("empty allowlist should permit")
	}
	if allow([]string{"x"}, "y") {
		t.Fatal("non-matching allowlist should deny")
	}
}
