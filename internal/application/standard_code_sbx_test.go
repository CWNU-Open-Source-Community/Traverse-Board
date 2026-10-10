package application

import (
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
)

func TestStandardCodeSBXSelectionIsExplicitAndRecoverable(t *testing.T) {
	fixture := newDrydockApplicationFixture(t, "standard-code-sbx")
	runtime := CapabilityReadinessRuntime{
		RunControlEnabled: true, RunExecutionEnabled: true,
		ExecutionPermissionControlEnabled: true, StandardCodePresetEnabled: true,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{
			WorkspaceSandboxEnabled: true, OperatorApprovalEnabled: true,
		},
		SBXStartupGateEnabled: true, SBXAvailable: true, SBXBackendReady: true,
		CommandRuntimeAdapters: []commandruntimeadapter.Identity{
			commandruntimeadapter.SandboxedWorkspace("docker_sandboxes", "sbx-fixture-policy", "sbx-fixture-generation"),
		},
	}
	service, err := NewStandardCodePresetService(fixture.state, fixture.service, runtime)
	if err != nil {
		t.Fatal(err)
	}
	request := ConfigureStandardCodeRequest{Version: domain.StandardCodePresetProtocolVersion,
		RunID: fixture.run.ID, BackendIntent: "auto", Action: "configure",
		OperationKey: "sbx-selection-test-001", RequestedBy: "operator"}
	blocked, err := service.Configure(t.Context(), request)
	if err != nil || blocked.Status != StandardCodeResultBlocked || blocked.SelectedBackend != "" ||
		!blocked.SBXReadiness.Available || blocked.LocalReadiness.Available {
		t.Fatalf("auto must remain Local-only: %+v err=%v", blocked, err)
	}
	request.BackendIntent = "sbx"
	preview, err := service.Configure(t.Context(), request)
	if err != nil || !preview.TrustRequired || preview.TrustDigest == "" {
		t.Fatalf("sbx trust preview: %+v err=%v", preview, err)
	}
	request.ConfirmWorkspaceTrust, request.ExpectedTrustDigest = true, preview.TrustDigest
	configured, err := service.Configure(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if configured.Status != StandardCodeResultConfigured || configured.SelectedBackend != domain.StandardCodeSelectedSBX ||
		configured.SelectionReason != domain.StandardCodeReasonExplicitSBX ||
		configured.Profile == nil || configured.Profile.Profile != domain.RunExecutionProfileSBX ||
		configured.Interaction == nil || configured.Interaction.ExecutionProfile != domain.RunExecutionProfileSBX ||
		configured.Interaction.RequiredGate != domain.ExecutionInteractionGateSBXMicroVM ||
		configured.Permission == nil || configured.Permission.Mode != domain.RunExecutionPermissionAsk ||
		configured.CapabilityGrant || !configured.DrydockReady {
		t.Fatalf("incomplete SBX preset: %+v", configured)
	}
	replayed, err := service.Configure(t.Context(), request)
	if err != nil || !replayed.Replayed || replayed.Profile.ID != configured.Profile.ID {
		t.Fatalf("sbx recovery: %+v err=%v", replayed, err)
	}
	request.BackendIntent = "docker"
	if _, err := service.Configure(t.Context(), request); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("changed backend reused operation: %v", err)
	}
}

func TestSBXCapabilityReadinessDoesNotBorrowDockerOrLocalProof(t *testing.T) {
	runtime := CapabilityReadinessRuntime{
		SBXStartupGateEnabled: true, SBXAvailable: true, SBXBackendReady: true,
	}
	if err := runtime.Validate(); err == nil {
		t.Fatal("SBX readiness was accepted without Workspace Sandbox gate")
	}
	runtime.ExecutionPermissionCapabilities.WorkspaceSandboxEnabled = true
	if err := runtime.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, adapter := range []commandruntimeadapter.Identity{
		commandruntimeadapter.SandboxedWorkspace("docker_sandboxes", "sbx-fixture-policy", "one"),
		commandruntimeadapter.SandboxedWorkspace("docker_standard_code", "docker-fixture-policy", "one"),
	} {
		profile := commandRuntimeExecutionProfile(adapter)
		if (adapter.Backend == "docker_sandboxes") != (profile == domain.RunExecutionProfileSBX) {
			t.Fatalf("backend alias: %+v profile=%s", adapter, profile)
		}
	}
}
