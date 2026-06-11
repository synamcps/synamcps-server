package mcpproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/secrets"
)

type Store struct {
	pool    *pgxpool.Pool
	useDB   bool
	cipher  *secrets.Cipher
	servers map[string]models.MCPServer
	acl     map[string]models.MCPServerACLBinding
	tools   []models.MCPServerTool
	resources []models.MCPServerResource
	prompts []models.MCPServerPrompt
	secrets map[string]secretRow
	tokenScopes []models.AccessTokenMCPServer
}

type secretRow struct {
	encrypted []byte
	nonce     []byte
}

type CreateServerInput struct {
	Slug            string
	Name            string
	OwnerSubjectKey string
	Transport       models.MCPTransportKind
	URL             string
	HeadersJSON     string
	AuthType        models.MCPAuthType
	AuthHeaderName  string
	AuthSecret      string
}

type UpdateServerInput struct {
	Name           *string
	Transport      *models.MCPTransportKind
	URL            *string
	HeadersJSON    *string
	AuthType       *models.MCPAuthType
	AuthHeaderName *string
	AuthSecret     *string
	ClearAuthSecret bool
}

func NewStore(ctx context.Context, dsn string, cipher *secrets.Cipher) (*Store, error) {
	s := &Store{
		cipher:  cipher,
		servers: map[string]models.MCPServer{},
		acl:     map[string]models.MCPServerACLBinding{},
		secrets: map[string]secretRow{},
	}
	if dsn == "" {
		return s, nil
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("mcpproxy pool: %w", err)
	}
	s.pool = pool
	s.useDB = true
	if err := s.migrate(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate(ctx context.Context) error {
	ddl := `
CREATE TABLE IF NOT EXISTS mcp_servers (
  id TEXT PRIMARY KEY,
  slug TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  owner_subject_key TEXT NOT NULL,
  transport TEXT NOT NULL,
  url TEXT NOT NULL,
  headers_json TEXT NOT NULL DEFAULT '{}',
  auth_type TEXT NOT NULL DEFAULT 'bearer',
  auth_header_name TEXT NOT NULL DEFAULT 'Authorization',
  auth_secret_encrypted BYTEA,
  auth_secret_nonce BYTEA,
  status TEXT NOT NULL DEFAULT 'active',
  last_connected_at TIMESTAMPTZ,
  last_error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE IF NOT EXISTS mcp_server_acl_bindings (
  id TEXT PRIMARY KEY,
  server_id TEXT NOT NULL,
  subject_key TEXT NOT NULL,
  role TEXT NOT NULL,
  granted_by TEXT,
  expires_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL,
  UNIQUE(server_id, subject_key, role)
);
CREATE INDEX IF NOT EXISTS idx_mcp_server_acl_subject ON mcp_server_acl_bindings(subject_key);
CREATE TABLE IF NOT EXISTS mcp_server_tools (
  server_id TEXT NOT NULL,
  tool_name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  input_schema_json TEXT NOT NULL DEFAULT '{}',
  enabled BOOLEAN NOT NULL DEFAULT false,
  discovered_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (server_id, tool_name)
);
CREATE TABLE IF NOT EXISTS mcp_server_resources (
  server_id TEXT NOT NULL,
  uri TEXT NOT NULL,
  name TEXT NOT NULL DEFAULT '',
  mime_type TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  enabled BOOLEAN NOT NULL DEFAULT false,
  discovered_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (server_id, uri)
);
CREATE TABLE IF NOT EXISTS mcp_server_prompts (
  server_id TEXT NOT NULL,
  prompt_name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  arguments_schema_json TEXT NOT NULL DEFAULT '[]',
  enabled BOOLEAN NOT NULL DEFAULT false,
  discovered_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (server_id, prompt_name)
);
CREATE TABLE IF NOT EXISTS access_token_mcp_servers (
  token_id TEXT NOT NULL,
  server_id TEXT NOT NULL,
  tool_allowlist TEXT[] NOT NULL DEFAULT '{}',
  resource_allowlist TEXT[] NOT NULL DEFAULT '{}',
  prompt_allowlist TEXT[] NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (token_id, server_id)
);
`
	_, err := s.pool.Exec(ctx, ddl)
	return err
}

func (s *Store) CreateServer(ctx context.Context, in CreateServerInput) (models.MCPServer, error) {
	now := time.Now().UTC()
	if in.URL == "" {
		return models.MCPServer{}, errors.New("url is required")
	}
	if in.Slug == "" {
		in.Slug = s.deriveServerSlug(ctx, in.Name)
	}
	if in.Transport == "" {
		in.Transport = models.MCPTransportAuto
	}
	if in.AuthType == "" {
		in.AuthType = models.MCPAuthTypeBearer
	}
	if in.AuthHeaderName == "" {
		in.AuthHeaderName = defaultAuthHeader(in.AuthType)
	}
	if in.HeadersJSON == "" {
		in.HeadersJSON = "{}"
	}
	srv := models.MCPServer{
		ID:              uuid.NewString(),
		Slug:            in.Slug,
		Name:            in.Name,
		OwnerSubjectKey: in.OwnerSubjectKey,
		Transport:       in.Transport,
		URL:             in.URL,
		HeadersJSON:     in.HeadersJSON,
		AuthType:        in.AuthType,
		AuthHeaderName:  in.AuthHeaderName,
		Status:          models.MCPServerStatusActive,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if srv.Name == "" {
		srv.Name = srv.Slug
	}
	enc, nonce, err := s.encryptSecret(in.AuthSecret)
	if err != nil {
		return models.MCPServer{}, err
	}
	srv.HasAuthSecret = len(enc) > 0
	if s.useDB {
		_, err := s.pool.Exec(ctx, `INSERT INTO mcp_servers (id, slug, name, owner_subject_key, transport, url, headers_json, auth_type, auth_header_name, auth_secret_encrypted, auth_secret_nonce, status, last_error, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'',$13,$13)`,
			srv.ID, srv.Slug, srv.Name, srv.OwnerSubjectKey, string(srv.Transport), srv.URL, srv.HeadersJSON, string(srv.AuthType), srv.AuthHeaderName, enc, nonce, string(srv.Status), now)
		if err != nil {
			return models.MCPServer{}, err
		}
		owner := models.MCPServerACLBinding{ID: uuid.NewString(), ServerID: srv.ID, SubjectKey: srv.OwnerSubjectKey, Role: models.RoleMCPServerOwner, GrantedBy: srv.OwnerSubjectKey, CreatedAt: now}
		_, err = s.pool.Exec(ctx, `INSERT INTO mcp_server_acl_bindings (id, server_id, subject_key, role, granted_by, created_at) VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`,
			owner.ID, owner.ServerID, owner.SubjectKey, string(owner.Role), owner.GrantedBy, owner.CreatedAt)
		return srv, err
	}
	s.servers[srv.ID] = srv
	if len(enc) > 0 {
		s.secrets[srv.ID] = secretRow{encrypted: enc, nonce: nonce}
	}
	owner := models.MCPServerACLBinding{ID: uuid.NewString(), ServerID: srv.ID, SubjectKey: srv.OwnerSubjectKey, Role: models.RoleMCPServerOwner, GrantedBy: srv.OwnerSubjectKey, CreatedAt: now}
	s.acl[owner.ID] = owner
	return srv, nil
}

func (s *Store) UpdateServer(ctx context.Context, id string, in UpdateServerInput) (models.MCPServer, error) {
	srv, err := s.GetServer(ctx, id)
	if err != nil {
		return models.MCPServer{}, err
	}
	if in.Name != nil {
		srv.Name = *in.Name
	}
	if in.Transport != nil {
		srv.Transport = *in.Transport
	}
	if in.URL != nil {
		srv.URL = *in.URL
	}
	if in.HeadersJSON != nil {
		srv.HeadersJSON = *in.HeadersJSON
	}
	if in.AuthType != nil {
		srv.AuthType = *in.AuthType
	}
	if in.AuthHeaderName != nil {
		srv.AuthHeaderName = *in.AuthHeaderName
	}
	srv.UpdatedAt = time.Now().UTC()
	enc, nonce, hasSecret, err := s.secretForUpdate(ctx, id, in)
	if err != nil {
		return models.MCPServer{}, err
	}
	srv.HasAuthSecret = hasSecret
	if s.useDB {
		_, err := s.pool.Exec(ctx, `UPDATE mcp_servers SET name=$2, transport=$3, url=$4, headers_json=$5, auth_type=$6, auth_header_name=$7, auth_secret_encrypted=$8, auth_secret_nonce=$9, updated_at=$10 WHERE id=$1`,
			id, srv.Name, string(srv.Transport), srv.URL, srv.HeadersJSON, string(srv.AuthType), srv.AuthHeaderName, enc, nonce, srv.UpdatedAt)
		return srv, err
	}
	s.servers[id] = srv
	if in.ClearAuthSecret {
		delete(s.secrets, id)
	} else if in.AuthSecret != nil && *in.AuthSecret != "" {
		s.secrets[id] = secretRow{encrypted: enc, nonce: nonce}
	}
	return srv, nil
}

func (s *Store) secretForUpdate(ctx context.Context, id string, in UpdateServerInput) (enc, nonce []byte, hasSecret bool, err error) {
	if in.ClearAuthSecret {
		return nil, nil, false, nil
	}
	if in.AuthSecret != nil && *in.AuthSecret != "" {
		enc, nonce, err = s.encryptSecret(*in.AuthSecret)
		return enc, nonce, len(enc) > 0, err
	}
	row, err := s.loadSecretRow(ctx, id)
	if err != nil {
		return nil, nil, false, err
	}
	return row.encrypted, row.nonce, len(row.encrypted) > 0, nil
}

func (s *Store) ClearAuthSecret(ctx context.Context, id string) error {
	if s.useDB {
		_, err := s.pool.Exec(ctx, `UPDATE mcp_servers SET auth_secret_encrypted=NULL, auth_secret_nonce=NULL, updated_at=$2 WHERE id=$1`, id, time.Now().UTC())
		return err
	}
	delete(s.secrets, id)
	if srv, ok := s.servers[id]; ok {
		srv.HasAuthSecret = false
		s.servers[id] = srv
	}
	return nil
}

func (s *Store) DeleteServer(ctx context.Context, id string) error {
	if s.useDB {
		_, err := s.pool.Exec(ctx, `
DELETE FROM access_token_mcp_servers WHERE server_id=$1;
DELETE FROM mcp_server_tools WHERE server_id=$1;
DELETE FROM mcp_server_resources WHERE server_id=$1;
DELETE FROM mcp_server_prompts WHERE server_id=$1;
DELETE FROM mcp_server_acl_bindings WHERE server_id=$1;
DELETE FROM mcp_servers WHERE id=$1`, id)
		return err
	}
	delete(s.servers, id)
	delete(s.secrets, id)
	for k, b := range s.acl {
		if b.ServerID == id {
			delete(s.acl, k)
		}
	}
	s.tools = filterToolsByServerID(s.tools, id)
	s.resources = filterResourcesByServerID(s.resources, id)
	s.prompts = filterPromptsByServerID(s.prompts, id)
	return nil
}

func filterToolsByServerID(in []models.MCPServerTool, serverID string) []models.MCPServerTool {
	out := make([]models.MCPServerTool, 0, len(in))
	for _, v := range in {
		if v.ServerID != serverID {
			out = append(out, v)
		}
	}
	return out
}

func filterResourcesByServerID(in []models.MCPServerResource, serverID string) []models.MCPServerResource {
	out := make([]models.MCPServerResource, 0, len(in))
	for _, v := range in {
		if v.ServerID != serverID {
			out = append(out, v)
		}
	}
	return out
}

func filterPromptsByServerID(in []models.MCPServerPrompt, serverID string) []models.MCPServerPrompt {
	out := make([]models.MCPServerPrompt, 0, len(in))
	for _, v := range in {
		if v.ServerID != serverID {
			out = append(out, v)
		}
	}
	return out
}

func (s *Store) ListServers(ctx context.Context) ([]models.MCPServer, error) {
	if s.useDB {
		rows, err := s.pool.Query(ctx, `SELECT id, slug, name, owner_subject_key, transport, url, headers_json, auth_type, auth_header_name,
CASE WHEN auth_secret_encrypted IS NOT NULL AND length(auth_secret_encrypted) > 0 THEN true ELSE false END,
status, last_connected_at, last_error, created_at, updated_at FROM mcp_servers ORDER BY created_at DESC`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanServers(rows)
	}
	out := make([]models.MCPServer, 0, len(s.servers))
	for _, srv := range s.servers {
		if row, ok := s.secrets[srv.ID]; ok && len(row.encrypted) > 0 {
			srv.HasAuthSecret = true
		}
		out = append(out, srv)
	}
	return out, nil
}

func (s *Store) GetServer(ctx context.Context, id string) (models.MCPServer, error) {
	if s.useDB {
		row := s.pool.QueryRow(ctx, `SELECT id, slug, name, owner_subject_key, transport, url, headers_json, auth_type, auth_header_name,
CASE WHEN auth_secret_encrypted IS NOT NULL AND length(auth_secret_encrypted) > 0 THEN true ELSE false END,
status, last_connected_at, last_error, created_at, updated_at FROM mcp_servers WHERE id=$1`, id)
		return scanServer(row)
	}
	srv, ok := s.servers[id]
	if !ok {
		return models.MCPServer{}, errors.New("mcp server not found")
	}
	if row, ok := s.secrets[id]; ok && len(row.encrypted) > 0 {
		srv.HasAuthSecret = true
	}
	return srv, nil
}

// deriveServerSlug builds a unique slug from the server name (falling back to
// "mcp") so operators no longer have to enter one by hand.
func (s *Store) deriveServerSlug(ctx context.Context, name string) string {
	base := Slugify(name)
	if base == "" {
		base = "mcp"
	}
	candidate := base
	for i := 0; i < 50; i++ {
		if _, err := s.GetServerBySlug(ctx, candidate); err != nil {
			return candidate // not found -> available
		}
		candidate = base + "-" + uuid.NewString()[:6]
	}
	return base + "-" + uuid.NewString()[:8]
}

func (s *Store) GetServerBySlug(ctx context.Context, slug string) (models.MCPServer, error) {
	if s.useDB {
		row := s.pool.QueryRow(ctx, `SELECT id, slug, name, owner_subject_key, transport, url, headers_json, auth_type, auth_header_name,
CASE WHEN auth_secret_encrypted IS NOT NULL AND length(auth_secret_encrypted) > 0 THEN true ELSE false END,
status, last_connected_at, last_error, created_at, updated_at FROM mcp_servers WHERE slug=$1`, slug)
		return scanServer(row)
	}
	for _, srv := range s.servers {
		if srv.Slug == slug {
			return srv, nil
		}
	}
	return models.MCPServer{}, errors.New("mcp server not found")
}

func (s *Store) SetServerStatus(ctx context.Context, id string, status models.MCPServerStatus, lastErr string, connected bool) error {
	now := time.Now().UTC()
	var connectedAt *time.Time
	if connected {
		connectedAt = &now
	}
	if s.useDB {
		_, err := s.pool.Exec(ctx, `UPDATE mcp_servers SET status=$2, last_error=$3, last_connected_at=$4, updated_at=$5 WHERE id=$1`,
			id, string(status), lastErr, connectedAt, now)
		return err
	}
	srv, ok := s.servers[id]
	if !ok {
		return errors.New("mcp server not found")
	}
	srv.Status = status
	srv.LastError = lastErr
	srv.LastConnectedAt = connectedAt
	srv.UpdatedAt = now
	s.servers[id] = srv
	return nil
}

func (s *Store) UpsertACL(ctx context.Context, b models.MCPServerACLBinding) (models.MCPServerACLBinding, error) {
	if b.ID == "" {
		b.ID = uuid.NewString()
	}
	if b.CreatedAt.IsZero() {
		b.CreatedAt = time.Now().UTC()
	}
	if s.useDB {
		_, err := s.pool.Exec(ctx, `INSERT INTO mcp_server_acl_bindings (id, server_id, subject_key, role, granted_by, expires_at, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (server_id, subject_key, role) DO UPDATE SET granted_by=EXCLUDED.granted_by, expires_at=EXCLUDED.expires_at`,
			b.ID, b.ServerID, b.SubjectKey, string(b.Role), b.GrantedBy, b.ExpiresAt, b.CreatedAt)
		return b, err
	}
	s.acl[b.ID] = b
	return b, nil
}

func (s *Store) ACLForServer(ctx context.Context, serverID string) ([]models.MCPServerACLBinding, error) {
	if s.useDB {
		rows, err := s.pool.Query(ctx, `SELECT id, server_id, subject_key, role, COALESCE(granted_by,''), expires_at, created_at FROM mcp_server_acl_bindings WHERE server_id=$1`, serverID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []models.MCPServerACLBinding
		for rows.Next() {
			var b models.MCPServerACLBinding
			var role string
			if err := rows.Scan(&b.ID, &b.ServerID, &b.SubjectKey, &role, &b.GrantedBy, &b.ExpiresAt, &b.CreatedAt); err != nil {
				return nil, err
			}
			b.Role = models.MCPServerRole(role)
			out = append(out, b)
		}
		return out, rows.Err()
	}
	var out []models.MCPServerACLBinding
	for _, b := range s.acl {
		if b.ServerID == serverID {
			out = append(out, b)
		}
	}
	return out, nil
}

func (s *Store) ACLForSubject(ctx context.Context, subjectKeys []string) ([]models.MCPServerACLBinding, error) {
	if len(subjectKeys) == 0 {
		return nil, nil
	}
	if s.useDB {
		rows, err := s.pool.Query(ctx, `SELECT id, server_id, subject_key, role, COALESCE(granted_by,''), expires_at, created_at FROM mcp_server_acl_bindings WHERE subject_key = ANY($1)`, subjectKeys)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []models.MCPServerACLBinding
		for rows.Next() {
			var b models.MCPServerACLBinding
			var role string
			if err := rows.Scan(&b.ID, &b.ServerID, &b.SubjectKey, &role, &b.GrantedBy, &b.ExpiresAt, &b.CreatedAt); err != nil {
				return nil, err
			}
			b.Role = models.MCPServerRole(role)
			out = append(out, b)
		}
		return out, rows.Err()
	}
	set := map[string]struct{}{}
	for _, k := range subjectKeys {
		set[k] = struct{}{}
	}
	var out []models.MCPServerACLBinding
	for _, b := range s.acl {
		if _, ok := set[b.SubjectKey]; ok {
			out = append(out, b)
		}
	}
	return out, nil
}

func (s *Store) UpsertDiscovery(ctx context.Context, serverID string, tools []models.MCPServerTool, resources []models.MCPServerResource, prompts []models.MCPServerPrompt) error {
	existingTools, _ := s.ListTools(ctx, serverID)
	existingResources, _ := s.ListResources(ctx, serverID)
	existingPrompts, _ := s.ListPrompts(ctx, serverID)
	enabledTools := map[string]bool{}
	for _, t := range existingTools {
		enabledTools[t.ToolName] = t.Enabled
	}
	enabledResources := map[string]bool{}
	for _, r := range existingResources {
		enabledResources[r.URI] = r.Enabled
	}
	enabledPrompts := map[string]bool{}
	for _, p := range existingPrompts {
		enabledPrompts[p.PromptName] = p.Enabled
	}
	for i := range tools {
		if v, ok := enabledTools[tools[i].ToolName]; ok {
			tools[i].Enabled = v
		}
	}
	for i := range resources {
		if v, ok := enabledResources[resources[i].URI]; ok {
			resources[i].Enabled = v
		}
	}
	for i := range prompts {
		if v, ok := enabledPrompts[prompts[i].PromptName]; ok {
			prompts[i].Enabled = v
		}
	}
	if s.useDB {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `DELETE FROM mcp_server_tools WHERE server_id=$1`, serverID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM mcp_server_resources WHERE server_id=$1`, serverID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM mcp_server_prompts WHERE server_id=$1`, serverID); err != nil {
			return err
		}
		for _, t := range tools {
			if _, err := tx.Exec(ctx, `INSERT INTO mcp_server_tools (server_id, tool_name, description, input_schema_json, enabled, discovered_at) VALUES ($1,$2,$3,$4,$5,$6)`,
				t.ServerID, t.ToolName, t.Description, t.InputSchemaJSON, t.Enabled, t.DiscoveredAt); err != nil {
				return err
			}
		}
		for _, r := range resources {
			if _, err := tx.Exec(ctx, `INSERT INTO mcp_server_resources (server_id, uri, name, mime_type, description, enabled, discovered_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
				r.ServerID, r.URI, r.Name, r.MimeType, r.Description, r.Enabled, r.DiscoveredAt); err != nil {
				return err
			}
		}
		for _, p := range prompts {
			if _, err := tx.Exec(ctx, `INSERT INTO mcp_server_prompts (server_id, prompt_name, description, arguments_schema_json, enabled, discovered_at) VALUES ($1,$2,$3,$4,$5,$6)`,
				p.ServerID, p.PromptName, p.Description, p.ArgumentsSchemaJSON, p.Enabled, p.DiscoveredAt); err != nil {
				return err
			}
		}
		return tx.Commit(ctx)
	}
	s.tools = filterByServerID(s.tools, serverID)
	s.resources = filterByServerIDResources(s.resources, serverID)
	s.prompts = filterByServerIDPrompts(s.prompts, serverID)
	s.tools = append(s.tools, tools...)
	s.resources = append(s.resources, resources...)
	s.prompts = append(s.prompts, prompts...)
	return nil
}

func (s *Store) SetEnabledCapabilities(ctx context.Context, serverID string, tools, resources, prompts []string) error {
	if s.useDB {
		_, err := s.pool.Exec(ctx, `UPDATE mcp_server_tools SET enabled=false WHERE server_id=$1`, serverID)
		if err != nil {
			return err
		}
		if len(tools) > 0 {
			_, err = s.pool.Exec(ctx, `UPDATE mcp_server_tools SET enabled=true WHERE server_id=$1 AND tool_name = ANY($2)`, serverID, tools)
			if err != nil {
				return err
			}
		}
		_, err = s.pool.Exec(ctx, `UPDATE mcp_server_resources SET enabled=false WHERE server_id=$1`, serverID)
		if err != nil {
			return err
		}
		if len(resources) > 0 {
			_, err = s.pool.Exec(ctx, `UPDATE mcp_server_resources SET enabled=true WHERE server_id=$1 AND uri = ANY($2)`, serverID, resources)
			if err != nil {
				return err
			}
		}
		_, err = s.pool.Exec(ctx, `UPDATE mcp_server_prompts SET enabled=false WHERE server_id=$1`, serverID)
		if err != nil {
			return err
		}
		if len(prompts) > 0 {
			_, err = s.pool.Exec(ctx, `UPDATE mcp_server_prompts SET enabled=true WHERE server_id=$1 AND prompt_name = ANY($2)`, serverID, prompts)
		}
		return err
	}
	toolSet := toSet(tools)
	resSet := toSet(resources)
	promptSet := toSet(prompts)
	for i := range s.tools {
		if s.tools[i].ServerID != serverID {
			continue
		}
		s.tools[i].Enabled = toolSet[s.tools[i].ToolName]
	}
	for i := range s.resources {
		if s.resources[i].ServerID != serverID {
			continue
		}
		s.resources[i].Enabled = resSet[s.resources[i].URI]
	}
	for i := range s.prompts {
		if s.prompts[i].ServerID != serverID {
			continue
		}
		s.prompts[i].Enabled = promptSet[s.prompts[i].PromptName]
	}
	return nil
}

func (s *Store) ListTools(ctx context.Context, serverID string) ([]models.MCPServerTool, error) {
	if s.useDB {
		rows, err := s.pool.Query(ctx, `SELECT server_id, tool_name, description, input_schema_json, enabled, discovered_at FROM mcp_server_tools WHERE server_id=$1 ORDER BY tool_name`, serverID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []models.MCPServerTool
		for rows.Next() {
			var t models.MCPServerTool
			if err := rows.Scan(&t.ServerID, &t.ToolName, &t.Description, &t.InputSchemaJSON, &t.Enabled, &t.DiscoveredAt); err != nil {
				return nil, err
			}
			out = append(out, t)
		}
		return out, rows.Err()
	}
	var out []models.MCPServerTool
	for _, t := range s.tools {
		if t.ServerID == serverID {
			out = append(out, t)
		}
	}
	return out, nil
}

func (s *Store) ListResources(ctx context.Context, serverID string) ([]models.MCPServerResource, error) {
	if s.useDB {
		rows, err := s.pool.Query(ctx, `SELECT server_id, uri, name, mime_type, description, enabled, discovered_at FROM mcp_server_resources WHERE server_id=$1 ORDER BY uri`, serverID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []models.MCPServerResource
		for rows.Next() {
			var r models.MCPServerResource
			if err := rows.Scan(&r.ServerID, &r.URI, &r.Name, &r.MimeType, &r.Description, &r.Enabled, &r.DiscoveredAt); err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}
	var out []models.MCPServerResource
	for _, r := range s.resources {
		if r.ServerID == serverID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *Store) ListPrompts(ctx context.Context, serverID string) ([]models.MCPServerPrompt, error) {
	if s.useDB {
		rows, err := s.pool.Query(ctx, `SELECT server_id, prompt_name, description, arguments_schema_json, enabled, discovered_at FROM mcp_server_prompts WHERE server_id=$1 ORDER BY prompt_name`, serverID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []models.MCPServerPrompt
		for rows.Next() {
			var p models.MCPServerPrompt
			if err := rows.Scan(&p.ServerID, &p.PromptName, &p.Description, &p.ArgumentsSchemaJSON, &p.Enabled, &p.DiscoveredAt); err != nil {
				return nil, err
			}
			out = append(out, p)
		}
		return out, rows.Err()
	}
	var out []models.MCPServerPrompt
	for _, p := range s.prompts {
		if p.ServerID == serverID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (s *Store) GetCapabilities(ctx context.Context, serverID string) (models.MCPServerCapabilities, error) {
	srv, err := s.GetServer(ctx, serverID)
	if err != nil {
		return models.MCPServerCapabilities{}, err
	}
	tools, err := s.ListTools(ctx, serverID)
	if err != nil {
		return models.MCPServerCapabilities{}, err
	}
	resources, err := s.ListResources(ctx, serverID)
	if err != nil {
		return models.MCPServerCapabilities{}, err
	}
	prompts, err := s.ListPrompts(ctx, serverID)
	if err != nil {
		return models.MCPServerCapabilities{}, err
	}
	return models.MCPServerCapabilities{Server: srv, Tools: tools, Resources: resources, Prompts: prompts}, nil
}

func (s *Store) TokenMCPServers(ctx context.Context, tokenID string) ([]models.AccessTokenMCPServer, error) {
	if s.useDB {
		rows, err := s.pool.Query(ctx, `SELECT token_id, server_id, tool_allowlist, resource_allowlist, prompt_allowlist, created_at FROM access_token_mcp_servers WHERE token_id=$1`, tokenID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []models.AccessTokenMCPServer
		for rows.Next() {
			var scope models.AccessTokenMCPServer
			if err := rows.Scan(&scope.TokenID, &scope.ServerID, &scope.ToolAllowlist, &scope.ResourceAllowlist, &scope.PromptAllowlist, &scope.CreatedAt); err != nil {
				return nil, err
			}
			out = append(out, scope)
		}
		return out, rows.Err()
	}
	var out []models.AccessTokenMCPServer
	for _, scope := range s.tokenScopes {
		if scope.TokenID == tokenID {
			out = append(out, scope)
		}
	}
	return out, nil
}

func (s *Store) ReplaceTokenMCPServers(ctx context.Context, tokenID string, scopes []models.AccessTokenMCPServer) error {
	now := time.Now().UTC()
	if s.useDB {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `DELETE FROM access_token_mcp_servers WHERE token_id=$1`, tokenID); err != nil {
			return err
		}
		for _, scope := range scopes {
			scope.TokenID = tokenID
			if scope.CreatedAt.IsZero() {
				scope.CreatedAt = now
			}
			if scope.ToolAllowlist == nil {
				scope.ToolAllowlist = []string{}
			}
			if scope.ResourceAllowlist == nil {
				scope.ResourceAllowlist = []string{}
			}
			if scope.PromptAllowlist == nil {
				scope.PromptAllowlist = []string{}
			}
			if _, err := tx.Exec(ctx, `INSERT INTO access_token_mcp_servers (token_id, server_id, tool_allowlist, resource_allowlist, prompt_allowlist, created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
				scope.TokenID, scope.ServerID, scope.ToolAllowlist, scope.ResourceAllowlist, scope.PromptAllowlist, scope.CreatedAt); err != nil {
				return err
			}
		}
		return tx.Commit(ctx)
	}
	filtered := s.tokenScopes[:0]
	for _, scope := range s.tokenScopes {
		if scope.TokenID != tokenID {
			filtered = append(filtered, scope)
		}
	}
	for _, scope := range scopes {
		scope.TokenID = tokenID
		if scope.CreatedAt.IsZero() {
			scope.CreatedAt = now
		}
		filtered = append(filtered, scope)
	}
	s.tokenScopes = filtered
	return nil
}

func (s *Store) SaveTokenMCPServers(ctx context.Context, tokenID string, scopes []models.AccessTokenMCPServer) error {
	return s.ReplaceTokenMCPServers(ctx, tokenID, scopes)
}

func (s *Store) AuthHeaders(ctx context.Context, srv models.MCPServer) (map[string]string, error) {
	headers := map[string]string{}
	if srv.HeadersJSON != "" {
		var extra map[string]string
		if err := json.Unmarshal([]byte(srv.HeadersJSON), &extra); err == nil {
			for k, v := range extra {
				headers[k] = v
			}
		}
	}
	row, err := s.loadSecretRow(ctx, srv.ID)
	if err != nil {
		return headers, err
	}
	secret, err := s.cipher.Decrypt(row.encrypted, row.nonce)
	if err != nil {
		return headers, err
	}
	if secret == "" {
		return headers, nil
	}
	switch srv.AuthType {
	case models.MCPAuthTypeAPIKey, models.MCPAuthTypeCustomHeader:
		headers[srv.AuthHeaderName] = secret
	default:
		if strings.HasPrefix(strings.ToLower(secret), "bearer ") {
			headers[srv.AuthHeaderName] = secret
		} else {
			headers[srv.AuthHeaderName] = "Bearer " + secret
		}
	}
	return headers, nil
}

func (s *Store) loadSecretRow(ctx context.Context, serverID string) (secretRow, error) {
	if s.useDB {
		var enc, nonce []byte
		err := s.pool.QueryRow(ctx, `SELECT auth_secret_encrypted, auth_secret_nonce FROM mcp_servers WHERE id=$1`, serverID).Scan(&enc, &nonce)
		return secretRow{encrypted: enc, nonce: nonce}, err
	}
	return s.secrets[serverID], nil
}

func (s *Store) encryptSecret(plain string) ([]byte, []byte, error) {
	if plain == "" || s.cipher == nil {
		return nil, nil, nil
	}
	return s.cipher.Encrypt(plain)
}

func defaultAuthHeader(auth models.MCPAuthType) string {
	switch auth {
	case models.MCPAuthTypeAPIKey:
		return "X-API-Key"
	default:
		return "Authorization"
	}
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanServer(row rowScanner) (models.MCPServer, error) {
	var srv models.MCPServer
	var transport, authType, status string
	if err := row.Scan(&srv.ID, &srv.Slug, &srv.Name, &srv.OwnerSubjectKey, &transport, &srv.URL, &srv.HeadersJSON, &authType, &srv.AuthHeaderName,
		&srv.HasAuthSecret, &status, &srv.LastConnectedAt, &srv.LastError, &srv.CreatedAt, &srv.UpdatedAt); err != nil {
		return models.MCPServer{}, err
	}
	srv.Transport = models.MCPTransportKind(transport)
	srv.AuthType = models.MCPAuthType(authType)
	srv.Status = models.MCPServerStatus(status)
	return srv, nil
}

func scanServers(rows pgx.Rows) ([]models.MCPServer, error) {
	var out []models.MCPServer
	for rows.Next() {
		srv, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, srv)
	}
	return out, rows.Err()
}

func filterByServerID(in []models.MCPServerTool, serverID string) []models.MCPServerTool {
	out := in[:0]
	for _, v := range in {
		if v.ServerID != serverID {
			out = append(out, v)
		}
	}
	return out
}

func filterByServerIDResources(in []models.MCPServerResource, serverID string) []models.MCPServerResource {
	out := in[:0]
	for _, v := range in {
		if v.ServerID != serverID {
			out = append(out, v)
		}
	}
	return out
}

func filterByServerIDPrompts(in []models.MCPServerPrompt, serverID string) []models.MCPServerPrompt {
	out := in[:0]
	for _, v := range in {
		if v.ServerID != serverID {
			out = append(out, v)
		}
	}
	return out
}

func toSet(items []string) map[string]bool {
	out := map[string]bool{}
	for _, item := range items {
		out[item] = true
	}
	return out
}
