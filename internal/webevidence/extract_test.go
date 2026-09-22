package webevidence

import (
	"strings"
	"testing"
	"time"
)

func TestExtractSnapshotFindsLateUnicodeMarkerByRuneOffset(t *testing.T) {
	body := strings.Repeat("unrelated prefix 段落🙂 ", 500) +
		"关键证据 火星样本 RETURNED_MARKER_90210 位于这里" + strings.Repeat(" tail", 500)
	snapshot := extractionTestSnapshot(t, body, SourceFetched)
	extraction, err := ExtractSnapshot(snapshot, "火星样本 returned_marker_90210")
	if err != nil || !extraction.Matched || extraction.SpanStart == 0 ||
		extraction.SpanEnd-extraction.SpanStart > MaxQuestionExcerptRunes {
		t.Fatalf("extraction=%#v err=%v", extraction, err)
	}
	selected := string([]rune(body)[extraction.SpanStart:extraction.SpanEnd])
	if !strings.Contains(selected, "RETURNED_MARKER_90210") ||
		extraction.BodySHA256 != DigestBytes([]byte(body)) {
		t.Fatalf("selected wrong rune span: %q %#v", selected, extraction)
	}
}

func TestExtractSnapshotNoMatchIsStablePrefix(t *testing.T) {
	snapshot := extractionTestSnapshot(t, "alpha beta emoji 🙂 content", SourcePartial)
	extraction, err := ExtractSnapshot(snapshot, "完全不存在的词语")
	if err != nil || extraction.Matched || extraction.SpanStart != 0 ||
		extraction.SpanEnd != len([]rune(snapshot.Body)) ||
		extraction.Coverage != ExtractionCoveragePartial {
		t.Fatalf("fallback=%#v err=%v", extraction, err)
	}
}

func TestExtractSnapshotWithinKeepsLateAnswerInsideProjectionBudget(t *testing.T) {
	body := strings.Repeat("无关", 4000) + " 发射日期 2026-09-22"
	snapshot := extractionTestSnapshot(t, body, SourceFetched)
	extraction, err := ExtractSnapshotWithin(snapshot, "发射日期", 512)
	if err != nil || !extraction.Matched || extraction.SpanEnd-extraction.SpanStart > 512 {
		t.Fatalf("extraction=%#v err=%v", extraction, err)
	}
	selected := string([]rune(body)[extraction.SpanStart:extraction.SpanEnd])
	if !strings.Contains(selected, "发射日期 2026-09-22") {
		t.Fatalf("smaller projection span lost answer: %q", selected)
	}
	if _, err := ExtractSnapshotWithin(snapshot, "发射日期", 0); err == nil {
		t.Fatal("accepted empty extraction window")
	}
}

func TestExtractionValidateBindsNormalizedQuestionAndCoverage(t *testing.T) {
	snapshot := extractionTestSnapshot(t, "answer", SourceFetched)
	extraction, err := ExtractSnapshot(snapshot, "answer")
	if err != nil {
		t.Fatal(err)
	}
	extraction.Question = " answer "
	if extraction.Validate(snapshot) == nil {
		t.Fatal("accepted non-normalized saved question")
	}
	extraction.Question = "answer"
	extraction.Coverage = ExtractionCoveragePartial
	if extraction.Validate(snapshot) == nil {
		t.Fatal("accepted partial coverage for complete snapshot")
	}
}

func extractionTestSnapshot(t *testing.T, body string, state SourceState) Snapshot {
	t.Helper()
	now := time.Now().UTC()
	snapshot, err := SealSnapshot(Snapshot{ID: "web-snapshot-extraction",
		SourceID: "web-source-extraction", RunID: "run-extraction", MissionID: "mission-extraction",
		RequestedURL: "https://example.com/article", FinalURL: "https://example.com/article",
		HTTPStatus: 200, FetchedAt: now, StaleAt: now.Add(time.Hour), Digest: DigestBytes([]byte("raw")),
		MIME: "text/plain", Body: body, State: state, Truncated: state == SourcePartial,
		Robots: "allowed", Provider: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
