-- Agent public keys (client custody only; private keys never stored).
CREATE TABLE agent_keys (
  key_id TEXT PRIMARY KEY,
  subject_key TEXT NOT NULL,
  token_id TEXT,
  algorithm TEXT NOT NULL DEFAULT 'ed25519',
  public_key BYTEA NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  revoked_at TIMESTAMPTZ
);

CREATE INDEX idx_agent_keys_subject ON agent_keys (subject_key);
CREATE INDEX idx_agent_keys_token ON agent_keys (token_id) WHERE token_id IS NOT NULL;

-- Versioned, server-signed policy documents per securebus object.
CREATE TABLE securebus_policies (
  object_id TEXT NOT NULL REFERENCES data_objects(id) ON DELETE CASCADE,
  version INT NOT NULL,
  document JSONB NOT NULL,
  hash BYTEA NOT NULL,
  signature BYTEA NOT NULL,
  signer_kid TEXT NOT NULL,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (object_id, version)
);

ALTER TABLE securebus_policies ENABLE ROW LEVEL SECURITY;
CREATE POLICY securebus_policies_isolation ON securebus_policies
  USING (object_id = ANY (string_to_array(current_setting('syna.object_ids', true), ',')));

CREATE TABLE securebus_messages (
  id BIGSERIAL PRIMARY KEY,
  object_id TEXT NOT NULL REFERENCES data_objects(id) ON DELETE CASCADE,
  author_key_id TEXT NOT NULL REFERENCES agent_keys(key_id),
  policy_version INT NOT NULL,
  policy_hash BYTEA NOT NULL,
  nonce TEXT NOT NULL,
  payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  hash BYTEA NOT NULL,
  prev_hash BYTEA,
  signature BYTEA NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  UNIQUE (object_id, nonce)
);

CREATE INDEX idx_securebus_messages_object ON securebus_messages (object_id, id);

ALTER TABLE securebus_messages ENABLE ROW LEVEL SECURITY;
CREATE POLICY securebus_messages_isolation ON securebus_messages
  USING (object_id = ANY (string_to_array(current_setting('syna.object_ids', true), ',')));

CREATE TABLE securebus_attestations (
  id BIGSERIAL PRIMARY KEY,
  object_id TEXT NOT NULL REFERENCES data_objects(id) ON DELETE CASCADE,
  message_id BIGINT NOT NULL REFERENCES securebus_messages(id) ON DELETE CASCADE,
  signer_key_id TEXT NOT NULL REFERENCES agent_keys(key_id),
  policy_version INT NOT NULL,
  status TEXT NOT NULL,
  comment TEXT NOT NULL DEFAULT '',
  message_hash BYTEA NOT NULL,
  signature BYTEA NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  superseded_at TIMESTAMPTZ
);

CREATE INDEX idx_securebus_attestations_message ON securebus_attestations (message_id, created_at DESC)
  WHERE superseded_at IS NULL;
CREATE INDEX idx_securebus_attestations_object ON securebus_attestations (object_id, id);

ALTER TABLE securebus_attestations ENABLE ROW LEVEL SECURITY;
CREATE POLICY securebus_attestations_isolation ON securebus_attestations
  USING (object_id = ANY (string_to_array(current_setting('syna.object_ids', true), ',')));

CREATE TABLE securebus_events (
  id BIGSERIAL PRIMARY KEY,
  object_id TEXT NOT NULL REFERENCES data_objects(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  message_id BIGINT NOT NULL REFERENCES securebus_messages(id) ON DELETE CASCADE,
  attestation_id BIGINT REFERENCES securebus_attestations(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_securebus_events_object ON securebus_events (object_id, id);

ALTER TABLE securebus_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY securebus_events_isolation ON securebus_events
  USING (object_id = ANY (string_to_array(current_setting('syna.object_ids', true), ',')));

CREATE TABLE securebus_subscriptions (
  object_id TEXT NOT NULL REFERENCES data_objects(id) ON DELETE CASCADE,
  consumer TEXT NOT NULL,
  filter JSONB NOT NULL DEFAULT '{}'::jsonb,
  last_event_id BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (object_id, consumer)
);

ALTER TABLE securebus_subscriptions ENABLE ROW LEVEL SECURITY;
CREATE POLICY securebus_subscriptions_isolation ON securebus_subscriptions
  USING (object_id = ANY (string_to_array(current_setting('syna.object_ids', true), ',')));

CREATE TABLE securebus_deliveries (
  object_id TEXT NOT NULL REFERENCES data_objects(id) ON DELETE CASCADE,
  consumer TEXT NOT NULL,
  message_id BIGINT NOT NULL REFERENCES securebus_messages(id) ON DELETE CASCADE,
  delivered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (object_id, consumer, message_id)
);

ALTER TABLE securebus_deliveries ENABLE ROW LEVEL SECURITY;
CREATE POLICY securebus_deliveries_isolation ON securebus_deliveries
  USING (object_id = ANY (string_to_array(current_setting('syna.object_ids', true), ',')));
