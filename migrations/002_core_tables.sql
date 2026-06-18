CREATE TABLE IF NOT EXISTS feishu_events (
  id BIGSERIAL PRIMARY KEY,
  event_id TEXT NOT NULL UNIQUE,
  event_type TEXT NOT NULL,
  schema_version TEXT,
  raw_payload JSONB NOT NULL,
  received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  processed_at TIMESTAMPTZ,
  process_status TEXT NOT NULL DEFAULT 'pending',
  process_error TEXT
);

CREATE TABLE IF NOT EXISTS chats (
  id BIGSERIAL PRIMARY KEY,
  feishu_chat_id TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  chat_type TEXT,
  enabled BOOLEAN NOT NULL DEFAULT false,
  sync_enabled BOOLEAN NOT NULL DEFAULT false,
  auto_reply_enabled BOOLEAN NOT NULL DEFAULT false,
  trigger_mode TEXT NOT NULL DEFAULT 'mention_bot',
  knowledge_scope TEXT NOT NULL DEFAULT 'current_chat',
  last_synced_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS messages (
  id BIGSERIAL PRIMARY KEY,
  feishu_message_id TEXT NOT NULL UNIQUE,
  feishu_chat_id TEXT NOT NULL,
  feishu_sender_id TEXT,
  message_type TEXT NOT NULL,
  content_text TEXT,
  raw_content JSONB,
  raw_payload JSONB NOT NULL,
  sent_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_messages_chat_sent_at ON messages (feishu_chat_id, sent_at DESC);

CREATE TABLE IF NOT EXISTS sync_jobs (
  id BIGSERIAL PRIMARY KEY,
  job_type TEXT NOT NULL,
  chat_id BIGINT REFERENCES chats(id),
  status TEXT NOT NULL DEFAULT 'pending',
  cursor TEXT,
  started_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ,
  error TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS knowledge_chunks (
  id BIGSERIAL PRIMARY KEY,
  source_type TEXT NOT NULL,
  source_id TEXT NOT NULL,
  chat_id BIGINT REFERENCES chats(id),
  content TEXT NOT NULL,
  embedding VECTOR(1536),
  token_count INT,
  metadata JSONB,
  visibility_scope TEXT NOT NULL DEFAULT 'current_chat',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (source_type, source_id)
);

CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_chat_id ON knowledge_chunks (chat_id);

CREATE TABLE IF NOT EXISTS reply_logs (
  id BIGSERIAL PRIMARY KEY,
  incoming_message_id BIGINT REFERENCES messages(id),
  feishu_chat_id TEXT NOT NULL,
  query TEXT NOT NULL,
  generated_answer TEXT,
  sent_answer TEXT,
  confidence NUMERIC(5,4),
  status TEXT NOT NULL DEFAULT 'pending',
  sources JSONB,
  error TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  sent_at TIMESTAMPTZ
);
