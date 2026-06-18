CREATE TABLE IF NOT EXISTS feishu_auth_sessions (
  id BIGSERIAL PRIMARY KEY,
  open_id TEXT,
  union_id TEXT,
  user_id TEXT,
  name TEXT,
  email TEXT,
  tenant_key TEXT,
  access_token TEXT NOT NULL,
  refresh_token TEXT,
  access_token_expires_at TIMESTAMPTZ,
  refresh_token_expires_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_feishu_auth_sessions_created_at ON feishu_auth_sessions (created_at DESC);

CREATE TABLE IF NOT EXISTS contacts (
  id BIGSERIAL PRIMARY KEY,
  feishu_open_id TEXT,
  feishu_user_id TEXT,
  union_id TEXT,
  name TEXT NOT NULL,
  email TEXT,
  selected BOOLEAN NOT NULL DEFAULT false,
  raw_payload JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (feishu_open_id)
);

CREATE INDEX IF NOT EXISTS idx_contacts_selected ON contacts (selected);

CREATE TABLE IF NOT EXISTS knowledge_units (
  id BIGSERIAL PRIMARY KEY,
  source_type TEXT NOT NULL,
  source_id TEXT NOT NULL,
  chat_id BIGINT REFERENCES chats(id),
  unit_date DATE,
  title TEXT,
  content TEXT NOT NULL,
  metadata JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (source_type, source_id)
);

CREATE INDEX IF NOT EXISTS idx_knowledge_units_chat_date ON knowledge_units (chat_id, unit_date DESC);
