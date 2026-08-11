package oauthas

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/synamcps/synamcps-server/internal/access"
	"github.com/synamcps/synamcps-server/internal/auth"
	"github.com/synamcps/synamcps-server/internal/config"
	"github.com/synamcps/synamcps-server/internal/models"
)

//go:embed templates/authorize_login.html
var authorizeLoginHTML string

var authorizeLoginTmpl = template.Must(template.New("authorize_login").Parse(authorizeLoginHTML))

type Server struct {
	cfg        config.OAuthASConfig
	oauth      config.OAuthConfig
	web        config.WebConfig
	store      *Store
	access     *access.Service
	router     *auth.ProviderRouter
	s3Bucket   string
	httpClient *http.Client
}

type Deps struct {
	Config   config.OAuthASConfig
	OAuth    config.OAuthConfig
	Web      config.WebConfig
	Store    *Store
	Access   *access.Service
	Router   *auth.ProviderRouter
	S3Bucket string
}

func NewServer(deps Deps) *Server {
	return &Server{
		cfg:        deps.Config,
		oauth:      deps.OAuth,
		web:        deps.Web,
		store:      deps.Store,
		access:     deps.Access,
		router:     deps.Router,
		s3Bucket:   deps.S3Bucket,
		httpClient: &http.Client{Timeout: 20 * time.Second},
	}
}

func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", s.protectedResourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/{path...}", s.protectedResourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.authorizationServerMetadata)
	mux.HandleFunc("POST /oauth/register", s.register)
	mux.HandleFunc("GET /oauth/authorize", s.authorize)
	mux.HandleFunc("POST /oauth/authorize/login", s.authorizeLogin)
	mux.HandleFunc("GET /oauth/authorize/oidc", s.authorizeOIDCStart)
	mux.HandleFunc("GET /oauth/callback", s.oidcCallback)
	mux.HandleFunc("POST /oauth/token", s.token)
	// Claude.ai may fall back to origin-root OAuth paths when discovery is incomplete.
	mux.HandleFunc("POST /register", s.register)
	mux.HandleFunc("GET /authorize", s.authorize)
	mux.HandleFunc("POST /token", s.token)
}

func (s *Server) Issuer(r *http.Request) string {
	return auth.ResolvePublicBaseURL(s.cfg.PublicBaseURL, r)
}

func (s *Server) ResourceMetadataURL(r *http.Request) string {
	return s.Issuer(r) + "/.well-known/oauth-protected-resource"
}

func (s *Server) MCPResourceURL(r *http.Request) string {
	return s.Issuer(r) + "/mcp"
}

func (s *Server) protectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"resource":                 s.MCPResourceURL(r),
		"authorization_servers":    []string{s.Issuer(r)},
		"scopes_supported":         []string{"openid", "mcp", "knowledge.read", "knowledge.write", "knowledge.search", "knowledge.delete"},
		"bearer_methods_supported": []string{"header"},
	}, http.StatusOK)
}

