package message

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

func (r *Repository) SaveEvent(ctx context.Context, event FeishuEvent) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO feishu_events (event_id, event_type, schema_version, raw_payload, processed_at, process_status)
		VALUES ($1, $2, $3, $4, now(), 'processed')
		ON CONFLICT (event_id) DO NOTHING
	`, event.EventID, event.EventType, event.SchemaVersion, event.RawPayload)
	if err != nil {
		return fmt.Errorf("insert feishu event: %w", err)
	}
	return nil
}

func (r *Repository) SaveMessage(ctx context.Context, msg Message) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO messages (
			feishu_message_id,
			feishu_chat_id,
			feishu_sender_id,
			message_type,
			content_text,
			raw_content,
			raw_payload,
			sent_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (feishu_message_id) DO UPDATE SET
			feishu_chat_id = EXCLUDED.feishu_chat_id,
			feishu_sender_id = EXCLUDED.feishu_sender_id,
			message_type = EXCLUDED.message_type,
			content_text = EXCLUDED.content_text,
			raw_content = EXCLUDED.raw_content,
			raw_payload = EXCLUDED.raw_payload,
			sent_at = EXCLUDED.sent_at
	`, msg.FeishuMessageID, msg.FeishuChatID, msg.FeishuSenderID, msg.MessageType, msg.ContentText, msg.RawContent, msg.RawPayload, msg.SentAt)
	if err != nil {
		return fmt.Errorf("upsert message: %w", err)
	}
	return nil
}

func (r *Repository) CountMessages(ctx context.Context) (int64, error) {
	var count int64
	if err := r.db.QueryRow(ctx, "SELECT count(*) FROM messages").Scan(&count); err != nil {
		return 0, fmt.Errorf("count messages: %w", err)
	}
	return count, nil
}
