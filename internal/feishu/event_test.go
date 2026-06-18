package feishu

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExtractTextContentPostWithMentionTextAndImage(t *testing.T) {
	raw := json.RawMessage(`{"title":"","content":[[{"tag":"at","user_id":"@_user_1","user_name":"Chongtao LU 陆崇滔","style":[]},{"tag":"text","text":" 那我就先把只保留图片的和云台机的那个了，你加油干","style":[]}],[{"tag":"img","image_key":"img_v3_0212p_13a91f22-7137-4ded-93e5-943e3cc5946g","width":586,"height":122}]]}`)

	got := extractTextContent("post", raw)

	for _, want := range []string{
		"@Chongtao LU 陆崇滔",
		"只保留图片",
		"云台机",
		"[图片:img_v3_0212p_13a91f22-7137-4ded-93e5-943e3cc5946g]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("extracted post content missing %q: %q", want, got)
		}
	}
}

func TestExtractTextContentImage(t *testing.T) {
	raw := json.RawMessage(`{"image_key":"img_v3_abc","width":586,"height":122}`)

	got := extractTextContent("image", raw)

	if got != "[图片:img_v3_abc]" {
		t.Fatalf("image content = %q", got)
	}
}

func TestExtractTextContentStringEncodedJSON(t *testing.T) {
	raw := json.RawMessage(`"{\"text\":\"hello\"}"`)

	got := extractTextContent("text", raw)

	if got != "hello" {
		t.Fatalf("text content = %q", got)
	}
}
