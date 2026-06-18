package source

import (
	"encoding/json"
	"time"
)

type RemoteChat struct {
	ChatID      string          `json:"chat_id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	ChatStatus  string          `json:"chat_status"`
	ChatMode    string          `json:"chat_mode"`
	Selected    bool            `json:"selected"`
	RawPayload  json.RawMessage `json:"raw_payload,omitempty"`
}

type RemoteContact struct {
	OpenID     string          `json:"open_id"`
	UserID     string          `json:"user_id"`
	UnionID    string          `json:"union_id"`
	Name       string          `json:"name"`
	Email      string          `json:"email"`
	ChatID     string          `json:"chat_id"`
	Selected   bool            `json:"selected"`
	RawPayload json.RawMessage `json:"raw_payload,omitempty"`
}

type RemoteMessage struct {
	MessageID   string          `json:"message_id"`
	ChatID      string          `json:"chat_id"`
	SenderID    string          `json:"sender_id"`
	MessageType string          `json:"message_type"`
	ContentText string          `json:"content_text"`
	RawContent  json.RawMessage `json:"raw_content"`
	RawPayload  json.RawMessage `json:"raw_payload"`
	SentAt      *time.Time      `json:"sent_at"`
}
