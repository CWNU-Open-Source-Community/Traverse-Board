package application

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/sandbox"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolbudget"
	"cyberagent-workbench/internal/toolgateway"
)

type sandboxCandidateCreationBoundary struct {
	*store.SQLiteStore
	before   func()
	calls    int
	snapshot sandbox.ExecutionCandidate
}

func (s *sandboxCandidateCreationBoundary) CreateSandboxExecutionCandidate(ctx context.Context,
	candidate sandbox.ExecutionCandidate, operation sandbox.CandidateOperation,
) (sandbox.ValidatedExecutionCandidate, bool, error) {
	s.calls++
	s.snapshot = candidate
	s.before()
	return s.SQLiteStore.CreateSandboxExecutionCandidate(ctx, candidate, operation)
}

// A normal Supervisor wait may charge the Run after application validation and
// before candidate creation obtains its transaction lock. The store must still
// check real current usage and authority without rewriting the captured snapshot.
func TestSandboxCandidateCreationRechecksTransactionBudgetAndLease(t *testing.T) {
	for _, change := range []string{"progress", "exhausted", "quiescent", "revoked", "replaced", "cancelled"} {
		t.Run(change, func(t *testing.T) {
			ctx := t.Context()
			st, run, _ := newSandboxManifestTestRuntime(t, ctx)
			wrapped := &sandboxCandidateCreationBoundary{SQLiteStore: st}
			service := NewSandboxManifestService(wrapped, policy.NewDefaultChecker())
			manifest := sandboxManifestTestFixture()
			prepared, err := service.Prepare(ctx, PrepareSandboxManifestRequest{
				RunID: run.ID, Manifest: manifest, OperationKey: "creation-budget-prepare", RequestedBy: "budget_operator"})
			if err != nil {
				t.Fatal(err)
			}
			charge := func() {
				t.Helper()
				if _, err := st.ChargeToolCall(ctx, toolbudget.ChargeRequest{
					RunID: run.ID, SessionID: run.SessionID, WorkspaceID: "ws-sandbox",
					ToolName: "command_runtime", ActionClass: "process", RequestedBy: "budget_operator",
				}); err != nil {
					t.Fatal(err)
				}
			}
			charge()
			var lease domain.RunExecutionLease
			if change != "quiescent" {
				acquired, err := st.AcquireRunExecutionLease(ctx, domain.AcquireRunExecutionLeaseRequest{
					RunID: run.ID, OwnerID: "creation-budget-owner", TTL: time.Minute})
				if err != nil {
					t.Fatal(err)
				}
				lease = acquired.Lease
			}
			expectedUsage := int64(2)
			wrapped.before = func() {
				charge()
				switch change {
				case "exhausted":
					charge()
					charge()
					expectedUsage = 4
				case "revoked", "replaced":
					if _, _, err := st.ReleaseRunExecutionLease(ctx, lease); err != nil {
						t.Fatal(err)
					}
					if change == "replaced" {
						if _, err := st.AcquireRunExecutionLease(ctx, domain.AcquireRunExecutionLeaseRequest{
							RunID: run.ID, OwnerID: "creation-replacement-owner", TTL: time.Minute}); err != nil {
							t.Fatal(err)
						}
					}
				case "cancelled":
					if _, err := NewRunService(st).Cancel(ctx, run.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			request := ValidateSandboxExecutionCandidateRequest{PreparationID: prepared.Preparation.ID,
				Manifest: manifest, OperationKey: "creation-budget-candidate", RequestedBy: "budget_operator"}
			if change == "quiescent" {
				_, err = service.ValidateExecutionCandidate(ctx, request)
			} else {
				_, err = service.validateLeaseBoundExecutionCandidate(ctx, request, lease)
			}
			if wrapped.calls != 1 || wrapped.snapshot.ToolCallsUsed != 1 {
				t.Fatalf("did not reach candidate creation with the captured snapshot: calls=%d snapshot=%+v err=%v", wrapped.calls, wrapped.snapshot, err)
			}
			if change == "progress" {
				if err != nil {
					t.Fatalf("candidate creation rejected still-budgeted progress: %v", err)
				}
			} else if err == nil {
				t.Fatal("candidate creation accepted invalid current usage or authority")
			} else if change == "exhausted" && apperror.CodeOf(err) != apperror.CodeResourceExhausted {
				t.Fatalf("candidate creation did not check current budget: %v", err)
			} else if (change == "revoked" || change == "replaced") && !strings.Contains(err.Error(), "lease binding is stale") {
				t.Fatalf("candidate creation did not check the exact active lease: %v", err)
			}
			stored, readErr := st.GetSandboxExecutionCandidate(ctx, wrapped.snapshot.ID)
			if change == "progress" {
				if readErr != nil || stored.Candidate.ToolCallsUsed != 1 {
					t.Fatalf("candidate snapshot was not preserved: %+v err=%v", stored, readErr)
				}
			} else if !errors.Is(readErr, sql.ErrNoRows) {
				t.Fatalf("rejected candidate was persisted: %+v err=%v", stored, readErr)
			}
			usage, usageErr := st.GetToolCallUsage(ctx, run.ID)
			if usageErr != nil || usage.Consumed != expectedUsage {
				t.Fatalf("candidate creation rewrote accounting: %+v err=%v", usage, usageErr)
			}
		})
	}
}

func TestSandboxBackgroundCandidateKeepsCurrentBudgetAndLease(t *testing.T) {
	for _, name := range []string{"quiescent usage changed", "live usage advances", "budget exhausted", "lease revoked", "run cancelled"} {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			st, run, _ := newSandboxManifestTestRuntime(t, ctx)
			service := NewSandboxManifestService(st, policy.NewDefaultChecker())
			manifest := sandboxManifestTestFixture()
			prepared, err := service.Prepare(ctx, PrepareSandboxManifestRequest{
				RunID: run.ID, Manifest: manifest, OperationKey: "background-budget-prepare", RequestedBy: "budget_operator"})
			if err != nil {
				t.Fatal(err)
			}
			charge := func() {
				t.Helper()
				if _, err := st.ChargeToolCall(ctx, toolbudget.ChargeRequest{
					RunID: run.ID, SessionID: run.SessionID, WorkspaceID: "ws-sandbox",
					ToolName: "command_runtime", ActionClass: "process", RequestedBy: "budget_operator",
				}); err != nil {
					t.Fatal(err)
				}
			}
			charge()
			request := ValidateSandboxExecutionCandidateRequest{PreparationID: prepared.Preparation.ID,
				Manifest: manifest, OperationKey: "background-budget-candidate", RequestedBy: "budget_operator"}
			var candidate sandbox.ValidatedExecutionCandidate
			var lease domain.RunExecutionLease
			if name == "quiescent usage changed" {
				candidate, err = service.ValidateExecutionCandidate(ctx, request)
			} else {
				acquired, acquireErr := st.AcquireRunExecutionLease(ctx, domain.AcquireRunExecutionLeaseRequest{
					RunID: run.ID, OwnerID: "background-budget-owner", TTL: time.Minute})
				if acquireErr != nil {
					t.Fatal(acquireErr)
				}
				lease = acquired.Lease
				candidate, err = service.validateLeaseBoundExecutionCandidate(ctx, request, lease)
			}
			if err != nil || candidate.Candidate.ToolCallsUsed != 1 {
				t.Fatalf("candidate did not preserve the exact creation snapshot: %+v err=%v", candidate, err)
			}
			charge()
			expectedUsage := int64(2)
			if name == "budget exhausted" {
				charge()
				charge()
				expectedUsage = 4
			}
			if name == "lease revoked" {
				if _, _, err := st.ReleaseRunExecutionLease(ctx, lease); err != nil {
					t.Fatal(err)
				}
			}
			if name == "run cancelled" {
				if _, err := NewRunService(st).Cancel(ctx, run.ID); err != nil {
					t.Fatal(err)
				}
			}
			_, err = service.BeginDisabledExecution(ctx, BeginSandboxExecutionRequest{
				CandidateID: candidate.Candidate.ID, Manifest: manifest,
				OperationKey: "background-budget-begin", RequestedBy: "budget_operator"})
			if name == "live usage advances" {
				if err != nil {
					t.Fatalf("still-budgeted background candidate rejected ordinary Run progress: %v", err)
				}
			} else if err == nil {
				t.Fatal("invalid or exhausted candidate was accepted")
			} else if name == "budget exhausted" && apperror.CodeOf(err) != apperror.CodeResourceExhausted {
				t.Fatalf("expected a current budget denial, got %v", err)
			}
			stored, readErr := st.GetSandboxExecutionCandidate(ctx, candidate.Candidate.ID)
			usage, usageErr := st.GetToolCallUsage(ctx, run.ID)
			if readErr != nil || usageErr != nil || stored.Candidate.ToolCallsUsed != 1 || usage.Consumed != expectedUsage {
				t.Fatalf("snapshot or actual accounting was rewritten: candidate=%+v usage=%+v errors=%v,%v", stored, usage, readErr, usageErr)
			}
		})
	}
}

func TestSandboxDisabledLifecycleRecoversCancelsCleansAndReplays(t *testing.T) {
	ctx := context.Background()
	st, run, root := newSandboxManifestTestRuntime(t, ctx)
	service := NewSandboxManifestService(st, policy.NewDefaultChecker())
	manifest := sandboxManifestTestFixture()
	validated := prepareSandboxLifecycleCandidate(t, ctx, service, run.ID, manifest,
		"lifecycle", "lifecycle_operator")
	begin := BeginSandboxExecutionRequest{
		CandidateID: validated.Candidate.ID, Manifest: manifest,
		OperationKey: "lifecycle-begin-operation", RequestedBy: "lifecycle_operator",
	}
	started, err := service.BeginDisabledExecution(ctx, begin)
	if err != nil {
		t.Fatal(err)
	}
	if started.Replayed || started.Status != sandbox.LifecyclePrepared ||
		started.Execution.BackendEnabled || started.Execution.ExecutionAuthorized ||
		started.Execution.BackendStarted || started.Lease.Status != sandbox.ExecutionLeaseReleased ||
		started.Execution.CandidateID != validated.Candidate.ID || len(started.Inputs) != 0 {
		t.Fatalf("unexpected disabled Sandbox lifecycle: %#v", started)
	}
	replayed, err := service.BeginDisabledExecution(ctx, begin)
	if err != nil || !replayed.Replayed || replayed.Execution.ID != started.Execution.ID ||
		replayed.Lease.Status != sandbox.ExecutionLeaseReleased {
		t.Fatalf("Sandbox lifecycle replay diverged: %#v err=%v", replayed, err)
	}
	changed := begin
	changed.Manifest.Command.Arguments = []string{"test", "./internal/..."}
	if _, err := service.BeginDisabledExecution(ctx, changed); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("changed Manifest reused begin operation key: %v", err)
	}

	cancel := CancelSandboxExecutionRequest{
		ExecutionID: started.Execution.ID, OperationKey: "lifecycle-cancel-operation",
		RequestedBy: "lifecycle_operator",
	}
	cancelled, err := service.CancelDisabledExecution(ctx, cancel)
	if err != nil || cancelled.Cancellation == nil ||
		cancelled.Status != sandbox.LifecycleCancelPending || cancelled.Replayed {
		t.Fatalf("Sandbox cancellation was not recorded: %#v err=%v", cancelled, err)
	}
	cancelReplay, err := service.CancelDisabledExecution(ctx, cancel)
	if err != nil || !cancelReplay.Replayed ||
		cancelReplay.Cancellation.ID != cancelled.Cancellation.ID {
		t.Fatalf("Sandbox cancellation replay diverged: %#v err=%v", cancelReplay, err)
	}
	if _, err := NewRunService(st).Cancel(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	clean := CleanupSandboxExecutionRequest{
		ExecutionID: started.Execution.ID, OperationKey: "lifecycle-cleanup-operation",
		ReconciledBy: "lifecycle_operator",
	}
	cleaned, err := service.CleanupDisabledExecution(ctx, clean)
	if err != nil || cleaned.Cleanup == nil ||
		cleaned.Status != sandbox.LifecycleCleanupComplete || cleaned.Replayed ||
		cleaned.Cleanup.Outcome != "backend_disabled" || cleaned.Cleanup.BackendStarted ||
		cleaned.Cleanup.OrphanDetected || cleaned.Cleanup.OrphanReaped ||
		!cleaned.Cleanup.InputArtifactsVerified || cleaned.Cleanup.OutputArtifactCount != 0 ||
		cleaned.Lease.Status != sandbox.ExecutionLeaseReleased ||
		cleaned.Lease.Generation != 2 {
		t.Fatalf("terminal-Run cleanup did not converge: %#v err=%v", cleaned, err)
	}
	cleanReplay, err := service.CleanupDisabledExecution(ctx, clean)
	if err != nil || !cleanReplay.Replayed || cleanReplay.Cleanup.ID != cleaned.Cleanup.ID {
		t.Fatalf("Sandbox cleanup replay diverged: %#v err=%v", cleanReplay, err)
	}

	timeline, err := st.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]int{
		events.SandboxExecutionPreparedEvent:         1,
		events.SandboxExecutionCancelRequestedEvent:  1,
		events.SandboxExecutionCleanupCompletedEvent: 1,
	}
	for _, event := range timeline {
		if _, ok := wanted[event.Type]; ok {
			wanted[event.Type]--
		}
		if strings.Contains(event.PayloadJSON, root) ||
			strings.Contains(event.PayloadJSON, started.Execution.InitialLeaseID) ||
			strings.Contains(event.PayloadJSON, started.Lease.OwnerID) ||
			strings.Contains(event.PayloadJSON, cleaned.Lease.OwnerID) ||
			strings.Contains(event.PayloadJSON, `"executable"`) {
			t.Fatalf("Sandbox lifecycle event leaked private intent or lease data: %#v", event)
		}
	}
	for eventType, remaining := range wanted {
		if remaining != 0 {
			t.Fatalf("Sandbox lifecycle event %s count mismatch: remaining=%d", eventType, remaining)
		}
	}
}

