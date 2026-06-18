CREATE TABLE IF NOT EXISTS knowledge_unit_messages (
  knowledge_unit_id BIGINT NOT NULL REFERENCES knowledge_units(id) ON DELETE CASCADE,
  message_id BIGINT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  PRIMARY KEY (knowledge_unit_id, message_id)
);

ALTER TABLE knowledge_chunks
  ALTER COLUMN embedding TYPE VECTOR(1536);

CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_embedding_cosine
  ON knowledge_chunks
  USING ivfflat (embedding vector_cosine_ops)
  WITH (lists = 100);

CREATE TABLE IF NOT EXISTS qa_logs (
  id BIGSERIAL PRIMARY KEY,
  question TEXT NOT NULL,
  answer TEXT,
  model TEXT,
  retrieved_chunks JSONB,
  status TEXT NOT NULL DEFAULT 'pending',
  error TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  answered_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_qa_logs_created_at ON qa_logs (created_at DESC);
