package application_test

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/modelregistry"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolrun"
)

// This exercises normal model selection and an explicit next Thread message
// through real SQLite services. The deterministic provider does not run tools
// or stand in for an end-to-end provider compatibility qualification.
func TestThreadModelSwitchPreservesExecutionSettingsWithoutAuthority(t *testing.T) {
	for _, backend := range []string{"local", "docker"} {
		t.Run(backend, func(t *testing.T) {
			st, created, registry := newThreadModelSettingsFixture(t)
			ctx := t.Context()
			setThreadModelTestProfile(t, st, created.ID, backend, "select-backend")
			setThreadModelTestInteraction(t, st, created.ID, "controlled", "trust-project")
			expectedPermission := domain.RunExecutionPermissionConservative
			if backend == "local" {
				_, err := application.NewThreadExecutionPermissionService(st, domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true}).Change(ctx,
					application.ChangeThreadExecutionPermissionRequest{ThreadID: domain.InitialThreadID(created.ID),
						Mode: "approval", ConfirmUserApproval: true, OperationKey: "model-settings-per-command",
						RequestedBy: "operator", Reason: "Keep per-command review across model switches"})
				if err != nil {
					t.Fatal(err)
				}
				expectedPermission = domain.RunExecutionPermissionApproval
			}
			oldProfile, _ := st.GetRunExecutionProfile(ctx, created.ID)
			oldInteraction, _ := st.GetRunExecutionInteraction(ctx, created.ID)
			grant, err := st.CreateSessionGrant(ctx, approval.CreateGrantRequest{
				SessionID: created.SessionID, ToolName: "shell", ActionClass: "shell",
				Reason: "old Session only", GrantedBy: "operator", IdempotencyKey: "old-session-grant",
			})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			oldTool, err := st.SaveToolRun(ctx, toolrun.ToolRun{ID: "old-approved-tool",
				SessionID: created.SessionID, WorkspaceID: "workspace-model-settings", ToolName: toolrun.ShellTool,
				Command: "echo previous approval", Status: toolrun.StatusProposed, CreatedAt: now, UpdatedAt: now})
			if err != nil {
				t.Fatal(err)
			}
			oldApproval, err := st.DecideApproval(ctx, approval.DecisionRequest{ProposalID: oldTool.ID,
				IdempotencyKey: "approve-old-tool", Action: approval.ActionApprove, ReviewedBy: "operator"})
			if err != nil {
				t.Fatal(err)
			}
			result := executeThreadModelSettingsSwitch(t, st, created, registry)
			next := result.Submission.Run
			profile, err := st.GetRunExecutionProfile(ctx, next.ID)
			if err != nil || string(profile.Profile) != backend || profile.ID == oldProfile.ID ||
				profile.ProcessEnabled || profile.ExecutionAuthorized || profile.CapabilityGrant {
				t.Fatalf("backend preference or authority incorrect: %+v %v", profile, err)
			}
			interaction, err := st.GetRunExecutionInteraction(ctx, next.ID)
			if err != nil || interaction.Mode != domain.RunExecutionInteractionControlled ||
				interaction.WorkspaceTrust != domain.WorkspaceTrustTrusted || !interaction.OperatorConfirmed ||
				interaction.ID == oldInteraction.ID || interaction.ExecutionProfileRevision != profile.Revision ||
				interaction.ProcessEnabled || interaction.ExecutionAuthorized || interaction.CapabilityGrant || interaction.AgentInputDefault {
				t.Fatalf("interaction preference or authority incorrect: %+v %v", interaction, err)
			}
			if profile.RunID != next.ID || interaction.RunID != next.ID || next.SessionID == created.SessionID {
				t.Fatal("settings did not bind to a fresh successor")
			}
			if old, err := st.GetRunExecutionProfile(ctx, created.ID); err != nil || !reflect.DeepEqual(old, oldProfile) {
				t.Fatalf("old profile changed: %+v %v", old, err)
			}
			if old, err := st.GetRunExecutionInteraction(ctx, created.ID); err != nil || !reflect.DeepEqual(old, oldInteraction) {
				t.Fatalf("old interaction changed: %+v %v", old, err)
			}
			if grants, err := st.ListSessionGrants(ctx, approval.GrantListFilter{RunID: next.ID, Limit: 100}); err != nil || len(grants) != 0 {
				t.Fatalf("old Session grant copied: %+v %v", grants, err)
			}
			if _, found, err := st.FindActiveSessionGrant(ctx, approval.GrantQuery{RunID: next.ID,
				SessionID: next.SessionID, WorkspaceID: "workspace-model-settings", ToolName: "shell", ActionClass: "shell"}); err != nil || found {
				t.Fatalf("old grant authorizes the successor: %v %v", found, err)
			}
			if old, err := st.GetSessionGrant(ctx, grant.Grant.ID); err != nil || old.SessionID != created.SessionID || old.RunID != created.ID {
				t.Fatalf("old grant binding moved: %+v %v", old, err)
			}
			if approvals, err := st.ListApprovals(ctx, approval.ListFilter{RunID: next.ID, Limit: 100}); err != nil || len(approvals) != 0 {
				t.Fatalf("old single approval copied: %+v %v", approvals, err)
			}
			if old, err := st.GetApprovalByProposal(ctx, oldTool.ID); err != nil || !reflect.DeepEqual(old, oldApproval.Approval) {
				t.Fatalf("old approval changed: %+v %v", old, err)
			}
			permission, err := st.GetRunExecutionPermission(ctx, next.ID)
			if err != nil || permission.Mode != expectedPermission || permission.CapabilityGrant || permission.ProcessEnabled || permission.ExecutionAuthorized {
				t.Fatalf("backend setting widened permission: %+v %v", permission, err)
			}
			lease, found, err := st.GetRunExecutionLease(ctx, next.ID)
			if err != nil || (found && lease.Status == domain.RunExecutionLeaseActive) {
				t.Fatalf("successor retained live execution after wait: %+v %v", lease, err)
			}
			// Another Thread in the same workspace starts with its own settings.
			_, independent, err := application.NewRunService(st).Create(ctx, application.CreateRunRequest{
				Goal: "independent task", Profile: "code", WorkspaceID: "workspace-model-settings"})
			if err != nil {
				t.Fatal(err)
			}
			otherProfile, err := st.GetRunExecutionProfile(ctx, independent.ID)
			if err != nil || otherProfile.Profile != domain.RunExecutionProfilePreview {
				t.Fatalf("settings escaped their Thread: %+v %v", otherProfile, err)
			}
		})
	}
}

