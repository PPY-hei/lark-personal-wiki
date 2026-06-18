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
