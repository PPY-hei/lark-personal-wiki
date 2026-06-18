package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

type WebSocketRunner struct {
	appID   string
	secret  string
	logger  *slog.Logger
	handler *EventHandler
}

func NewWebSocketRunner(appID string, secret string, logger *slog.Logger, handler *EventHandler) *WebSocketRunner {
	return &WebSocketRunner{
		appID:   appID,
		secret:  secret,
		logger:  logger,
		handler: handler,
	}
}

func (r *WebSocketRunner) Start(ctx context.Context) error {
	if r.appID == "" || r.secret == "" {
		return fmt.Errorf("FEISHU_APP_ID and FEISHU_APP_SECRET are required for websocket mode")
	}

	eventDispatcher := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
			return r.handleMessageReceive(ctx, event)
		})

	client := larkws.NewClient(
		r.appID,
		r.secret,
		larkws.WithEventHandler(eventDispatcher),
		larkws.WithLogLevel(larkcore.LogLevelInfo),
	)

	r.logger.Info("starting feishu websocket event receiver")
	return client.Start(ctx)
}

func (r *WebSocketRunner) handleMessageReceive(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
	envelope, rawPayload, err := envelopeFromSDKMessageEvent(event)
	if err != nil {
		return err
	}

	result, err := r.handler.ProcessEnvelope(ctx, envelope, rawPayload)
	if err != nil {
		return err
	}
	if result.Duplicate {
		r.logger.Info("duplicate feishu websocket event", "event_id", envelope.Header.EventID)
		return nil
	}

	r.logger.Info(
		"processed feishu websocket message",
		"event_id", envelope.Header.EventID,
		"message_id", messageIDFromSDKEvent(event),
	)
	return nil
}

func envelopeFromSDKMessageEvent(event *larkim.P2MessageReceiveV1) (EventEnvelope, json.RawMessage, error) {
	if event == nil || event.EventV2Base == nil || event.EventV2Base.Header == nil {
		return EventEnvelope{}, nil, fmt.Errorf("missing sdk event header")
	}
	if event.Event == nil || event.Event.Message == nil {
		return EventEnvelope{}, nil, fmt.Errorf("missing sdk message event")
	}

	eventBody := map[string]any{
		"sender":  event.Event.Sender,
		"message": sdkMessageToEventMessage(event.Event.Message),
	}
	eventJSON, err := json.Marshal(eventBody)
	if err != nil {
		return EventEnvelope{}, nil, fmt.Errorf("marshal sdk event body: %w", err)
	}

	envelope := EventEnvelope{
		Schema: event.EventV2Base.Schema,
		Header: EventHeader{
			EventID:    event.EventV2Base.Header.EventID,
			EventType:  event.EventV2Base.Header.EventType,
			CreateTime: event.EventV2Base.Header.CreateTime,
			Token:      event.EventV2Base.Header.Token,
			AppID:      event.EventV2Base.Header.AppID,
			TenantKey:  event.EventV2Base.Header.TenantKey,
		},
		Event: eventJSON,
	}

	rawPayload, err := json.Marshal(larkevent.EventV2Body{
		EventV2Base: *event.EventV2Base,
		Event:       eventBody,
	})
	if err != nil {
		return EventEnvelope{}, nil, fmt.Errorf("marshal raw sdk event: %w", err)
	}

	return envelope, rawPayload, nil
}

func sdkMessageToEventMessage(message *larkim.EventMessage) map[string]any {
	body := map[string]any{
		"message_id":   deref(message.MessageId),
		"chat_id":      deref(message.ChatId),
		"message_type": deref(message.MessageType),
		"create_time":  deref(message.CreateTime),
	}
	if message.Content != nil && *message.Content != "" {
		var content json.RawMessage
		if err := json.Unmarshal([]byte(*message.Content), &content); err == nil {
			body["content"] = content
		} else {
			body["content"] = map[string]string{"text": *message.Content}
		}
	}
	return body
}

func messageIDFromSDKEvent(event *larkim.P2MessageReceiveV1) string {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return ""
	}
	return deref(event.Event.Message.MessageId)
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
