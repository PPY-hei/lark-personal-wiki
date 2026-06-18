package autoreply

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"feishu-kb-assistant/internal/message"
	"feishu-kb-assistant/internal/source"
)

type P2PFeishuClient interface {
	ListUserP2PChats(ctx context.Context, userAccessToken string) ([]source.RemoteChat, error)
	ListHistoryMessages(ctx context.Context, accessToken string, chatID string, start time.Time, end time.Time) ([]source.RemoteMessage, error)
}

type P2PSourceRepository interface {
	ListSelectedContacts(ctx context.Context) ([]source.RemoteContact, error)
	SaveContactChatID(ctx context.Context, openID string, chatID string) error
}

type P2PMessageRepository interface {
	SaveMessageIfNew(ctx context.Context, msg message.Message) (bool, error)
}

type P2PPoller struct {
	logger   *slog.Logger
	authRepo AuthRepository
	feishu   P2PFeishuClient
	source   P2PSourceRepository
	messages P2PMessageRepository
	replier  *Service
	interval time.Duration
	lookback time.Duration
}

func NewP2PPoller(
	logger *slog.Logger,
	authRepo AuthRepository,
	feishu P2PFeishuClient,
	sourceRepo P2PSourceRepository,
	messageRepo P2PMessageRepository,
	replier *Service,
	interval time.Duration,
	lookback time.Duration,
) *P2PPoller {
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = time.Minute
	}
	if lookback <= 0 {
		lookback = 2 * time.Minute
	}
	return &P2PPoller{
		logger:   logger,
		authRepo: authRepo,
		feishu:   feishu,
		source:   sourceRepo,
		messages: messageRepo,
		replier:  replier,
		interval: interval,
		lookback: lookback,
	}
}

func (p *P2PPoller) Start(ctx context.Context) {
	p.logger.Info("starting feishu p2p polling", "interval", p.interval.String(), "lookback", p.lookback.String())
	p.poll(ctx)
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			p.logger.Info("stopped feishu p2p polling")
			return
		case <-ticker.C:
			p.poll(ctx)
		}
	}
}

func (p *P2PPoller) poll(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, maxDuration(30*time.Second, p.interval))
	defer cancel()

	session, err := p.authRepo.Latest(ctx)
	if err != nil {
		p.logger.Warn("skip p2p polling without feishu authorization", "error", err)
		return
	}
	contacts, err := p.source.ListSelectedContacts(ctx)
	if err != nil {
		p.logger.Warn("list selected contacts for p2p polling", "error", err)
		return
	}
	if len(contacts) == 0 {
		return
	}
	contacts = p.resolveP2PChatIDs(ctx, session.AccessToken, contacts)
	start := time.Now().Add(-p.lookback)
	end := time.Now()
	for _, contact := range contacts {
		if strings.TrimSpace(contact.ChatID) == "" {
			continue
		}
		p.pollContact(ctx, session.AccessToken, contact, start, end)
	}
}

func (p *P2PPoller) resolveP2PChatIDs(ctx context.Context, accessToken string, contacts []source.RemoteContact) []source.RemoteContact {
	needsResolve := false
	for _, contact := range contacts {
		if contact.ChatID == "" {
			needsResolve = true
			break
		}
	}
	if !needsResolve {
		return contacts
	}
	chats, err := p.feishu.ListUserP2PChats(ctx, accessToken)
	if err != nil {
		p.logger.Warn("list p2p chats for polling", "error", err)
		return contacts
	}
	for i, contact := range contacts {
		if contact.ChatID != "" {
			continue
		}
		chatID := matchP2PChat(contact, chats)
		if chatID == "" {
			continue
		}
		contacts[i].ChatID = chatID
		if contact.OpenID != "" {
			if err := p.source.SaveContactChatID(ctx, contact.OpenID, chatID); err != nil {
				p.logger.Warn("save p2p contact chat id", "open_id", contact.OpenID, "chat_id", chatID, "error", err)
			}
		}
	}
	return contacts
}

func (p *P2PPoller) pollContact(ctx context.Context, accessToken string, contact source.RemoteContact, start time.Time, end time.Time) {
	items, err := p.feishu.ListHistoryMessages(ctx, accessToken, contact.ChatID, start, end)
	if err != nil {
		p.logger.Warn("poll p2p history messages", "chat_id", contact.ChatID, "contact", contact.Name, "error", err)
		return
	}
	for _, item := range items {
		if item.MessageID == "" {
			continue
		}
		msg := message.Message{
			FeishuMessageID: item.MessageID,
			FeishuChatID:    firstNonEmpty(item.ChatID, contact.ChatID),
			FeishuSenderID:  item.SenderID,
			ChatType:        "p2p",
			MessageType:     firstNonEmpty(item.MessageType, "unknown"),
			ContentText:     item.ContentText,
			RawContent:      item.RawContent,
			RawPayload:      item.RawPayload,
			SentAt:          item.SentAt,
		}
		inserted, err := p.messages.SaveMessageIfNew(ctx, msg)
		if err != nil {
			p.logger.Warn("save polled p2p message", "message_id", item.MessageID, "error", err)
			continue
		}
		if inserted {
			p.replier.HandlePolledUserP2PMessage(ctx, msg)
		}
	}
}

func matchP2PChat(contact source.RemoteContact, chats []source.RemoteChat) string {
	name := normalizeName(contact.Name)
	if name == "" {
		return ""
	}
	for _, chat := range chats {
		chatName := normalizeName(chat.Name)
		if chat.ChatID == "" || chatName == "" {
			continue
		}
		if chatName == name || strings.Contains(chatName, name) || strings.Contains(name, chatName) {
			return chat.ChatID
		}
	}
	return ""
}

func normalizeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "")
	value = strings.ReplaceAll(value, "　", "")
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func maxDuration(a time.Duration, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
