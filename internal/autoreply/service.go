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
	TenantAccessToken(ctx context.Context) (string, error)
	SendTextToChat(ctx context.Context, accessToken string, chatID string, text string) (string, error)
}

type AuthRepository interface {
	Latest(ctx context.Context) (auth.Session, error)
}

type ReplyLogRepository interface {
	AutoReplyAlreadySent(ctx context.Context, feishuMessageID string) (bool, error)
	SaveAutoReplyResult(ctx context.Context, msg message.Message, query string, answer string, status string, replyErr error) error
}

type ContactChatRepository interface {
	IsSelectedContactChat(ctx context.Context, chatID string) (bool, error)
}

type KnowledgeService interface {
	Ask(ctx context.Context, question string, limit int) (knowledge.AskResult, error)
}

type Service struct {
	logger    *slog.Logger
	authRepo  AuthRepository
	replies   ReplyLogRepository
	contacts  ContactChatRepository
	feishu    FeishuClient
	knowledge KnowledgeService
}

type replyIdentity string

const (
	replyIdentityUser replyIdentity = "user"
	replyIdentityBot  replyIdentity = "bot"
)

type replyDecision struct {
	ShouldReply bool
	Reason      string
	Identity    replyIdentity
}

func New(logger *slog.Logger, authRepo AuthRepository, replyRepo ReplyLogRepository, contactRepo ContactChatRepository, feishu FeishuClient, knowledge KnowledgeService) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		logger:    logger,
		authRepo:  authRepo,
		replies:   replyRepo,
		contacts:  contactRepo,
		feishu:    feishu,
		knowledge: knowledge,
	}
}

func (s *Service) HandleMessage(ctx context.Context, msg message.Message) {
	decision := s.shouldReply(ctx, msg)
	if !decision.ShouldReply {
		s.logger.Info("skip auto reply", "message_id", msg.FeishuMessageID, "reason", decision.Reason)
		return
	}
	s.logger.Info("trigger auto reply", "message_id", msg.FeishuMessageID, "chat_id", msg.FeishuChatID, "chat_type", msg.ChatType, "identity", decision.Identity)
	go s.reply(context.Background(), msg, decision.Identity)
}

func (s *Service) HandlePolledUserP2PMessage(ctx context.Context, msg message.Message) {
	if strings.TrimSpace(msg.ContentText) == "" {
		return
	}
	session, err := s.authRepo.Latest(ctx)
	if err != nil {
		s.logger.Warn("skip polled p2p auto reply without authorization", "message_id", msg.FeishuMessageID, "error", err)
		return
	}
	if msg.FeishuSenderID != "" && msg.FeishuSenderID == session.OpenID {
		return
	}
	if sent, err := s.autoReplyAlreadySent(ctx, msg.FeishuMessageID); err != nil {
		s.logger.Warn("check polled p2p auto reply dedupe", "message_id", msg.FeishuMessageID, "error", err)
		return
	} else if sent {
		return
	}
	s.logger.Info("trigger polled p2p auto reply", "message_id", msg.FeishuMessageID, "chat_id", msg.FeishuChatID, "identity", replyIdentityUser)
	go s.reply(context.Background(), msg, replyIdentityUser)
}

func (s *Service) shouldReply(ctx context.Context, msg message.Message) replyDecision {
	if msg.FeishuMessageID == "" || msg.FeishuChatID == "" {
		return skip("missing_message_or_chat_id")
	}
	if strings.TrimSpace(msg.ContentText) == "" {
		return skip("empty_content")
	}
	if msg.SenderType == "app" || msg.SenderType == "bot" {
		return skip("sender_is_bot")
	}
	session, err := s.authRepo.Latest(ctx)
	if err != nil {
		s.logger.Warn("skip auto reply without feishu authorization", "error", err)
		return skip("missing_authorization")
	}
	if msg.ChatType == "p2p" {
		if msg.FeishuSenderID != "" && msg.FeishuSenderID == session.OpenID {
			return skip("self_p2p_message")
		}
		isContactChat, err := s.isSelectedContactChat(ctx, msg.FeishuChatID)
		if err != nil {
			s.logger.Warn("check p2p chat owner", "chat_id", msg.FeishuChatID, "error", err)
			return skip("p2p_chat_owner_unknown")
		}
		if isContactChat {
			return replyAs(replyIdentityUser, "p2p_authorized_user")
		}
		return replyAs(replyIdentityBot, "p2p_bot")
	}
	for _, openID := range msg.MentionOpenIDs {
		if openID == session.OpenID {
			return replyAs(replyIdentityUser, "mention_authorized_user")
		}
	}
	if hasMentionType(msg, "bot") {
		return replyAs(replyIdentityBot, "mention_bot")
	}
	for _, key := range msg.MentionKeys {
		if strings.TrimSpace(key) != "" {
			return replyAs(replyIdentityBot, "mention_present")
		}
	}
	return skip("group_without_mention")
}

