package application

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/toolgateway"
	"cyberagent-workbench/internal/webevidence"
)

func TestWebQuestionRelevantOriginalSurvivesModelProjection(t *testing.T) {
	const question = "发射日期"
	const answer = "发射日期为九月二十二日🙂"
	body := strings.Repeat("无关材料。", 1600) + answer + strings.Repeat("其他记录。", 500)
	state, scope := savedSnapshotFixture(t, body)
	before := state.snapshot
	result, err := (&WebEvidenceToolExecutor{store: state}).readWebSnapshotPage(t.Context(), scope, toolgateway.WebFetchPayload{
		Version: "web_fetch.v1", SourceID: state.source.ID, SnapshotID: state.snapshot.ID, Question: question,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := marshalSupervisorToolResultEnvelope(supervisorToolResultEnvelope{Version: supervisorToolResultVersion,
		Tool: "web_fetch", Status: "completed", Stdout: result.Content, Metadata: result.Metadata, Truncated: result.Truncated})
	if err != nil {
		t.Fatal(err)
	}
	call := domain.SupervisorToolCall{ToolName: "web_fetch", Status: domain.SupervisorToolCompleted, ResultJSON: string(raw)}
	projected, err := supervisorWebFetchContextResult(call)
	if err != nil {
		t.Fatal(err)
	}
	var envelope supervisorToolResultEnvelope
	var output webFetchToolOutput
	if json.Unmarshal([]byte(projected), &envelope) != nil || json.Unmarshal([]byte(envelope.Stdout), &output) != nil {
		t.Fatal("invalid model projection")
	}
	e := output.Extraction
	if e == nil || !e.Matched || e.Validate(before) != nil || e.Coverage != webevidence.ExtractionCoveragePartial ||
		e.SpanStart != output.Snapshot.BodyOffset || e.SpanEnd != e.SpanStart+utf8.RuneCountInString(output.Snapshot.Body) ||
		e.SpanStart <= 0 || output.Snapshot.Body != string([]rune(body)[e.SpanStart:e.SpanEnd]) ||
		!strings.Contains(output.Snapshot.Body, answer) || contextmgr.EstimateTokens(output.Snapshot.Body) > webSnapshotContextBodyTokens {
		t.Fatalf("model lost relevant original text or exact range: extraction=%+v offset=%d runes=%d contains=%t", e,
			output.Snapshot.BodyOffset, utf8.RuneCountInString(output.Snapshot.Body), strings.Contains(output.Snapshot.Body, answer))
	}
	if result.Metadata["network_called"] != "false" || !reflect.DeepEqual(state.snapshot, before) || call.ResultJSON != string(raw) {
		t.Fatal("question read called network or mutated durable evidence")
	}
}
