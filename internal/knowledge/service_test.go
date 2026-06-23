package knowledge

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type stubAI struct {
	needsWebSearch bool
	webAnswer      string
	webErr         error
	plainAnswer    string
	webCalls       int
	plainCalls     int
}

func (s *stubAI) Embed(context.Context, string) ([]float32, error) {
	return nil, nil
}

func (s *stubAI) GenerateAnswer(context.Context, string, string) (string, error) {
	s.plainCalls++
	return s.plainAnswer, nil
}

func (s *stubAI) GenerateAnswerWithWebSearch(context.Context, string, string) (string, error) {
	s.webCalls++
	if s.webErr != nil {
		return "", s.webErr
	}
	return s.webAnswer, nil
}

func (s *stubAI) ShouldUseWebSearch(context.Context, string, string) (bool, string, error) {
	return s.needsWebSearch, "需要最新信息", nil
}

func (s *stubAI) ExpandSearchKeywords(context.Context, string) ([]string, error) {
	return nil, nil
}

func (s *stubAI) Model() string {
	return "stub"
}

func TestExpandSourceReferences(t *testing.T) {
	chunks := []RetrievedChunk{
		{SourceID: "oc_1:2026-06-01:000", DisplaySource: "华望私有化 / 2026-06-01 / 片段 1"},
		{SourceID: "oc_2:2026-06-02:000", DisplaySource: "门店项目群 / 2026-06-02 / 片段 1"},
	}

	answer := "参考来源：\n- 来源 1\n- 来源 2"
	got := expandSourceReferences(answer, chunks)

	for _, want := range []string{
		"来源 1：华望私有化 / 2026-06-01 / 片段 1",
		"来源 2：门店项目群 / 2026-06-02 / 片段 1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expanded answer missing %q: %s", want, got)
		}
	}
}

func TestExpandSourceReferencesKeepsExistingReadableSource(t *testing.T) {
	chunks := []RetrievedChunk{{DisplaySource: "华望私有化 / 2026-06-01 / 片段 1"}}

	answer := "参考来源：\n- 来源 1：华望私有化 / 2026-06-01 / 片段 1"
	got := expandSourceReferences(answer, chunks)

	if strings.Count(got, "华望私有化") != 1 {
		t.Fatalf("source label duplicated: %s", got)
	}
}

func TestExpandSourceReferencesFillsBareColonLine(t *testing.T) {
	chunks := []RetrievedChunk{{DisplaySource: "门店项目群 / 2026-06-02 / 片段 1"}}

	answer := "参考来源：\n- 来源 1："
	got := expandSourceReferences(answer, chunks)

	want := "- 来源 1：门店项目群 / 2026-06-02 / 片段 1"
	if !strings.Contains(got, want) {
		t.Fatalf("expanded answer missing %q: %s", want, got)
	}
}

func TestMergeRetrievedChunksPrefersTextResults(t *testing.T) {
	textItems := []RetrievedChunk{{ID: 2, Content: "hive jdbc"}, {ID: 3, Content: "password"}}
	embeddingItems := []RetrievedChunk{{ID: 1, Content: "semantic"}, {ID: 2, Content: "hive jdbc"}}

	got := mergeRetrievedChunks(3, textItems, embeddingItems)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for i, wantID := range []int64{2, 3, 1} {
		if got[i].ID != wantID {
			t.Fatalf("got[%d].ID = %d, want %d", i, got[i].ID, wantID)
		}
	}
}

func TestKeywordLikePatterns(t *testing.T) {
	got := keywordLikePatterns([]string{"hive", "jdbc_path", "100%"})
	want := []string{"%hive%", "%jdbc_path%", "%100%%"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("patterns = %#v, want %#v", got, want)
	}
}

func TestExtractKeywordsAddsASCIIWordsFromMixedText(t *testing.T) {
	got := extractKeywords("hive的jdbc路径")
	joined := strings.Join(got, "|")
	for _, want := range []string{"hive", "jdbc"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("keywords = %#v, missing %q", got, want)
		}
	}
}

func TestMergeKeywordsPrefersExpandedTerms(t *testing.T) {
	got := mergeKeywords([]string{"jdbc:hive2", "hive", "jdbc"}, []string{"hive的jdbc路径", "hive"}, 4)
	want := []string{"jdbc:hive2", "hive", "jdbc", "hive的jdbc路径"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("keywords = %#v, want %#v", got, want)
	}
}

func TestGenerateAnswerUsesWebSearchWhenDecisionRequiresIt(t *testing.T) {
	ai := &stubAI{webAnswer: "联网答案"}
	service := &Service{ai: ai}

	got, err := service.generateAnswer(context.Background(), "今天有什么更新", "上下文", true)
	if err != nil {
		t.Fatal(err)
	}
	if got != "联网答案" {
		t.Fatalf("answer = %q, want 联网答案", got)
	}
	if ai.webCalls != 1 || ai.plainCalls != 0 {
		t.Fatalf("webCalls=%d plainCalls=%d, want 1/0", ai.webCalls, ai.plainCalls)
	}
}

func TestGenerateAnswerFallsBackWhenWebSearchFails(t *testing.T) {
	ai := &stubAI{webErr: errors.New("unsupported tool"), plainAnswer: "知识库答案"}
	service := &Service{ai: ai}

	got, err := service.generateAnswer(context.Background(), "今天有什么更新", "上下文", true)
	if err != nil {
		t.Fatal(err)
	}
	if got != "知识库答案" {
		t.Fatalf("answer = %q, want 知识库答案", got)
	}
	if ai.webCalls != 1 || ai.plainCalls != 1 {
		t.Fatalf("webCalls=%d plainCalls=%d, want 1/1", ai.webCalls, ai.plainCalls)
	}
}
