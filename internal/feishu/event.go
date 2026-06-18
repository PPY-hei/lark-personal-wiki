package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"feishu-kb-assistant/internal/config"
	"feishu-kb-assistant/internal/message"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

type MessageRepository interface {
	SaveEvent(ctx context.Context, event message.FeishuEvent) error
	SaveMessage(ctx context.Context, msg message.Message) error
}

type EventHandler struct {
	cfg    config.Config
	logger *slog.Logger
	redis  *redis.Client
	repo   MessageRepository
}

func NewEventHandler(cfg config.Config, logger *slog.Logger, redisClient *redis.Client, repo MessageRepository) *EventHandler {
	return &EventHandler{
		cfg:    cfg,
		logger: logger,
		redis:  redisClient,
		repo:   repo,
	}
}

func (h *EventHandler) Handle(c *gin.Context) {
	ctx := c.Request.Context()
	rawBody, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "read body"})
		return
	}

	body, err := h.decodeBody(rawBody)
	if err != nil {
		h.logger.Warn("decode feishu event", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "decode event"})
		return
	}

	var envelope EventEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}

	if envelope.Type == "url_verification" {
		if !h.validToken(envelope.Token) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid verification token"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"challenge": envelope.Challenge})
		return
	}

	result, err := h.ProcessEnvelope(ctx, envelope, body)
	if err != nil {
		h.logger.Error("process feishu event", "error", err)
		c.JSON(result.StatusCode, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "duplicate": result.Duplicate})
}

type ProcessResult struct {
	Duplicate  bool
	StatusCode int
}

func (h *EventHandler) ProcessEnvelope(ctx context.Context, envelope EventEnvelope, rawPayload json.RawMessage) (ProcessResult, error) {
	if envelope.Header.EventID == "" {
		return ProcessResult{StatusCode: http.StatusBadRequest}, fmt.Errorf("missing event id")
	}
	if !h.validToken(envelope.Header.Token) {
		return ProcessResult{StatusCode: http.StatusUnauthorized}, fmt.Errorf("invalid verification token")
	}

	seen, err := h.markEventSeen(ctx, envelope.Header.EventID)
	if err != nil {
		return ProcessResult{StatusCode: http.StatusInternalServerError}, fmt.Errorf("dedupe failed: %w", err)
	}
	if seen {
		return ProcessResult{Duplicate: true, StatusCode: http.StatusOK}, nil
	}

	if err := h.repo.SaveEvent(ctx, message.FeishuEvent{
		EventID:       envelope.Header.EventID,
		EventType:     envelope.Header.EventType,
		SchemaVersion: envelope.Schema,
		RawPayload:    rawPayload,
	}); err != nil {
		return ProcessResult{StatusCode: http.StatusInternalServerError}, fmt.Errorf("save event failed: %w", err)
	}

	if envelope.Header.EventType == "im.message.receive_v1" {
		msg, ok, err := ParseMessageEvent(envelope)
		if err != nil {
			h.logger.Warn("parse message event", "event_id", envelope.Header.EventID, "error", err)
		}
		if ok {
			if err := h.repo.SaveMessage(ctx, msg); err != nil {
				return ProcessResult{StatusCode: http.StatusInternalServerError}, fmt.Errorf("save message failed: %w", err)
			}
		}
	}

	return ProcessResult{StatusCode: http.StatusOK}, nil
}

func (h *EventHandler) decodeBody(rawBody []byte) ([]byte, error) {
	var encrypted struct {
		Encrypt string `json:"encrypt"`
	}
	if err := json.Unmarshal(rawBody, &encrypted); err == nil && encrypted.Encrypt != "" {
		if h.cfg.FeishuEncryptKey == "" {
			return nil, fmt.Errorf("event is encrypted but FEISHU_ENCRYPT_KEY is empty")
		}
		return DecryptEvent(encrypted.Encrypt, h.cfg.FeishuEncryptKey)
	}
	return rawBody, nil
}

func (h *EventHandler) validToken(token string) bool {
	if h.cfg.FeishuVerificationToken == "" {
		return true
	}
	return token == h.cfg.FeishuVerificationToken
}

func (h *EventHandler) markEventSeen(ctx context.Context, eventID string) (bool, error) {
	key := "feishu:event:" + eventID
	ok, err := h.redis.SetNX(ctx, key, "1", 24*time.Hour).Result()
	if err != nil {
		return false, err
	}
	return !ok, nil
}

type EventEnvelope struct {
	Schema    string          `json:"schema"`
	Type      string          `json:"type"`
	Token     string          `json:"token"`
	Challenge string          `json:"challenge"`
	Header    EventHeader     `json:"header"`
	Event     json.RawMessage `json:"event"`
}

type EventHeader struct {
	EventID    string `json:"event_id"`
	EventType  string `json:"event_type"`
	CreateTime string `json:"create_time"`
	Token      string `json:"token"`
	AppID      string `json:"app_id"`
	TenantKey  string `json:"tenant_key"`
}

func ParseMessageEvent(envelope EventEnvelope) (message.Message, bool, error) {
	var event struct {
		Sender struct {
			SenderID struct {
				UserID string `json:"user_id"`
				OpenID string `json:"open_id"`
			} `json:"sender_id"`
		} `json:"sender"`
		Message struct {
			MessageID   string          `json:"message_id"`
			ChatID      string          `json:"chat_id"`
			MessageType string          `json:"message_type"`
			Content     json.RawMessage `json:"content"`
			CreateTime  string          `json:"create_time"`
		} `json:"message"`
	}
	if err := json.Unmarshal(envelope.Event, &event); err != nil {
		return message.Message{}, false, fmt.Errorf("unmarshal message event: %w", err)
	}
	if event.Message.MessageID == "" {
		return message.Message{}, false, nil
	}

	var sentAt *time.Time
	if event.Message.CreateTime != "" {
		if ms, err := parseFeishuMillis(event.Message.CreateTime); err == nil {
			sentAt = &ms
		}
	}

	senderID := event.Sender.SenderID.OpenID
	if senderID == "" {
		senderID = event.Sender.SenderID.UserID
	}

	return message.Message{
		FeishuMessageID: event.Message.MessageID,
		FeishuChatID:    event.Message.ChatID,
		FeishuSenderID:  senderID,
		MessageType:     event.Message.MessageType,
		ContentText:     extractTextContent(event.Message.MessageType, event.Message.Content),
		RawContent:      event.Message.Content,
		RawPayload:      envelope.Event,
		SentAt:          sentAt,
	}, true, nil
}

func parseFeishuMillis(value string) (time.Time, error) {
	var millis int64
	if _, err := fmt.Sscanf(value, "%d", &millis); err != nil {
		return time.Time{}, err
	}
	return time.UnixMilli(millis), nil
}

func extractTextContent(messageType string, raw json.RawMessage) string {
	if messageType != "text" {
		return ""
	}
	raw = normalizeContentJSON(raw)
	var content struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &content); err != nil {
		return ""
	}
	return content.Text
}

func normalizeContentJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		text = strings.TrimSpace(text)
		if text == "" {
			return raw
		}
		return json.RawMessage(text)
	}
	return raw
}
