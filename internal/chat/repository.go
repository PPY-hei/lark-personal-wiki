package chat

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func (r *Repository) List(ctx context.Context) ([]Chat, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, feishu_chat_id, name, chat_type, enabled, sync_enabled, auto_reply_enabled,
		       trigger_mode, knowledge_scope, last_synced_at, created_at, updated_at
		FROM chats
		ORDER BY id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list chats: %w", err)
	}
	defer rows.Close()

	var chats []Chat
	for rows.Next() {
		var item Chat
		if err := rows.Scan(
			&item.ID,
			&item.FeishuChatID,
			&item.Name,
			&item.ChatType,
			&item.Enabled,
			&item.SyncEnabled,
			&item.AutoReplyEnabled,
			&item.TriggerMode,
			&item.KnowledgeScope,
			&item.LastSyncedAt,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan chat: %w", err)
		}
		chats = append(chats, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate chats: %w", err)
	}
	return chats, nil
}

func (r *Repository) Upsert(ctx context.Context, req UpsertRequest) (Chat, error) {
	triggerMode := req.TriggerMode
	if triggerMode == "" {
		triggerMode = "mention_bot"
	}
	knowledgeScope := req.KnowledgeScope
	if knowledgeScope == "" {
		knowledgeScope = "current_chat"
	}

	var item Chat
	err := r.db.QueryRow(ctx, `
		INSERT INTO chats (
			feishu_chat_id, name, chat_type, enabled, sync_enabled, auto_reply_enabled, trigger_mode, knowledge_scope
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (feishu_chat_id) DO UPDATE SET
			name = EXCLUDED.name,
			chat_type = EXCLUDED.chat_type,
			enabled = EXCLUDED.enabled,
			sync_enabled = EXCLUDED.sync_enabled,
			auto_reply_enabled = EXCLUDED.auto_reply_enabled,
			trigger_mode = EXCLUDED.trigger_mode,
			knowledge_scope = EXCLUDED.knowledge_scope,
			updated_at = now()
		RETURNING id, feishu_chat_id, name, chat_type, enabled, sync_enabled, auto_reply_enabled,
		          trigger_mode, knowledge_scope, last_synced_at, created_at, updated_at
	`, req.FeishuChatID, req.Name, req.ChatType, req.Enabled, req.SyncEnabled, req.AutoReplyEnabled, triggerMode, knowledgeScope).Scan(
		&item.ID,
		&item.FeishuChatID,
		&item.Name,
		&item.ChatType,
		&item.Enabled,
		&item.SyncEnabled,
		&item.AutoReplyEnabled,
		&item.TriggerMode,
		&item.KnowledgeScope,
		&item.LastSyncedAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err != nil {
		return Chat{}, fmt.Errorf("upsert chat: %w", err)
	}
	return item, nil
}