func (s *Server) authorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	issuer := s.Issuer(r)
	writeJSON(w, map[string]any{
		"issuer":                                issuer,
		"authorization_endpoint":                issuer + "/oauth/authorize",
		"token_endpoint":                        issuer + "/oauth/token",
		"registration_endpoint":                 issuer + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none", "client_secret_post", "client_secret_basic"},
		"scopes_supported":                      []string{"openid", "mcp", "knowledge.read", "knowledge.write", "knowledge.search", "knowledge.delete"},
	}, http.StatusOK)
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RedirectURIs            []string `json:"redirect_uris"`
		ClientName              string   `json:"client_name"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
		GrantTypes              []string `json:"grant_types"`
		ResponseTypes           []string `json:"response_types"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "invalid JSON body")
		return
	}
	if len(req.RedirectURIs) == 0 {
		oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris required")
		return
	}
	for _, uri := range req.RedirectURIs {
		if !RedirectURIAllowed(uri, nil, s.cfg.AllowedRedirectURIPatterns) {
			oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uri not allowed: "+uri)
			return
		}
	}
	method := req.TokenEndpointAuthMethod
	if method == "" {
		method = "none"
	}
	client, err := s.store.CreateClient(r.Context(), Client{
		ClientName:              req.ClientName,
		RedirectURIs:            req.RedirectURIs,
		TokenEndpointAuthMethod: method,
	})
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	writeJSON(w, map[string]any{
		"client_id":                  client.ClientID,
		"client_id_issued_at":        client.CreatedAt.Unix(),
		"client_name":                client.ClientName,
		"redirect_uris":              client.RedirectURIs,
		"token_endpoint_auth_method": client.TokenEndpointAuthMethod,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	}, http.StatusCreated)
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	clientID := q.Get("client_id")
	redirectURI := q.Get("redirect_uri")
	responseType := q.Get("response_type")
	state := q.Get("state")
	scope := q.Get("scope")
	challenge := q.Get("code_challenge")
	method := q.Get("code_challenge_method")
	if responseType != "code" {
		http.Error(w, "unsupported_response_type", http.StatusBadRequest)
		return
	}
	if method != "" && method != "S256" {
		http.Error(w, "code_challenge_method must be S256", http.StatusBadRequest)
		return
	}
	if challenge == "" {
		http.Error(w, "code_challenge required", http.StatusBadRequest)
		return
	}
	client, ok, err := s.store.GetClient(r.Context(), clientID)
	if err != nil || !ok {
		http.Error(w, "invalid_client", http.StatusBadRequest)
		return
	}
	if !ClientHasRedirectURI(client, redirectURI) || !RedirectURIAllowed(redirectURI, client.RedirectURIs, s.cfg.AllowedRedirectURIPatterns) {
		http.Error(w, "invalid_redirect_uri", http.StatusBadRequest)
		return
	}
	sess, err := s.store.SaveLoginSession(r.Context(), LoginSession{
		ClientID:            clientID,
		RedirectURI:         redirectURI,
		Scope:               scope,
		State:               state,
		CodeChallenge:       challenge,
		CodeChallengeMethod: "S256",
		ExpiresAt:           time.Now().UTC().Add(time.Duration(s.cfg.AuthorizationCodeTTLSeconds) * time.Second),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderLogin(w, sess, client, "")
}

func (s *Server) renderLogin(w http.ResponseWriter, sess LoginSession, client Client, errMsg string) {
	// Ensure we have the session ID that was persisted.
	if sess.ID == "" {
		http.Error(w, "missing login session", http.StatusInternalServerError)
		return
	}
	providers := []config.ProviderConfig{}
	for _, p := range s.oauth.Providers {
		if p.ClientID != "" {
			providers = append(providers, p)
		}
	}
	name := client.ClientName
	if name == "" {
		name = "MCP client"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = authorizeLoginTmpl.Execute(w, map[string]any{
		"LoginSessionID": sess.ID,
		"ClientID":       client.ClientID,
		"ClientName":     name,
		"Providers":      providers,
		"Error":          errMsg,
	})
}

func (s *Server) authorizeLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	sessionID := r.FormValue("login_session_id")
	sess, ok, err := s.store.GetLoginSession(r.Context(), sessionID)
	if err != nil || !ok {
		http.Error(w, "login session expired", http.StatusBadRequest)
		return
	}
	client, ok, err := s.store.GetClient(r.Context(), sess.ClientID)
	if err != nil || !ok {
		http.Error(w, "invalid_client", http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	principal, err := s.authenticateInternal(r.Context(), username, password)
	if err != nil {
		s.renderLogin(w, sess, client, "invalid credentials")
		return
	}
	if _, _, err := s.store.TakeLoginSession(r.Context(), sessionID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.completeAuthorize(w, r, sess, principal)
}

func (s *Server) authenticateInternal(ctx context.Context, username, password string) (models.Principal, error) {
	expectedPassword := ""
	if s.web.Admin.Enabled {
		expectedPassword = resolveAdminPassword(s.web.Admin.PasswordEnvRef)
	}
	if s.web.Admin.Enabled && expectedPassword != "" && username == s.web.Admin.Username && password == expectedPassword {
		p := models.Principal{
			UserID:     "default-admin",
			Email:      s.web.Admin.Username,
			SubjectKey: "user:internal:default-admin",
			Scopes:     []string{"platform_admin", "admin"},
			AuthSource: "internal",
		}
		return p, nil
	}
	if s.access == nil {
		return models.Principal{}, fmt.Errorf("invalid credentials")
	}
	user, ok, err := s.access.Store().AuthenticateUser(ctx, username, password)
	if err != nil {
		return models.Principal{}, err
	}
	if !ok {
		return models.Principal{}, fmt.Errorf("invalid credentials")
	}
	p := models.Principal{
		UserID:     user.ExternalSubject,
		Email:      user.Email,
		SubjectKey: user.SubjectKey,
		AuthSource: user.Source,
	}
	if user.SubjectKey == "user:internal:default-admin" {
		p.Scopes = []string{"platform_admin", "admin"}
	}
	return p, nil
}

func resolveAdminPassword(ref string) string {
	return configResolveSecret(ref)
}

// configResolveSecret mirrors config.resolveSecret without exporting it.
func configResolveSecret(value string) string {
	if strings.HasPrefix(value, "env:") {
		return getenv(strings.TrimPrefix(value, "env:"))
	}
	if strings.HasPrefix(value, "$") {
		return getenv(strings.TrimPrefix(value, "$"))
	}
	if strings.HasPrefix(value, "literal:") {
		return strings.TrimPrefix(value, "literal:")
	}
	return value
}

func (s *Server) authorizeOIDCStart(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("login_session_id")
	providerName := r.URL.Query().Get("provider")
	sess, ok, err := s.store.GetLoginSession(r.Context(), sessionID)
	if err != nil || !ok {
		http.Error(w, "login session expired", http.StatusBadRequest)
		return
	}
	provider, ok := s.providerByName(providerName)
	if !ok || provider.ClientID == "" {
		http.Error(w, "provider not configured for login", http.StatusBadRequest)
		return
	}
	sess.ProviderName = provider.Name
	if _, err := s.store.SaveLoginSession(r.Context(), sess); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	authURL, _ := oidcEndpoints(provider)
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", provider.ClientID)
	q.Set("redirect_uri", s.Issuer(r)+"/oauth/callback")
	q.Set("scope", "openid email profile")
	q.Set("state", sess.ID)
	http.Redirect(w, r, authURL+"?"+q.Encode(), http.StatusFound)
}

func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if sessionID == "" || code == "" {
		http.Error(w, "missing code/state", http.StatusBadRequest)
		return
	}
	sess, ok, err := s.store.TakeLoginSession(r.Context(), sessionID)
	if err != nil || !ok {
		http.Error(w, "login session expired", http.StatusBadRequest)
		return
	}
	provider, ok := s.providerByName(sess.ProviderName)
	if !ok {
		http.Error(w, "unknown provider", http.StatusBadRequest)
		return
	}
	rawIDToken, err := s.exchangeOIDCCode(r.Context(), provider, code, s.Issuer(r)+"/oauth/callback")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	claims, err := s.router.VerifyAndParseClaims(r.Context(), rawIDToken)
	if err != nil {
		http.Error(w, "invalid id token: "+err.Error(), http.StatusBadRequest)
		return
	}
	principal, err := s.router.ToPrincipal(claims)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	s.completeAuthorize(w, r, sess, principal)
}

func (s *Server) exchangeOIDCCode(ctx context.Context, provider config.ProviderConfig, code, redirectURI string) (string, error) {
	_, tokenURL := oidcEndpoints(provider)
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", provider.ClientID)
	if secret := provider.ClientSecret(); secret != "" {
		form.Set("client_secret", secret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("oidc token exchange failed: %s", resp.Status)
	}
	var out struct {
		IDToken     string `json:"id_token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	if out.IDToken != "" {
		return out.IDToken, nil
	}
	if out.AccessToken != "" {
		return out.AccessToken, nil
	}
	return "", fmt.Errorf("oidc token response missing id_token")
}

func (s *Server) completeAuthorize(w http.ResponseWriter, r *http.Request, sess LoginSession, principal models.Principal) {
	if s.access != nil {
		_, _, _ = s.access.EnsurePrincipal(r.Context(), principal, s.s3Bucket)
	}
	code, err := RandomURLToken(32)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	authCode := AuthorizationCode{
		Code:                code,
		ClientID:            sess.ClientID,
		SubjectKey:          models.SubjectKeyForPrincipal(principal),
		Principal:           principal,
		RedirectURI:         sess.RedirectURI,
		Scope:               sess.Scope,
		CodeChallenge:       sess.CodeChallenge,
		CodeChallengeMethod: sess.CodeChallengeMethod,
		ExpiresAt:           time.Now().UTC().Add(time.Duration(s.cfg.AuthorizationCodeTTLSeconds) * time.Second),
	}
	if err := s.store.SaveAuthorizationCode(r.Context(), authCode); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redir, err := url.Parse(sess.RedirectURI)
	if err != nil {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	q := redir.Query()
	q.Set("code", code)
	if sess.State != "" {
		q.Set("state", sess.State)
	}
	redir.RawQuery = q.Encode()
	http.Redirect(w, r, redir.String(), http.StatusSeeOther)
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "invalid form")
		return
	}
	grant := r.FormValue("grant_type")
	switch grant {
	case "authorization_code":
		s.tokenAuthorizationCode(w, r)
	case "refresh_token":
		s.tokenRefresh(w, r)
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "unsupported grant_type")
	}
}

func (s *Server) tokenAuthorizationCode(w http.ResponseWriter, r *http.Request) {
	code := r.FormValue("code")
	redirectURI := r.FormValue("redirect_uri")
	clientID := r.FormValue("client_id")
	verifier := r.FormValue("code_verifier")
	authCode, err := s.store.ConsumeAuthorizationCode(r.Context(), code)
	if err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_grant", err.Error())
		return
	}
	if authCode.ClientID != clientID {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "client_id mismatch")
		return
	}
	if authCode.RedirectURI != redirectURI {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri mismatch")
		return
	}
	if !VerifyPKCES256(verifier, authCode.CodeChallenge) {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "pkce verification failed")
		return
	}
	tokenResp, err := s.mintTokens(r.Context(), authCode.Principal, clientID, authCode.Scope)
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	writeJSON(w, tokenResp, http.StatusOK)
}

func (s *Server) tokenRefresh(w http.ResponseWriter, r *http.Request) {
	rawRefresh := r.FormValue("refresh_token")
	clientID := r.FormValue("client_id")
	existing, err := s.store.ConsumeRefreshToken(r.Context(), HashOpaque(rawRefresh))
	if err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_grant", err.Error())
		return
	}
	if clientID != "" && existing.ClientID != clientID {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "client_id mismatch")
		return
	}
	principal := models.Principal{SubjectKey: existing.SubjectKey, UserID: existing.SubjectKey, AuthSource: "oauth"}
	if s.access != nil {
		_ = s.access.Store().RevokeToken(r.Context(), existing.AccessTokenID)
		if user, ok, _ := s.access.Store().UserBySubjectKey(r.Context(), existing.SubjectKey); ok {
			principal = models.Principal{
				UserID:     user.ExternalSubject,
				Email:      user.Email,
				SubjectKey: user.SubjectKey,
				AuthSource: user.Source,
			}
			if user.SubjectKey == "user:internal:default-admin" {
				principal.Scopes = []string{"platform_admin", "admin"}
			}
		}
	}
	tokenResp, err := s.mintTokens(r.Context(), principal, existing.ClientID, "")
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	writeJSON(w, tokenResp, http.StatusOK)
}

func (s *Server) mintTokens(ctx context.Context, principal models.Principal, clientID, scope string) (map[string]any, error) {
	if s.access == nil {
		return nil, fmt.Errorf("access service required")
	}
	_, st, err := s.access.EnsurePrincipal(ctx, principal, s.s3Bucket)
	if err != nil {
		return nil, err
	}
	readable, err := s.access.ReadableStorageIDs(ctx, principal, nil, nil)
	if err != nil {
		return nil, err
	}
	storageIDs := make([]string, 0, len(readable))
	for id := range readable {
		storageIDs = append(storageIDs, id)
	}
	if len(storageIDs) == 0 {
		storageIDs = []string{st.ID}
	}
	mode := models.AccessMode(s.cfg.DefaultTokenMode)
	if mode == "" {
		mode = models.AccessModeReadWrite
	}
	scopes := make([]models.AccessTokenStorage, 0, len(storageIDs))
	for _, id := range storageIDs {
		scopes = append(scopes, models.AccessTokenStorage{StorageID: id, MaxMode: mode})
	}
	expires := time.Now().UTC().Add(time.Duration(s.cfg.AccessTokenTTLHours) * time.Hour)
	token, raw, err := s.access.Store().CreateToken(ctx, access.CreateTokenInput{
		OwnerSubjectKey: models.SubjectKeyForPrincipal(principal),
		Name:            "oauth:mcp-client",
		Mode:            mode,
		StorageScopes:   scopes,
		ExpiresAt:       &expires,
		CreatedBy:       "oauth_as",
	})
	if err != nil {
		return nil, err
	}
	refreshRaw, err := RandomURLToken(32)
	if err != nil {
		return nil, err
	}
	refreshExpiry := time.Now().UTC().Add(time.Duration(s.cfg.RefreshTokenTTLHours) * time.Hour)
	if err := s.store.SaveRefreshToken(ctx, RefreshToken{
		TokenHash:     HashOpaque(refreshRaw),
		ClientID:      clientID,
		SubjectKey:    models.SubjectKeyForPrincipal(principal),
		AccessTokenID: token.ID,
		ExpiresAt:     refreshExpiry,
	}); err != nil {
		return nil, err
	}
	if scope == "" {
		scope = "mcp"
	}
	return map[string]any{
		"access_token":  raw,
		"token_type":    "Bearer",
		"expires_in":    int(time.Until(expires).Seconds()),
		"refresh_token": refreshRaw,
		"scope":         scope,
	}, nil
}

func (s *Server) providerByName(name string) (config.ProviderConfig, bool) {
	for _, p := range s.oauth.Providers {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return config.ProviderConfig{}, false
}

func oidcEndpoints(provider config.ProviderConfig) (authURL, tokenURL string) {
	issuer := strings.TrimRight(provider.Issuer, "/")
	if strings.EqualFold(provider.Name, "keycloak") || strings.Contains(issuer, "/realms/") {
		return issuer + "/protocol/openid-connect/auth", issuer + "/protocol/openid-connect/token"
	}
	if strings.Contains(issuer, "accounts.google.com") {
		return "https://accounts.google.com/o/oauth2/v2/auth", "https://oauth2.googleapis.com/token"
	}
	return issuer + "/oauth2/authorize", issuer + "/oauth2/token"
}

func writeJSON(w http.ResponseWriter, v any, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func oauthError(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, map[string]string{"error": code, "error_description": desc}, status)
}
