package message

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestImageKeys(t *testing.T) {
	tests := []struct {
		name        string
		messageType string
		raw         json.RawMessage
		want        []string
	}{
		{
			name:        "image message",
			messageType: "image",
			raw:         json.RawMessage(`{"image_key":"img_v3_abc"}`),
			want:        []string{"img_v3_abc"},
		},
		{
			name:        "post image",
			messageType: "post",
			raw:         json.RawMessage(`{"content":[[{"tag":"text","text":"看图"},{"tag":"img","image_key":"img_v3_post"}]]}`),
			want:        []string{"img_v3_post"},
		},
		{
			name:        "string encoded image",
			messageType: "image",
			raw:         json.RawMessage(`"{\"image_key\":\"img_v3_string\"}"`),
			want:        []string{"img_v3_string"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ImageKeys(tt.messageType, tt.raw); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ImageKeys() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
