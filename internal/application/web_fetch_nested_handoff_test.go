package application_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
	"cyberagent-workbench/internal/webevidence"
)

type nestedWebHandoffStore struct {
	*store.SQLiteStore
	prepared []domain.RunExecutionHandoff
}

type nestedWebProvider struct {
	*scriptedToolProvider
	stopNext context.CancelFunc
}

func (p *nestedWebProvider) StreamChat(ctx context.Context, request llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	if p.stopNext != nil {
		p.mu.Lock()
		p.requests = append(p.requests, request)
		p.mu.Unlock()
		p.stopNext()
		return nil, ctx.Err()
	}
	return p.scriptedToolProvider.StreamChat(ctx, request)
}

func (s *nestedWebHandoffStore) PrepareWebFetchAuthorizationHandoff(ctx context.Context, id, attempt string,
	phase domain.SupervisorPhase,
) (domain.RunExecutionHandoff, bool, error) {
	h, replay, err := s.SQLiteStore.PrepareWebFetchAuthorizationHandoff(ctx, id, attempt, phase)
	if err == nil && h.Operation.ID != "" {
		s.prepared = append(s.prepared, h)
	}
	return h, replay, err
}

func TestNestedWebFetchApprovalClosesEachContinuationBeforeNextReview(t *testing.T) {
	for _, name := range []string{"approve_second", "deny_second", "provider_failure", "stop_during_resume"} {
		t.Run(name, func(t *testing.T) {
			approveSecond := name != "deny_second"
			st, err := store.Open(filepath.Join(t.TempDir(), "nested-web.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			wrapped := &nestedWebHandoffStore{SQLiteStore: st}
			_, run, err := application.NewRunService(st).Create(t.Context(), application.CreateRunRequest{
				Goal: "Read two independently reviewed public sources", Profile: "review", Surface: "code", Phase: "deliver",
				ModelRoute: "tool-loop/model", Interactive: true, NetworkMode: "disabled",
				Budget: domain.Budget{MaxTurns: 5, MaxToolCalls: 5},
			})
			if err != nil {
				t.Fatal(err)
			}
			batch := toolResponse("nested-web-one", "web_fetch", `{"version":"web_fetch.v1","url":"https://one.example.net/report"}`)
			batch.ToolCalls = append(batch.ToolCalls, toolResponse("nested-web-two", "web_fetch", `{"version":"web_fetch.v1","url":"https://two.example.net/report"}`).ToolCalls...)
			provider := &nestedWebProvider{scriptedToolProvider: &scriptedToolProvider{responses: []*llm.ChatResponse{batch,
				textResponse(rootActionResponse(domain.RootActionFinish, "Both review outcomes observed", "done", "")),
			}}}
			router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
			router.RegisterProvider(provider)
			checker := policy.NewDefaultChecker()
			backend := &applicationWebFetchBackend{}
			handoff := application.NewRunExecutionHandoffService(wrapped, router, checker).
				WithWebEvidence(webevidence.NewService(st, nil, backend)).WithWebFetchAuthorizationScheduler(true)
			turns := application.NewThreadTurnService(st, application.NewRunLifecycleControlService(st), handoff)
			request := application.ExecuteThreadTurnRequest{Version: domain.ThreadMessageProtocolVersion,
				ThreadID: domain.InitialThreadID(run.ID), Content: "Read both sources exactly once and preserve their outcomes",
				OperationKey: "nested-web-original-input", RequestedBy: "test_operator"}
			first, err := turns.Execute(t.Context(), request)
			if err != nil || first.Execution == nil || first.Execution.Handoff.Result == nil || backend.calls != 0 {
				t.Fatalf("first approval boundary: %#v fetches=%d err=%v", first, backend.calls, err)
			}
			decide := func(approve bool, key string) domain.WebFetchAuthorization {
				t.Helper()
				pending, err := st.ListApprovals(t.Context(), approval.ListFilter{RunID: run.ID, Status: approval.StatusPending, Limit: 10})
				if err != nil || len(pending) != 1 {
					t.Fatalf("pending=%#v err=%v", pending, err)
				}
				auth, err := st.GetWebFetchAuthorizationByApproval(t.Context(), pending[0].ID)
				if err != nil {
					t.Fatal(err)
				}
				action := application.ApprovalControlApproveOnce
				if !approve {
					action = application.ApprovalControlDeny
				}
				_, err = application.NewApprovalControlService(st, toolgateway.New(st, checker), checker).Decide(t.Context(), application.DecideApprovalControlRequest{
					Version: application.ApprovalControlProtocolVersion, RunID: run.ID, ApprovalID: pending[0].ID,
					Action: action, OperationKey: key, ReviewedBy: "test_operator",
				})
				if err != nil {
					t.Fatal(err)
				}
				return auth
			}
			one := decide(true, "nested-first-approve")
			beforeModels := len(provider.Requests())
			resumed, _, err := handoff.ResumeWebFetchAuthorization(t.Context(), run.ID, one.ID)
			if err != nil {
				t.Fatalf("nested approval continuation lost terminal receipt: %v", err)
			}
			if resumed.RunStatus != domain.RunWaitingApproval || backend.calls != 1 || len(provider.Requests()) != beforeModels {
				t.Fatalf("nested review performed extra work: %#v fetches=%d model requests=%d", resumed, backend.calls, len(provider.Requests()))
			}
			if len(wrapped.prepared) != 1 {
				t.Fatalf("continuations=%d", len(wrapped.prepared))
			}
			h, found, err := st.GetRunExecutionHandoff(t.Context(), wrapped.prepared[0].Operation.KeyDigest)
			if err != nil || !found || h.Result == nil || h.Result.Status != domain.RunExecutionHandoffCompleted ||
				h.Result.RunStatus != domain.RunWaitingApproval || !h.Result.ToolCalled || !h.Result.ModelCalled || h.Result.PreparedCount != 1 {
				t.Fatalf("nested receipt=%#v found=%t err=%v", h, found, err)
			}
			if modelAttempt, err := st.GetWebFetchContinuationModelAttempt(t.Context(), one, resumed.AttemptID); err != nil || modelAttempt != 1 {
				t.Fatalf("exact model provenance=%d err=%v", modelAttempt, err)
			}
			// A model completion elsewhere in this Run cannot supply provenance
			// for another attempt, call, Session or logical Turn.
			for _, changed := range []domain.WebFetchAuthorization{
				func() domain.WebFetchAuthorization { v := one; v.SessionID = "other-session"; return v }(),
				func() domain.WebFetchAuthorization { v := one; v.SupervisorToolCallID = "other-call"; return v }(),
				func() domain.WebFetchAuthorization { v := one; v.SupervisorTurn++; return v }(),
			} {
				if _, err := st.GetWebFetchContinuationModelAttempt(t.Context(), changed, resumed.AttemptID); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
					t.Fatalf("unrelated provenance was accepted: %v", err)
				}
			}
			if _, err := st.GetWebFetchContinuationModelAttempt(t.Context(), one, "other-attempt"); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
				t.Fatalf("unrelated attempt was accepted: %v", err)
			}
			// The receipt is cumulative for the logical turn, not a claim that
			// this approval-resume HTTP operation sent another model request.
			two := decide(approveSecond, "nested-second-review")
			// Recreate the service at the approval boundary. Durable call and
			// handoff identities, not a previous service's memory, own the resume.
			handoff = application.NewRunExecutionHandoffService(wrapped, router, checker).
				WithWebEvidence(webevidence.NewService(st, nil, backend)).WithWebFetchAuthorizationScheduler(true)
			resumeCtx := t.Context()
			if name == "provider_failure" {
				provider.responses = nil
			}
			if name == "stop_during_resume" {
				var cancel context.CancelFunc
				resumeCtx, cancel = context.WithCancel(t.Context())
				defer cancel()
				provider.stopNext = cancel
			}
			_, _, resumeErr := handoff.ResumeWebFetchAuthorization(resumeCtx, run.ID, two.ID)
			failedResume := name == "provider_failure" || name == "stop_during_resume"
			if (resumeErr != nil) != failedResume {
				t.Fatalf("second resume error=%v failed=%t", resumeErr, failedResume)
			}
			if name == "stop_during_resume" && !errors.Is(resumeErr, context.Canceled) && apperror.CodeOf(resumeErr) != apperror.CodeCancelled {
				t.Fatalf("cancellation was not preserved: %v", resumeErr)
			}
			wantFetches := 1
			if approveSecond {
				wantFetches = 2
			}
			if backend.calls != wantFetches || len(provider.Requests()) != 2 {
				t.Fatalf("fetches=%d requests=%d", backend.calls, len(provider.Requests()))
			}
			for _, prepared := range wrapped.prepared {
				value, found, err := st.GetRunExecutionHandoff(t.Context(), prepared.Operation.KeyDigest)
				if err != nil || !found || value.Result == nil {
					t.Fatalf("unsettled operation=%#v err=%v", value, err)
				}
			}
			last, _, err := st.GetRunExecutionHandoff(t.Context(), wrapped.prepared[len(wrapped.prepared)-1].Operation.KeyDigest)
			if err != nil || (last.Result.Status == domain.RunExecutionHandoffFailed) != failedResume {
				t.Fatalf("terminal status does not preserve outcome: %#v err=%v", last.Result, err)
			}
			lease, found, err := st.GetRunExecutionLease(t.Context(), run.ID)
			if err != nil || !found || lease.Status != domain.RunExecutionLeaseReleased {
				t.Fatalf("lease remains active: %#v err=%v", lease, err)
			}
			unchanged, _, err := st.GetRunExecutionHandoff(t.Context(), h.Operation.KeyDigest)
			if err != nil || !reflect.DeepEqual(h, unchanged) {
				t.Fatal("completed nested receipt was rewritten")
			}
			original, _, err := st.GetRunExecutionHandoff(t.Context(), first.Execution.Handoff.Operation.KeyDigest)
			if err != nil || !reflect.DeepEqual(original, first.Execution.Handoff) {
				t.Fatal("original wait receipt was rewritten")
			}
			input, err := st.GetOperatorSteering(t.Context(), first.Submission.Message.ID)
			if err != nil || name != "stop_during_resume" && input.Status != domain.OperatorSteeringCommitted {
				t.Fatalf("original input=%#v err=%v", input, err)
			}
			if name == "stop_during_resume" {
				provider.stopNext = nil
				provider.responses = []*llm.ChatResponse{textResponse(rootActionResponse(domain.RootActionFinish,
					"Continue from retained results without fetching again", "done", ""))}
				next := request
				next.Content, next.OperationKey = "Use the saved results; do not fetch again", "nested-after-stop"
				continued, err := turns.Execute(t.Context(), next)
				if err != nil || continued.Submission.Run.ID != run.ID || backend.calls != wantFetches {
					t.Fatalf("ordinary message after stop failed or repeated fetches: %#v fetches=%d err=%v", continued, backend.calls, err)
				}
			}
			beforeReplayModels, beforeReplayFetches := len(provider.Requests()), backend.calls
			// Confirm the original input by its original key. Its stored outcome
			// may be a failure, but confirmation must never reexecute either fetch.
			_, _ = turns.Execute(t.Context(), request)
			if len(provider.Requests()) != beforeReplayModels || backend.calls != beforeReplayFetches {
				t.Fatal("original request confirmation replayed model or tools")
			}
		})
	}
}
