-- Unified first-class data objects (storage is kind=storage for RAG).
CREATE TABLE data_objects (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  slug TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  owner_subject_key TEXT NOT NULL,
  visibility TEXT NOT NULL DEFAULT 'personal',
  default_access TEXT NOT NULL DEFAULT 'none',
  status TEXT NOT NULL DEFAULT 'active',
  backend TEXT NOT NULL DEFAULT 'postgres',
  config_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  s3_bucket TEXT,
  s3_prefix TEXT NOT NULL DEFAULT '',
  pg_role TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  archived_at TIMESTAMPTZ
);

CREATE INDEX idx_data_objects_kind ON data_objects (kind);
CREATE INDEX idx_data_objects_owner ON data_objects (owner_subject_key);

INSERT INTO data_objects (
  id, kind, slug, name, owner_subject_key, visibility, default_access, status,
  backend, config_json, s3_bucket, s3_prefix, created_at, updated_at, archived_at
)
SELECT
  id, 'storage', slug, name, owner_subject_key, visibility, default_access, status,
  'postgres', '{}'::jsonb, s3_bucket, s3_prefix, created_at, updated_at, archived_at
FROM storages
ON CONFLICT (id) DO NOTHING;

CREATE TABLE object_acl_bindings (
  id TEXT PRIMARY KEY,
  object_id TEXT NOT NULL REFERENCES data_objects(id) ON DELETE CASCADE,
  subject_key TEXT NOT NULL,
  role TEXT NOT NULL,
  granted_by TEXT,
  expires_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL,
  UNIQUE (object_id, subject_key, role)
);

CREATE INDEX idx_object_acl_subject ON object_acl_bindings (subject_key);
CREATE INDEX idx_object_acl_object ON object_acl_bindings (object_id);

INSERT INTO object_acl_bindings (id, object_id, subject_key, role, granted_by, expires_at, created_at)
SELECT id, storage_id, subject_key, role, granted_by, expires_at, created_at
FROM storage_acl_bindings
ON CONFLICT (object_id, subject_key, role) DO NOTHING;

CREATE TABLE access_token_objects (
  token_id TEXT NOT NULL,
  object_id TEXT NOT NULL REFERENCES data_objects(id) ON DELETE CASCADE,
  max_mode TEXT NOT NULL,
  method_allowlist TEXT[] NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (token_id, object_id)
);

INSERT INTO access_token_objects (token_id, object_id, max_mode, method_allowlist, created_at)
SELECT token_id, storage_id, max_mode, tool_allowlist, created_at
FROM access_token_storages
ON CONFLICT (token_id, object_id) DO NOTHING;

-- Role bookkeeping (actual CREATE ROLE happens in app).
CREATE TABLE data_plane_roles (
  role_name TEXT PRIMARY KEY,
  kind TEXT NOT NULL, -- user | token
  subject_key TEXT,
  token_id TEXT,
  created_at TIMESTAMPTZ NOT NULL
);

-- Kind-specific shared tables (RLS enforced).
CREATE TABLE variable_entries (
  object_id TEXT NOT NULL REFERENCES data_objects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  prefix TEXT NOT NULL DEFAULT '',
  payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (object_id, prefix, name)
);

ALTER TABLE variable_entries ENABLE ROW LEVEL SECURITY;
CREATE POLICY variable_entries_isolation ON variable_entries
  USING (object_id = ANY (string_to_array(current_setting('syna.object_ids', true), ',')));

CREATE TABLE counter_meta (
  object_id TEXT PRIMARY KEY REFERENCES data_objects(id) ON DELETE CASCADE,
  value DOUBLE PRECISION NOT NULL DEFAULT 0,
  updated_at TIMESTAMPTZ NOT NULL
);

ALTER TABLE counter_meta ENABLE ROW LEVEL SECURITY;
CREATE POLICY counter_meta_isolation ON counter_meta
  USING (object_id = ANY (string_to_array(current_setting('syna.object_ids', true), ',')));

