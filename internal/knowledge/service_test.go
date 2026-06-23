package knowledge

import (
	"strings"
	"testing"
)

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
