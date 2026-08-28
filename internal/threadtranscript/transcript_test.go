package threadtranscript

import (
	"encoding/json"
	"testing"
	"time"

	"cyberagent-workbench/internal/events"
)

func TestBuildOrdersRunBoundariesMessagesAndStructuredToolStages(t *testing.T) {
	now := time.Date(2026, 8, 24, 1, 2, 3, 0, time.UTC)
	batch, _ := json.Marshal(map[string]any{
		"tools":              []string{"read_file", "replace_file"},
		"stream_response_id": "resp-stable", "stream_item_ids": []string{"item-read", "item-edit"},
		"stream_call_ids": []string{"call-read", "call-edit"},
	})
	source := []Source{
		eventSource("run-2", 2, 2, events.SupervisorToolExecutionStartedEvent,
			`{"tool":"read_file","status":"pending","stream_response_id":"resp-stable","stream_item_id":"item-read","stream_call_id":"call-read","durable_call_id":"durable-read"}`, now.Add(5*time.Second)),
		{RunID: "run-1", SessionID: "session-1", Ordinal: 1, RunStatus: "failed", CreatedAt: now},
		eventSource("run-1", 1, 1, events.SessionMessageEvent,
			`{"role":"assistant","content":"public answer","source_kind":"model_response","instruction_authorized":false}`, now.Add(time.Second)),
		{RunID: "run-2", SessionID: "session-2", Ordinal: 2,
			PredecessorRunID: "run-1", PredecessorRunStatus: "failed",
			RunStatus: "running", CreatedAt: now.Add(2 * time.Second)},
		eventSource("run-2", 2, 1, events.SupervisorToolBatchEvent, string(batch), now.Add(4*time.Second)),
	}
	items, err := Build("thread-1", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 6 {
		t.Fatalf("unexpected transcript items: %#v", items)
	}
	if items[0].ID != "run-boundary:run-1" || items[1].Type != TypeMessage ||
		items[2].BoundaryReason != "predecessor_terminal_failed" {
		t.Fatalf("Run narrative is not stable: %#v", items[:3])
	}
	if items[3].CanonicalID != "item-read" || items[3].Type != TypeRead ||
		items[3].Stage != StageArgumentsReady || items[4].Type != TypeEdit {
		t.Fatalf("tool batch was not projected item-by-item: %#v", items[3:5])
	}
	if items[5].CanonicalID != "item-read" || items[5].Stage != StageRunning ||
		items[5].DurableCallID != "durable-read" || !items[5].Verifiable {
		t.Fatalf("durable tool execution identity was lost: %#v", items[5])
	}
}

func TestBuildProjectsPendingAndCancelledComposerInputWithoutDuplicatingCommittedInput(t *testing.T) {
	now := time.Now().UTC()
	queued := eventSource("run-1", 1, 1, events.OperatorSteeringQueuedEvent, `{}`, now)
	queued.Event.SubjectID = "steering-1"
	queued.OperatorStatus = "pending"
	queued.OperatorContent = "continue safely"
	items, err := Build("thread-1", []Source{queued})
	if err != nil || len(items) != 1 || items[0].SourceRef != "steering-1" ||
		items[0].Detail != "continue safely" || !items[0].InstructionAuthorized {
		t.Fatalf("pending Composer projection is wrong: items=%#v err=%v", items, err)
	}
	queued.OperatorStatus = "cancelled"
	items, err = Build("thread-1", []Source{queued})
	if err != nil || len(items) != 1 || items[0].Status != "cancelled" ||
		items[0].Stage != StageBlocked || items[0].InstructionAuthorized ||
		items[0].Title != "用户消息已取消" || items[0].Detail != "continue safely" {
		t.Fatalf("cancelled Composer projection is wrong: items=%#v err=%v", items, err)
	}
	queued.OperatorStatus = "committed"
	items, err = Build("thread-1", []Source{queued})
	if err != nil || len(items) != 0 {
		t.Fatalf("committed steering duplicated Session history: items=%#v err=%v", items, err)
	}
}

func TestBuildRejectsUnboundedOrInconsistentSource(t *testing.T) {
	now := time.Now().UTC()
	source := make([]Source, MaxSourceRecords+1)
	if _, err := Build("thread-1", source); err == nil {
		t.Fatal("unbounded source page was accepted")
	}
	broken := eventSource("run-1", 1, 1, events.RunStatusChangedEvent,
		`{"status":"running"}`, now)
	broken.Event.RunID = "other-run"
	if _, err := Build("thread-1", []Source{broken}); err == nil {
		t.Fatal("cross-Run source event was accepted")
	}
}

func TestClassifyToolUsesExactCurrentToolRegistryNames(t *testing.T) {
	tests := map[string]ActivityType{
		"workspace_list":              TypeSearch,
		"workspace_glob":              TypeSearch,
		"code_references":             TypeSearch,
		"web_search":                  TypeSearch,
		"workspace_read":              TypeRead,
		"web_fetch":                   TypeRead,
		"web_citation":                TypeRead,
		"github_review_evidence_read": TypeRead,
		"workspace_apply":             TypeEdit,
		"code_diagnostics":            TypeVerify,
		"work_item_create":            TypeCheckpoint,
		"plan_delivery_propose":       TypeDelivery,
		"shell":                       TypeExecute,
		"please_read_everything":      TypeExecute,
	}
	for name, want := range tests {
		if got := classifyTool(name); got != want {
			t.Errorf("classifyTool(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestBuildProjectsOnlyValidatedWebEvidencePresentation(t *testing.T) {
	now := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	payload, _ := json.Marshal(map[string]any{
		"tool": "web_citation", "status": "completed",
		"stream_response_id": "response-web", "stream_item_id": "item-web",
		"stream_call_id": "stream-call-web", "durable_call_id": "call-web",
		"web_evidence": map[string]any{
			"version": "web_evidence_presentation.v1", "source_id": "source-web",
			"snapshot_id": "snapshot-web", "citation_id": "citation-web",
			"url": "https://docs.example.com/report", "title": "Fetched report",
			"state": "partial", "fetched_at": now,
			"stale_at": now.Add(24 * time.Hour),
			"digest":   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"partial":  true, "stale": false, "citeable": true,
			"untrusted": true, "instruction_authorized": false,
		},
	})
	var raw map[string]any
	_ = json.Unmarshal(payload, &raw)
	presentation := raw["web_evidence"].(map[string]any)
	items, err := Build("thread-web", []Source{eventSource("run-web", 1, 1,
		events.SupervisorToolResultEvent, string(payload), now)})
	if err != nil || len(items) != 1 || items[0].WebEvidence == nil ||
		items[0].WebEvidence.URL != "https://docs.example.com/report" ||
		!items[0].WebEvidence.Untrusted || items[0].WebEvidence.InstructionAuthorized {
		t.Fatalf("web presentation items=%#v err=%v", items, err)
	}
	presentation["url"] = "http://127.0.0.1/private"
	payload, _ = json.Marshal(raw)
	items, err = Build("thread-web", []Source{eventSource("run-web", 1, 1,
		events.SupervisorToolResultEvent, string(payload), now)})
	if err != nil || len(items) != 1 || items[0].WebEvidence != nil {
		t.Fatalf("unsafe presentation items=%#v err=%v", items, err)
	}
}

func eventSource(runID string, ordinal, sequence int64, eventType, payload string,
	createdAt time.Time,
) Source {
	event := &events.Event{EventID: "event-" + runID + "-" + eventType,
		Version: events.EnvelopeVersion, RunID: runID, MissionID: "mission-1",
		Sequence: sequence, Type: eventType, Source: "test", PayloadJSON: payload,
		CreatedAt: createdAt}
	return Source{RunID: runID, SessionID: "session-" + runID, Ordinal: ordinal,
		RunStatus: "running", Sequence: sequence, CreatedAt: createdAt, Event: event}
}
