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
	MessageType     string
	ContentText     string
	RawContent      json.RawMessage
	RawPayload      json.RawMessage
	SentAt          *time.Time
}
