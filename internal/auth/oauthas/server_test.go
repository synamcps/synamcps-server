package oauthas

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/synamcps/synamcps-server/internal/access"
	"github.com/synamcps/synamcps-server/internal/auth"
	"github.com/synamcps/synamcps-server/internal/config"
	"github.com/synamcps/synamcps-server/internal/mcp"
	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/session"
	"github.com/synamcps/synamcps-server/internal/transport/streamablehttp"
)

func TestAuthorizationServerMetadataShape(t *testing.T) {
	srv := NewServer(Deps{
		Config: config.OAuthASConfig{Enabled: true, PublicBaseURL: "https://syna.example"},
		Store:  NewInMemoryStore(),
	})
	mux := http.NewServeMux()
	srv.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var meta map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	if meta["issuer"] != "https://syna.example" {
		t.Fatalf("issuer=%v", meta["issuer"])
	}
	if meta["authorization_endpoint"] != "https://syna.example/oauth/authorize" {
		t.Fatalf("authorization_endpoint=%v", meta["authorization_endpoint"])
	}
	if meta["token_endpoint"] != "https://syna.example/oauth/token" {
		t.Fatalf("token_endpoint=%v", meta["token_endpoint"])
	}
	if meta["registration_endpoint"] != "https://syna.example/oauth/register" {
		t.Fatalf("registration_endpoint=%v", meta["registration_endpoint"])
	}
	methods, _ := meta["code_challenge_methods_supported"].([]any)
	if len(methods) != 1 || methods[0] != "S256" {
		t.Fatalf("code_challenge_methods_supported=%v", meta["code_challenge_methods_supported"])
	}
}

