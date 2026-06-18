package message

import (
	"encoding/json"
	"time"
)

type FeishuEvent struct {
	EventID       string
	EventType     string
	SchemaVersion string
	RawPayload    json.RawMessage
}

type Message struct {
	FeishuMessageID string
	FeishuChatID    string
	FeishuSenderID  string
	SenderType      string
	ChatType        string
	MessageType     string
	ContentText     string
	MentionKeys     []string
	MentionOpenIDs  []string
	MentionTypes    []string
	RawContent      json.RawMessage
	RawPayload      json.RawMessage
	SentAt          *time.Time
}
