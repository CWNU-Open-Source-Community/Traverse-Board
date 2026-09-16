package application_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/store"
)

func TestThreadTurnEpochPreservesUnexecutedRequirementsAndExcludesUserCancellation(t *testing.T) {
	for _, kind := range []string{"budget", "configuration"} {
		t.Run(kind, func(t *testing.T) {
			st, err := store.Open(filepath.Join(t.TempDir(), "epoch.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			maxTurns := 8
			if kind == "budget" {
				maxTurns = 1
			}
			_, run, err := application.NewRunService(st).Create(t.Context(), application.CreateRunRequest{Goal: "continue with accepted requirements", Profile: "review", ModelRoute: "tool-loop/model", Interactive: true, Budget: domain.Budget{MaxTurns: maxTurns}})
			if err != nil {
				t.Fatal(err)
			}
			provider := &scriptedToolProvider{responses: []*llm.ChatResponse{textResponse(rootActionResponse(domain.RootActionFinish, "First turn complete", "done", "")), textResponse(rootActionResponse(domain.RootActionFinish, "New requirement handled", "done", ""))}}
			router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
			router.RegisterProvider(provider)
			caps := domain.ExecutionPermissionRuntimeCapabilities{WorkspaceSandboxEnabled: true}
			turns := application.NewThreadTurnServiceWithExecutionCapabilities(st, application.NewRunLifecycleControlService(st), application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker()), caps)
			request := application.ExecuteThreadTurnRequest{Version: domain.ThreadMessageProtocolVersion, ThreadID: domain.InitialThreadID(run.ID), Content: "Finish the initial review", OperationKey: "epoch-first-turn", RequestedBy: "test_operator"}
			if _, err := turns.Execute(t.Context(), request); err != nil {
				t.Fatal(err)
			}
			accepted, err := st.EnqueueOperatorSteering(t.Context(), domain.EnqueueOperatorSteeringRequest{RunID: run.ID, SessionID: run.SessionID, Content: "RETAIN_ONLY_FRONTEND_REQUIREMENT", OperationKey: "epoch-retained-input", RequestedBy: "test_operator"})
			if err != nil {
				t.Fatal(err)
			}
			cancelled, err := st.EnqueueOperatorSteering(t.Context(), domain.EnqueueOperatorSteeringRequest{RunID: run.ID, SessionID: run.SessionID, Content: "WITHDRAWN_BACKEND_CHANGE", OperationKey: "epoch-withdrawn-input", RequestedBy: "test_operator"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.CancelOperatorSteering(t.Context(), domain.CancelOperatorSteeringRequest{MessageID: cancelled.Message.ID, OperationKey: "explicit-user-withdraw", RequestedBy: "test_operator", Reason: "I withdraw this requirement"}); err != nil {
				t.Fatal(err)
			}
			if kind == "configuration" {
				if _, err := application.NewThreadExecutionPermissionService(st, caps).Change(t.Context(), application.ChangeThreadExecutionPermissionRequest{ThreadID: request.ThreadID, Mode: string(domain.RunExecutionPermissionWorkspaceAccess), OperationKey: "epoch-new-permission", RequestedBy: "test_operator", Reason: "Use the requested workspace permission", ConfirmWorkspaceAccess: true}); err != nil {
					t.Fatal(err)
				}
			}
			lease, err := st.AcquireRunExecutionLease(t.Context(), domain.AcquireRunExecutionLeaseRequest{RunID: run.ID, OwnerID: "epoch-active-owner", TTL: time.Minute})
			if err != nil {
				t.Fatal(err)
			}
			request.Content = "Continue, keep the frontend-only constraint"
			request.OperationKey = "epoch-second-turn"
			if _, err := turns.Execute(t.Context(), request); err == nil {
				t.Fatal("epoch transition ignored a live worker")
			}
			unchanged, err := st.GetRun(t.Context(), run.ID)
			if err != nil || unchanged.Status != domain.RunRunning {
				t.Fatalf("leased predecessor was changed: %#v %v", unchanged, err)
			}
			if _, _, err := st.ReleaseRunExecutionLease(t.Context(), lease.Lease); err != nil {
				t.Fatal(err)
			}
			result, err := turns.Execute(t.Context(), request)
			if err != nil || !result.Submission.SuccessorCreated || result.Submission.PredecessorRunID != run.ID {
				t.Fatalf("new epoch failed: %#v %v", result, err)
			}
			requests := provider.Requests()
			if len(requests) != 2 {
				t.Fatalf("old pending request was separately reexecuted: %d", len(requests))
			}
			retained := false
			for _, message := range requests[1].Messages {
				if strings.Contains(message.Content, "WITHDRAWN_BACKEND_CHANGE") {
					t.Fatalf("user cancellation was reauthorized: %#v", requests[1].Messages)
				}
				if message.Role == "user" && strings.Contains(message.Content, "RETAIN_ONLY_FRONTEND_REQUIREMENT") {
					retained = true
				}
			}
			if !retained {
				t.Fatalf("epoch dropped accepted requirement: %#v", requests[1].Messages)
			}
			old, err := st.GetOperatorSteering(t.Context(), accepted.Message.ID)
			if err != nil || old.Status != domain.OperatorSteeringCancelled || old.SessionMessageID != 0 {
				t.Fatalf("old input falsely marked executed: %#v %v", old, err)
			}
			if _, err := turns.Execute(t.Context(), request); err != nil || len(provider.Requests()) != 2 {
				t.Fatalf("epoch retry duplicated execution: %v", err)
			}
			stored, err := st.GetRun(t.Context(), run.ID)
			if err != nil || stored.Budget.MaxTurns != maxTurns || stored.Status != domain.RunCancelled {
				t.Fatalf("old budget was refilled: %#v %v", stored, err)
			}
			request.Content = "different request"
			if _, err := turns.Execute(t.Context(), request); apperror.CodeOf(err) != apperror.CodeConflict {
				t.Fatalf("epoch key lost exact intent: %v", err)
			}
		})
	}
}
