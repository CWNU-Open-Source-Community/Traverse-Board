package store

import (
	"context"
	"reflect"
	"testing"

	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/events"
)

func TestContextDiagnosticsReadsRealCompactionReceiptsWithoutUnrelatedTraffic(t *testing.T) {
	st, turn, _ := newSupervisorCompactionTest(t)
	result, _, err := st.CompactSupervisorContextGenerated(t.Context(), turn.Checkpoint, 2,
		supervisorSummaryGeneratorFunc(func(ctx context.Context, request contextmgr.SummaryGenerationRequest) (contextmgr.SummaryGenerationResponse, error) {
			response, _, _ := compactionTestComplete(t, st, turn.Checkpoint, request, generatedTestJSON)
			return response, nil
		}))
	if err != nil || !result.Compacted {
		t.Fatalf("real compaction failed: %#v %v", result, err)
	}
	before, err := st.ListRunContextDiagnosticEvents(t.Context(), turn.Run.ID, 41)
	if err != nil || len(before) != 3 || before[0].Type != "session.context_compacted" ||
		before[1].Type != events.ModelCompletedEvent || before[2].Type != events.ModelStartedEvent {
		t.Fatalf("missing real receipts: %#v %v", before, err)
	}
	tx, err := st.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for range 45 {
		value, err := events.New(turn.Run.ID, turn.Run.MissionID, events.ModelCompletedEvent, "model_gateway", "ordinary-model",
			map[string]any{"purpose": "ordinary_task", "compaction_response": "not-a-compaction"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := insertRunEventTx(t.Context(), tx, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	after, err := st.ListRunContextDiagnosticEvents(t.Context(), turn.Run.ID, 41)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("unrelated traffic hid receipts: %#v %v", after, err)
	}
	bounded, err := st.ListRunContextDiagnosticEvents(t.Context(), turn.Run.ID, 2)
	if err != nil || len(bounded) != 2 || bounded[0].Sequence != before[0].Sequence {
		t.Fatalf("latest bound failed: %#v %v", bounded, err)
	}
	if _, err := st.ListRunContextDiagnosticEvents(t.Context(), turn.Run.ID, 0); err == nil {
		t.Fatal("invalid bound accepted")
	}
}