func TestThreadModelSwitchRespectsLatestSettingsAndStaleConfirmation(t *testing.T) {
	for _, change := range []string{"preview", "docker", "local-again", "interaction-preview"} {
		t.Run(change, func(t *testing.T) {
			st, created, registry := newThreadModelSettingsFixture(t)
			setThreadModelTestProfile(t, st, created.ID, "local", "initial-local")
			setThreadModelTestInteraction(t, st, created.ID, "controlled", "initial-trust")
			desiredProfile := change
			switch change {
			case "local-again":
				setThreadModelTestProfile(t, st, created.ID, "preview", "leave-local")
				setThreadModelTestProfile(t, st, created.ID, "local", "return-local")
				desiredProfile = "local"
			case "interaction-preview":
				setThreadModelTestInteraction(t, st, created.ID, "preview", "explicit-untrust")
				desiredProfile = "local"
			default:
				setThreadModelTestProfile(t, st, created.ID, change, "latest-backend")
			}
			result := executeThreadModelSettingsSwitch(t, st, created, registry)
			profile, err := st.GetRunExecutionProfile(t.Context(), result.Submission.Run.ID)
			if err != nil || string(profile.Profile) != desiredProfile {
				t.Fatalf("latest backend ignored: %+v %v", profile, err)
			}
			interaction, err := st.GetRunExecutionInteraction(t.Context(), result.Submission.Run.ID)
			if err != nil || interaction.Mode != domain.RunExecutionInteractionPreview || interaction.OperatorConfirmed ||
				interaction.WorkspaceTrust != domain.WorkspaceTrustUntrusted || interaction.ExecutionProfileRevision != profile.Revision {
				t.Fatalf("old confirmation resurrected: %+v %v", interaction, err)
			}
		})
	}
}

