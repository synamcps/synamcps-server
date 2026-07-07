package e2e

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/synamcps/synamcps-server/internal/access"
	"github.com/synamcps/synamcps-server/internal/config"
	"github.com/synamcps/synamcps-server/internal/session"
	"github.com/synamcps/synamcps-server/internal/web"
)

func TestMCPConnectPageAvailable(t *testing.T) {
	cfg := config.Config{
		Transport: config.TransportConfig{LegacySSE: true},
		OAuth: config.OAuthConfig{Providers: []config.ProviderConfig{
			{Name: "keycloak"},
			{Name: "google"},
		}},
		Teleport: config.TeleportConfig{Enabled: true},
	}
	sessions := session.NewStore(config.RedisConfig{TTLHours: 1})
	accessSvc := access.NewService(access.NewInMemoryStore())
	handler := web.NewHandler(cfg, sessions, accessSvc)

	srv := httptest.NewServer(handler)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/app/mcp-connect")
	if err != nil {
		t.Fatalf("http get: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "MCP Connection Guide") {
		t.Fatalf("missing guide content")
	}
}