CREATE TABLE log_entries (
  id BIGSERIAL PRIMARY KEY,
  object_id TEXT NOT NULL REFERENCES data_objects(id) ON DELETE CASCADE,
  level TEXT NOT NULL DEFAULT 'info',
  message TEXT NOT NULL,
  fields JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_log_entries_object_created ON log_entries (object_id, created_at DESC);

ALTER TABLE log_entries ENABLE ROW LEVEL SECURITY;
CREATE POLICY log_entries_isolation ON log_entries
  USING (object_id = ANY (string_to_array(current_setting('syna.object_ids', true), ',')));

CREATE TABLE queue_messages (
  id BIGSERIAL PRIMARY KEY,
  object_id TEXT NOT NULL REFERENCES data_objects(id) ON DELETE CASCADE,
  payload JSONB NOT NULL,
  visible_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  locked_until TIMESTAMPTZ,
  lock_token TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_queue_messages_ready ON queue_messages (object_id, visible_at)
  WHERE locked_until IS NULL;

ALTER TABLE queue_messages ENABLE ROW LEVEL SECURITY;
CREATE POLICY queue_messages_isolation ON queue_messages
  USING (object_id = ANY (string_to_array(current_setting('syna.object_ids', true), ',')));

CREATE TABLE bus_events (
  id BIGSERIAL PRIMARY KEY,
  object_id TEXT NOT NULL REFERENCES data_objects(id) ON DELETE CASCADE,
  payload JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_bus_events_object ON bus_events (object_id, id);

ALTER TABLE bus_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY bus_events_isolation ON bus_events
  USING (object_id = ANY (string_to_array(current_setting('syna.object_ids', true), ',')));

CREATE TABLE bus_offsets (
  object_id TEXT NOT NULL REFERENCES data_objects(id) ON DELETE CASCADE,
  consumer TEXT NOT NULL,
  last_id BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (object_id, consumer)
);

ALTER TABLE bus_offsets ENABLE ROW LEVEL SECURITY;
CREATE POLICY bus_offsets_isolation ON bus_offsets
  USING (object_id = ANY (string_to_array(current_setting('syna.object_ids', true), ',')));

CREATE TABLE schedule_entries (
  object_id TEXT NOT NULL REFERENCES data_objects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  cron_expr TEXT NOT NULL,
  task TEXT NOT NULL,
  data JSONB NOT NULL DEFAULT '{}'::jsonb,
  next_run_at TIMESTAMPTZ NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT true,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (object_id, name)
);

ALTER TABLE schedule_entries ENABLE ROW LEVEL SECURITY;
CREATE POLICY schedule_entries_isolation ON schedule_entries
  USING (object_id = ANY (string_to_array(current_setting('syna.object_ids', true), ',')));

CREATE TABLE schedule_deliveries (
  id BIGSERIAL PRIMARY KEY,
  object_id TEXT NOT NULL REFERENCES data_objects(id) ON DELETE CASCADE,
  entry_name TEXT NOT NULL,
  run_at TIMESTAMPTZ NOT NULL,
  task TEXT NOT NULL,
  data JSONB NOT NULL DEFAULT '{}'::jsonb,
  claimed_by TEXT,
  claimed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_schedule_deliveries_wait ON schedule_deliveries (object_id, created_at)
  WHERE claimed_by IS NULL;

ALTER TABLE schedule_deliveries ENABLE ROW LEVEL SECURITY;
CREATE POLICY schedule_deliveries_isolation ON schedule_deliveries
  USING (object_id = ANY (string_to_array(current_setting('syna.object_ids', true), ',')));

CREATE TABLE table_registry (
  object_id TEXT PRIMARY KEY REFERENCES data_objects(id) ON DELETE CASCADE,
  schema_name TEXT NOT NULL,
  table_name TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);
