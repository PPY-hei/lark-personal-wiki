package source

import "encoding/json"

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
	Selected   bool            `json:"selected"`
	RawPayload json.RawMessage `json:"raw_payload,omitempty"`
}
