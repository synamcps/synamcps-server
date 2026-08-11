package oauthas

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synamcps/synamcps-server/internal/models"
)

type Client struct {
	ClientID                string
	ClientSecretHash        string
	ClientName              string
	RedirectURIs            []string
	TokenEndpointAuthMethod string
	CreatedAt               time.Time
}

type AuthorizationCode struct {
	Code                string
	ClientID            string
	SubjectKey          string
	Principal           models.Principal
	RedirectURI         string
	Scope               string
	CodeChallenge       string
	CodeChallengeMethod string
	ExpiresAt           time.Time
	UsedAt              *time.Time
	CreatedAt           time.Time
}

type RefreshToken struct {
	TokenHash     string
	ClientID      string
	SubjectKey    string
	AccessTokenID string
	ExpiresAt     time.Time
	RevokedAt     *time.Time
	CreatedAt     time.Time
}

type LoginSession struct {
	ID                  string
	ClientID            string
	RedirectURI         string
	Scope               string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	ProviderName        string
	ExpiresAt           time.Time
	CreatedAt           time.Time
}

type Store struct {
	mu      sync.RWMutex
	clients map[string]Client
	codes   map[string]AuthorizationCode
	refresh map[string]RefreshToken
	logins  map[string]LoginSession
	pool    *pgxpool.Pool
	useDB   bool
}

func NewStore(pool *pgxpool.Pool) *Store {
	s := NewInMemoryStore()
	if pool != nil {
		s.pool = pool
		s.useDB = true
	}
	return s
}

func NewInMemoryStore() *Store {
	return &Store{
		clients: map[string]Client{},
		codes:   map[string]AuthorizationCode{},
		refresh: map[string]RefreshToken{},
		logins:  map[string]LoginSession{},
	}
}

