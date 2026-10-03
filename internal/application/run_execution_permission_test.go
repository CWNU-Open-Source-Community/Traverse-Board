package application

import (
	"context"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
)

type runtimeFenceRunPermissionStore struct {
	run        domain.Run
	permission domain.RunExecutionPermissionSnapshot
	operations map[string]domain.RunExecutionPermissionOperation
	snapshots  map[string]domain.RunExecutionPermissionSnapshot
}

func (s *runtimeFenceRunPermissionStore) GetRun(context.Context, string) (domain.Run, error) {
	return s.run, nil
}

func (s *runtimeFenceRunPermissionStore) GetRunExecutionPermission(
	context.Context, string,
) (domain.RunExecutionPermissionSnapshot, error) {
	return s.permission, nil
}

func (s *runtimeFenceRunPermissionStore) GetRunExecutionPermissionSnapshot(
	_ context.Context, id string,
) (domain.RunExecutionPermissionSnapshot, error) {
	if value, ok := s.snapshots[id]; ok {
		return value, nil
	}
	return s.permission, nil
}

func (s *runtimeFenceRunPermissionStore) GetRunExecutionPermissionOperation(
	_ context.Context, digest string,
) (domain.RunExecutionPermissionOperation, bool, error) {
	value, ok := s.operations[digest]
	if ok {
		return value, true, nil
	}
	return domain.RunExecutionPermissionOperation{}, false, nil
}

func (s *runtimeFenceRunPermissionStore) TransitionRunExecutionPermission(
	_ context.Context, snapshot domain.RunExecutionPermissionSnapshot,
	operation domain.RunExecutionPermissionOperation, _ events.Event,
) (domain.RunExecutionPermissionSnapshot, bool, error) {
	if existing, ok := s.operations[operation.KeyDigest]; ok {
		return s.snapshots[existing.SnapshotID], true, nil
	}
	s.permission = snapshot
	if s.operations == nil {
		s.operations = make(map[string]domain.RunExecutionPermissionOperation)
	}
	if s.snapshots == nil {
		s.snapshots = make(map[string]domain.RunExecutionPermissionSnapshot)
	}
	s.operations[operation.KeyDigest] = operation
	s.snapshots[snapshot.ID] = snapshot
	return snapshot, false, nil
}

func TestRunExecutionPermissionRequiresExactCurrentConfirmation(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{
		domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull,
	} {
		t.Run(string(mode), func(t *testing.T) {
			base := ChangeRunExecutionPermissionRequest{RunID: "run-current-permission",
				Mode: string(mode), OperationKey: "current-permission-confirmation-0001",
				RequestedBy: "operator", Reason: "select the current approval preference",
				ConfirmFull: mode == domain.RunExecutionPermissionFull}
			normalized, actual, confirmed, err := normalizeChangeRunExecutionPermissionRequest(base)
			if err != nil || actual != mode || confirmed != base.ConfirmFull || normalized.Mode != string(mode) {
				t.Fatalf("normalized=%+v mode=%s confirmed=%t err=%v", normalized, actual, confirmed, err)
			}
			invalid := base
			invalid.ConfirmFull = !base.ConfirmFull
			if _, _, _, err := normalizeChangeRunExecutionPermissionRequest(invalid); err == nil {
				t.Fatal("mode accepted an inexact Full confirmation")
			}
			for _, legacyFlag := range []func(*ChangeRunExecutionPermissionRequest){
				func(r *ChangeRunExecutionPermissionRequest) { r.ConfirmWorkspaceAccess = true },
				func(r *ChangeRunExecutionPermissionRequest) { r.ConfirmUserApproval = true },
				func(r *ChangeRunExecutionPermissionRequest) { r.ConfirmDangerFullAccess = true },
				func(r *ChangeRunExecutionPermissionRequest) { r.ConfirmDebugAccess = true },
			} {
				invalid = base
				legacyFlag(&invalid)
				if _, _, _, err := normalizeChangeRunExecutionPermissionRequest(invalid); err == nil {
					t.Fatal("current mode accepted a retired confirmation flag")
				}
			}
		})
	}
}

