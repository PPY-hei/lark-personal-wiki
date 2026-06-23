package message

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
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

func (r *Repository) SaveMessageIfNew(ctx context.Context, msg Message) (bool, error) {
	tag, err := r.db.Exec(ctx, `
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
		ON CONFLICT (feishu_message_id) DO NOTHING
	`, msg.FeishuMessageID, msg.FeishuChatID, msg.FeishuSenderID, msg.MessageType, msg.ContentText, msg.RawContent, msg.RawPayload, msg.SentAt)
	if err != nil {
		return false, fmt.Errorf("insert new message: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (r *Repository) FindByFeishuMessageID(ctx context.Context, feishuMessageID string) (Message, bool, error) {
	var msg Message
	err := r.db.QueryRow(ctx, `
		SELECT
			feishu_message_id,
			feishu_chat_id,
			COALESCE(feishu_sender_id, ''),
			message_type,
			COALESCE(content_text, ''),
			raw_content,
			raw_payload,
			sent_at
		FROM messages
		WHERE feishu_message_id=$1
	`, feishuMessageID).Scan(
		&msg.FeishuMessageID,
		&msg.FeishuChatID,
		&msg.FeishuSenderID,
		&msg.MessageType,
		&msg.ContentText,
		&msg.RawContent,
		&msg.RawPayload,
		&msg.SentAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Message{}, false, nil
		}
		return Message{}, false, fmt.Errorf("find message: %w", err)
	}
	return msg, true, nil
}

func (r *Repository) RecentMessagesByChatSender(ctx context.Context, chatID string, senderID string, since time.Time, until time.Time, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.Query(ctx, `
		SELECT
			feishu_message_id,
			feishu_chat_id,
			COALESCE(feishu_sender_id, ''),
			message_type,
			COALESCE(content_text, ''),
			raw_content,
			raw_payload,
			sent_at
		FROM messages
		WHERE feishu_chat_id=$1
		  AND COALESCE(feishu_sender_id, '')=$2
		  AND COALESCE(sent_at, created_at) >= $3
		  AND COALESCE(sent_at, created_at) <= $4
		  AND NULLIF(trim(COALESCE(content_text, '')), '') IS NOT NULL
		ORDER BY COALESCE(sent_at, created_at), id
		LIMIT $5
	`, chatID, senderID, since, until, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent chat sender messages: %w", err)
	}
	defer rows.Close()

	items := make([]Message, 0)
	for rows.Next() {
		var msg Message
		if err := rows.Scan(
			&msg.FeishuMessageID,
			&msg.FeishuChatID,
			&msg.FeishuSenderID,
			&msg.MessageType,
			&msg.ContentText,
			&msg.RawContent,
			&msg.RawPayload,
			&msg.SentAt,
		); err != nil {
			return nil, fmt.Errorf("scan recent chat sender message: %w", err)
		}
		items = append(items, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent chat sender messages: %w", err)
	}
	return items, nil
}

func (r *Repository) CountMessages(ctx context.Context) (int64, error) {
	var count int64
	if err := r.db.QueryRow(ctx, "SELECT count(*) FROM messages").Scan(&count); err != nil {
		return 0, fmt.Errorf("count messages: %w", err)
	}
	return count, nil
}

func (r *Repository) CountMessagesByChat(ctx context.Context, feishuChatID string) (int64, error) {
	var count int64
	if err := r.db.QueryRow(ctx, "SELECT count(*) FROM messages WHERE feishu_chat_id=$1", feishuChatID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count messages by chat: %w", err)
	}
	return count, nil
}

func (r *Repository) AutoReplyAlreadySent(ctx context.Context, feishuMessageID string) (bool, error) {
	var exists bool
	if err := r.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM reply_logs rl
			JOIN messages m ON m.id = rl.incoming_message_id
			WHERE m.feishu_message_id=$1
			  AND rl.status='sent'
		)
	`, feishuMessageID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check auto reply sent: %w", err)
	}
	return exists, nil
}

func (r *Repository) SaveAutoReplyResult(ctx context.Context, msg Message, query string, answer string, status string, replyErr error) error {
	errText := ""
	if replyErr != nil {
		errText = replyErr.Error()
	}
	sentAt := any(nil)
	if status == "sent" {
		sentAt = "now()"
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO reply_logs (
			incoming_message_id,
			feishu_chat_id,
			query,
			generated_answer,
			sent_answer,
			status,
			error,
			sent_at
		)
		VALUES (
			(SELECT id FROM messages WHERE feishu_message_id=$1),
			$2,
			$3,
			NULLIF($4, ''),
			NULLIF($4, ''),
			$5,
			NULLIF($6, ''),
			CASE WHEN $7::boolean THEN now() ELSE NULL END
		)
	`, msg.FeishuMessageID, msg.FeishuChatID, query, answer, status, errText, sentAt != nil)
	if err != nil {
		return fmt.Errorf("save auto reply result: %w", err)
	}
	return nil
}
