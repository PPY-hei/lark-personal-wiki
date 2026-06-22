package message

import (
	"encoding/json"
	"strings"
)

func ImageKeys(messageType string, raw json.RawMessage) []string {
	raw = normalizeJSON(raw)
	keys := make([]string, 0, 2)
	switch messageType {
	case "image":
		var content struct {
			ImageKey string `json:"image_key"`
		}
		if err := json.Unmarshal(raw, &content); err == nil {
			keys = appendKey(keys, content.ImageKey)
		}
	case "post":
		var content struct {
			Content [][]struct {
				Tag      string `json:"tag"`
				ImageKey string `json:"image_key"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &content); err == nil {
			for _, line := range content.Content {
				for _, item := range line {
					if item.Tag == "img" {
						keys = appendKey(keys, item.ImageKey)
					}
				}
			}
		}
	default:
		var object map[string]any
		if err := json.Unmarshal(raw, &object); err == nil {
			if value, ok := object["image_key"].(string); ok {
				keys = appendKey(keys, value)
			}
		}
	}
	return keys
}

func appendKey(keys []string, key string) []string {
	key = strings.TrimSpace(key)
	if key == "" {
		return keys
	}
	for _, existing := range keys {
		if existing == key {
			return keys
		}
	}
	return append(keys, key)
}

func normalizeJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		text = strings.TrimSpace(text)
		if text != "" {
			return json.RawMessage(text)
		}
	}
	return raw
}
