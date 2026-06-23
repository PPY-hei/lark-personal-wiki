package autoreply

import (
	"context"
	"strings"
	"testing"
	"time"

	"feishu-kb-assistant/internal/auth"
	"feishu-kb-assistant/internal/message"
)

type stubAuthRepo struct {
	session auth.Session
	err     error
}

func (s stubAuthRepo) Latest(context.Context) (auth.Session, error) {
	return s.session, s.err
}

type stubContactRepo struct {
	selected map[string]bool
}

func (s stubContactRepo) IsSelectedContactChat(_ context.Context, chatID string) (bool, error) {
	return s.selected[chatID], nil
}

type stubReplyRepo struct{}

func (s stubReplyRepo) AutoReplyAlreadySent(context.Context, string) (bool, error) {
	return false, nil
}

func (s stubReplyRepo) SaveAutoReplyResult(context.Context, message.Message, string, string, string, error) error {
	return nil
}

func (s stubReplyRepo) RecentMessagesByChatSender(context.Context, string, string, time.Time, time.Time, int) ([]message.Message, error) {
	return nil, nil
}

func TestShouldReply(t *testing.T) {
	service := New(
		nil,
		stubAuthRepo{session: auth.Session{OpenID: "ou_authorized"}},
		nil,
		stubContactRepo{selected: map[string]bool{"oc_contact": true}},
		nil,
		nil,
	)

	tests := []struct {
		name         string
		msg          message.Message
		want         bool
		wantIdentity replyIdentity
	}{
		{
			name: "bot p2p message triggers bot reply",
			msg: message.Message{
				FeishuMessageID: "om_1",
				FeishuChatID:    "oc_1",
				FeishuSenderID:  "ou_other",
				ChatType:        "p2p",
				ContentText:     "帮我查一下",
			},
			want:         true,
			wantIdentity: replyIdentityBot,
		},
		{
			name: "selected contact p2p message triggers user reply",
			msg: message.Message{
				FeishuMessageID: "om_8",
				FeishuChatID:    "oc_contact",
				FeishuSenderID:  "ou_other",
				ChatType:        "p2p",
				ContentText:     "帮我查一下",
			},
			want:         true,
			wantIdentity: replyIdentityUser,
		},
		{
			name: "group mention authorized user triggers",
			msg: message.Message{
				FeishuMessageID: "om_2",
				FeishuChatID:    "oc_1",
				FeishuSenderID:  "ou_other",
				ChatType:        "group",
				ContentText:     "@User 帮忙看下",
				MentionOpenIDs:  []string{"ou_authorized"},
			},
			want:         true,
			wantIdentity: replyIdentityUser,
		},
		{
			name: "group mention bot placeholder triggers",
			msg: message.Message{
				FeishuMessageID: "om_3",
				FeishuChatID:    "oc_1",
				FeishuSenderID:  "ou_other",
				ChatType:        "group",
				ContentText:     "@Bot 帮忙看下",
				MentionKeys:     []string{"@_user_1"},
				MentionTypes:    []string{"bot"},
			},
			want:         true,
			wantIdentity: replyIdentityBot,
		},
		{
			name: "plain group message does not trigger",
			msg: message.Message{
				FeishuMessageID: "om_4",
				FeishuChatID:    "oc_1",
				FeishuSenderID:  "ou_other",
				ChatType:        "group",
				ContentText:     "普通消息",
			},
			want: false,
		},
		{
			name: "authorized sender p2p does not trigger",
			msg: message.Message{
				FeishuMessageID: "om_5",
				FeishuChatID:    "oc_1",
				FeishuSenderID:  "ou_authorized",
				ChatType:        "p2p",
				ContentText:     "自己说的话",
			},
			want: false,
		},
		{
			name: "authorized sender group mention bot triggers",
			msg: message.Message{
				FeishuMessageID: "om_7",
				FeishuChatID:    "oc_1",
				FeishuSenderID:  "ou_authorized",
				ChatType:        "group",
				ContentText:     "@Bot 帮我查一下",
				MentionKeys:     []string{"@_user_1"},
				MentionTypes:    []string{"bot"},
			},
			want:         true,
			wantIdentity: replyIdentityBot,
		},
		{
			name: "bot sender does not trigger",
			msg: message.Message{
				FeishuMessageID: "om_6",
				FeishuChatID:    "oc_1",
				FeishuSenderID:  "ou_bot",
				SenderType:      "app",
				ChatType:        "p2p",
				ContentText:     "机器人消息",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := service.shouldReply(context.Background(), tt.msg)
			if got.ShouldReply != tt.want {
				t.Fatalf("ShouldReply = %v, want %v", got.ShouldReply, tt.want)
			}
			if got.ShouldReply && got.Identity != tt.wantIdentity {
				t.Fatalf("Identity = %v, want %v", got.Identity, tt.wantIdentity)
			}
		})
	}
}

func TestBuildConversationQuestionMergesMessages(t *testing.T) {
	got := buildConversationQuestion([]message.Message{
		{ContentText: "hive的账号密码是什么"},
		{ContentText: "怎么不回答我了"},
	})

	for _, want := range []string{"连续发送的私聊消息", "hive的账号密码是什么", "怎么不回答我了"} {
		if !strings.Contains(got, want) {
			t.Fatalf("merged question missing %q: %s", want, got)
		}
	}
}

func TestScheduleP2PReplyDebouncesByChatAndSender(t *testing.T) {
	service := New(nil, nil, stubReplyRepo{}, nil, nil, nil)
	first := message.Message{
		FeishuMessageID: "om_1",
		FeishuChatID:    "oc_1",
		FeishuSenderID:  "ou_1",
		ContentText:     "hive的账号密码是什么",
	}
	second := message.Message{
		FeishuMessageID: "om_2",
		FeishuChatID:    "oc_1",
		FeishuSenderID:  "ou_1",
		ContentText:     "怎么不回答我了",
	}

	service.scheduleP2PReply(first)
	service.scheduleP2PReply(second)

	key := p2pReplyKey(second)
	service.p2pMu.Lock()
	pending := service.p2pPending[key]
	timerCount := len(service.p2pTimers)
	if timer := service.p2pTimers[key]; timer != nil {
		timer.Stop()
	}
	service.p2pMu.Unlock()

	if pending.Message.FeishuMessageID != "om_2" {
		t.Fatalf("pending message = %s, want om_2", pending.Message.FeishuMessageID)
	}
	if timerCount != 1 {
		t.Fatalf("timer count = %d, want 1", timerCount)
	}
}
