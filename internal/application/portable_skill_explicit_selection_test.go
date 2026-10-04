package application_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/coordinator"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/plugins"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/skills"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
)

// Exercise the new writer, not a seeded historical installation. Operator-only
// instructions must become usable through an explicit, durable Run selection.
func TestPortableSkillNewLegacyExplicitSelectionAndRestart(t *testing.T) {
	for _, test := range []struct {
		name     string
		explicit bool
	}{{"user-only", false}, {"explicit-only", true}} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "new-explicit-skill.db")
			st := openHistoryRecallStore(t, path)
			defer func() { _ = st.Close() }()
			body := []byte("# Explicit operator instructions\nRead only the operator-selected evidence.\n")
			manifest := skills.BindManifestContent(skills.Manifest{Protocol: skills.ProtocolVersion,
				Name: "new-" + test.name, Version: "1.0.0", Description: "User-selected instructions",
				Profiles: []domain.Profile{domain.ProfileCode}, Surfaces: []domain.ExecutionSurface{domain.ExecutionSurfaceCode},
				Phases: []domain.ExecutionPhase{domain.ExecutionPhaseDeliver}, Roles: []domain.AgentRole{domain.AgentRoleRoot},
				UserInvocable: true, ModelInvocable: false, ExplicitOnly: test.explicit,
				ToolDependencies: []toolgateway.ToolName{toolgateway.ReadFileTool}}, body)
			raw, err := skills.BuildUnsignedPackage(manifest, body)
			if err != nil {
				t.Fatal(err)
			}
			builtins, _ := skills.BuiltinRegistry()
			objects, _ := skills.NewLocalPackageObjectStore(t.TempDir())
			registry := application.NewSkillPackageRegistryService(st, objects, builtins)
			imported, err := registry.Import(t.Context(), application.ImportSkillPackageRequest{Raw: raw,
				Surface: domain.ExecutionSurfaceCode, OperationKey: "explicit-new-import-" + test.name,
				InstalledBy: "operator", ConfirmUntrusted: true})
			if err != nil || imported.Installation == nil || imported.Package.Installation.ID != "" {
				t.Fatalf("new Plugin import=%+v err=%v", imported, err)
			}
			value := reviewInstalledFixture(t, st, *imported.Installation, plugins.ReviewApprove)
			value = reviewInstalledFixture(t, st, value, plugins.ReviewEnable)
			legacy, err := st.ListInstalledPackages(t.Context(), "", "", true)
			if err != nil || len(legacy) != 0 {
				t.Fatalf("new import unexpectedly wrote the legacy ledger: count=%d err=%v", len(legacy), err)
			}
			_, run, err := application.NewRunService(st).Create(t.Context(), application.CreateRunRequest{
				Goal: "Use the operator-selected new Skill", Profile: "code", Surface: "code", Phase: "deliver",
				ModelRoute: "tool-loop/model", Budget: domain.Budget{MaxTurns: 4, MaxToolCalls: 12}})
			if err != nil {
				t.Fatal(err)
			}
			selectionRequest := application.SelectExternalSkillsRequest{RunID: run.ID,
				PackageRefs: []string{manifest.Name + "@" + manifest.Version}, TokenBudget: 4096,
				OperationKey: "explicit-new-selection-" + test.name, RequestedBy: "operator", ConfirmUntrustedContext: true}
			selected := true
			for _, phase := range []string{"before-restart", "after-restart"} {
				current, err := st.GetPluginInstallation(t.Context(), value.ID)
				if err != nil || current.State != plugins.StateEnabled || current.Snapshot.Legacy.Manifest.ModelInvocable {
					t.Fatalf("enabled Plugin/policy changed: %+v err=%v", current, err)
				}
				selection, selectErr := application.NewExternalSkillSelectionService(st).Select(t.Context(), selectionRequest)
				err = selectErr
				if err == nil {
					item := selection.Selection.Items[0]
					if selection.Selection.ProtocolVersion != skills.PluginExternalSelectionProtocolVersion || item.Plugin == nil ||
						item.InstallationID != value.ID || item.Plugin.PackageID != value.PackageID() || item.Plugin.Revision != value.Revision() ||
						item.Plugin.Generation != value.Generation || item.Plugin.ComponentID != value.Snapshot.Skills[0].Instructions.Component.ComponentID ||
						item.InstallResultFingerprint != "" || item.ObjectKey != "" || selection.Selection.ToolCapabilityGrant {
						t.Fatalf("selection did not retain the real Plugin binding: %+v", selection)
					}
				}
				t.Logf("%s: Plugin=%s generation=%d legacy_rows=0 Run=%s selection_code=%s err=%v",
					phase, current.State, current.Generation, run.Status, apperror.CodeOf(err), err)
				if err != nil {
					selected = false
					t.Errorf("enabled user-invocable new Plugin could not be explicitly selected: %v", err)
				}
				if phase == "before-restart" {
					if err := st.Close(); err != nil {
						t.Fatal(err)
					}
					st = openHistoryRecallStore(t, path)
				}
			}
			if !selected {
				return // Do not claim an execution/restart pass after selection failed.
			}
			if _, err := application.NewRunService(st).Start(t.Context(), run.ID); err != nil {
				t.Fatal(err)
			}
			pin := installedReadPin(value)
			provider := &scriptedToolProvider{responses: []*llm.ChatResponse{
				{ToolCalls: []llm.ToolCall{installedReadCall(pin, "explicit-read", "")}},
				textResponse(rootActionResponse(domain.RootActionContinue, "Read explicitly selected instructions", "", "")),
			}}
			if _, err := newToolLoopSupervisor(st, provider).Step(t.Context(), run.ID); err != nil {
				t.Fatal(err)
			}
			requests := provider.Requests()
			if len(requests) != 2 {
				t.Fatalf("unexpected provider fixture rounds: %d", len(requests))
			}
			assertInstalledReadResult(t, requests[1], pin, "", body)
			assertExplicitGuidance(t, requests[0], "root", body)
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			st = openHistoryRecallStore(t, path)
			restored := &scriptedToolProvider{responses: []*llm.ChatResponse{
				{ToolCalls: []llm.ToolCall{installedReadCall(pin, "restored-explicit-read", "")}},
				textResponse(rootActionResponse(domain.RootActionContinue, "Read restored selection", "", "")),
			}}
			if _, err := newToolLoopSupervisor(st, restored).Step(t.Context(), run.ID); err != nil {
				t.Fatal(err)
			}
			after := restored.Requests()
			if len(after) != 2 {
				t.Fatalf("restored request count=%d", len(after))
			}
			assertExplicitGuidance(t, after[0], "root", body)
			assertInstalledReadResult(t, after[1], pin, "", body)
			restoredReferences := false
			for _, msg := range after[0].Messages {
				if strings.Contains(msg.Content, "Previously activated installed Skill references") && strings.Contains(msg.Content, pin.InstallationID) {
					restoredReferences = true
				}
			}
			if !restoredReferences {
				t.Fatal("restart lost the successful exact Skill read reference")
			}

		})
	}
}

