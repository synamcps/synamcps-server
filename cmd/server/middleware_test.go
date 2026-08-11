package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/synamcps/synamcps-server/internal/config"
)

func TestMCPRouteAllowsClaudeOrigin(t *testing.T) {
	h := withMiddlewares(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), config.Config{})

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Origin", "https://claude.ai")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://claude.ai" {
		t.Fatalf("ACAO=%q", got)
	}
}

func TestAPIRouteStillRejectsUnknownOrigin(t *testing.T) {
	h := withMiddlewares(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), config.Config{})

	req := httptest.NewRequest(http.MethodGet, "/api/capabilities", nil)
	req.Header.Set("Origin", "https://claude.ai")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