func TestDCRAndTokenExchangeFlow(t *testing.T) {
	accessStore := access.NewInMemoryStore()
	accessSvc := access.NewService(accessStore)
	_ = os.Setenv("SYNA_ADMIN_PASSWORD", "admin-secret")
	t.Cleanup(func() { _ = os.Unsetenv("SYNA_ADMIN_PASSWORD") })

	asStore := NewInMemoryStore()
	srv := NewServer(Deps{
		Config: config.OAuthASConfig{
			Enabled:                     true,
			PublicBaseURL:               "http://localhost:8080",
			AuthorizationCodeTTLSeconds: 300,
			AccessTokenTTLHours:         1,
			RefreshTokenTTLHours:        24,
			DefaultTokenMode:            "read_write",
			AllowedRedirectURIPatterns:  []string{"http://localhost*", "https://*"},
		},
		Web: config.WebConfig{
			Admin: config.DefaultAdminConfig{
				Enabled:        true,
				Username:       "admin",
				PasswordEnvRef: "env:SYNA_ADMIN_PASSWORD",
			},
		},
		Store:    asStore,
		Access:   accessSvc,
		S3Bucket: "knowledge",
	})
	mux := http.NewServeMux()
	srv.Register(mux)

	redirectURI := "http://localhost:9999/callback"
	regBody := `{"redirect_uris":["` + redirectURI + `"],"client_name":"claude-desktop","token_endpoint_auth_method":"none"}`
	regReq := httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(regBody))
	regReq.Header.Set("Content-Type", "application/json")
	regRec := httptest.NewRecorder()
	mux.ServeHTTP(regRec, regReq)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("register status=%d body=%s", regRec.Code, regRec.Body.String())
	}
	var reg map[string]any
	if err := json.Unmarshal(regRec.Body.Bytes(), &reg); err != nil {
		t.Fatal(err)
	}
	clientID, _ := reg["client_id"].(string)
	if clientID == "" {
		t.Fatal("missing client_id")
	}

	verifier := "test-code-verifier-0123456789abcdef"
	sum := sha256Sum(verifier)
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	authURL := "/oauth/authorize?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"state":                 {"xyz"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"scope":                 {"mcp"},
	}.Encode()
	authRec := httptest.NewRecorder()
	mux.ServeHTTP(authRec, httptest.NewRequest(http.MethodGet, authURL, nil))
	if authRec.Code != http.StatusOK {
		t.Fatalf("authorize status=%d body=%s", authRec.Code, authRec.Body.String())
	}
	body := authRec.Body.String()
	sessionID := extractHidden(body, "login_session_id")
	if sessionID == "" {
		t.Fatalf("missing login_session_id in %s", body)
	}

	form := url.Values{
		"login_session_id": {sessionID},
		"username":         {"admin"},
		"password":         {"admin-secret"},
	}
	loginReq := httptest.NewRequest(http.MethodPost, "/oauth/authorize/login", strings.NewReader(form.Encode()))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRec := httptest.NewRecorder()
	mux.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusFound {
		t.Fatalf("login status=%d body=%s", loginRec.Code, loginRec.Body.String())
	}
	loc := loginRec.Header().Get("Location")
	redir, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	code := redir.Query().Get("code")
	if code == "" {
		t.Fatalf("missing code in %s", loc)
	}
	if redir.Query().Get("state") != "xyz" {
		t.Fatalf("state mismatch in %s", loc)
	}

	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}
	tokenReq := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(tokenForm.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRec := httptest.NewRecorder()
	mux.ServeHTTP(tokenRec, tokenReq)
	if tokenRec.Code != http.StatusOK {
		t.Fatalf("token status=%d body=%s", tokenRec.Code, tokenRec.Body.String())
	}
	var tokenResp map[string]any
	if err := json.Unmarshal(tokenRec.Body.Bytes(), &tokenResp); err != nil {
		t.Fatal(err)
	}
	accessToken, _ := tokenResp["access_token"].(string)
	refreshToken, _ := tokenResp["refresh_token"].(string)
	if accessToken == "" || refreshToken == "" {
		t.Fatalf("missing tokens: %v", tokenResp)
	}

	// Reused authorization code must fail.
	reuseRec := httptest.NewRecorder()
	mux.ServeHTTP(reuseRec, cloneFormRequest(tokenForm))
	if reuseRec.Code == http.StatusOK {
		t.Fatal("expected reused code to fail")
	}

	// Bad verifier must fail on a fresh code.
	failFlow(t, mux, clientID, redirectURI, "admin", "admin-secret", "wrong-verifier")

	// Refresh rotation.
	refreshForm := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
	}
	refreshReq := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(refreshForm.Encode()))
	refreshReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	refreshRec := httptest.NewRecorder()
	mux.ServeHTTP(refreshRec, refreshReq)
	if refreshRec.Code != http.StatusOK {
		t.Fatalf("refresh status=%d body=%s", refreshRec.Code, refreshRec.Body.String())
	}
	var refreshed map[string]any
	_ = json.Unmarshal(refreshRec.Body.Bytes(), &refreshed)
	if refreshed["access_token"] == "" || refreshed["access_token"] == accessToken {
		t.Fatalf("expected rotated access token, got %v", refreshed)
	}

	// MCP initialize with minted token.
	gateway := auth.NewGateway(auth.GatewayConfig{
		ResourceMetadataURL: "http://localhost:8080/.well-known/oauth-protected-resource",
	})
	gateway.SetOpaqueTokenResolver(accessSvc)
	sessions := session.NewStore(config.RedisConfig{TTLHours: 1})
	mcpServer := mcp.NewServer(mcp.ServerDeps{Sessions: sessions, Access: accessSvc})
	mcpMux := http.NewServeMux()
	streamablehttp.NewHandler(mcpServer, gateway, sessions).Register(mcpMux)

	unauth := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{}}`))
	unauth.Header.Set("Content-Type", "application/json")
	unauth.Header.Set("Accept", "application/json")
	unauthRec := httptest.NewRecorder()
	mcpMux.ServeHTTP(unauthRec, unauth)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth status=%d", unauthRec.Code)
	}
	www := unauthRec.Header().Get("WWW-Authenticate")
	wantMeta := "resource" + `_metadata="http://localhost:8080/.well-known/oauth-protected-resource"`
	if !strings.Contains(www, wantMeta) {
		t.Fatalf("WWW-Authenticate=%q", www)
	}

	initReq := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{}}`))
	initReq.Header.Set("Content-Type", "application/json")
	initReq.Header.Set("Accept", "application/json")
	initReq.Header.Set("Authorization", "Bearer "+refreshed["access_token"].(string))
	initRec := httptest.NewRecorder()
	mcpMux.ServeHTTP(initRec, initReq)
	if initRec.Code != http.StatusOK {
		t.Fatalf("initialize status=%d body=%s", initRec.Code, initRec.Body.String())
	}
}