func TestSandboxDisabledLifecycleBindsInputArtifactAndRejectsCrossRunScope(t *testing.T) {
	ctx := context.Background()
	st, run, _ := newSandboxManifestTestRuntime(t, ctx)
	gateway := toolgateway.New(st, policy.NewDefaultChecker())
	proposal, err := gateway.Invoke(ctx, toolgateway.ToolCall{
		Name: toolgateway.ShellTool, Arguments: map[string]string{"command": "echo lifecycle-evidence"},
		RunID: run.ID, SessionID: run.SessionID, WorkspaceID: "ws-sandbox",
		RequestedBy: "lifecycle_operator",
	})
	if err != nil || proposal.Proposal == nil {
		t.Fatalf("create Artifact source: %#v err=%v", proposal, err)
	}
	reviewed, err := gateway.Review(ctx, toolgateway.ReviewRequest{
		Action: toolgateway.ReviewApprove, Tool: toolgateway.ShellTool,
		ProposalID: proposal.Proposal.ID, ReviewedBy: "lifecycle_operator",
	})
	if err != nil || reviewed.Result == nil || reviewed.Result.Metadata["artifact_stdout_id"] == "" {
		t.Fatalf("capture lifecycle Artifact: %#v err=%v", reviewed, err)
	}
	artifactID := reviewed.Result.Metadata["artifact_stdout_id"]
	manifest := sandboxManifestTestFixture()
	manifest.InputArtifactIDs = []string{artifactID}
	service := NewSandboxManifestService(st, policy.NewDefaultChecker())
	validated := prepareSandboxLifecycleCandidate(t, ctx, service, run.ID, manifest,
		"artifact", "lifecycle_operator")
	started, err := service.BeginDisabledExecution(ctx, BeginSandboxExecutionRequest{
		CandidateID: validated.Candidate.ID, Manifest: manifest,
		OperationKey: "artifact-begin-operation", RequestedBy: "lifecycle_operator",
	})
	if err != nil || len(started.Inputs) != 1 || started.Inputs[0].ArtifactID != artifactID ||
		started.Inputs[0].SHA256 == "" || started.Execution.InputArtifactBytes <= 0 ||
		started.Execution.InputArtifactDigest != sandbox.InputArtifactBindingsDigest(started.Inputs) {
		t.Fatalf("input Artifact binding is incomplete: %#v err=%v", started, err)
	}
	preflight, err := service.PrepareDisabledPreflight(ctx, PrepareSandboxPreflightRequest{
		ExecutionID: started.Execution.ID, Manifest: manifest,
		OperationKey: "artifact-preflight-operation", RequestedBy: "lifecycle_operator",
	})
	if err != nil || preflight.InputArtifactDigest != started.Execution.InputArtifactDigest ||
		preflight.BackendEnabled || preflight.ExecutionAuthorized ||
		preflight.ArtifactCommitAuthorized {
		t.Fatalf("preflight did not reverify the input Artifact: %#v err=%v", preflight, err)
	}

	_, otherRun, err := NewRunService(st).Create(ctx, CreateRunRequest{
		Goal: "reject cross Run sandbox Artifact", Profile: "code", WorkspaceID: "ws-sandbox",
	})
	if err != nil {
		t.Fatal(err)
	}
	otherManifest := sandboxManifestTestFixture()
	otherManifest.InputArtifactIDs = []string{artifactID}
	prepared, err := service.Prepare(ctx, PrepareSandboxManifestRequest{
		RunID: otherRun.ID, Manifest: otherManifest, OperationKey: "cross-run-prepare",
		RequestedBy: "lifecycle_operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := service.ValidateExecutionCandidate(ctx, ValidateSandboxExecutionCandidateRequest{
		PreparationID: prepared.Preparation.ID, Manifest: otherManifest,
		OperationKey: "cross-run-candidate", RequestedBy: "lifecycle_operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.BeginDisabledExecution(ctx, BeginSandboxExecutionRequest{
		CandidateID: candidate.Candidate.ID, Manifest: otherManifest,
		OperationKey: "cross-run-begin-operation", RequestedBy: "lifecycle_operator",
	}); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("cross-Run Artifact entered Sandbox lifecycle: %v", err)
	}
}

func prepareSandboxLifecycleCandidate(t *testing.T, ctx context.Context,
	service *SandboxManifestService, runID string, manifest sandbox.Manifest, prefix, requestedBy string,
) sandbox.ValidatedExecutionCandidate {
	t.Helper()
	prepared, err := service.Prepare(ctx, PrepareSandboxManifestRequest{
		RunID: runID, Manifest: manifest,
		OperationKey: prefix + "-prepare", RequestedBy: requestedBy,
	})
	if err != nil {
		t.Fatal(err)
	}
	validated, err := service.ValidateExecutionCandidate(ctx, ValidateSandboxExecutionCandidateRequest{
		PreparationID: prepared.Preparation.ID, Manifest: manifest,
		OperationKey: prefix + "-candidate", RequestedBy: requestedBy,
	})
	if err != nil {
		t.Fatal(err)
	}
	return validated
}
