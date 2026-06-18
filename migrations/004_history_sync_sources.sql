ALTER TABLE contacts
  ADD COLUMN IF NOT EXISTS feishu_chat_id TEXT;

CREATE INDEX IF NOT EXISTS idx_contacts_feishu_chat_id ON contacts (feishu_chat_id);

ALTER TABLE sync_jobs
  ADD COLUMN IF NOT EXISTS source_type TEXT,
  ADD COLUMN IF NOT EXISTS source_id TEXT,
  ADD COLUMN IF NOT EXISTS message_count INT NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_sync_jobs_created_at ON sync_jobs (created_at DESC);
