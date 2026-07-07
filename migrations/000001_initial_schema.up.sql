-- Access control and identity
CREATE TABLE access_users (
  id TEXT PRIMARY KEY,
  subject_key TEXT NOT NULL UNIQUE,
  source TEXT NOT NULL,
  issuer TEXT,
  external_subject TEXT NOT NULL,
  email TEXT,
  display_name TEXT,
  status TEXT NOT NULL,
  password_hash TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  last_seen_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE access_groups (
  id TEXT PRIMARY KEY,
  subject_key TEXT NOT NULL UNIQUE,
  source TEXT NOT NULL,
  issuer TEXT,
  external_group_id TEXT,
  name TEXT NOT NULL,
  managed_by TEXT NOT NULL,
  sync_status TEXT,
  last_synced_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE access_group_memberships (
  group_id TEXT NOT NULL,
  user_id TEXT NOT NULL,
  source TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  expires_at TIMESTAMPTZ,
  PRIMARY KEY (group_id, user_id)
);

CREATE TABLE storages (
  id TEXT PRIMARY KEY,
  slug TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  owner_subject_key TEXT NOT NULL,
  visibility TEXT NOT NULL,
  default_access TEXT NOT NULL,
  storage_kind TEXT NOT NULL,
  s3_bucket TEXT,
  s3_prefix TEXT NOT NULL,
  status TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  archived_at TIMESTAMPTZ
);

CREATE TABLE storage_acl_bindings (
  id TEXT PRIMARY KEY,
  storage_id TEXT NOT NULL,
  subject_key TEXT NOT NULL,
  role TEXT NOT NULL,
  granted_by TEXT,
  expires_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL,
  UNIQUE (storage_id, subject_key, role)
);

CREATE INDEX idx_storage_acl_subject ON storage_acl_bindings (subject_key);

CREATE TABLE access_tokens (
  id TEXT PRIMARY KEY,
  owner_subject_key TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  mode TEXT NOT NULL,
  allowed_permissions TEXT[] NOT NULL DEFAULT '{}',
  rate_limit_enabled BOOLEAN NOT NULL DEFAULT true,
  rate_limit_rpm INTEGER NOT NULL DEFAULT 0,
  rate_limit_rph INTEGER NOT NULL DEFAULT 0,
  rate_limit_rpd INTEGER NOT NULL DEFAULT 0,
  burst_limit INTEGER NOT NULL DEFAULT 0,
  expires_at TIMESTAMPTZ,
  revoked_at TIMESTAMPTZ,
  last_used_at TIMESTAMPTZ,
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE access_token_storages (
  token_id TEXT NOT NULL,
  storage_id TEXT NOT NULL,
  max_mode TEXT NOT NULL,
  tool_allowlist TEXT[] NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (token_id, storage_id)
);

CREATE TABLE audit_events (
  id TEXT PRIMARY KEY,
  actor_subject_key TEXT NOT NULL,
  action TEXT NOT NULL,
  resource_type TEXT NOT NULL,
  resource_id TEXT NOT NULL,
  storage_id TEXT,
  created_at TIMESTAMPTZ NOT NULL
);

-- MCP proxy
CREATE TABLE mcp_servers (
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

CREATE TABLE mcp_server_acl_bindings (
  id TEXT PRIMARY KEY,
  server_id TEXT NOT NULL,
  subject_key TEXT NOT NULL,
  role TEXT NOT NULL,
  granted_by TEXT,
  expires_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL,
  UNIQUE (server_id, subject_key, role)
);

CREATE INDEX idx_mcp_server_acl_subject ON mcp_server_acl_bindings (subject_key);

CREATE TABLE mcp_server_tools (
  server_id TEXT NOT NULL,
  tool_name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  input_schema_json TEXT NOT NULL DEFAULT '{}',
  enabled BOOLEAN NOT NULL DEFAULT false,
  discovered_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (server_id, tool_name)
);

CREATE TABLE mcp_server_resources (
  server_id TEXT NOT NULL,
  uri TEXT NOT NULL,
  name TEXT NOT NULL DEFAULT '',
  mime_type TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  enabled BOOLEAN NOT NULL DEFAULT false,
  discovered_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (server_id, uri)
);

CREATE TABLE mcp_server_prompts (
  server_id TEXT NOT NULL,
  prompt_name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  arguments_schema_json TEXT NOT NULL DEFAULT '[]',
  enabled BOOLEAN NOT NULL DEFAULT false,
  discovered_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (server_id, prompt_name)
);

CREATE TABLE access_token_mcp_servers (
  token_id TEXT NOT NULL,
  server_id TEXT NOT NULL,
  tool_allowlist TEXT[] NOT NULL DEFAULT '{}',
  resource_allowlist TEXT[] NOT NULL DEFAULT '{}',
  prompt_allowlist TEXT[] NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (token_id, server_id)
);

-- Knowledge metadata catalog
CREATE TABLE knowledge_documents (
  doc_id TEXT PRIMARY KEY,
  storage_id TEXT NOT NULL DEFAULT 'legacy',
  owner_id TEXT NOT NULL,
  visibility TEXT NOT NULL,
  group_ids TEXT[] NOT NULL DEFAULT '{}',
  title TEXT NOT NULL,
  mime_type TEXT NOT NULL,
  source TEXT NOT NULL,
  source_url TEXT,
  source_hash TEXT,
  s3_key TEXT,
  summary_chunk_id TEXT,
  status TEXT NOT NULL,
  body TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_knowledge_docs_source ON knowledge_documents (source);
CREATE INDEX idx_knowledge_docs_updated ON knowledge_documents (updated_at DESC);
CREATE INDEX idx_knowledge_docs_storage ON knowledge_documents (storage_id);

-- Vector store (pgvector backend)
CREATE TABLE knowledge_vectors (
  chunk_id TEXT PRIMARY KEY,
  doc_id TEXT NOT NULL,
  payload_json JSONB NOT NULL,
  embedding_json JSONB NOT NULL,
  text_content TEXT NOT NULL
);

CREATE INDEX idx_knowledge_vectors_doc_id ON knowledge_vectors (doc_id);
