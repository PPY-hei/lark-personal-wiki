package syncer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"feishu-kb-assistant/internal/message"
	"feishu-kb-assistant/internal/source"

	"github.com/jackc/pgx/v5/pgxpool"
)

type FeishuClient interface {
	ListUserP2PChats(ctx context.Context, userAccessToken string) ([]source.RemoteChat, error)
	ListHistoryMessages(ctx context.Context, accessToken string, chatID string, start time.Time, end time.Time) ([]source.RemoteMessage, error)
}

type SourceRepository interface {
	ListSelectedChats(ctx context.Context) ([]source.RemoteChat, error)
	ListSelectedContacts(ctx context.Context) ([]source.RemoteContact, error)
	SaveContactChatID(ctx context.Context, openID string, chatID string) error
}

type MessageRepository interface {
	SaveMessage(ctx context.Context, msg message.Message) error
	CountMessagesByChat(ctx context.Context, feishuChatID string) (int64, error)
}

type Runner struct {
	db        *pgxpool.Pool
	feishu    FeishuClient
	source    SourceRepository
	messages  MessageRepository
	tokenFunc func(context.Context) (string, error)
}

type Result struct {
	SyncedSources   int            `json:"synced_sources"`
	SavedMessages   int            `json:"saved_messages"`
	SkippedContacts []SkippedItem  `json:"skipped_contacts,omitempty"`
	Sources         []SourceResult `json:"sources"`
}

