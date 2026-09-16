package application_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/runactivity"
)

func TestApprovalContinuationSealsExactModelFailureStageBeforeClosingInput(t *testing.T) {
	for _, tc := range []struct {
		name, stage, notice string
		response            *llm.ChatResponse
	}{
		{"empty", domain.ThreadFailureEmptyModelResponse, "模型没有返回有效答复", textResponse(" \t\n")},
		{"tool", domain.ThreadFailureToolRequestRejected, "该批请求尚未执行", toolResponse("invalid-tool", "note_create", `{"title":123,"private":"must-not-enter-stage-event"}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantCalls := 3
			if tc.name == "tool" {
				wantCalls = 4
			}
			st, run, _, input := toolBoundaryFixture(t, domain.Budget{MaxTurns: 8, MaxToolCalls: 20})
			provider := &boundaryJourneyProvider{}
			provider.respond = func(ctx context.Context, request llm.ChatRequest, index int) (*llm.ChatResponse, error) {
				switch index {
				case 1:
					return boundaryPropose("proposal"), nil
				case 2:
					return textResponse(rootActionResponse(domain.RootActionWait, "Review", "", "operator review")), nil
				case 3, 4:
					return tc.response, nil
				default:
					return nil, fmt.Errorf("unexpected model call %d", index)
				}
			}
			turns := toolBoundaryService(st, st, provider)
			if _, err := turns.Execute(t.Context(), input); err != nil {
				t.Fatal(err)
			}
			edit := approveBoundaryEdit(t, st, run)
			request := application.ApprovalContinuationRequest{RunID: run.ID, Kind: "file_edit", ProposalID: edit.ID}
			result := turns.ResumeApproval(t.Context(), request)
			if result.State != "failed" || result.ErrorCode != "FAILED_PRECONDITION" || len(provider.Requests()) != wantCalls {
				t.Fatalf("continuation=%#v calls=%d", result, len(provider.Requests()))
			}
			cp, _, err := st.GetSupervisorCheckpoint(t.Context(), run.ID)
			if err != nil || cp.Phase != domain.SupervisorIdle || cp.AttemptID != "" || cp.NextTurn != 3 {
				t.Fatalf("checkpoint was not safely closed: %#v %v", cp, err)
			}
			recovery, found, err := st.GetThreadRunRecovery(t.Context(), input.ThreadID)
			if err != nil || !found || recovery.FailureStage != tc.stage {
				t.Fatalf("closed checkpoint lost exact cause: %#v %v", recovery, err)
			}
			list, err := st.ListRunEvents(t.Context(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			sealed := ""
			for _, event := range list {
				if event.Type == events.RunExecutionHandoffCompletedEvent && event.SubjectID == result.HandoffID {
					var payload map[string]any
					if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
						t.Fatal(err)
					}
					if payload["failure_stage"] != tc.stage || payload["failure_attempt_id"] == "" ||
						strings.Contains(event.PayloadJSON, "must-not-enter-stage-event") {
						t.Fatalf("invalid sealed category: %s", event.PayloadJSON)
					}
					sealed = event.PayloadJSON
				}
			}
			projection, err := runactivity.Build(run.ID, list, false)
			if err != nil {
				t.Fatal(err)
			}
			noticed := false
			for _, item := range projection.Items {
				noticed = noticed || strings.HasPrefix(item.Detail, "审批已保存") && strings.Contains(item.Detail, tc.notice)
			}
			if sealed == "" || !noticed {
				t.Fatalf("stage or conversation notice missing: %q %+v", sealed, projection.Items)
			}
			again := turns.ResumeApproval(t.Context(), request)
			if !again.Replayed || again.HandoffID != result.HandoffID || len(provider.Requests()) != wantCalls {
				t.Fatalf("replay changed execution: %#v", again)
			}
			after, err := st.ListRunEvents(t.Context(), run.ID)
			if err != nil || len(after) != len(list) {
				t.Fatalf("replay changed history: before=%d after=%d err=%v", len(list), len(after), err)
			}
			stored, err := st.GetFileEdit(t.Context(), edit.ID)
			if err != nil || stored.Status != "approved" {
				t.Fatalf("saved approval changed: %#v %v", stored, err)
			}
			assertOneBoundaryTranscriptInput(t, st, input.ThreadID, input.Content)
		})
	}
}
