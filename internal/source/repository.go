package source

import (
	"context"
	"fmt"
	"strings"

	"feishu-kb-assistant/internal/chat"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db       *pgxpool.Pool
	chatRepo *chat.Repository
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{
		db:       db,
		chatRepo: chat.NewRepository(db),
	}
}

func (r *Repository) CacheChats(ctx context.Context, items []RemoteChat) error {
	for _, item := range items {
		if item.ChatID == "" || item.Name == "" {
			continue
		}
		if _, err := r.db.Exec(ctx, `
			INSERT INTO chats (
				feishu_chat_id, name, chat_type, enabled, sync_enabled, auto_reply_enabled, trigger_mode, knowledge_scope
			)
			VALUES ($1, $2, 'group', false, false, false, 'mention_bot', 'current_chat')
			ON CONFLICT (feishu_chat_id) DO UPDATE SET
				name = EXCLUDED.name,
				chat_type = COALESCE(NULLIF(chats.chat_type, ''), EXCLUDED.chat_type),
				updated_at = now()
		`, item.ChatID, item.Name); err != nil {
			return fmt.Errorf("cache chat %s: %w", item.ChatID, err)
		}
	}
	return nil
}

func (r *Repository) ListCachedChats(ctx context.Context) ([]RemoteChat, error) {
	rows, err := r.db.Query(ctx, `
		SELECT feishu_chat_id, name, chat_type, enabled
		FROM chats
		ORDER BY updated_at DESC, id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list cached chats: %w", err)
	}
	defer rows.Close()

	items := make([]RemoteChat, 0)
	for rows.Next() {
		var item RemoteChat
		var chatType string
		if err := rows.Scan(&item.ChatID, &item.Name, &chatType, &item.Selected); err != nil {
			return nil, fmt.Errorf("scan cached chat: %w", err)
		}
		item.ChatMode = chatType
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cached chats: %w", err)
	}
	return items, nil
}

func (r *Repository) ListSelectedChats(ctx context.Context) ([]RemoteChat, error) {
	rows, err := r.db.Query(ctx, `
		SELECT feishu_chat_id, name, chat_type, enabled
		FROM chats
		WHERE enabled=true
		ORDER BY updated_at DESC, id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list selected chats: %w", err)
	}
	defer rows.Close()

	items := make([]RemoteChat, 0)
	for rows.Next() {
		var item RemoteChat
		var chatType string
		if err := rows.Scan(&item.ChatID, &item.Name, &chatType, &item.Selected); err != nil {
			return nil, fmt.Errorf("scan selected chat: %w", err)
		}
		item.ChatMode = chatType
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate selected chats: %w", err)
	}
	return items, nil
}

func (r *Repository) SaveSelectedChats(ctx context.Context, items []RemoteChat) error {
	if _, err := r.db.Exec(ctx, `UPDATE chats SET enabled=false, sync_enabled=false, updated_at=now()`); err != nil {
		return fmt.Errorf("clear selected chats: %w", err)
	}
	for _, item := range items {
		if item.ChatID == "" || item.Name == "" {
			continue
		}
		_, err := r.chatRepo.Upsert(ctx, chat.UpsertRequest{
			FeishuChatID:     item.ChatID,
			Name:             item.Name,
			ChatType:         "group",
			Enabled:          true,
			SyncEnabled:      true,
			AutoReplyEnabled: false,
			TriggerMode:      "mention_bot",
			KnowledgeScope:   "current_chat",
		})
		if err != nil {
			return fmt.Errorf("save selected chat %s: %w", item.ChatID, err)
		}
	}
	return nil
}

func (r *Repository) SaveSelectedContacts(ctx context.Context, items []RemoteContact) error {
	if _, err := r.db.Exec(ctx, `UPDATE contacts SET selected=false, updated_at=now()`); err != nil {
		return fmt.Errorf("clear selected contacts: %w", err)
	}
	for _, item := range items {
		if item.OpenID == "" && item.UserID == "" {
			continue
		}
		if item.Name == "" {
			item.Name = item.OpenID
		}
		if _, err := r.db.Exec(ctx, `
			INSERT INTO contacts (feishu_open_id, feishu_user_id, union_id, name, email, feishu_chat_id, selected, raw_payload)
			VALUES ($1, $2, $3, $4, $5, $6, true, $7)
			ON CONFLICT (feishu_open_id) DO UPDATE SET
				feishu_user_id = EXCLUDED.feishu_user_id,
				union_id = EXCLUDED.union_id,
				name = EXCLUDED.name,
				email = EXCLUDED.email,
				feishu_chat_id = COALESCE(NULLIF(EXCLUDED.feishu_chat_id, ''), contacts.feishu_chat_id),
				selected = true,
				raw_payload = EXCLUDED.raw_payload,
				updated_at = now()
		`, item.OpenID, item.UserID, item.UnionID, item.Name, item.Email, item.ChatID, item.RawPayload); err != nil {
			return fmt.Errorf("save selected contact %s: %w", item.OpenID, err)
		}
	}
	return nil
}

