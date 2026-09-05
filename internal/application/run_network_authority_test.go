package application_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/store"
)

func TestRunNetworkAuthorityExpansionIsExactAuditedAndFenced(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "run-network-authority.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := context.Background()
	runs := application.NewRunService(state)
	_, run, err := runs.Create(ctx, application.CreateRunRequest{
		Goal: "search only approved public hosts", Phase: "deliver",
		Budget: domain.Budget{MaxTurns: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	authority := domain.NewExecutionPermissionRuntimeAuthority()
	oldFence, err := authority.IssueRunAuthorizationFence(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewRunNetworkAuthorityService(state).WithRuntimeAuthority(authority)
	request := application.ExpandRunNetworkAuthorityRequest{
		Version: application.RunNetworkAuthorityControlProtocolVersion,
		RunID:   run.ID, ExpectedModeRevision: 1,
		AddAllowedTargets: []string{"https://SEARCH.Example.org/", "docs.example.org"},
		OperationKey:      "network-authority-expand-0001", RequestedBy: "operator",
		Reason: "allow the selected search service and documentation host",
	}
	result, err := service.Expand(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Replayed || result.Mode.Revision != 2 ||
		result.Mode.Scope.NetworkMode != "allowlist" ||
		strings.Join(result.Mode.Scope.AllowedTargets, ",") !=
			"docs.example.org,search.example.org" ||
		strings.Join(result.AddedTargets, ",") != "docs.example.org,search.example.org" ||
		result.Mode.Phase != domain.ExecutionPhaseDeliver {
		t.Fatalf("unexpected network authority expansion: %#v", result)
	}
	if authority.AllowsRunAuthorizationFence(run.ID, oldFence) {
		t.Fatal("network authority expansion left the old tool fence live")
	}
	newFence, err := authority.IssueRunAuthorizationFence(run.ID)
	if err != nil || newFence == oldFence {
		t.Fatalf("new fence=%d old=%d err=%v", newFence, oldFence, err)
	}
	replayed, err := service.Expand(ctx, request)
	if err != nil || !replayed.Replayed || replayed.Mode.ID != result.Mode.ID ||
		!authority.AllowsRunAuthorizationFence(run.ID, newFence) {
		t.Fatalf("replay=%#v err=%v fence_live=%t", replayed, err,
			authority.AllowsRunAuthorizationFence(run.ID, newFence))
	}
	conflict := request
	conflict.AddAllowedTargets = []string{"other.example.org"}
	if _, err := service.Expand(ctx, conflict); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("operation-key intent conflict error=%v", err)
	}

	phase, err := runs.ChangePhase(ctx, application.ChangeRunPhaseRequest{
		RunID: run.ID, Phase: "plan", OperationKey: "network-phase-change-0001",
		RequestedBy: "operator", Reason: "plan with the expanded scope",
	})
	if err != nil || phase.Mode.Revision != 3 ||
		strings.Join(phase.Mode.Scope.AllowedTargets, ",") !=
			"docs.example.org,search.example.org" {
		t.Fatalf("phase transition did not preserve expanded scope: %#v err=%v", phase, err)
	}
	items, err := runs.Events(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if countEventType(items, events.RunNetworkAuthorityExpandedEvent) != 1 {
		t.Fatalf("network authority audit event count is wrong: %#v", items)
	}
	for _, item := range items {
		if item.Type == events.RunNetworkAuthorityExpandedEvent &&
			(!strings.Contains(item.PayloadJSON, `"added_targets":["docs.example.org","search.example.org"]`) ||
				!strings.Contains(item.PayloadJSON, `"capability_grant":true`)) {
			t.Fatalf("network authority audit payload is incomplete: %s", item.PayloadJSON)
		}
	}
}

func TestRunNetworkAuthorityExpansionRejectsImplicitAndRunningAuthority(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "run-network-authority-reject.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := context.Background()
	runs := application.NewRunService(state)
	_, run, err := runs.Create(ctx, application.CreateRunRequest{
		Goal: "reject implicit network grants", Budget: domain.Budget{MaxTurns: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewRunNetworkAuthorityService(state).WithRuntimeAuthority(
		domain.NewExecutionPermissionRuntimeAuthority())
	base := application.ExpandRunNetworkAuthorityRequest{
		Version: application.RunNetworkAuthorityControlProtocolVersion,
		RunID:   run.ID, ExpectedModeRevision: 1,
		OperationKey: "network-authority-reject-0001", RequestedBy: "operator",
	}
	for index, targets := range [][]string{
		{"public_https"}, {"*.example.org"}, {"http://search.example.org"},
		{"https://8.8.8.8"}, {""},
	} {
		request := base
		request.OperationKey += string(rune('a' + index))
		request.AddAllowedTargets = targets
		if _, err := service.Expand(ctx, request); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
			t.Fatalf("targets=%v error=%v", targets, err)
		}
	}
	if _, err := runs.Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	base.AddAllowedTargets = []string{"search.example.org"}
	base.OperationKey = "network-authority-running-0001"
	if _, err := service.Expand(ctx, base); apperror.CodeOf(err) != apperror.CodeConflict ||
		!strings.Contains(err.Error(), "running") {
		t.Fatalf("running expansion error=%v", err)
	}
	if _, err := runs.Pause(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	base.OperationKey = "network-authority-paused-0001"
	paused, err := service.Expand(ctx, base)
	if err != nil || paused.Mode.Revision != 2 ||
		len(paused.Mode.Scope.AllowedTargets) != 1 ||
		paused.Mode.Scope.AllowedTargets[0] != "search.example.org" {
		t.Fatalf("paused expansion=%#v err=%v", paused, err)
	}
}

func TestThreadSuccessorInheritsOnlyExactNetworkPreference(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "thread-network-authority.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := context.Background()
	runs := application.NewRunService(state)
	_, predecessor, err := runs.Create(ctx, application.CreateRunRequest{
		Goal:   "continue searches on the same approved hosts",
		Budget: domain.Budget{MaxTurns: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunNetworkAuthorityService(state).WithRuntimeAuthority(
		domain.NewExecutionPermissionRuntimeAuthority()).Expand(ctx,
		application.ExpandRunNetworkAuthorityRequest{
			Version: application.RunNetworkAuthorityControlProtocolVersion,
			RunID:   predecessor.ID, ExpectedModeRevision: 1,
			AddAllowedTargets: []string{"search.example.org"},
			OperationKey:      "thread-network-authority-0001", RequestedBy: "operator",
		}); err != nil {
		t.Fatal(err)
	}
	if _, err := runs.Cancel(ctx, predecessor.ID); err != nil {
		t.Fatal(err)
	}
	threadRecord, err := state.GetThreadByRun(ctx, predecessor.ID)
	if err != nil {
		t.Fatal(err)
	}
	continued, err := application.NewThreadService(state).Submit(ctx,
		application.SubmitThreadMessageRequest{
			Version: domain.ThreadMessageProtocolVersion, ThreadID: threadRecord.ID,
			Content:      "continue with the same approved search host",
			OperationKey: "thread-network-successor-message-0001",
			RequestedBy:  "operator",
		})
	if err != nil || !continued.SuccessorCreated {
		t.Fatalf("successor=%#v err=%v", continued, err)
	}
	successorMode, err := state.GetRunMode(ctx, continued.Run.ID)
	if err != nil || successorMode.Revision != 1 ||
		successorMode.Scope.NetworkMode != "allowlist" ||
		len(successorMode.Scope.AllowedTargets) != 1 ||
		successorMode.Scope.AllowedTargets[0] != "search.example.org" {
		t.Fatalf("successor mode=%#v err=%v", successorMode, err)
	}
	permission, err := state.GetRunExecutionPermission(ctx, continued.Run.ID)
	if err != nil || permission.Mode != domain.RunExecutionPermissionConservative ||
		permission.ProcessEnabled || permission.ExecutionAuthorized || permission.CapabilityGrant {
		t.Fatalf("successor inherited runtime capability: %#v err=%v", permission, err)
	}
	if lease, found, err := state.GetRunExecutionLease(ctx, continued.Run.ID); err != nil || found {
		t.Fatalf("successor inherited execution lease: found=%v lease=%#v err=%v",
			found, lease, err)
	}
}
