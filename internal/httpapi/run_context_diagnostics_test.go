package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/store"
)

type contextDiagnosticFixtureStore struct {
	*store.SQLiteStore
	records []events.Event
}

func (s *contextDiagnosticFixtureStore) ListRunContextDiagnosticEvents(context.Context, string, int) ([]events.Event, error) {
	return s.records, nil
}

func TestRunContextDiagnosticsSeparatesReceivedFromSavedWithoutRawPayload(t *testing.T) {
	f := newAPIFixture(t)
	sha := strings.Repeat("a", 64)
	makeEvent := func(sequence int64, kind, source string, payload map[string]any) events.Event {
		payload["attempt_id"] = "context-attempt"
		payload["compaction_response"] = "private-provider-response"
		encoded, _ := json.Marshal(payload)
		return events.Event{RunID: f.run.ID, MissionID: f.run.MissionID, Sequence: sequence,
			Type: kind, Source: source, PayloadJSON: string(encoded), CreatedAt: time.Now().UTC()}
	}
	stub := &contextDiagnosticFixtureStore{SQLiteStore: f.store, records: []events.Event{
		makeEvent(5, "session.context_compacted", "context_manager", map[string]any{
			"source_sha256": sha, "summary_id": 8, "generated": false, "removed_messages": 12,
			"generation_fallback_reason": "generation_provider_failure: private-provider-secret"}),
		makeEvent(4, events.ModelCompletedEvent, "model_gateway", map[string]any{
			"purpose": "context_compaction", "compaction_source_sha256": sha, "model_attempt": 1}),
		makeEvent(3, events.ModelStartedEvent, "model_gateway", map[string]any{
			"purpose": "context_compaction", "compaction_source_sha256": sha, "model_attempt": 1}),
	}}
	f.api.store = stub
	path := "/api/v1/runs/" + f.run.ID + "/context-summary"
	response := f.get(t, path)
	var view RunContextSummaryView
	decodeData(t, response, &view)
	if view.Diagnostics == nil || len(view.Diagnostics.Records) != 3 || view.Diagnostics.Truncated {
		t.Fatalf("missing diagnostic history: %#v", view.Diagnostics)
	}
	records := view.Diagnostics.Records
	if records[0].Phase != "summary_saved" || records[0].SummaryID != 8 || records[0].Generated == nil || *records[0].Generated ||
		records[0].FallbackCode != "generation_provider_failure" || records[1].Phase != "generation_received" || records[1].SummaryID != 0 ||
		records[2].Phase != "generation_started" || strings.Contains(response.Body.String(), "private-provider") {
		t.Fatalf("receipt phases or privacy lost: %s", response.Body.String())
	}
	// A start or response receipt alone is never upgraded into a saved summary.
	stub.records = stub.records[1:]
	decodeData(t, f.get(t, path), &view)
	if view.Diagnostics.Records[0].Phase != "generation_received" || view.CurrentSummary != nil {
		t.Fatalf("candidate was presented as saved: %#v", view)
	}
	stub.records[0].RunID = "foreign-run"
	saved, err := f.store.SaveContextSummary(t.Context(), contextmgr.Summary{TaskID: f.run.SessionID, WorkspaceID: f.workspace.ID,
		Content: "已保存且可验证的摘要仍保留", SourceMessageCount: 24, PreservedMessageCount: 4})
	if err != nil {
		t.Fatal(err)
	}
	response = f.get(t, path)
	view = RunContextSummaryView{}
	decodeData(t, response, &view)
	if response.Code != http.StatusOK || !view.DiagnosticsUnavailable || view.Diagnostics != nil || strings.Contains(response.Body.String(), sha) {
		t.Fatalf("foreign record leaked: %d %s", response.Code, response.Body.String())
	}
	if view.CurrentSummary == nil || view.CurrentSummary.ID != saved.ID || view.CurrentSummary.Content != saved.Content {
		t.Fatalf("unavailable diagnostics hid the verified summary: %#v", view)
	}
}

func TestRunContextDiagnosticsBoundsAndRejectsMisorderedOrMalformedReceipts(t *testing.T) {
	f := newAPIFixture(t)
	stub := &contextDiagnosticFixtureStore{SQLiteStore: f.store}
	for sequence := int64(41); sequence > 0; sequence-- {
		stub.records = append(stub.records, events.Event{RunID: f.run.ID, MissionID: f.run.MissionID,
			Sequence: sequence, Type: events.ModelStartedEvent, Source: "model_gateway", CreatedAt: time.Now().UTC(),
			PayloadJSON: `{"purpose":"context_compaction","attempt_id":"context-attempt","model_attempt":1,"compaction_source_sha256":"` + strings.Repeat("a", 64) + `"}`})
	}
	view, err := readRunContextDiagnostics(t.Context(), stub, f.run)
	if err != nil || !view.Truncated || len(view.Records) != 40 || view.Records[39].Sequence != 2 {
		t.Fatalf("bound/order lost: %#v %v", view, err)
	}
	stub.records[1].Sequence = stub.records[0].Sequence
	if _, err := readRunContextDiagnostics(t.Context(), stub, f.run); err == nil {
		t.Fatal("duplicate/out-of-order sequence accepted")
	}
	stub.records = stub.records[:1]
	stub.records[0].PayloadJSON = `{"attempt_id":"context-attempt","model_attempt":1}`
	if _, err := readRunContextDiagnostics(t.Context(), stub, f.run); err == nil {
		t.Fatal("malformed receipt accepted")
	}
	if got := contextFallbackCode("private arbitrary message"); got != "unclassified" {
		t.Fatalf("unknown fallback guessed: %s", got)
	}
	for _, reason := range []string{"generation_input_data", "generation_input_window", "generation_token_budget"} {
		if got := contextFallbackCode(reason + ": private diagnostic detail"); got != reason {
			t.Fatalf("known fallback was not projected safely: %s", got)
		}
	}
}