func (r *Repository) CacheContacts(ctx context.Context, items []RemoteContact) error {
	for _, item := range items {
		if item.OpenID == "" && item.UserID == "" {
			continue
		}
		if item.Name == "" {
			item.Name = item.OpenID
		}
		if _, err := r.db.Exec(ctx, `
			INSERT INTO contacts (feishu_open_id, feishu_user_id, union_id, name, email, feishu_chat_id, selected, raw_payload)
			VALUES ($1, $2, $3, $4, $5, $6, false, $7)
			ON CONFLICT (feishu_open_id) DO UPDATE SET
				feishu_user_id = EXCLUDED.feishu_user_id,
				union_id = EXCLUDED.union_id,
				name = EXCLUDED.name,
				email = EXCLUDED.email,
				feishu_chat_id = COALESCE(NULLIF(EXCLUDED.feishu_chat_id, ''), contacts.feishu_chat_id),
				raw_payload = EXCLUDED.raw_payload,
				updated_at = now()
		`, item.OpenID, item.UserID, item.UnionID, item.Name, item.Email, item.ChatID, item.RawPayload); err != nil {
			return fmt.Errorf("cache contact %s: %w", item.OpenID, err)
		}
	}
	return nil
}

func (r *Repository) ListCachedContacts(ctx context.Context) ([]RemoteContact, error) {
	rows, err := r.db.Query(ctx, `
		SELECT COALESCE(feishu_open_id, ''), COALESCE(feishu_user_id, ''), COALESCE(union_id, ''),
		       name, COALESCE(email, ''), COALESCE(feishu_chat_id, ''), selected
		FROM contacts
		ORDER BY updated_at DESC, id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list cached contacts: %w", err)
	}
	defer rows.Close()

	items := make([]RemoteContact, 0)
	for rows.Next() {
		var item RemoteContact
		if err := rows.Scan(&item.OpenID, &item.UserID, &item.UnionID, &item.Name, &item.Email, &item.ChatID, &item.Selected); err != nil {
			return nil, fmt.Errorf("scan cached contact: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cached contacts: %w", err)
	}
	return items, nil
}

func (r *Repository) SearchCachedContacts(ctx context.Context, query string) ([]RemoteContact, error) {
	items, err := r.ListCachedContacts(ctx)
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return items, nil
	}
	filtered := make([]RemoteContact, 0, len(items))
	for _, item := range items {
		if strings.Contains(strings.ToLower(item.Name), query) ||
			strings.Contains(strings.ToLower(item.OpenID), query) ||
			strings.Contains(strings.ToLower(item.UserID), query) ||
			strings.Contains(strings.ToLower(item.UnionID), query) ||
			strings.Contains(strings.ToLower(item.Email), query) ||
			strings.Contains(strings.ToLower(item.ChatID), query) {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func (r *Repository) ListSelectedContacts(ctx context.Context) ([]RemoteContact, error) {
	rows, err := r.db.Query(ctx, `
		SELECT COALESCE(feishu_open_id, ''), COALESCE(feishu_user_id, ''), COALESCE(union_id, ''),
		       name, COALESCE(email, ''), COALESCE(feishu_chat_id, ''), selected
		FROM contacts
		WHERE selected=true
		ORDER BY updated_at DESC, id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list selected contacts: %w", err)
	}
	defer rows.Close()

	items := make([]RemoteContact, 0)
	for rows.Next() {
		var item RemoteContact
		if err := rows.Scan(&item.OpenID, &item.UserID, &item.UnionID, &item.Name, &item.Email, &item.ChatID, &item.Selected); err != nil {
			return nil, fmt.Errorf("scan selected contact: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate selected contacts: %w", err)
	}
	return items, nil
}

func (r *Repository) SaveContactChatID(ctx context.Context, openID string, chatID string) error {
	if openID == "" || chatID == "" {
		return nil
	}
	if _, err := r.db.Exec(ctx, `
		UPDATE contacts
		SET feishu_chat_id=$2, updated_at=now()
		WHERE feishu_open_id=$1
	`, openID, chatID); err != nil {
		return fmt.Errorf("save contact chat id: %w", err)
	}
	return nil
}