func TestExecutionPermissionRejectsNonOperatorAuthoritySources(t *testing.T) {
	for _, requester := range []string{"model", "agent", "skill", "repository",
		"project_config", "recovery_data", "mcp", "plugin", "hook"} {
		request := ChangeRunExecutionPermissionRequest{RunID: "run-authority-source",
			Mode:         string(domain.RunExecutionPermissionAuto),
			OperationKey: "permission-source-" + requester + "-0001",
			RequestedBy:  requester, Reason: "attempt unauthorized selection"}
		if _, _, _, err := normalizeChangeRunExecutionPermissionRequest(request); err == nil ||
			!strings.Contains(err.Error(), "cannot select execution permission modes") {
			t.Fatalf("requester %q authority rejection=%v", requester, err)
		}
	}
}

func TestRunExecutionPermissionAutoToFullRotatesChildAuthorityFence(t *testing.T) {
	now := time.Now().UTC().Round(time.Millisecond)
	run := domain.Run{ID: "run-auto-to-full", MissionID: "mission-auto-to-full",
		Status: domain.RunCreated, CreatedAt: now, UpdatedAt: now}
	mission := domain.Mission{ID: run.MissionID, CreatedAt: now}
	initial, err := domain.NewInitialRunExecutionPermissionSnapshot(
		"run-permission-initial", run, mission, "operator", now)
	if err != nil {
		t.Fatal(err)
	}
	auto, err := initial.Next("run-permission-auto",
		domain.RunExecutionPermissionAuto, false, "operator",
		"select the Auto preference", now)
	if err != nil {
		t.Fatal(err)
	}
	state := &runtimeFenceRunPermissionStore{run: run, permission: auto}
	authority := domain.NewExecutionPermissionRuntimeAuthority()
	oldFence, err := authority.IssueRunAuthorizationFence(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		FullAccessRequiresRuntimeGrant: true,
		RuntimeAuthority:               authority,
	}
	service := NewRunExecutionPermissionService(state, capabilities)
	result, err := service.Change(t.Context(), ChangeRunExecutionPermissionRequest{
		RunID: run.ID, Mode: string(domain.RunExecutionPermissionFull),
		OperationKey: "auto-to-full-fence-operation-0001",
		RequestedBy:  "operator", Reason: "switch current task to full access",
		ConfirmFull: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if authority.AllowsRunAuthorizationFence(run.ID, oldFence) {
		t.Fatal("Auto child authorization fence survived the Full revision")
	}
	if !capabilities.AllowsSnapshot(result.Permission) {
		t.Fatal("new Full revision was not rebound to dynamic runtime authority")
	}
}

func TestRunExecutionPermissionExactAutoReplayPreservesAuthorizationFence(t *testing.T) {
	now := time.Now().UTC().Round(time.Millisecond)
	run := domain.Run{ID: "run-auto-replay", MissionID: "mission-auto-replay",
		Status: domain.RunCreated, CreatedAt: now, UpdatedAt: now}
	mission := domain.Mission{ID: run.MissionID, CreatedAt: now}
	initial, err := domain.NewInitialRunExecutionPermissionSnapshot(
		"run-auto-replay-initial", run, mission, "operator", now)
	if err != nil {
		t.Fatal(err)
	}
	state := &runtimeFenceRunPermissionStore{run: run, permission: initial}
	authority := domain.NewExecutionPermissionRuntimeAuthority()
	service := NewRunExecutionPermissionService(state,
		domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
			FullAccessRequiresRuntimeGrant: true,
			RuntimeAuthority:               authority,
		})
	request := ChangeRunExecutionPermissionRequest{RunID: run.ID,
		Mode:         string(domain.RunExecutionPermissionAuto),
		OperationKey: "run-auto-replay-operation-0001", RequestedBy: "operator",
		Reason: "select Auto for this Run"}
	first, err := service.Change(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := authority.IssueRunAuthorizationFence(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.Change(t.Context(), request)
	if err != nil || !replayed.Replayed || replayed.Permission.ID != first.Permission.ID {
		t.Fatalf("exact Auto replay=%+v err=%v", replayed, err)
	}
	if !authority.AllowsRunAuthorizationFence(run.ID, fence) {
		t.Fatal("exact Auto replay rotated its live authorization fence")
	}
}

func TestRunExecutionPermissionSameModeFullReconfirmsWithoutReplayRevocation(t *testing.T) {
	now := time.Now().UTC().Round(time.Millisecond)
	run := domain.Run{ID: "run-full-reconfirm", MissionID: "mission-full-reconfirm",
		Status: domain.RunCreated, CreatedAt: now, UpdatedAt: now}
	mission := domain.Mission{ID: run.MissionID, CreatedAt: now}
	initial, err := domain.NewInitialRunExecutionPermissionSnapshot(
		"run-permission-full-initial", run, mission, "operator", now)
	if err != nil {
		t.Fatal(err)
	}
	durableFull, err := initial.Next("run-permission-cold-full",
		domain.RunExecutionPermissionFull, true, "operator",
		"durable Full preference without a live activation", now)
	if err != nil {
		t.Fatal(err)
	}
	state := &runtimeFenceRunPermissionStore{
		run: run, permission: durableFull,
		operations: make(map[string]domain.RunExecutionPermissionOperation),
		snapshots: map[string]domain.RunExecutionPermissionSnapshot{
			durableFull.ID: durableFull,
		},
	}
	authority := domain.NewExecutionPermissionRuntimeAuthority()
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		FullAccessRequiresRuntimeGrant: true, RuntimeAuthority: authority,
	}
	service := NewRunExecutionPermissionService(state, capabilities)
	request := ChangeRunExecutionPermissionRequest{
		RunID: run.ID, Mode: string(domain.RunExecutionPermissionFull),
		OperationKey: "run-full-reconfirm-cold-0001", RequestedBy: "operator",
		Reason:      "explicitly reactivate Full Access for this Run",
		ConfirmFull: true,
	}
	first, err := service.Change(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Permission.ID == durableFull.ID ||
		first.Permission.Revision != durableFull.Revision+1 ||
		!capabilities.AllowsSnapshot(first.Permission) ||
		capabilities.AllowsSnapshot(durableFull) {
		t.Fatalf("cold Full reconfirmation did not mint one exact live revision: old=%+v new=%+v",
			durableFull, first.Permission)
	}
	firstGeneration, ok := authority.AllowsFullAccess(first.Permission)
	if !ok {
		t.Fatal("first Full reconfirmation did not expose a live generation")
	}

	request.OperationKey = "run-full-reconfirm-live-0002"
	request.Reason = "rotate the current Full Access confirmation"
	second, err := service.Change(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	secondGeneration, ok := authority.AllowsFullAccess(second.Permission)
	if !ok || secondGeneration == firstGeneration ||
		second.Permission.Revision != first.Permission.Revision+1 ||
		capabilities.AllowsSnapshot(first.Permission) {
		t.Fatalf("live Full reconfirmation did not rotate exact authority: first=%+v second=%+v generations=%d/%d",
			first.Permission, second.Permission, firstGeneration, secondGeneration)
	}

	replayed, err := service.Change(t.Context(), request)
	if err != nil || !replayed.Replayed || replayed.Permission.ID != second.Permission.ID {
		t.Fatalf("exact Full replay was not idempotent: %+v err=%v", replayed, err)
	}
	replayedGeneration, ok := authority.AllowsFullAccess(second.Permission)
	if !ok || replayedGeneration != secondGeneration {
		t.Fatalf("exact replay rotated or revoked live authority: before=%d after=%d ok=%t",
			secondGeneration, replayedGeneration, ok)
	}

	conflict := request
	conflict.Reason = "different intent with the same operation key"
	if _, err := service.Change(t.Context(), conflict); err == nil {
		t.Fatal("changed intent reused a Full reconfirmation operation key")
	}
	if generation, ok := authority.AllowsFullAccess(second.Permission); !ok || generation != secondGeneration {
		t.Fatalf("invalid replay revoked current Full authority: generation=%d ok=%t",
			generation, ok)
	}
	invalid := request
	invalid.OperationKey = "run-full-reconfirm-invalid-0003"
	invalid.ConfirmFull = false
	if _, err := service.Change(t.Context(), invalid); err == nil {
		t.Fatal("Full reconfirmation without confirmation succeeded")
	}
	if generation, ok := authority.AllowsFullAccess(second.Permission); !ok || generation != secondGeneration {
		t.Fatalf("invalid request revoked current Full authority: generation=%d ok=%t",
			generation, ok)
	}
}
