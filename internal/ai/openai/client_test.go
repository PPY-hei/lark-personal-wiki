package openai

import (
	"strings"
	"testing"
)

func TestParseKeywordExpansion(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "object",
			text: `{"keywords":["jdbc:hive2","hive","jdbc","hive"]}`,
			want: []string{"jdbc:hive2", "hive", "jdbc"},
		},
		{
			name: "array",
			text: `["NVR","摄像头"]`,
			want: []string{"NVR", "摄像头"},
		},
		{
			name: "fenced",
			text: "```json\n{\"keywords\":[\"账号\",\"密码\"]}\n```",
			want: []string{"账号", "密码"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseKeywordExpansion(tt.text)
			if err != nil {
				t.Fatalf("parseKeywordExpansion() error = %v", err)
			}
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Fatalf("keywords = %#v, want %#v", got, tt.want)
			}
		})
	}
}