func (s *Service) reply(parent context.Context, msg message.Message, identity replyIdentity) {
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()

	accessToken, err := s.accessToken(ctx, identity)
	if err != nil {
		s.logger.Warn("load auto reply token", "message_id", msg.FeishuMessageID, "identity", identity, "error", err)
		return
	}
	question := buildQuestion(msg)
	result, err := s.knowledge.Ask(ctx, question, 8)
	if err != nil {
		_ = s.saveAutoReplyResult(ctx, msg, question, "", "failed", err)
		s.logger.Warn("generate auto reply answer", "message_id", msg.FeishuMessageID, "error", err)
		return
	}
	answer := strings.TrimSpace(result.Answer)
	if answer == "" {
		s.logger.Warn("skip empty auto reply answer", "message_id", msg.FeishuMessageID)
		return
	}
	if _, err := s.feishu.SendTextToChat(ctx, accessToken, msg.FeishuChatID, answer); err != nil {
		_ = s.saveAutoReplyResult(ctx, msg, question, answer, "failed", err)
		s.logger.Warn("send auto reply", "message_id", msg.FeishuMessageID, "chat_id", msg.FeishuChatID, "identity", identity, "error", err)
		return
	}
	_ = s.saveAutoReplyResult(ctx, msg, question, answer, "sent", nil)
	s.logger.Info("sent auto reply", "message_id", msg.FeishuMessageID, "chat_id", msg.FeishuChatID, "identity", identity)
}

func (s *Service) accessToken(ctx context.Context, identity replyIdentity) (string, error) {
	if identity == replyIdentityBot {
		return s.feishu.TenantAccessToken(ctx)
	}
	session, err := s.authRepo.Latest(ctx)
	if err != nil {
		return "", fmt.Errorf("load feishu authorization: %w", err)
	}
	return session.AccessToken, nil
}

func skip(reason string) replyDecision {
	return replyDecision{Reason: reason}
}

func replyAs(identity replyIdentity, reason string) replyDecision {
	return replyDecision{ShouldReply: true, Reason: reason, Identity: identity}
}

func hasMentionType(msg message.Message, typ string) bool {
	for _, item := range msg.MentionTypes {
		if strings.EqualFold(strings.TrimSpace(item), typ) {
			return true
		}
	}
	return false
}

func (s *Service) isSelectedContactChat(ctx context.Context, chatID string) (bool, error) {
	if s.contacts == nil {
		return false, nil
	}
	return s.contacts.IsSelectedContactChat(ctx, chatID)
}

func (s *Service) autoReplyAlreadySent(ctx context.Context, feishuMessageID string) (bool, error) {
	if s.replies == nil || feishuMessageID == "" {
		return false, nil
	}
	return s.replies.AutoReplyAlreadySent(ctx, feishuMessageID)
}

func (s *Service) saveAutoReplyResult(ctx context.Context, msg message.Message, query string, answer string, status string, replyErr error) error {
	if s.replies == nil {
		return nil
	}
	return s.replies.SaveAutoReplyResult(ctx, msg, query, answer, status, replyErr)
}

func buildQuestion(msg message.Message) string {
	content := strings.TrimSpace(msg.ContentText)
	if content == "" {
		return ""
	}
	return fmt.Sprintf("请基于整个个人飞书知识库回答这条消息：%s", content)
}
