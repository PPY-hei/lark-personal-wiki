package autoreply

import (
	"context"
	"testing"

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

func TestShouldReply(t *testing.T) {
	service := New(nil, stubAuthRepo{session: auth.Session{OpenID: "ou_authorized"}}, nil, nil)

	tests := []struct {
		name string
		msg  message.Message
		want bool
	}{
		{
			name: "p2p message triggers",
			msg: message.Message{
				FeishuMessageID: "om_1",
				FeishuChatID:    "oc_1",
				FeishuSenderID:  "ou_other",
				ChatType:        "p2p",
				ContentText:     "帮我查一下",
			},
			want: true,
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
			want: true,
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
			},
			want: true,
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
			},
			want: true,
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
			got, _ := service.shouldReply(context.Background(), tt.msg)
			if got != tt.want {
				t.Fatalf("shouldReply() = %v, want %v", got, tt.want)
			}
		})
	}
}
