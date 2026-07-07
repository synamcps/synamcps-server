CREATE TABLE knowledge_ingest_jobs (
  id TEXT PRIMARY KEY,
  doc_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  status TEXT NOT NULL,
  attempts INT NOT NULL DEFAULT 0,
  max_attempts INT NOT NULL DEFAULT 3,
  last_error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  started_at TIMESTAMPTZ,
  completed_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX idx_knowledge_ingest_jobs_active_doc
  ON knowledge_ingest_jobs (doc_id, kind)
  WHERE status IN ('pending', 'running');

CREATE INDEX idx_knowledge_ingest_jobs_pending
  ON knowledge_ingest_jobs (status, created_at);