func explicitPluginFixture(t *testing.T, st *store.SQLiteStore, name string, specialist bool) (plugins.Installation, []byte) {
	t.Helper()
	body := []byte("# " + name + "\nUse the explicitly selected evidence only.\n")
	roles := []domain.AgentRole{domain.AgentRoleRoot}
	if specialist {
		roles = append(roles, domain.AgentRoleSpecialist)
	}
	manifest := skills.BindManifestContent(skills.Manifest{Protocol: skills.ProtocolVersion, Name: name, Version: "1.0.0", Description: "Explicit fixture",
		Profiles: []domain.Profile{domain.ProfileCode}, Surfaces: []domain.ExecutionSurface{domain.ExecutionSurfaceCode},
		Phases: []domain.ExecutionPhase{domain.ExecutionPhaseDeliver}, Roles: roles, UserInvocable: true, ExplicitOnly: true,
		ToolDependencies: []toolgateway.ToolName{toolgateway.ReadFileTool}}, body)
	raw, err := skills.BuildUnsignedPackage(manifest, body)
	if err != nil {
		t.Fatal(err)
	}
	builtins, _ := skills.BuiltinRegistry()
	objects, _ := skills.NewLocalPackageObjectStore(t.TempDir())
	result, err := application.NewSkillPackageRegistryService(st, objects, builtins).Import(t.Context(), application.ImportSkillPackageRequest{
		Raw: raw, Surface: domain.ExecutionSurfaceCode, OperationKey: "fresh-" + name, InstalledBy: "operator", ConfirmUntrusted: true})
	if err != nil || result.Installation == nil || result.Package.Installation.ID != "" {
		t.Fatalf("fresh Plugin import: %+v %v", result, err)
	}
	value := reviewInstalledFixture(t, st, *result.Installation, plugins.ReviewApprove)
	return reviewInstalledFixture(t, st, value, plugins.ReviewEnable), body
}

