package store

import (
	"database/sql"
	"encoding/json"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
)

// An isolated event table makes the cross-attempt and legacy read boundaries
// explicit. The application regression exercises real handoff transactions.
func TestRecordedApprovalFailureStageDoesNotReclassifyHistory(t *testing.T) {
	for _, tc := range []struct {
		name, sealedStage, lastType, lastStage, want string
	}{
		{"exact", domain.ThreadFailureEmptyModelResponse, events.ModelFailedEvent, domain.ThreadFailureEmptyModelResponse, domain.ThreadFailureEmptyModelResponse},
		{"historical_without_stage", "", events.ModelFailedEvent, domain.ThreadFailureEmptyModelResponse, ""},
		{"last_completed", "", events.ModelCompletedEvent, "", ""},
		{"unknown_stage", "future_stage", events.ModelFailedEvent, "future_stage", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := sql.Open("sqlite3", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			db.SetMaxOpenConns(1)
			if _, err := db.Exec(`CREATE TABLE run_events (run_id TEXT,sequence INTEGER,subject_id TEXT,type TEXT,source TEXT,payload_json TEXT)`); err != nil {
				t.Fatal(err)
			}
			insert := func(seq int, subject, kind, source string, payload map[string]any) {
				t.Helper()
				body, _ := json.Marshal(payload)
				if _, err := db.Exec(`INSERT INTO run_events VALUES (?,?,?,?,?,?)`, "run-1", seq, subject, kind, source, string(body)); err != nil {
					t.Fatal(err)
				}
			}
			insert(1, "old-model", events.ModelFailedEvent, "model_gateway", map[string]any{
				"attempt_id": "exact-attempt", "failure_stage": domain.ThreadFailureToolRequestRejected})
			insert(2, "exact-model", tc.lastType, "model_gateway", map[string]any{
				"attempt_id": "exact-attempt", "failure_stage": tc.lastStage})
			insert(3, "handoff-1", events.RunExecutionHandoffCompletedEvent, "run_execution_handoff", map[string]any{
				"failure_stage": tc.sealedStage, "failure_attempt_id": "exact-attempt"})
			insert(4, "new-model", events.ModelFailedEvent, "model_gateway", map[string]any{
				"attempt_id": "other-attempt", "failure_stage": domain.ThreadFailureInvalidModelResponse})
			handoff := domain.RunExecutionHandoff{Operation: domain.RunExecutionHandoffOperation{ID: "handoff-1", RunID: "run-1", RequestedBy: approvalContinuationActor},
				Result: &domain.RunExecutionHandoffResult{Status: domain.RunExecutionHandoffFailed, ErrorCode: "failed_precondition", CompletionEventSequence: 3}}
			stage, err := recordedApprovalContinuationFailureStage(t.Context(), db, handoff)
			if err != nil || stage != tc.want {
				t.Fatalf("stage=%q want=%q err=%v", stage, tc.want, err)
			}
			if tc.name == "last_completed" {
				stage, err := recordedThreadModelFailureStage(t.Context(), db, "run-1", "exact-attempt")
				if err != nil || stage != "" {
					t.Fatalf("prior failure survived a successful model completion: %q %v", stage, err)
				}
			}
		})
	}
}
