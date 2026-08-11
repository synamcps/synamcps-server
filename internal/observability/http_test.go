package observability

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMiddlewareLogsMCPResponse(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	h := NewHTTPMetrics().Middleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-MCP-Method", "initialize")
		w.Header().Set("Mcp-Session-Id", "sess-1")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":0,"result":{"protocolVersion":"2025-11-25"}}`))
	}))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	line := buf.String()
	if !strings.Contains(line, `"mcp_method":"initialize"`) {
		t.Fatalf("missing mcp_method: %s", line)
	}
	if !strings.Contains(line, `"mcp_session_id":"sess-1"`) {
		t.Fatalf("missing mcp_session_id: %s", line)
	}
	if !strings.Contains(line, `"response":`) || !strings.Contains(line, `protocolVersion`) {
		t.Fatalf("missing response body: %s", line)
	}
}

func TestMiddlewareRedactsTokenResponse(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	h := NewHTTPMetrics().Middleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"secret-at","refresh_token":"secret-rt","token_type":"Bearer","expires_in":3600}`))
	}))

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	line := buf.String()
	if strings.Contains(line, "secret-at") || strings.Contains(line, "secret-rt") {
		t.Fatalf("tokens leaked into logs: %s", line)
	}
	if !strings.Contains(line, "[redacted]") {
		t.Fatalf("expected redacted tokens: %s", line)
	}
	if !strings.Contains(line, "Bearer") {
		t.Fatalf("expected non-secret fields kept: %s", line)
	}
}

func TestMiddlewareOmitsHTMLAuthorizeBody(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	h := NewHTTPMetrics().Middleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!DOCTYPE html><html><body>login</body></html>`))
	}))

	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	line := buf.String()
	if !strings.Contains(line, "[html omitted]") {
		t.Fatalf("expected html omitted: %s", line)
	}
	if strings.Contains(line, "<body>login") {
		t.Fatalf("html body leaked: %s", line)
	}
}

func TestMiddlewareRedactsLocationCode(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	h := NewHTTPMetrics().Middleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://claude.ai/callback?code=secret-code&state=abc", http.StatusSeeOther)
	}))

	req := httptest.NewRequest(http.MethodPost, "/oauth/authorize/login", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	line := buf.String()
	if strings.Contains(line, "secret-code") {
		t.Fatalf("auth code leaked: %s", line)
	}
	if !strings.Contains(line, "code=%5Bredacted%5D") && !strings.Contains(line, "code=[redacted]") {
		t.Fatalf("expected redacted location code: %s", line)
	}
}

func TestShouldCaptureResponse(t *testing.T) {
	cases := map[string]bool{
		"/mcp":                              true,
		"/oauth/token":                      true,
		"/.well-known/openid-configuration": true,
		"/register":                         true,
		"/api/capabilities":                 false,
		"/healthz":                          false,
	}
	for path, want := range cases {
		if got := shouldCaptureResponse(path); got != want {
			t.Fatalf("%s: got %v want %v", path, got, want)
		}
	}
}

func TestRedactJSONSecrets(t *testing.T) {
	got := redactJSONSecrets(`{"access_token":"x","nested":{"refresh_token":"y"},"ok":true,"error":{"code":-32600}}`)
	if strings.Contains(got, `"x"`) || strings.Contains(got, `"y"`) {
		t.Fatalf("not redacted: %s", got)
	}
	if !strings.Contains(got, `"ok":true`) {
		t.Fatalf("lost fields: %s", got)
	}
	if !strings.Contains(got, `"code":-32600`) {
		t.Fatalf("json-rpc error code should not be redacted: %s", got)
	}
}