func createExplicitRun(t *testing.T, st *store.SQLiteStore, provider string) domain.Run {
	t.Helper()
	_, run, err := application.NewRunService(st).Create(t.Context(), application.CreateRunRequest{Goal: "Read explicitly selected guidance", Profile: "code", Surface: "code", Phase: "deliver", ModelRoute: provider + "/model", Budget: domain.Budget{MaxTurns: 8, MaxToolCalls: 16}})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func selectExplicitPlugins(t *testing.T, st *store.SQLiteStore, run domain.Run, values []plugins.Installation, specialist string) skills.ExternalSelection {
	t.Helper()
	refs := make([]string, 0, len(values))
	for _, value := range values {
		refs = append(refs, value.Snapshot.Legacy.Manifest.Name+"@1.0.0")
	}
	result, err := application.NewExternalSkillSelectionService(st).Select(t.Context(), application.SelectExternalSkillsRequest{RunID: run.ID,
		PackageRefs: refs, SpecialistRef: specialist, TokenBudget: 4096, OperationKey: "select-" + run.ID, RequestedBy: "operator", ConfirmUntrustedContext: true})
	if err != nil {
		t.Fatal(err)
	}
	return result.Selection
}

func assertExplicitGuidance(t *testing.T, request llm.ChatRequest, audience string, body []byte) {
	t.Helper()
	found := false
	for _, msg := range request.Messages {
		var envelope struct {
			Version   string          `json:"version"`
			Audience  string          `json:"audience"`
			Content   string          `json:"content"`
			Authority map[string]bool `json:"authority"`
		}
		if json.Unmarshal([]byte(msg.Content), &envelope) != nil || envelope.Version != "external_skill_guidance.v1" || envelope.Content != string(body) {
			continue
		}
		if msg.Role != "user" || envelope.Audience != audience || !envelope.Authority["workflow_guidance"] {
			t.Fatalf("wrong explicit delivery envelope: %+v", msg)
		}
		for key, allowed := range envelope.Authority {
			if key != "workflow_guidance" && allowed {
				t.Fatalf("Skill delivery granted %s", key)
			}
		}
		found = true
	}
	if !found {
		t.Fatalf("actual %s model request lacks the selected Skill body", audience)
	}
}

func TestPortableSkillExplicitSelectionIsRunScoped(t *testing.T) {
	st := openHistoryRecallStore(t, filepath.Join(t.TempDir(), "scope.db"))
	defer st.Close()
	value, body := explicitPluginFixture(t, st, "run-scoped", false)
	pin := installedReadPin(value)
	for _, phase := range []string{"unselected", "another-run-selected"} {
		if phase == "another-run-selected" {
			owner := createExplicitRun(t, st, "tool-loop")
			selectExplicitPlugins(t, st, owner, []plugins.Installation{value}, "")
		}
		run := createExplicitRun(t, st, "tool-loop")
		if _, err := application.NewRunService(st).Start(t.Context(), run.ID); err != nil {
			t.Fatal(err)
		}
		p := &scriptedToolProvider{responses: []*llm.ChatResponse{
			{ToolCalls: []llm.ToolCall{installedReadCall(pin, "unselected-read-"+phase, "")}},
			textResponse(rootActionResponse(domain.RootActionContinue, "Selection required", "", "")),
		}}
		if _, err := newToolLoopSupervisor(st, p).Step(t.Context(), run.ID); err != nil {
			t.Fatal(err)
		}
		requests := p.Requests()
		if len(requests) != 2 || !hasErrorToolResult(requests[1], string(apperror.CodePolicyDenied)) || hasToolResult(requests[1], strings.SplitN(string(body), "\n", 2)[0]) {
			t.Fatalf("%s read escaped Run selection", phase)
		}
		for _, msg := range requests[0].Messages {
			if strings.Contains(msg.Content, pin.InstallationID) || strings.Contains(msg.Content, strings.SplitN(string(body), "\n", 2)[0]) {
				t.Fatalf("%s exposed unselected explicit Skill", phase)
			}
		}
		reads, err := st.ListBuiltinSkillReadCalls(t.Context(), run.ID)
		if err != nil || len(reads) != 0 {
			t.Fatalf("denied read activated: %v", err)
		}
	}
}

type changedAfterExplicitPreparationStore struct {
	*store.SQLiteStore
	change func()
}

func (s *changedAfterExplicitPreparationStore) PrepareExternalRootSkillContext(ctx context.Context, checkpoint domain.SupervisorCheckpoint, request skills.ExternalRootContextPreparationRequest) (skills.ExternalRootContextPreparation, error) {
	preparation, err := s.SQLiteStore.PrepareExternalRootSkillContext(ctx, checkpoint, request)
	if err == nil && s.change != nil {
		change := s.change
		s.change = nil
		change()
	}
	return preparation, err
}

func (s *changedAfterExplicitPreparationStore) PrepareExternalSpecialistSkillContext(ctx context.Context, ref domain.AgentAttemptRef, request skills.ExternalSpecialistContextPreparationRequest) (skills.ExternalSpecialistContextPreparation, error) {
	preparation, err := s.SQLiteStore.PrepareExternalSpecialistSkillContext(ctx, ref, request)
	if err == nil && s.change != nil {
		change := s.change
		s.change = nil
		change()
	}
	return preparation, err
}

func TestPortableSkillExplicitSelectionRejectsLifecycleDrift(t *testing.T) {
	for _, scenario := range []struct {
		name, stage string
		action      plugins.ReviewAction
		reenable    bool
	}{
		{"disabled-before-read", "before", plugins.ReviewDisable, false},
		{"revoked-before-read", "before", plugins.ReviewRevoke, false},
		{"generation-before-read", "before", plugins.ReviewDisable, true},
		{"disabled-during-read", "read", plugins.ReviewDisable, false},
		{"disabled-after-preparation", "commit", plugins.ReviewDisable, false},
		{"generation-after-preparation", "commit", plugins.ReviewDisable, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			st := openHistoryRecallStore(t, filepath.Join(t.TempDir(), "lifecycle.db"))
			defer st.Close()
			value, _ := explicitPluginFixture(t, st, "lifecycle-explicit", false)
			run := createExplicitRun(t, st, "tool-loop")
			selected := selectExplicitPlugins(t, st, run, []plugins.Installation{value}, "")
			if _, err := application.NewRunService(st).Start(t.Context(), run.ID); err != nil {
				t.Fatal(err)
			}
			change := func() {
				value = reviewInstalledFixture(t, st, value, scenario.action)
				if scenario.reenable {
					value = reviewInstalledFixture(t, st, value, plugins.ReviewEnable)
				}
			}
			var source application.AgentRunnerStore = st
			switch scenario.stage {
			case "before":
				change()
			case "read":
				source = &changedDuringPortableReadStore{SQLiteStore: st, change: change}
			case "commit":
				source = &changedAfterExplicitPreparationStore{SQLiteStore: st, change: change}
			}
			p := &scriptedToolProvider{responses: []*llm.ChatResponse{textResponse(rootActionResponse(domain.RootActionContinue, "Must not be called", "", ""))}}
			if _, err := newToolLoopSupervisor(source, p).Step(t.Context(), run.ID); err == nil || len(p.Requests()) != 0 {
				t.Fatalf("stale explicit Skill reached provider: calls=%d err=%v", len(p.Requests()), err)
			}
			durable, found, err := st.GetExternalSkillSelectionByRun(t.Context(), run.ID)
			if err != nil || !found || !reflect.DeepEqual(durable, selected) {
				t.Fatalf("lifecycle drift rewrote the immutable selection: %v", err)
			}
			projection, found, err := st.GetExternalSkillProjectionByRun(t.Context(), run.ID)
			if err != nil || !found || projection.RootCommittedCount != 0 {
				t.Fatalf("stale content committed: %+v %v", projection, err)
			}
			if scenario.stage == "commit" && projection.RootPreparedCount != 1 {
				t.Fatal("test did not reach the post-preparation boundary")
			}
		})
	}
}

