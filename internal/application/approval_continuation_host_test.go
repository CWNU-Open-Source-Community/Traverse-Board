package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/session"
)

// The executor is deliberately simulated. This test verifies actual SQLite
// review/intent/result/model boundaries, not OS command or shell behavior.
type approvalHostExecutor struct {
	calls, exit int
	unknown     bool
}

func (*approvalHostExecutor) Available() bool { return true }
func (e *approvalHostExecutor) Execute(ctx context.Context, request runner.HostExecutionRequest) (runner.HostExecutionResult, error) {
	e.calls++
	if e.unknown {
		return runner.HostExecutionResult{}, errors.New("injected unconfirmed execution")
	}
	i := request.Intent
	now := time.Now().UTC()
	out := func(text string) runner.ControlledOutput {
		return runner.ControlledOutput{Data: []byte(text), ObservedBytes: int64(len(text)), CapturedBytes: len(text), CapturedPrefixSHA256: session.ContentSHA256(text)}
	}
	return runner.HostExecutionResult{ProtocolVersion: runner.HostExecutionProtocolVersion, PolicyVersion: runner.HostExecutionPolicyVersion,
		RequestID: i.RequestID, OperationKeyDigest: i.OperationKeyDigest, RunID: i.RunID, MissionID: i.MissionID, SessionID: i.SessionID, WorkspaceID: i.WorkspaceID,
		InteractionSnapshotID: i.InteractionSnapshotID, InteractionRevision: i.InteractionRevision, ExecutionProfileRevision: i.ExecutionProfileRevision,
		PermissionSnapshotID: i.PermissionSnapshotID, PermissionRevision: i.PermissionRevision, PermissionMode: i.PermissionMode,
		AuthorizationProposalID: i.AuthorizationProposalID, AuthorizationProposalFingerprint: i.AuthorizationProposalFingerprint,
		AuthorizationReviewID: i.AuthorizationReviewID, AuthorizationReviewFingerprint: i.AuthorizationReviewFingerprint,
		SpecFingerprint: i.Spec.Fingerprint, Backend: "simulated-approval-test", ExitCode: e.exit, Stdout: out("test receipt output"), Stderr: out(""), StartedAt: now, CompletedAt: now.Add(time.Millisecond),
		TreeReaped: true, NonSandboxed: true, JobAssignedAtCreation: true, KillOnJobClose: true, ActiveProcessLimit: runner.MaxHostActiveProcesses,
		JobMemoryLimit: runner.MaxHostProcessMemoryBytes, StdinClosed: true, NetworkRequested: true, ProductExecutionEnabled: true}, nil
}

func TestApprovalContinuationHostResultAndUnknownExecution(t *testing.T) {
	for _, scenario := range []string{"success", "nonzero", "deny", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			st, run, input, _ := rejectedHostFixture(t)
			workspace, err := st.GetWorkspaceByID(t.Context(), "ws-tool-boundary")
			if err != nil {
				t.Fatal(err)
			}
			executable := filepath.Join(workspace.RootPath, "review-helper.exe")
			if err := os.WriteFile(executable, []byte(strings.Repeat("never executed; simulated executor", 32)), 0700); err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(map[string]any{"version": runner.HostCommandProposalProtocolVersion, "executable_path": executable, "argv": []string{"inspect"}, "working_directory": workspace.RootPath, "timeout_milliseconds": 1000, "purpose": "Read-only inspection under review"})
			var proposalID string
			p := &boundaryJourneyProvider{}
			p.respond = func(ctx context.Context, request llm.ChatRequest, index int) (*llm.ChatResponse, error) {
				switch index {
				case 1:
					return toolResponse("host-proposal", "host_command_propose", string(payload)), nil
				case 2:
					return textResponse(rootActionResponse(domain.RootActionWait, "Review exact command", "", "operator decision required")), nil
				case 3:
					var all strings.Builder
					for _, m := range request.Messages {
						all.WriteString(m.Content)
					}
					want := "exit 0"
					if scenario == "nonzero" {
						want = "exit 7"
					}
					if scenario == "deny" {
						want = "denied, not executed"
					}
					if !strings.Contains(all.String(), proposalID) || !strings.Contains(all.String(), want) {
						t.Fatalf("model lost exact Host decision %q", want)
					}
					return textResponse(rootActionResponse(domain.RootActionFinish, "Recorded the actual command outcome; no automatic repeat", "done", "")), nil
				default:
					return nil, fmt.Errorf("unexpected model call %d", index)
				}
			}
			turns := rejectedHostService(st, st, p)
			if _, err := turns.Execute(t.Context(), input); err != nil {
				t.Fatal(err)
			}
			proposals, err := st.ListHostCommandProposals(t.Context(), run.ID, 10)
			if err != nil || len(proposals) != 1 {
				for _, m := range p.Requests()[1].Messages {
					for _, r := range m.ToolResults {
						t.Logf("tool result: %s", r.Content)
					}
				}
				t.Fatalf("proposals=%#v %v", proposals, err)
			}
			proposalID = proposals[0].ID
			executor := &approvalHostExecutor{}
			if scenario == "nonzero" {
				executor.exit = 7
			}
			executor.unknown = scenario == "unknown"
			service := application.NewHostCommandProposalReviewService(st, executor, domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true})
			review := application.ReviewHostCommandProposalRequest{ProposalID: proposalID, Decision: "approve", OperationKey: "review-exact-host", ReviewedBy: "test_operator", Reason: "exact command inspected", ConfirmExecution: true}
			if scenario == "deny" {
				review.Decision = "deny"
				review.ConfirmExecution = false
			}
			_, err = service.Review(t.Context(), review)
			if (err != nil) != executor.unknown {
				t.Fatalf("review error=%v", err)
			}
			request := application.ApprovalContinuationRequest{RunID: run.ID, Kind: "host_command", ProposalID: proposalID}
			result := turns.ResumeApproval(t.Context(), request)
			if executor.unknown {
				if result.State != "failed" || len(p.Requests()) != 2 {
					t.Fatalf("unknown command resumed: %#v", result)
				}
				if _, err := service.Review(t.Context(), review); err == nil || executor.calls != 1 {
					t.Fatalf("uncertain command retried: calls=%d %v", executor.calls, err)
				}
				return
			}
			if result.State != "completed" || !result.ModelCalled || result.ToolCalled {
				t.Fatalf("continuation=%#v", result)
			}
			if _, err := service.Review(t.Context(), review); err != nil {
				t.Fatal(err)
			}
			if replay := turns.ResumeApproval(t.Context(), request); !replay.Replayed || len(p.Requests()) != 3 {
				t.Fatalf("replay=%#v", replay)
			}
			wantCalls := 1
			if scenario == "deny" {
				wantCalls = 0
			}
			if executor.calls != wantCalls {
				t.Fatalf("executor called %d times", executor.calls)
			}
			assertOneBoundaryTranscriptInput(t, st, input.ThreadID, input.Content)
		})
	}
}
