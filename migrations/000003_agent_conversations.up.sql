CREATE TABLE agent_conversations (
  id TEXT PRIMARY KEY,
  owner_subject_key TEXT NOT NULL,
  title TEXT NOT NULL,
  dataset_storage_ids TEXT[] NOT NULL DEFAULT '{}',
  status TEXT NOT NULL DEFAULT 'active',
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_agent_conversations_owner_updated
  ON agent_conversations (owner_subject_key, updated_at DESC);

CREATE TABLE agent_messages (
  id TEXT PRIMARY KEY,
  conversation_id TEXT NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
  role TEXT NOT NULL,
  content TEXT NOT NULL,
  model TEXT NOT NULL DEFAULT '',
  document_refs_json TEXT NOT NULL DEFAULT '[]',
  input_tokens INT NOT NULL DEFAULT 0,
  output_tokens INT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_agent_messages_conversation_created
  ON agent_messages (conversation_id, created_at ASC);
