package application_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/fileedit"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
	"cyberagent-workbench/internal/workspace"
)

func TestAgentCodeExecutorCreatesReviewedFileAndFailsClosedOnCASConflict(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	workspaceRoot := filepath.Join(home, "workspace")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	state, err := store.Open(filepath.Join(home, "agent-code.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	record := store.WorkspaceRecord{ID: "workspace-agent-code", Name: "agent-code",
		RootPath: workspaceRoot}
	if err := state.SaveWorkspace(ctx, record); err != nil {
		t.Fatal(err)
	}
	mission, created, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "edit workspace", Profile: "code",
			WorkspaceID: record.ID, Budget: domain.Budget{MaxTurns: 8, MaxToolCalls: 20}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := application.NewRunService(state).Start(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	mode, err := state.GetRunMode(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	permission, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	rootAgent, found, err := state.GetRootAgent(ctx, run.ID)
	if err != nil || !found {
		t.Fatalf("root Agent found=%t err=%v", found, err)
	}
	leaseResult, err := state.AcquireRunExecutionLease(ctx,
		domain.AcquireRunExecutionLeaseRequest{RunID: run.ID, OwnerID: "agent-code-test",
			TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	rootFingerprint, err := workspace.AgentCodeRootFingerprint(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	capabilityContext := toolgateway.AgentCodeCapabilityContext{RunID: run.ID,
		MissionID: mission.ID, RootAgentID: rootAgent.ID, WorkspaceID: record.ID,
		RootFingerprint: rootFingerprint, Surface: mode.Surface, Phase: mode.Phase,
		Role: rootAgent.Role, Profile: rootAgent.Profile, PermissionMode: permission.Mode,
		ModeRevision: mode.Revision, PermissionRevision: permission.Revision}
	capabilities := toolgateway.AgentCodeCapabilities(capabilityContext)
	scope := toolgateway.AgentCodeExecutionScope{InvocationID: "invocation-agent-code-1",
		OperationKey: "agent-code-create-operation-0001", RunID: run.ID,
		MissionID: mission.ID, RootAgentID: rootAgent.ID, SessionID: run.SessionID,
		WorkspaceID: record.ID, WorkspaceRoot: workspaceRoot,
		RootFingerprint: rootFingerprint, Surface: mode.Surface, Phase: mode.Phase,
		Role: rootAgent.Role, Profile: rootAgent.Profile, PermissionMode: permission.Mode,
		ModeRevision: mode.Revision, PermissionRevision: permission.Revision,
		CapabilityGeneration: capabilities.Generation, LeaseID: leaseResult.Lease.LeaseID,
		LeaseGeneration: leaseResult.Lease.Generation, RequestedBy: "run_supervisor",
		PolicyDecision: toolgateway.Decision{Allowed: true,
			Approval: toolgateway.ApprovalAutomatic, Risk: "low", Reason: "test allowed"}}
	executor := application.NewAgentCodeToolExecutor(state, policy.NewDefaultChecker())

	createPayload := mustAgentCodePayload(t, toolgateway.WorkspaceChangePayload{
		Version: toolgateway.AgentCodeRegistryVersion, Action: "create", Path: "note.txt",
		ExpectedSHA256: "missing", Content: "first\n"})
	proposed, err := executor.ExecuteAgentCode(ctx, scope, toolgateway.WorkspaceChangeTool,
		createPayload)
	if err != nil {
		t.Fatal(err)
	}
	createResult := decodeAgentCodeToolResult(t, proposed.JSON)
	if createResult.EditID == "" || createResult.Status != fileedit.StatusProposed ||
		createResult.OriginalSHA256 != "missing" {
		t.Fatalf("create proposal=%#v", createResult)
	}
	if _, err := application.NewFileEditReviewService(state).Review(ctx,
		application.ReviewFileEditRequest{Version: application.FileEditReviewProtocolVersion,
			RunID: run.ID, EditID: createResult.EditID,
			Action: application.FileEditApproveIntent}); err != nil {
		t.Fatal(err)
	}
	applyScope := scope
	applyScope.InvocationID = "invocation-agent-code-2"
	applyScope.OperationKey = "agent-code-create-apply-operation-0001"
	applied, err := executor.ExecuteAgentCode(ctx, applyScope, toolgateway.WorkspaceApplyTool,
		mustAgentCodePayload(t, toolgateway.WorkspaceApplyPayload{
			Version: toolgateway.AgentCodeRegistryVersion, EditID: createResult.EditID,
			ExpectedAction: "create", ExpectedOriginalSHA256: createResult.OriginalSHA256,
			ExpectedProposedSHA256: createResult.ProposedSHA256}))
	if err != nil || !strings.Contains(applied.JSON, `"file_written":true`) {
		t.Fatalf("create apply=%s err=%v", applied.JSON, err)
	}
	written, err := os.ReadFile(filepath.Join(workspaceRoot, "note.txt"))
	if err != nil || string(written) != "first\n" {
		t.Fatalf("created file=%q err=%v", written, err)
	}

	readScope := scope
	readScope.InvocationID = "invocation-agent-code-3"
	readScope.OperationKey = "agent-code-read-operation-0001"
	readResult, err := executor.ExecuteAgentCode(ctx, readScope, toolgateway.WorkspaceReadTool,
		mustAgentCodePayload(t, toolgateway.WorkspaceReadPayload{
			Version: toolgateway.AgentCodeRegistryVersion, Path: "note.txt",
			StartLine: 1, EndLine: 20}))
	if err != nil {
		t.Fatal(err)
	}
	var read workspace.AgentCodeRead
	if err := json.Unmarshal([]byte(readResult.JSON), &read); err != nil {
		t.Fatal(err)
	}
	patchScope := scope
	patchScope.InvocationID = "invocation-agent-code-4"
	patchScope.OperationKey = "agent-code-patch-operation-0001"
	patchResult, err := executor.ExecuteAgentCode(ctx, patchScope,
		toolgateway.WorkspaceChangeTool, mustAgentCodePayload(t,
			toolgateway.WorkspaceChangePayload{Version: toolgateway.AgentCodeRegistryVersion,
				Action: "propose_patch", Path: "note.txt", ExpectedSHA256: read.ContentSHA256,
				Replacements: []toolgateway.WorkspaceReplacement{{OldText: "first",
					NewText: "second", ExpectedOccurrences: 1}}}))
	if err != nil {
		t.Fatal(err)
	}
	patch := decodeAgentCodeToolResult(t, patchResult.JSON)
	if _, err := application.NewFileEditReviewService(state).Review(ctx,
		application.ReviewFileEditRequest{Version: application.FileEditReviewProtocolVersion,
			RunID: run.ID, EditID: patch.EditID,
			Action: application.FileEditApproveIntent}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "note.txt"), []byte("external\n"),
		0o600); err != nil {
		t.Fatal(err)
	}
	conflictScope := scope
	conflictScope.InvocationID = "invocation-agent-code-5"
	conflictScope.OperationKey = "agent-code-patch-apply-operation-0001"
	_, err = executor.ExecuteAgentCode(ctx, conflictScope, toolgateway.WorkspaceApplyTool,
		mustAgentCodePayload(t, toolgateway.WorkspaceApplyPayload{
			Version: toolgateway.AgentCodeRegistryVersion, EditID: patch.EditID,
			ExpectedAction: "propose_patch", ExpectedOriginalSHA256: patch.OriginalSHA256,
			ExpectedProposedSHA256: patch.ProposedSHA256}))
	if apperror.CodeOf(apperror.Normalize(err)) != apperror.CodeConflict {
		t.Fatalf("CAS conflict code=%s err=%v", apperror.CodeOf(apperror.Normalize(err)), err)
	}
	written, _ = os.ReadFile(filepath.Join(workspaceRoot, "note.txt"))
	if string(written) != "external\n" {
		t.Fatalf("CAS conflict overwrote file: %q", written)
	}

	moveScope := scope
	moveScope.InvocationID = "invocation-agent-code-6"
	moveScope.OperationKey = "agent-code-move-operation-0001"
	moveProposal, err := executor.ExecuteAgentCode(ctx, moveScope,
		toolgateway.WorkspaceChangeTool, mustAgentCodePayload(t,
			toolgateway.WorkspaceChangePayload{Version: toolgateway.AgentCodeRegistryVersion,
				Action: "move", Path: "note.txt", ExpectedSHA256: fileedit.HashText("external\n"),
				DestinationPath: "moved.txt", DestinationExpectedSHA256: "missing"}))
	if err != nil {
		t.Fatal(err)
	}
	move := decodeAgentCodeToolResult(t, moveProposal.JSON)
	if move.Status != fileedit.StatusProposed || move.ProposedSHA256 != "missing" {
		t.Fatalf("move proposal=%#v", move)
	}
	if _, err := application.NewFileEditReviewService(state).Review(ctx,
		application.ReviewFileEditRequest{Version: application.FileEditReviewProtocolVersion,
			RunID: run.ID, EditID: move.EditID,
			Action: application.FileEditApproveIntent}); err != nil {
		t.Fatal(err)
	}
	moveApplyScope := scope
	moveApplyScope.InvocationID = "invocation-agent-code-7"
	moveApplyScope.OperationKey = "agent-code-move-apply-operation-0001"
	if _, err := executor.ExecuteAgentCode(ctx, moveApplyScope,
		toolgateway.WorkspaceApplyTool, mustAgentCodePayload(t,
			toolgateway.WorkspaceApplyPayload{Version: toolgateway.AgentCodeRegistryVersion,
				EditID: move.EditID, ExpectedAction: "move",
				ExpectedOriginalSHA256: move.OriginalSHA256,
				ExpectedProposedSHA256: move.ProposedSHA256})); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, "note.txt")); !os.IsNotExist(err) {
		t.Fatalf("move source still exists: %v", err)
	}
	written, err = os.ReadFile(filepath.Join(workspaceRoot, "moved.txt"))
	if err != nil || string(written) != "external\n" {
		t.Fatalf("moved file=%q err=%v", written, err)
	}

	deleteScope := scope
	deleteScope.InvocationID = "invocation-agent-code-8"
	deleteScope.OperationKey = "agent-code-delete-operation-0001"
	deleteProposal, err := executor.ExecuteAgentCode(ctx, deleteScope,
		toolgateway.WorkspaceDeleteTool, mustAgentCodePayload(t,
			toolgateway.WorkspaceDeletePayload{Version: toolgateway.AgentCodeRegistryVersion,
				Action: "propose", Path: "moved.txt", ConfirmPath: "moved.txt",
				ExpectedSHA256: fileedit.HashText("external\n")}))
	if err != nil {
		t.Fatal(err)
	}
	deletion := decodeAgentCodeToolResult(t, deleteProposal.JSON)
	if deletion.Status != fileedit.StatusProposed || deletion.ProposedSHA256 != "missing" {
		t.Fatalf("delete proposal=%#v", deletion)
	}
	if _, err := application.NewFileEditReviewService(state).Review(ctx,
		application.ReviewFileEditRequest{Version: application.FileEditReviewProtocolVersion,
			RunID: run.ID, EditID: deletion.EditID,
			Action: application.FileEditApproveIntent}); err != nil {
		t.Fatal(err)
	}
	deleteApplyScope := scope
	deleteApplyScope.InvocationID = "invocation-agent-code-9"
	deleteApplyScope.OperationKey = "agent-code-delete-apply-operation-0001"
	if _, err := executor.ExecuteAgentCode(ctx, deleteApplyScope,
		toolgateway.WorkspaceDeleteTool, mustAgentCodePayload(t,
			toolgateway.WorkspaceDeletePayload{Version: toolgateway.AgentCodeRegistryVersion,
				Action: "apply", Path: "moved.txt", ConfirmPath: "moved.txt",
				ExpectedSHA256: deletion.OriginalSHA256, EditID: deletion.EditID})); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, "moved.txt")); !os.IsNotExist(err) {
		t.Fatalf("delete target still exists: %v", err)
	}
}

type agentCodeToolResultFixture struct {
	EditID         string `json:"edit_id"`
	Status         string `json:"status"`
	OriginalSHA256 string `json:"original_sha256"`
	ProposedSHA256 string `json:"proposed_sha256"`
}

func decodeAgentCodeToolResult(t *testing.T, value string) agentCodeToolResultFixture {
	t.Helper()
	var result agentCodeToolResultFixture
	if err := json.Unmarshal([]byte(value), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func mustAgentCodePayload(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
