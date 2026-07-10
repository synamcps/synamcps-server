package e2e

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/synamcps/synamcps-server/internal/access"
	"github.com/synamcps/synamcps-server/internal/config"
	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/session"
	"github.com/synamcps/synamcps-server/internal/web"
)

func TestMCPConnectPageAvailable(t *testing.T) {
	cfg := config.Config{
		Web:       config.WebConfig{UserUI: config.UserUIConfig{Enabled: true}},
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

	adminSession := sessions.CreateWebSession(models.Principal{
		UserID:     "admin",
		SubjectKey: "user:internal:admin",
		Scopes:     []string{"platform_admin"},
		AuthSource: "internal",
	}, time.Hour)
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/admin/mcp-connect", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: "session_id", Value: adminSession.SessionID})
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http get: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "MCP Connection Guide") {
		t.Fatalf("missing guide content")
	}
}

func TestWebRoutesSplitAdminAndUser(t *testing.T) {
	cfg := config.Config{Web: config.WebConfig{UserUI: config.UserUIConfig{Enabled: true}}}
	sessions := session.NewStore(config.RedisConfig{TTLHours: 1})
	accessSvc := access.NewService(access.NewInMemoryStore())
	handler := web.NewHandler(cfg, sessions, accessSvc)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	userSession := sessions.CreateWebSession(models.Principal{
		UserID:     "alice",
		SubjectKey: "user:internal:alice",
		AuthSource: "internal",
	}, time.Hour)

	userClient := &http.Client{}
	adminReq, err := http.NewRequest(http.MethodGet, srv.URL+"/admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	adminReq.AddCookie(&http.Cookie{Name: "session_id", Value: userSession.SessionID})
	adminRes, err := userClient.Do(adminReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = adminRes.Body.Close()
	if adminRes.StatusCode != http.StatusForbidden {
		t.Fatalf("expected user forbidden on /admin, got %d", adminRes.StatusCode)
	}

	appReq, err := http.NewRequest(http.MethodGet, srv.URL+"/app", nil)
	if err != nil {
		t.Fatal(err)
	}
	appReq.AddCookie(&http.Cookie{Name: "session_id", Value: userSession.SessionID})
	appRes, err := userClient.Do(appReq)
	if err != nil {
		t.Fatal(err)
	}
	defer appRes.Body.Close()
	body, _ := io.ReadAll(appRes.Body)
	if appRes.StatusCode != http.StatusOK || !strings.Contains(string(body), "Synamcps Assistant") {
		t.Fatalf("expected user app, status=%d body=%s", appRes.StatusCode, string(body))
	}
}
