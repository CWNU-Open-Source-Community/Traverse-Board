package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
)

func newProposalFixture(t *testing.T, mode string) (*store.SQLiteStore, string, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "proposal.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	workspaceRoot := t.TempDir()
	if err := st.SaveWorkspace(context.Background(), store.WorkspaceRecord{
		ID: "ws-1", Name: "proposal", RootPath: workspaceRoot, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	_, run, err := NewRunService(st).Create(context.Background(), CreateRunRequest{
		Goal: "proposal", Profile: "code", Surface: "code", Phase: "plan",
		WorkspaceID: "ws-1", Budget: domain.Budget{MaxTurns: 10, MaxToolCalls: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	if mode != "conservative" {
		capabilities := domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		}
		_, err = NewRunExecutionPermissionService(st, capabilities).Change(context.Background(),
			ChangeRunExecutionPermissionRequest{
				RunID: run.ID, Mode: mode, OperationKey: "proposal-key-" + mode + "-0001",
				RequestedBy: "operator", Reason: "proposal test",
				ConfirmUserApproval:     mode == string(domain.RunExecutionPermissionApproval),
				ConfirmDangerFullAccess: mode == string(domain.RunExecutionPermissionFullAccess),
			})
		if err != nil {
			t.Fatalf("set permission %s: %v", mode, err)
		}
	}
	return st, run.ID, workspaceRoot
}

func proposeFixture(t *testing.T, st *store.SQLiteStore, runID, workspaceRoot string) runner.OnceCommandProposal {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executor := NewOneShotCommandProposalToolExecutor(st)
	result, err := executor.ProposeOneShotCommand(context.Background(),
		toolgateway.OneShotCommandProposalContext{
			InvocationID: "inv-1", OperationKey: "once-prop-op-000001", RunID: runID,
			RootAgentID: "agent-root-1", SessionID: "session-1", WorkspaceID: "ws-1",
			LeaseID: "lease-1", LeaseGeneration: 1, RequestedBy: "run_supervisor",
			PolicyDecision: toolgateway.Decision{Allowed: true, Approval: toolgateway.ApprovalAutomatic, Risk: "low", Reason: "test"},
		},
		toolgateway.OneShotCommandProposalSpec{
			Version: runner.OnceCommandProtocolVersion, ExecutablePath: executable,
			Argv: []string{"-test.run", "^$"}, WorkingDirectory: workspaceRoot,
			Environment: []string{"TEMP=" + os.TempDir()}, TimeoutMS: 30000, Purpose: "proposal fixture",
		})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	proposal, found, err := st.GetOnceCommandProposal(context.Background(), result.ProposalID)
	if err != nil || !found || proposal.Status != "proposed" {
		t.Fatalf("proposal not stored: found=%t status=%q err=%v", found, proposal.Status, err)
	}
	if len(proposal.EnvironmentKeys) != 1 || proposal.EnvironmentKeys[0] != "TEMP" ||
		proposal.EnvironmentSHA256 == "" || len(proposal.SpecFingerprint) != 64 {
		t.Fatalf("proposal metadata invalid: %#v", proposal)
	}
	// Idempotent replay of the same operation key.
	replayed, err := executor.ProposeOneShotCommand(context.Background(),
		toolgateway.OneShotCommandProposalContext{
			InvocationID: "inv-1", OperationKey: "once-prop-op-000001", RunID: runID,
			RootAgentID: "agent-root-1", SessionID: "session-1", WorkspaceID: "ws-1",
			LeaseID: "lease-1", LeaseGeneration: 1, RequestedBy: "run_supervisor",
			PolicyDecision: toolgateway.Decision{Allowed: true, Approval: toolgateway.ApprovalAutomatic, Risk: "low", Reason: "test"},
		},
		toolgateway.OneShotCommandProposalSpec{
			Version: runner.OnceCommandProtocolVersion, ExecutablePath: executable,
			Argv: []string{"-test.run", "^$"}, WorkingDirectory: workspaceRoot,
			Environment: []string{"TEMP=" + os.TempDir()}, TimeoutMS: 30000, Purpose: "proposal fixture",
		})
	if err != nil || !replayed.Replayed || replayed.ProposalID != result.ProposalID {
		t.Fatalf("replay failed: %#v err=%v", replayed, err)
	}
	return proposal
}

func TestOneShotCommandProposalReviewExecuteChain(t *testing.T) {
	ctx := context.Background()
	st, runID, root := newProposalFixture(t, "approval")
	proposal := proposeFixture(t, st, runID, root)
	executor := &onceExecutorStub{}
	service := NewOnceCommandProposalReviewService(st, executor,
		domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true})
	// Mismatched environment must be rejected before execution.
	if _, err := service.Execute(ctx, proposal.ID, "operator", []string{"SystemRoot=C:\\Windows"}); err == nil {
		t.Fatal("environment drift was executed")
	}
	// Approve with the operator-approval gate enabled.
	updated, err := service.Review(ctx, proposal.ID, "approve", "operator", "approved for test")
	if err != nil || updated.Status != "approved" || updated.ApprovalFingerprint == "" {
		t.Fatalf("approve failed: %#v err=%v", updated, err)
	}
	// Second review is a conflict.
	if _, err := service.Review(ctx, proposal.ID, "deny", "operator", "late"); err == nil ||
		apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("second review was accepted: %v", err)
	}
	// Execute with the exact approved environment.
	result, err := service.Execute(ctx, proposal.ID, "operator", []string{"TEMP=" + os.TempDir()})
	if err != nil || !executor.executed {
		t.Fatalf("execute failed: %v (unwrapped=%v) err=%t", err, errors.Unwrap(err), executor.executed)
	}
	executed, found, err := st.GetOnceCommandProposal(ctx, proposal.ID)
	if err != nil || !found || executed.Status != "executed" ||
		executed.ExecutionRequestFingerprint != result.RequestFingerprint {
		t.Fatalf("proposal did not reach executed: %#v", executed)
	}
}

func TestOneShotCommandProposalReviewDeniedInConservativeMode(t *testing.T) {
	ctx := context.Background()
	st, runID, root := newProposalFixture(t, "conservative")
	proposal := proposeFixture(t, st, runID, root)
	executor := &onceExecutorStub{}
	service := NewOnceCommandProposalReviewService(st, executor,
		domain.ExecutionPermissionRuntimeCapabilities{})
	_, err := service.Review(ctx, proposal.ID, "approve", "operator", "should not pass")
	if err == nil || apperror.CodeOf(err) != apperror.CodePolicyDenied {
		t.Fatalf("conservative approval was not denied: %v", err)
	}
}

func TestOneShotCommandProposeRejectsShellInterpreterAndEscape(t *testing.T) {
	ctx := context.Background()
	st, runID, root := newProposalFixture(t, "full_access")
	executor := NewOneShotCommandProposalToolExecutor(st)
	scope := toolgateway.OneShotCommandProposalContext{
		InvocationID: "inv-1", OperationKey: "once-prop-op-000002", RunID: runID,
		RootAgentID: "agent-root-1", SessionID: "session-1", WorkspaceID: "ws-1",
		LeaseID: "lease-1", LeaseGeneration: 1, RequestedBy: "run_supervisor",
		PolicyDecision: toolgateway.Decision{Allowed: true, Approval: toolgateway.ApprovalAutomatic, Risk: "low", Reason: "test"},
	}
	// Shell interpreter rejected at propose time.
	shellPath := filepath.Join(os.TempDir(), "powershell.exe")
	if err := os.WriteFile(shellPath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.ProposeOneShotCommand(ctx, scope, toolgateway.OneShotCommandProposalSpec{
		Version: runner.OnceCommandProtocolVersion, ExecutablePath: shellPath,
		Argv: []string{"x"}, WorkingDirectory: root, TimeoutMS: 30000, Purpose: "shell",
	}); err == nil {
		t.Fatal("shell interpreter proposal was recorded")
	}
	// Workspace escape rejected at propose time.
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.ProposeOneShotCommand(ctx, scope, toolgateway.OneShotCommandProposalSpec{
		Version: runner.OnceCommandProtocolVersion, ExecutablePath: executable,
		Argv: []string{"x"}, WorkingDirectory: t.TempDir(), TimeoutMS: 30000, Purpose: "escape",
	}); err == nil {
		t.Fatal("workspace escape proposal was recorded")
	}
}