func newThreadModelSettingsFixture(t *testing.T) (*store.SQLiteStore, domain.Run, *mutableThreadModelRouteRegistry) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "model-settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.SaveWorkspace(t.Context(), store.WorkspaceRecord{ID: "workspace-model-settings",
		Name: "model settings", RootPath: t.TempDir(), CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	_, created, err := application.NewRunService(st).Create(t.Context(), application.CreateRunRequest{
		Goal: "switch the model and preserve project preferences", Profile: "code", Phase: "deliver",
		WorkspaceID: "workspace-model-settings", ModelRoute: "lifecycle-test/model", Interactive: true,
		Budget: domain.Budget{MaxTurns: 4}})
	if err != nil {
		t.Fatal(err)
	}
	provider := &lifecycleProvider{responses: []string{rootActionResponse(domain.RootActionWait, "Selected model is ready.", "", "operator boundary")}}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	registry := &mutableThreadModelRouteRegistry{router: router, snapshot: modelregistry.Snapshot{
		ProtocolVersion: modelregistry.ProtocolVersion, Generation: 1,
		Providers: []modelregistry.ProviderAvailability{{Name: provider.Name(), Enabled: true,
			DisplayName: "Lifecycle Provider", Kind: modelregistry.ProviderKindOpenAICompatible,
			Status: modelregistry.ProviderAvailable, Models: []string{"model", "model-next"}, CredentialSource: "test",
			Harnesses: []modelregistry.HarnessAvailability{
				{ProtocolVersion: modelregistry.HarnessQualificationProtocolVersion, Model: "model", RootEligible: true, LatestQualificationStatus: modelregistry.QualificationStatusAvailable},
				{ProtocolVersion: modelregistry.HarnessQualificationProtocolVersion, Model: "model-next", RootEligible: true, LatestQualificationStatus: modelregistry.QualificationStatusAvailable},
			}},
		}}}
	_, err = application.NewThreadModelRouteService(st, registry).Change(t.Context(), application.ChangeThreadModelRouteRequest{
		Version: domain.ThreadModelRouteControlProtocolVersion, ThreadID: domain.InitialThreadID(created.ID),
		Action: domain.ThreadModelRouteSelect, Provider: provider.Name(), Model: "model-next",
		OperationKey: "select-model-next", RequestedBy: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	return st, created, registry
}

func executeThreadModelSettingsSwitch(t *testing.T, st *store.SQLiteStore, created domain.Run,
	registry *mutableThreadModelRouteRegistry,
) application.ExecuteThreadTurnResult {
	t.Helper()
	// Match a real conversation waiting at an operator boundary, rather than
	// only exercising a freshly created Run's configuration defaults.
	runs := application.NewRunService(st)
	if _, err := runs.Start(t.Context(), created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runs.Pause(t.Context(), created.ID); err != nil {
		t.Fatal(err)
	}
	turns := application.NewThreadTurnServiceWithExecutionCapabilities(st, application.NewRunLifecycleControlService(st),
		application.NewRunExecutionHandoffService(st, registry.router, policy.NewDefaultChecker()),
		domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true}).WithModelRouteRegistry(registry)
	request := application.ExecuteThreadTurnRequest{
		Version: domain.ThreadMessageProtocolVersion, ThreadID: domain.InitialThreadID(created.ID),
		Content: "Continue with the selected model.", OperationKey: "next-model-message", RequestedBy: "operator"}
	result, err := turns.Execute(t.Context(), request)
	if err != nil || !result.Submission.SuccessorCreated || result.Submission.PredecessorRunID != created.ID ||
		result.Submission.Run.Config.ModelRoute != "lifecycle-test/model-next" || !result.ModelCalled || result.ToolCalled {
		t.Fatalf("normal model switch failed: %+v %v", result, err)
	}
	replayed, err := turns.Execute(t.Context(), request)
	if err != nil || !replayed.Replayed || replayed.Submission.Run.ID != result.Submission.Run.ID ||
		replayed.Submission.Message.ID != result.Submission.Message.ID {
		t.Fatalf("model switch retry did not confirm the same result: %+v %v", replayed, err)
	}
	bindings, err := st.ListThreadRuns(t.Context(), domain.InitialThreadID(created.ID))
	if err != nil || len(bindings) != 2 {
		t.Fatalf("model switch duplicated its successor: %+v %v", bindings, err)
	}
	return result
}

func setThreadModelTestProfile(t *testing.T, st *store.SQLiteStore, runID, profile, key string) {
	t.Helper()
	_, err := application.NewRunExecutionProfileService(st).Change(t.Context(), application.ChangeRunExecutionProfileRequest{
		RunID: runID, Profile: profile, OperationKey: "model-settings-" + key, RequestedBy: "operator", Reason: fmt.Sprintf("select %s for this project", profile)})
	if err != nil {
		t.Fatal(err)
	}
}

func setThreadModelTestInteraction(t *testing.T, st *store.SQLiteStore, runID, mode, key string) {
	t.Helper()
	trust := "untrusted"
	if mode != "preview" {
		trust = "trusted"
	}
	_, err := application.NewRunExecutionInteractionService(st).Change(t.Context(), application.ChangeRunExecutionInteractionRequest{
		RunID: runID, Mode: mode, Trust: trust, ConfirmWorkspaceTrust: mode != "preview",
		OperationKey: "model-settings-" + key, RequestedBy: "operator", Reason: "explicit project interaction preference"})
	if err != nil {
		t.Fatal(err)
	}
}