func TestPortableSkillExplicitSpecialistDeliveryAndRestart(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "restart", true: "revoke-after-preparation"}[changed], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "specialist.db")
			st := openHistoryRecallStore(t, path)
			defer func() { _ = st.Close() }()
			designated, body := explicitPluginFixture(t, st, "designated-specialist", true)
			rootOnly, rootBody := explicitPluginFixture(t, st, "root-only-guidance", false)
			run := createExplicitRun(t, st, "specialist-test")
			selectExplicitPlugins(t, st, run, []plugins.Installation{designated, rootOnly}, "designated-specialist@1.0.0")
			if _, err := application.NewRunService(st).Start(t.Context(), run.ID); err != nil {
				t.Fatal(err)
			}
			root, found, err := st.GetRootAgent(t.Context(), run.ID)
			if err != nil || !found {
				t.Fatal("missing Root", err)
			}
			coord, err := coordinator.NewWithSpecialistAdmission(st, coordinator.SpecialistAdmissionPolicy{MaxChildren: 1, MaxTurnsPerChild: 3, MaxTokensPerChild: 128})
			if err != nil {
				t.Fatal(err)
			}
			admitted, err := coord.AdmitSpecialist(t.Context(), coordinator.AdmitSpecialistRequest{RunID: run.ID, ParentAgentID: root.ID, Title: "Selected Skill review", Skills: []string{"model.chat"}, TurnLimit: 3, TokenLimit: 128, IdempotencyKey: "explicit-specialist-admission"})
			if err != nil {
				t.Fatal(err)
			}
			for step := 0; step < 2; step++ {
				provider := &specialistTestProvider{responses: []llm.ChatResponse{{Text: specialistResponse(t, domain.SpecialistAction{Version: domain.SpecialistLifecycleVersion, Kind: domain.SpecialistActionContinue, Message: "Used selected guidance"}), Usage: llm.Usage{InputTokens: 2, OutputTokens: 2, TotalTokens: 4}}}}
				router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
				router.RegisterProvider(provider)
				var source application.SubagentRunnerStore = st
				if changed {
					source = &changedAfterExplicitPreparationStore{SQLiteStore: st, change: func() { reviewInstalledFixture(t, st, designated, plugins.ReviewRevoke) }}
				}
				runner := application.NewSubagentRunner(source, router, policy.NewDefaultChecker())
				result, err := runner.Step(t.Context(), run.ID, admitted.Agent.ID)
				if changed {
					if err == nil || len(provider.requests) != 0 {
						t.Fatal("revoked Specialist Skill reached the provider", err)
					}
					projection, _, err := st.GetExternalSkillProjectionByRun(t.Context(), run.ID)
					if err != nil || projection.SpecialistPreparedCount != 1 || projection.SpecialistCommittedCount != 0 {
						t.Fatalf("stale Specialist commit: %+v %v", projection, err)
					}
					break
				}
				if err != nil || result.ExternalSkillItems != 1 || len(provider.requests) != 1 || len(provider.requests[0].Tools) != 0 {
					t.Fatalf("Specialist delivery: %+v %v", result, err)
				}
				assertExplicitGuidance(t, provider.requests[0], "specialist", body)
				for _, msg := range provider.requests[0].Messages {
					if strings.Contains(msg.Content, strings.SplitN(string(rootBody), "\n", 2)[0]) {
						t.Fatal("Root-only Skill reached Specialist")
					}
				}
				if step == 0 {
					if err := st.Close(); err != nil {
						t.Fatal(err)
					}
					st = openHistoryRecallStore(t, path)
				}
			}
			legacy, err := st.ListInstalledPackages(t.Context(), "", "", true)
			if err != nil || len(legacy) != 0 {
				t.Fatal("new Specialist path fabricated legacy installations", err)
			}
		})
	}
}