func TestRedirectURIMismatchAndExpiredCode(t *testing.T) {
	accessStore := access.NewInMemoryStore()
	accessSvc := access.NewService(accessStore)
	asStore := NewInMemoryStore()
	srv := NewServer(Deps{
		Config: config.OAuthASConfig{
			Enabled:                     true,
			PublicBaseURL:               "http://localhost:8080",
			AuthorizationCodeTTLSeconds: 1,
			AccessTokenTTLHours:         1,
			RefreshTokenTTLHours:        24,
			DefaultTokenMode:            "read",
			AllowedRedirectURIPatterns:  []string{"http://localhost*"},
		},
		Store:  asStore,
		Access: accessSvc,
	})
	client, err := asStore.CreateClient(context.Background(), Client{
		ClientName:   "test",
		RedirectURIs: []string{"http://localhost:1/cb", "http://localhost:2/cb"},
	})
	if err != nil {
		t.Fatal(err)
	}
	principal := models.Principal{UserID: "u1", SubjectKey: "user:internal:u1", AuthSource: "internal"}
	code := "code-expired"
	_ = asStore.SaveAuthorizationCode(context.Background(), AuthorizationCode{
		Code:                code,
		ClientID:            client.ClientID,
		SubjectKey:          principal.SubjectKey,
		Principal:           principal,
		RedirectURI:         "http://localhost:1/cb",
		CodeChallenge:       "challenge",
		CodeChallengeMethod: "S256",
		ExpiresAt:           time.Now().UTC().Add(-time.Minute),
	})
	mux := http.NewServeMux()
	srv.Register(mux)

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"http://localhost:1/cb"},
		"client_id":     {client.ClientID},
		"code_verifier": {"anything"},
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, cloneFormRequest(form))
	if rec.Code == http.StatusOK {
		t.Fatal("expected expired code rejection")
	}

	fresh := "code-fresh"
	sum := sha256Sum("verifier-value-1234567890")
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	_ = asStore.SaveAuthorizationCode(context.Background(), AuthorizationCode{
		Code:                fresh,
		ClientID:            client.ClientID,
		SubjectKey:          principal.SubjectKey,
		Principal:           principal,
		RedirectURI:         "http://localhost:1/cb",
		CodeChallenge:       challenge,
		CodeChallengeMethod: "S256",
		ExpiresAt:           time.Now().UTC().Add(time.Minute),
	})
	badRedirect := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {fresh},
		"redirect_uri":  {"http://localhost:2/cb"},
		"client_id":     {client.ClientID},
		"code_verifier": {"verifier-value-1234567890"},
	}
	badRec := httptest.NewRecorder()
	mux.ServeHTTP(badRec, cloneFormRequest(badRedirect))
	if badRec.Code == http.StatusOK {
		t.Fatal("expected redirect_uri mismatch rejection")
	}
}

func failFlow(t *testing.T, mux *http.ServeMux, clientID, redirectURI, user, pass, verifier string) {
	t.Helper()
	sum := sha256Sum(verifier)
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	authURL := "/oauth/authorize?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode()
	authRec := httptest.NewRecorder()
	mux.ServeHTTP(authRec, httptest.NewRequest(http.MethodGet, authURL, nil))
	sessionID := extractHidden(authRec.Body.String(), "login_session_id")
	form := url.Values{"login_session_id": {sessionID}, "username": {user}, "password": {pass}}
	loginReq := httptest.NewRequest(http.MethodPost, "/oauth/authorize/login", strings.NewReader(form.Encode()))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRec := httptest.NewRecorder()
	mux.ServeHTTP(loginRec, loginReq)
	loc, _ := url.Parse(loginRec.Header().Get("Location"))
	code := loc.Query().Get("code")
	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {"not-the-right-verifier"},
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, cloneFormRequest(tokenForm))
	if rec.Code == http.StatusOK {
		t.Fatal("expected bad verifier rejection")
	}
}

func cloneFormRequest(form url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func extractHidden(html, name string) string {
	marker := `name="` + name + `"`
	idx := strings.Index(html, marker)
	if idx < 0 {
		return ""
	}
	slice := html[idx:]
	valMarker := `value="`
	vIdx := strings.Index(slice, valMarker)
	if vIdx < 0 {
		return ""
	}
	rest := slice[vIdx+len(valMarker):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func sha256Sum(v string) [32]byte {
	return sha256.Sum256([]byte(v))
}
