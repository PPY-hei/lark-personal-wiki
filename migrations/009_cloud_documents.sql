CREATE TABLE IF NOT EXISTS cloud_documents (
  id BIGSERIAL PRIMARY KEY,
  docs_token TEXT NOT NULL,
  docs_type TEXT NOT NULL,
  title TEXT NOT NULL,
  owner_id TEXT,
  url TEXT,
  selected BOOLEAN NOT NULL DEFAULT false,
  raw_payload JSONB,
  last_synced_at TIMESTAMPTZ,
  last_sync_error TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (docs_token, docs_type)
);

CREATE INDEX IF NOT EXISTS idx_cloud_documents_selected ON cloud_documents (selected);