func TestPortableSkillExplicitSelectionRespectsPhaseProfileAndRole(t *testing.T) {
	st := openHistoryRecallStore(t, filepath.Join(t.TempDir(), "manifest-scope.db"))
	defer st.Close()
	value, _ := explicitPluginFixture(t, st, "bounded-explicit", false)
	for _, scenario := range []struct{ name, phase, profile, specialist string }{
		{"wrong-phase", "plan", "code", ""}, {"wrong-profile", "deliver", "review", ""},
		{"wrong-role", "deliver", "code", "bounded-explicit@1.0.0"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			_, run, err := application.NewRunService(st).Create(t.Context(), application.CreateRunRequest{Goal: "Respect manifest selection scope", Profile: scenario.profile, Surface: "code", Phase: scenario.phase, ModelRoute: "tool-loop/model", Budget: domain.Budget{MaxTurns: 4}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = application.NewExternalSkillSelectionService(st).Select(t.Context(), application.SelectExternalSkillsRequest{RunID: run.ID, PackageRefs: []string{value.Snapshot.Legacy.Manifest.Name + "@1.0.0"}, SpecialistRef: scenario.specialist, TokenBudget: 4096, OperationKey: "refused-" + scenario.name, RequestedBy: "operator", ConfirmUntrustedContext: true})
			if apperror.CodeOf(err) != apperror.CodeInvalidArgument {
				t.Fatalf("manifest boundary accepted or failed unexpectedly: %v", err)
			}
			if _, found, err := st.GetExternalSkillSelectionByRun(t.Context(), run.ID); err != nil || found {
				t.Fatalf("refused manifest selection persisted: %v", err)
			}
		})
	}
}

func TestPortableSkillLegacyV1RootDeliveryStillReadsRetainedObject(t *testing.T) {
	p := &scriptedToolProvider{responses: []*llm.ChatResponse{textResponse(rootActionResponse(domain.RootActionContinue, "Read retained historical instructions", "", ""))}}
	st, run, _, _ := newSubagentRunnerFixtureWithExternal(t, p, domain.Budget{MaxTurns: 10}, 2, 64, true)
	selection, found, err := st.GetExternalSkillSelectionByRun(t.Context(), run.ID)
	if err != nil || !found || selection.ProtocolVersion != skills.ExternalSelectionProtocolVersion || selection.Items[0].Plugin != nil || selection.Items[0].ObjectKey == "" {
		t.Fatalf("missing real v1 selection: %+v %v", selection, err)
	}
	if _, err := newToolLoopSupervisor(st, p).Step(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	requests := p.Requests()
	if len(requests) != 1 {
		t.Fatal("historical Root delivery was not performed")
	}
	body := []byte("# External Specialist review\n\n" + strings.Repeat("Verify repository evidence before conclusions.\n", 30))
	assertExplicitGuidance(t, requests[0], "root", body)
}