type SourceResult struct {
	Type          string `json:"type"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	MessageCount  int    `json:"message_count"`
	TotalMessages int64  `json:"total_messages"`
	Error         string `json:"error,omitempty"`
}

type SkippedItem struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

func NewRunner(
	db *pgxpool.Pool,
	feishu FeishuClient,
	sourceRepo SourceRepository,
	messageRepo MessageRepository,
	tokenFunc func(context.Context) (string, error),
) *Runner {
	return &Runner{
		db:        db,
		feishu:    feishu,
		source:    sourceRepo,
		messages:  messageRepo,
		tokenFunc: tokenFunc,
	}
}

func (r *Runner) SyncSelectedHistory(ctx context.Context, days int) (Result, error) {
	if days <= 0 {
		days = 30
	}
	if days > 180 {
		days = 180
	}

	token, err := r.tokenFunc(ctx)
	if err != nil {
		return Result{}, err
	}

	end := time.Now()
	start := end.AddDate(0, 0, -days)
	result := Result{
		Sources:         make([]SourceResult, 0),
		SkippedContacts: make([]SkippedItem, 0),
	}

	chats, err := r.source.ListSelectedChats(ctx)
	if err != nil {
		return Result{}, err
	}
	for _, chat := range chats {
		sourceResult := r.syncChat(ctx, token, "chat", chat.ChatID, chat.Name, start, end)
		result.Sources = append(result.Sources, sourceResult)
		if sourceResult.Error == "" {
			result.SyncedSources++
			result.SavedMessages += sourceResult.MessageCount
		}
	}

	contacts, err := r.source.ListSelectedContacts(ctx)
	if err != nil {
		return Result{}, err
	}
	if len(contacts) > 0 {
		contacts = r.resolveP2PChatIDs(ctx, token, contacts)
	}
	for _, contact := range contacts {
		if strings.TrimSpace(contact.ChatID) == "" {
			result.SkippedContacts = append(result.SkippedContacts, SkippedItem{
				ID:     firstNonEmpty(contact.OpenID, contact.UserID),
				Name:   contact.Name,
				Reason: "missing_single_chat_id",
			})
			continue
		}
		sourceResult := r.syncChat(ctx, token, "contact", contact.ChatID, contact.Name, start, end)
		result.Sources = append(result.Sources, sourceResult)
		if sourceResult.Error == "" {
			result.SyncedSources++
			result.SavedMessages += sourceResult.MessageCount
		}
	}

	return result, nil
}

func (r *Runner) resolveP2PChatIDs(ctx context.Context, accessToken string, contacts []source.RemoteContact) []source.RemoteContact {
	chats, err := r.feishu.ListUserP2PChats(ctx, accessToken)
	if err != nil {
		return contacts
	}
	for i, contact := range contacts {
		if contact.ChatID != "" {
			continue
		}
		chatID := matchP2PChat(contact, chats)
		if chatID == "" {
			continue
		}
		contacts[i].ChatID = chatID
		if contact.OpenID != "" {
			_ = r.source.SaveContactChatID(ctx, contact.OpenID, chatID)
		}
	}
	return contacts
}

func matchP2PChat(contact source.RemoteContact, chats []source.RemoteChat) string {
	name := normalizeName(contact.Name)
	if name == "" {
		return ""
	}
	for _, chat := range chats {
		chatName := normalizeName(chat.Name)
		if chat.ChatID == "" || chatName == "" {
			continue
		}
		if chatName == name || strings.Contains(chatName, name) || strings.Contains(name, chatName) {
			return chat.ChatID
		}
	}
	return ""
}

func normalizeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "")
	value = strings.ReplaceAll(value, "　", "")
	return value
}

func (r *Runner) syncChat(ctx context.Context, accessToken string, sourceType string, chatID string, name string, start time.Time, end time.Time) SourceResult {
	sourceResult := SourceResult{
		Type: sourceType,
		ID:   chatID,
		Name: name,
	}

	jobID, err := r.createJob(ctx, sourceType, chatID)
	if err != nil {
		sourceResult.Error = err.Error()
		return sourceResult
	}

	items, err := r.feishu.ListHistoryMessages(ctx, accessToken, chatID, start, end)
	if err != nil {
		sourceResult.Error = err.Error()
		_ = r.finishJob(ctx, jobID, "failed", 0, err)
		return sourceResult
	}

	for _, item := range items {
		if item.MessageID == "" {
			continue
		}
		if item.ChatID == "" {
			item.ChatID = chatID
		}
		if err := r.messages.SaveMessage(ctx, message.Message{
			FeishuMessageID: item.MessageID,
			FeishuChatID:    item.ChatID,
			FeishuSenderID:  item.SenderID,
			MessageType:     firstNonEmpty(item.MessageType, "unknown"),
			ContentText:     item.ContentText,
			RawContent:      item.RawContent,
			RawPayload:      item.RawPayload,
			SentAt:          item.SentAt,
		}); err != nil {
			sourceResult.Error = err.Error()
			_ = r.finishJob(ctx, jobID, "failed", sourceResult.MessageCount, err)
			return sourceResult
		}
		sourceResult.MessageCount++
	}

	total, err := r.messages.CountMessagesByChat(ctx, chatID)
	if err == nil {
		sourceResult.TotalMessages = total
	}
	if err := r.finishJob(ctx, jobID, "finished", sourceResult.MessageCount, nil); err != nil {
		sourceResult.Error = err.Error()
	}
	return sourceResult
}

func (r *Runner) createJob(ctx context.Context, sourceType string, sourceID string) (int64, error) {
	var id int64
	if err := r.db.QueryRow(ctx, `
		INSERT INTO sync_jobs (job_type, source_type, source_id, status, started_at)
		VALUES ('history_messages', $1, $2, 'running', now())
		RETURNING id
	`, sourceType, sourceID).Scan(&id); err != nil {
		return 0, fmt.Errorf("create sync job: %w", err)
	}
	return id, nil
}

func (r *Runner) finishJob(ctx context.Context, jobID int64, status string, messageCount int, jobErr error) error {
	errText := ""
	if jobErr != nil {
		errText = jobErr.Error()
	}
	if _, err := r.db.Exec(ctx, `
		UPDATE sync_jobs
		SET status=$2, message_count=$3, error=NULLIF($4, ''), finished_at=now()
		WHERE id=$1
	`, jobID, status, messageCount, errText); err != nil {
		return fmt.Errorf("finish sync job: %w", err)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
