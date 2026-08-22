package application_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/store"
)

func TestRunCapabilityReadinessProjectsStableFailureReasons(t *testing.T) {
	ctx, state, run := newCapabilityReadinessRun(t, "code")
	runtime := application.CapabilityReadinessRuntime{
		RunControlEnabled: true, ExecutionPermissionControlEnabled: true,
	}
	projection, err := application.NewRunCapabilityReadinessService(state, runtime).
		Project(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if projection.ProtocolVersion != application.RunCapabilityReadinessProtocolVersion ||
		projection.RunID != run.ID || projection.CapabilityGrant || projection.Validate() != nil {
		t.Fatalf("invalid readiness envelope: %#v", projection)
	}
	workspace := readinessOption(t, projection.Permissions, "workspace_access")
	assertReadinessBlockers(t, workspace,
		application.CapabilityBlockerStartupGateClosed,
		application.CapabilityBlockerSandboxUnproven)
	if workspace.Selectable || workspace.RuntimeAvailable || !workspace.RestartRequired {
		t.Fatalf("unexpected Workspace Access readiness: %#v", workspace)
	}
	local := readinessOption(t, projection.Profiles, "local")
	assertReadinessBlockers(t, local,
		application.CapabilityBlockerStartupGateClosed,
		application.CapabilityBlockerWorkspaceUntrusted,
		application.CapabilityBlockerSandboxUnproven)
	if !local.Selectable || local.RuntimeAvailable {
		t.Fatalf("Local intent must remain selectable without claiming runtime: %#v", local)
	}
	docker := readinessOption(t, projection.Profiles, "docker")
	assertReadinessBlockers(t, docker,
		application.CapabilityBlockerStartupGateClosed,
		application.CapabilityBlockerWorkspaceUntrusted,
		application.CapabilityBlockerDockerUnavailable)
	if !docker.Selectable || docker.RuntimeAvailable {
		t.Fatalf("Docker intent must remain selectable without claiming runtime: %#v", docker)
	}
	controlled := readinessOption(t, projection.Interactions, "controlled")
	assertReadinessBlockers(t, controlled,
		application.CapabilityBlockerStartupGateClosed,
		application.CapabilityBlockerProfileMismatch,
		application.CapabilityBlockerWorkspaceUntrusted,
		application.CapabilityBlockerSandboxUnproven)
	if controlled.Selectable || controlled.RuntimeAvailable {
		t.Fatalf("controlled interaction ignored its profile mismatch: %#v", controlled)
	}
	cyber := readinessOption(t, projection.Interactions, "cyber")
	assertReadinessBlockers(t, cyber,
		application.CapabilityBlockerStartupGateClosed,
		application.CapabilityBlockerSurfaceMismatch,
		application.CapabilityBlockerProfileMismatch,
		application.CapabilityBlockerWorkspaceUntrusted,
		application.CapabilityBlockerDockerUnavailable)
	fullCDP := readinessOption(t, projection.BrowserCDPPermissions, "full_debug")
	assertReadinessBlockers(t, fullCDP,
		application.CapabilityBlockerStartupGateClosed,
		application.CapabilityBlockerPermissionMismatch)
	preset := readinessOption(t, projection.Presets, application.StandardCodePresetValue)
	if !hasReadinessBlocker(preset, application.CapabilityBlockerCapabilityUnimplemented) ||
		preset.Selectable || preset.RuntimeAvailable || preset.Selected {
		t.Fatalf("Standard Code preset did not fail closed: %#v", preset)
	}
}

func TestRunCapabilityReadinessDistinguishesRunningPausedAndActiveLease(t *testing.T) {
	ctx, state, run := newCapabilityReadinessRun(t, "code")
	runtime := readyCapabilityReadinessRuntime()
	service := application.NewRunCapabilityReadinessService(state, runtime)
	runService := application.NewRunService(state)
	if _, err := runService.Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	running, err := service.Project(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	preview := readinessOption(t, running.Profiles, "preview")
	assertReadinessBlockers(t, preview, application.CapabilityBlockerRunNotQuiescent)
	if preview.Selectable || !preview.RuntimeAvailable || !preview.Selected {
		t.Fatalf("running selected Preview was conflated with runtime failure: %#v", preview)
	}
	if _, err := runService.Pause(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	paused, err := service.Project(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	preview = readinessOption(t, paused.Profiles, "preview")
	if !preview.Selectable || !preview.RuntimeAvailable || len(preview.BlockedBy) != 0 {
		t.Fatalf("paused Preview did not become selectable: %#v", preview)
	}
	lease, err := state.AcquireRunExecutionLease(ctx, domain.AcquireRunExecutionLeaseRequest{
		RunID: run.ID, OwnerID: "readiness-test-owner",
		TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if lease.Lease.Status != domain.RunExecutionLeaseActive {
		t.Fatalf("unexpected lease: %#v", lease)
	}
	leased, err := service.Project(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	preview = readinessOption(t, leased.Profiles, "preview")
	assertReadinessBlockers(t, preview, application.CapabilityBlockerExecutionLeaseActive)
	if preview.Selectable || !preview.RuntimeAvailable {
		t.Fatalf("active lease was not separated from runtime readiness: %#v", preview)
	}
	conservative := readinessOption(t, leased.Permissions, "conservative")
	if !conservative.Selectable || !conservative.RuntimeAvailable {
		t.Fatalf("permission revision must remain selectable so it can revoke authority: %#v",
			conservative)
	}
}

func TestRunCapabilityReadinessSeparatesUnprovenAndUnreadyBackends(t *testing.T) {
	ctx, state, run := newCapabilityReadinessRun(t, "cyber")
	runtime := readyCapabilityReadinessRuntime()
	runtime.LocalBackendReady = false
	runtime.DockerBackendReady = false
	projection, err := application.NewRunCapabilityReadinessService(state, runtime).
		Project(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	local := readinessOption(t, projection.Profiles, "local")
	if !hasReadinessBlocker(local, application.CapabilityBlockerBackendNotReady) ||
		hasReadinessBlocker(local, application.CapabilityBlockerSandboxUnproven) {
		t.Fatalf("proven but unready Local backend was misclassified: %#v", local)
	}
	docker := readinessOption(t, projection.Profiles, "docker")
	if !hasReadinessBlocker(docker, application.CapabilityBlockerBackendNotReady) ||
		hasReadinessBlocker(docker, application.CapabilityBlockerDockerUnavailable) {
		t.Fatalf("installed but unready Docker backend was misclassified: %#v", docker)
	}
}

func TestCapabilityReadinessOptionValidationRejectsContradictoryDisposition(t *testing.T) {
	tests := []struct {
		name   string
		option application.CapabilityReadinessOption
	}{
		{"missing matching remediation", application.CapabilityReadinessOption{
			Value: "local", BlockedBy: []application.CapabilityReadinessBlocker{
				application.CapabilityBlockerSandboxUnproven},
			Remediation: []application.CapabilityReadinessRemediation{
				application.CapabilityRemediationRetryBackendReadiness},
		}},
		{"unrelated extra remediation", application.CapabilityReadinessOption{
			Value: "preview", BlockedBy: []application.CapabilityReadinessBlocker{
				application.CapabilityBlockerRunNotQuiescent},
			Remediation: []application.CapabilityReadinessRemediation{
				application.CapabilityRemediationPauseRun,
				application.CapabilityRemediationTrustWorkspace},
		}},
		{"runtime available with runtime blocker", application.CapabilityReadinessOption{
			Value: "docker", RuntimeAvailable: true,
			BlockedBy: []application.CapabilityReadinessBlocker{
				application.CapabilityBlockerDockerUnavailable},
			Remediation: []application.CapabilityReadinessRemediation{
				application.CapabilityRemediationInstallOrStartDocker},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.option.Validate(); err == nil {
				t.Fatalf("contradictory readiness option passed validation: %#v", test.option)
			}
		})
	}
}

func newCapabilityReadinessRun(t *testing.T, surface string) (
	context.Context, *store.SQLiteStore, domain.Run,
) {
	t.Helper()
	state, err := store.Open(filepath.Join(t.TempDir(), "readiness.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	ctx := context.Background()
	_, run, err := application.NewRunService(state).Create(ctx, application.CreateRunRequest{
		Goal: "project stable capability readiness", Profile: "review", Surface: surface,
		Phase: "deliver", Budget: domain.Budget{MaxTurns: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	return ctx, state, run
}

func readyCapabilityReadinessRuntime() application.CapabilityReadinessRuntime {
	return application.CapabilityReadinessRuntime{
		RunControlEnabled: true, ExecutionPermissionControlEnabled: true,
		BrowserCDPPermissionControlEnabled: true,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{
			WorkspaceSandboxEnabled: true, OperatorApprovalEnabled: true,
			DangerFullAccessEnabled: true, DebugMaximumAccessEnabled: true,
		},
		BrowserCDPPermissionCapabilities: domain.BrowserCDPPermissionRuntimeCapabilities{
			ControlEnabled: true, FullDebugEnabled: true,
		},
		LocalSandboxInstalled: true, LocalSandboxProven: true, LocalBackendReady: true,
		DockerStartupGateEnabled: true, DockerAvailable: true, DockerBackendReady: true,
		BrowserBackendReady: true,
	}
}

func readinessOption(t *testing.T, options []application.CapabilityReadinessOption,
	value string,
) application.CapabilityReadinessOption {
	t.Helper()
	for _, option := range options {
		if option.Value == value {
			return option
		}
	}
	t.Fatalf("readiness option %q not found in %#v", value, options)
	return application.CapabilityReadinessOption{}
}

func assertReadinessBlockers(t *testing.T, option application.CapabilityReadinessOption,
	want ...application.CapabilityReadinessBlocker,
) {
	t.Helper()
	if len(option.BlockedBy) != len(want) {
		t.Fatalf("option %s blockers=%v want=%v", option.Value, option.BlockedBy, want)
	}
	for index := range want {
		if option.BlockedBy[index] != want[index] {
			t.Fatalf("option %s blockers=%v want=%v", option.Value, option.BlockedBy, want)
		}
	}
}

func hasReadinessBlocker(option application.CapabilityReadinessOption,
	want application.CapabilityReadinessBlocker,
) bool {
	for _, blocker := range option.BlockedBy {
		if blocker == want {
			return true
		}
	}
	return false
}
