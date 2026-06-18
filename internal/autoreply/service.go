package autoreply

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"feishu-kb-assistant/internal/auth"
	"feishu-kb-assistant/internal/knowledge"
	"feishu-kb-assistant/internal/message"
)

type FeishuClient interface {
	SendTextToChat(ctx context.Context, accessToken string, chatID string, text string) (string, error)
}

type AuthRepository interface {
	Latest(ctx context.Context) (auth.Session, error)
}

type KnowledgeService interface {
	Ask(ctx context.Context, question string, limit int) (knowledge.AskResult, error)
}

type Service struct {
	logger    *slog.Logger
	authRepo  AuthRepository
	feishu    FeishuClient
	knowledge KnowledgeService
}

func New(logger *slog.Logger, authRepo AuthRepository, feishu FeishuClient, knowledge KnowledgeService) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		logger:    logger,
		authRepo:  authRepo,
		feishu:    feishu,
		knowledge: knowledge,
	}
}

func (s *Service) HandleMessage(ctx context.Context, msg message.Message) {
	shouldReply, reason := s.shouldReply(ctx, msg)
	if !shouldReply {
		s.logger.Info("skip auto reply", "message_id", msg.FeishuMessageID, "reason", reason)
		return
	}
	s.logger.Info("trigger auto reply", "message_id", msg.FeishuMessageID, "chat_id", msg.FeishuChatID, "chat_type", msg.ChatType)
	go s.reply(context.Background(), msg)
}

func (s *Service) shouldReply(ctx context.Context, msg message.Message) (bool, string) {
	if msg.FeishuMessageID == "" || msg.FeishuChatID == "" {
		return false, "missing_message_or_chat_id"
	}
	if strings.TrimSpace(msg.ContentText) == "" {
		return false, "empty_content"
	}
	if msg.SenderType == "app" || msg.SenderType == "bot" {
		return false, "sender_is_bot"
	}
	session, err := s.authRepo.Latest(ctx)
	if err != nil {
		s.logger.Warn("skip auto reply without feishu authorization", "error", err)
		return false, "missing_authorization"
	}
	if msg.ChatType == "p2p" {
		if msg.FeishuSenderID != "" && msg.FeishuSenderID == session.OpenID {
			return false, "self_p2p_message"
		}
		return true, "p2p"
	}
	for _, openID := range msg.MentionOpenIDs {
		if openID == session.OpenID {
			return true, "mention_authorized_user"
		}
	}
	for _, key := range msg.MentionKeys {
		if strings.TrimSpace(key) != "" {
			return true, "mention_present"
		}
	}
	return false, "group_without_mention"
}

func (s *Service) reply(parent context.Context, msg message.Message) {
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()

	session, err := s.authRepo.Latest(ctx)
	if err != nil {
		s.logger.Warn("load feishu authorization for auto reply", "message_id", msg.FeishuMessageID, "error", err)
		return
	}
	question := buildQuestion(msg)
	result, err := s.knowledge.Ask(ctx, question, 8)
	if err != nil {
		s.logger.Warn("generate auto reply answer", "message_id", msg.FeishuMessageID, "error", err)
		return
	}
	answer := strings.TrimSpace(result.Answer)
	if answer == "" {
		s.logger.Warn("skip empty auto reply answer", "message_id", msg.FeishuMessageID)
		return
	}
	if _, err := s.feishu.SendTextToChat(ctx, session.AccessToken, msg.FeishuChatID, answer); err != nil {
		s.logger.Warn("send auto reply", "message_id", msg.FeishuMessageID, "chat_id", msg.FeishuChatID, "error", err)
		return
	}
	s.logger.Info("sent auto reply", "message_id", msg.FeishuMessageID, "chat_id", msg.FeishuChatID)
}

func buildQuestion(msg message.Message) string {
	content := strings.TrimSpace(msg.ContentText)
	if content == "" {
		return ""
	}
	return fmt.Sprintf("请基于整个个人飞书知识库回答这条消息：%s", content)
}
