package media

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"feishu-kb-assistant/internal/message"
)

type FeishuClient interface {
	DownloadMessageResource(ctx context.Context, accessToken string, messageID string, fileKey string, resourceType string, maxBytes int64) ([]byte, string, error)
}

type VisionClient interface {
	DescribeImage(ctx context.Context, mimeType string, imageBytes []byte, hint string) (string, error)
}

type Enricher struct {
	logger        *slog.Logger
	enabled       bool
	feishu        FeishuClient
	vision        VisionClient
	maxImageBytes int64
}

func NewEnricher(logger *slog.Logger, enabled bool, feishu FeishuClient, vision VisionClient, maxImageBytes int) *Enricher {
	if logger == nil {
		logger = slog.Default()
	}
	if maxImageBytes <= 0 {
		maxImageBytes = 10 << 20
	}
	return &Enricher{
		logger:        logger,
		enabled:       enabled,
		feishu:        feishu,
		vision:        vision,
		maxImageBytes: int64(maxImageBytes),
	}
}

func (e *Enricher) EnrichMessage(ctx context.Context, accessToken string, msg message.Message) message.Message {
	if !e.enabled || e.feishu == nil || e.vision == nil {
		return msg
	}
	keys := message.ImageKeys(msg.MessageType, msg.RawContent)
	if len(keys) == 0 {
		return msg
	}

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		analysis, err := e.describeImage(ctx, accessToken, msg, key)
		if err != nil {
			e.logger.Warn("describe message image", "message_id", msg.FeishuMessageID, "image_key", key, "error", err)
			continue
		}
		if strings.TrimSpace(analysis) != "" {
			parts = append(parts, fmt.Sprintf("[图片 %s 解析]\n%s", key, strings.TrimSpace(analysis)))
		}
	}
	if len(parts) == 0 {
		return msg
	}

	base := strings.TrimSpace(msg.ContentText)
	mediaText := strings.Join(parts, "\n\n")
	if base == "" {
		msg.ContentText = mediaText
		return msg
	}
	msg.ContentText = base + "\n\n" + mediaText
	return msg
}

func (e *Enricher) describeImage(parent context.Context, accessToken string, msg message.Message, imageKey string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()

	data, mimeType, err := e.feishu.DownloadMessageResource(ctx, accessToken, msg.FeishuMessageID, imageKey, "image", e.maxImageBytes)
	if err != nil {
		return "", err
	}
	hint := strings.TrimSpace(msg.ContentText)
	return e.vision.DescribeImage(ctx, mimeType, data, hint)
}