func (s *Store) CreateClient(ctx context.Context, c Client) (Client, error) {
	now := time.Now().UTC()
	if c.ClientID == "" {
		c.ClientID = uuid.NewString()
	}
	if c.TokenEndpointAuthMethod == "" {
		c.TokenEndpointAuthMethod = "none"
	}
	if c.RedirectURIs == nil {
		c.RedirectURIs = []string{}
	}
	c.CreatedAt = now
	if s.useDB {
		_, err := s.pool.Exec(ctx, `
INSERT INTO oauth_clients (client_id, client_secret_hash, client_name, redirect_uris, token_endpoint_auth_method, created_at)
VALUES ($1,$2,$3,$4,$5,$6)`,
			c.ClientID, nullIfEmpty(c.ClientSecretHash), c.ClientName, c.RedirectURIs, c.TokenEndpointAuthMethod, c.CreatedAt)
		return c, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients[c.ClientID] = c
	return c, nil
}

func (s *Store) GetClient(ctx context.Context, clientID string) (Client, bool, error) {
	if s.useDB {
		row := s.pool.QueryRow(ctx, `
SELECT client_id, COALESCE(client_secret_hash,''), client_name, redirect_uris, token_endpoint_auth_method, created_at
FROM oauth_clients WHERE client_id=$1`, clientID)
		var c Client
		if err := row.Scan(&c.ClientID, &c.ClientSecretHash, &c.ClientName, &c.RedirectURIs, &c.TokenEndpointAuthMethod, &c.CreatedAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return Client{}, false, nil
			}
			return Client{}, false, err
		}
		return c, true, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.clients[clientID]
	return c, ok, nil
}

func (s *Store) SaveAuthorizationCode(ctx context.Context, code AuthorizationCode) error {
	now := time.Now().UTC()
	if code.CreatedAt.IsZero() {
		code.CreatedAt = now
	}
	principalJSON, err := json.Marshal(code.Principal)
	if err != nil {
		return err
	}
	if s.useDB {
		_, err := s.pool.Exec(ctx, `
INSERT INTO oauth_authorization_codes
(code, client_id, subject_key, principal_json, redirect_uri, scope, code_challenge, code_challenge_method, expires_at, used_at, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			code.Code, code.ClientID, code.SubjectKey, string(principalJSON), code.RedirectURI, code.Scope,
			code.CodeChallenge, code.CodeChallengeMethod, code.ExpiresAt, code.UsedAt, code.CreatedAt)
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codes[code.Code] = code
	return nil
}

func (s *Store) ConsumeAuthorizationCode(ctx context.Context, rawCode string) (AuthorizationCode, error) {
	now := time.Now().UTC()
	if s.useDB {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return AuthorizationCode{}, err
		}
		defer tx.Rollback(ctx)
		row := tx.QueryRow(ctx, `
SELECT code, client_id, subject_key, principal_json, redirect_uri, scope, code_challenge, code_challenge_method, expires_at, used_at, created_at
FROM oauth_authorization_codes WHERE code=$1 FOR UPDATE`, rawCode)
		code, err := scanAuthCode(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return AuthorizationCode{}, errors.New("invalid authorization code")
			}
			return AuthorizationCode{}, err
		}
		if code.UsedAt != nil {
			return AuthorizationCode{}, errors.New("authorization code already used")
		}
		if now.After(code.ExpiresAt) {
			return AuthorizationCode{}, errors.New("authorization code expired")
		}
		if _, err := tx.Exec(ctx, `UPDATE oauth_authorization_codes SET used_at=$1 WHERE code=$2`, now, rawCode); err != nil {
			return AuthorizationCode{}, err
		}
		code.UsedAt = &now
		return code, tx.Commit(ctx)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	code, ok := s.codes[rawCode]
	if !ok {
		return AuthorizationCode{}, errors.New("invalid authorization code")
	}
	if code.UsedAt != nil {
		return AuthorizationCode{}, errors.New("authorization code already used")
	}
	if now.After(code.ExpiresAt) {
		return AuthorizationCode{}, errors.New("authorization code expired")
	}
	code.UsedAt = &now
	s.codes[rawCode] = code
	return code, nil
}

func (s *Store) SaveRefreshToken(ctx context.Context, token RefreshToken) error {
	now := time.Now().UTC()
	if token.CreatedAt.IsZero() {
		token.CreatedAt = now
	}
	if s.useDB {
		_, err := s.pool.Exec(ctx, `
INSERT INTO oauth_refresh_tokens (token_hash, client_id, subject_key, access_token_id, expires_at, revoked_at, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			token.TokenHash, token.ClientID, token.SubjectKey, token.AccessTokenID, token.ExpiresAt, token.RevokedAt, token.CreatedAt)
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh[token.TokenHash] = token
	return nil
}

func (s *Store) ConsumeRefreshToken(ctx context.Context, tokenHash string) (RefreshToken, error) {
	now := time.Now().UTC()
	if s.useDB {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return RefreshToken{}, err
		}
		defer tx.Rollback(ctx)
		row := tx.QueryRow(ctx, `
SELECT token_hash, client_id, subject_key, access_token_id, expires_at, revoked_at, created_at
FROM oauth_refresh_tokens WHERE token_hash=$1 FOR UPDATE`, tokenHash)
		var t RefreshToken
		if err := row.Scan(&t.TokenHash, &t.ClientID, &t.SubjectKey, &t.AccessTokenID, &t.ExpiresAt, &t.RevokedAt, &t.CreatedAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return RefreshToken{}, errors.New("invalid refresh token")
			}
			return RefreshToken{}, err
		}
		if t.RevokedAt != nil {
			return RefreshToken{}, errors.New("refresh token revoked")
		}
		if now.After(t.ExpiresAt) {
			return RefreshToken{}, errors.New("refresh token expired")
		}
		if _, err := tx.Exec(ctx, `UPDATE oauth_refresh_tokens SET revoked_at=$1 WHERE token_hash=$2`, now, tokenHash); err != nil {
			return RefreshToken{}, err
		}
		t.RevokedAt = &now
		return t, tx.Commit(ctx)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.refresh[tokenHash]
	if !ok {
		return RefreshToken{}, errors.New("invalid refresh token")
	}
	if t.RevokedAt != nil {
		return RefreshToken{}, errors.New("refresh token revoked")
	}
	if now.After(t.ExpiresAt) {
		return RefreshToken{}, errors.New("refresh token expired")
	}
	t.RevokedAt = &now
	s.refresh[tokenHash] = t
	return t, nil
}

func (s *Store) SaveLoginSession(ctx context.Context, sess LoginSession) (LoginSession, error) {
	now := time.Now().UTC()
	if sess.ID == "" {
		sess.ID = uuid.NewString()
	}
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = now
	}
	if s.useDB {
		_, err := s.pool.Exec(ctx, `
INSERT INTO oauth_as_login_sessions
(id, client_id, redirect_uri, scope, state, code_challenge, code_challenge_method, provider_name, expires_at, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (id) DO UPDATE SET
  client_id=EXCLUDED.client_id,
  redirect_uri=EXCLUDED.redirect_uri,
  scope=EXCLUDED.scope,
  state=EXCLUDED.state,
  code_challenge=EXCLUDED.code_challenge,
  code_challenge_method=EXCLUDED.code_challenge_method,
  provider_name=EXCLUDED.provider_name,
  expires_at=EXCLUDED.expires_at`,
			sess.ID, sess.ClientID, sess.RedirectURI, sess.Scope, sess.State, sess.CodeChallenge, sess.CodeChallengeMethod, sess.ProviderName, sess.ExpiresAt, sess.CreatedAt)
		return sess, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logins[sess.ID] = sess
	return sess, nil
}

func (s *Store) TakeLoginSession(ctx context.Context, id string) (LoginSession, bool, error) {
	now := time.Now().UTC()
	if s.useDB {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return LoginSession{}, false, err
		}
		defer tx.Rollback(ctx)
		row := tx.QueryRow(ctx, `
SELECT id, client_id, redirect_uri, scope, state, code_challenge, code_challenge_method, provider_name, expires_at, created_at
FROM oauth_as_login_sessions WHERE id=$1 FOR UPDATE`, id)
		var sess LoginSession
		if err := row.Scan(&sess.ID, &sess.ClientID, &sess.RedirectURI, &sess.Scope, &sess.State, &sess.CodeChallenge, &sess.CodeChallengeMethod, &sess.ProviderName, &sess.ExpiresAt, &sess.CreatedAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return LoginSession{}, false, nil
			}
			return LoginSession{}, false, err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM oauth_as_login_sessions WHERE id=$1`, id); err != nil {
			return LoginSession{}, false, err
		}
		if now.After(sess.ExpiresAt) {
			_ = tx.Commit(ctx)
			return LoginSession{}, false, nil
		}
		return sess, true, tx.Commit(ctx)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.logins[id]
	if !ok {
		return LoginSession{}, false, nil
	}
	delete(s.logins, id)
	if now.After(sess.ExpiresAt) {
		return LoginSession{}, false, nil
	}
	return sess, true, nil
}

func (s *Store) GetLoginSession(ctx context.Context, id string) (LoginSession, bool, error) {
	now := time.Now().UTC()
	if s.useDB {
		row := s.pool.QueryRow(ctx, `
SELECT id, client_id, redirect_uri, scope, state, code_challenge, code_challenge_method, provider_name, expires_at, created_at
FROM oauth_as_login_sessions WHERE id=$1`, id)
		var sess LoginSession
		if err := row.Scan(&sess.ID, &sess.ClientID, &sess.RedirectURI, &sess.Scope, &sess.State, &sess.CodeChallenge, &sess.CodeChallengeMethod, &sess.ProviderName, &sess.ExpiresAt, &sess.CreatedAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return LoginSession{}, false, nil
			}
			return LoginSession{}, false, err
		}
		if now.After(sess.ExpiresAt) {
			return LoginSession{}, false, nil
		}
		return sess, true, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.logins[id]
	if !ok || now.After(sess.ExpiresAt) {
		return LoginSession{}, false, nil
	}
	return sess, true, nil
}

func scanAuthCode(row pgx.Row) (AuthorizationCode, error) {
	var code AuthorizationCode
	var principalJSON string
	if err := row.Scan(&code.Code, &code.ClientID, &code.SubjectKey, &principalJSON, &code.RedirectURI, &code.Scope,
		&code.CodeChallenge, &code.CodeChallengeMethod, &code.ExpiresAt, &code.UsedAt, &code.CreatedAt); err != nil {
		return AuthorizationCode{}, err
	}
	_ = json.Unmarshal([]byte(principalJSON), &code.Principal)
	return code, nil
}

func nullIfEmpty(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func RandomURLToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func HashOpaque(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
