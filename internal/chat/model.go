package chat

import "time"

type Chat struct {
	ID               int64      `json:"id"`
	FeishuChatID     string     `json:"feishu_chat_id"`
	Name             string     `json:"name"`
	ChatType         string     `json:"chat_type"`
	Enabled          bool       `json:"enabled"`
	SyncEnabled      bool       `json:"sync_enabled"`
	AutoReplyEnabled bool       `json:"auto_reply_enabled"`
	TriggerMode      string     `json:"trigger_mode"`
	KnowledgeScope   string     `json:"knowledge_scope"`
	LastSyncedAt     *time.Time `json:"last_synced_at"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type UpsertRequest struct {
	FeishuChatID     string `json:"feishu_chat_id" binding:"required"`
	Name             string `json:"name" binding:"required"`
	ChatType         string `json:"chat_type"`
	Enabled          bool   `json:"enabled"`
	SyncEnabled      bool   `json:"sync_enabled"`
	AutoReplyEnabled bool   `json:"auto_reply_enabled"`
	TriggerMode      string `json:"trigger_mode"`
	KnowledgeScope   string `json:"knowledge_scope"`
}
