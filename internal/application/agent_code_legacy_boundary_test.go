package application_test

import (
	"context"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/fileedit"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
)

// This tests the executor boundary against a retained snapshot. It is not a
// migration fixture: all other state and every possible write use real SQLite.
type legacyPermissionReadStore struct {
	*store.SQLiteStore
	permission domain.RunExecutionPermissionSnapshot
}

func (s legacyPermissionReadStore) GetRunExecutionPermission(context.Context, string) (domain.RunExecutionPermissionSnapshot, error) {
	return s.permission, nil
}

func TestAgentCodeLegacySnapshotsCannotReadOrCreateNewProposals(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionConservative, domain.RunExecutionPermissionWorkspaceAccess, domain.RunExecutionPermissionApproval, domain.RunExecutionPermissionFullAccess, domain.RunExecutionPermissionDebug} {
		t.Run(string(mode), func(t *testing.T) {
			f := newAgentCodeReplaceFixtureForPermission(t, domain.RunExecutionPermissionAsk)
			ctx := t.Context()
			current, err := f.state.GetRunExecutionPermission(ctx, f.scope.RunID)
			if err != nil {
				t.Fatal(err)
			}
			old, err := current.Next("legacy-file-permission", mode, mode != domain.RunExecutionPermissionConservative, "operator", "retained snapshot", time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			f.scope.PermissionMode, f.scope.PermissionRevision = old.Mode, old.Revision
			f.scope.PermissionSnapshotID, f.scope.PermissionRuntimeEpoch = "", ""
			f.scope.PermissionGeneration, f.scope.RunAuthorizationFence = 0, 0
			bound := toolgateway.AgentCodeCapabilityContext{RunID: f.scope.RunID, MissionID: f.scope.MissionID, RootAgentID: f.scope.RootAgentID, WorkspaceID: f.scope.WorkspaceID, RootFingerprint: f.scope.RootFingerprint, Surface: f.scope.Surface, Phase: f.scope.Phase, Role: f.scope.Role, Profile: f.scope.Profile, PermissionMode: old.Mode, ModeRevision: f.scope.ModeRevision, PermissionRevision: old.Revision}
			f.scope.CapabilityGeneration = toolgateway.AgentCodeCapabilities(bound).Generation
			historical, err := toolgateway.NewAgentCodeCallAuthority(bound, f.scope.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := toolgateway.EncodeAgentCodeCallAuthority(historical)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := toolgateway.DecodeAgentCodeCallAuthority(encoded)
			if err != nil || decoded != historical {
				t.Fatal("retained authority no longer decodes", err)
			}
			eventsBefore, err := f.state.ListRunEvents(ctx, f.scope.RunID)
			if err != nil {
				t.Fatal(err)
			}
			approvalsBefore, err := f.state.ListApprovals(ctx, approval.ListFilter{RunID: f.scope.RunID})
			if err != nil {
				t.Fatal(err)
			}
			f.executor = application.NewAgentCodeToolExecutor(legacyPermissionReadStore{f.state, old}, &fileOperationPolicy{review: true}).WithExecutionPermissionCapabilities(f.capabilities)
			for _, call := range []struct {
				name    toolgateway.ToolName
				payload any
			}{
				{toolgateway.WorkspaceReadTool, toolgateway.WorkspaceReadPayload{Version: toolgateway.AgentCodeRegistryVersion, Path: "target.txt", StartLine: 1, EndLine: 1}},
				{toolgateway.WorkspaceChangeTool, f.payload("after\n")},
			} {
				result, err := f.execute(t, call.name, mustAgentCodePayload(t, call.payload), "reject-legacy-"+string(call.name))
				if apperror.CodeOf(err) != apperror.CodePolicyDenied || result.JSON != "" {
					t.Fatalf("legacy %s reached executor: %+v %v", call.name, result, err)
				}
			}
			edits, err := f.state.ListFileEdits(ctx, fileedit.ListFilter{SessionID: f.scope.SessionID})
			if err != nil || len(edits) != 0 {
				t.Fatal("legacy call created edits", len(edits), err)
			}
			approvalsAfter, err := f.state.ListApprovals(ctx, approval.ListFilter{RunID: f.scope.RunID})
			if err != nil || len(approvalsAfter) != len(approvalsBefore) {
				t.Fatal("legacy call changed approvals", err)
			}
			eventsAfter, err := f.state.ListRunEvents(ctx, f.scope.RunID)
			if err != nil || len(eventsAfter) != len(eventsBefore) {
				t.Fatal("legacy call changed events", err)
			}
			f.assertFile(t, "before\n")
		})
	}
}
