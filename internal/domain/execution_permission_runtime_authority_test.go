package domain

import (
	"sync"
	"testing"
	"time"
)

func runtimeAuthorityFixtures(t *testing.T) (ThreadExecutionPermissionSnapshot,
	RunExecutionPermissionSnapshot,
) {
	t.Helper()
	now := time.Now().UTC()
	thread := Thread{ID: "thread-runtime-authority", MissionID: "mission-runtime-authority",
		Status: ThreadActive, CreatedAt: now, UpdatedAt: now, Version: 1}
	initialThread, err := NewInitialThreadExecutionPermissionSnapshot(
		"thread-permission-initial", thread, "test_operator", now)
	if err != nil {
		t.Fatal(err)
	}
	fullThread, err := initialThread.Next("thread-permission-full",
		RunExecutionPermissionFullAccess, true, "test_operator", "confirm full access", now)
	if err != nil {
		t.Fatal(err)
	}
	mission := Mission{ID: thread.MissionID, CreatedAt: now}
	run := Run{ID: "run-runtime-authority", MissionID: mission.ID,
		Status: RunCreated, CreatedAt: now, UpdatedAt: now}
	initialRun, err := NewInitialRunExecutionPermissionSnapshot(
		"run-permission-initial", run, mission, "test_operator", now)
	if err != nil {
		t.Fatal(err)
	}
	fullRun, err := initialRun.Next("run-permission-full",
		RunExecutionPermissionFullAccess, true, "test_operator", "confirm full access", now)
	if err != nil {
		t.Fatal(err)
	}
	return fullThread, fullRun
}

func TestExecutionPermissionRuntimeAuthorityStartsClosedAndFencesRevocation(t *testing.T) {
	thread, run := runtimeAuthorityFixtures(t)
	authority := NewExecutionPermissionRuntimeAuthority()
	capabilities := ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		FullAccessRequiresRuntimeGrant: true, RuntimeAuthority: authority,
	}
	if capabilities.AllowsSnapshot(run) {
		t.Fatal("durable Full Access snapshot reopened an empty process authority")
	}
	grant, err := authority.ActivateThreadFullAccess(thread, &run)
	if err != nil || grant.Generation == 0 || !capabilities.AllowsSnapshot(run) {
		t.Fatalf("Full Access activation failed: grant=%+v err=%v", grant, err)
	}
	fence, err := authority.IssueRunAuthorizationFence(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	authority.RevokeThread(thread.ThreadID)
	if capabilities.AllowsSnapshot(run) ||
		authority.AllowsFullAccessGeneration(run, grant.Generation) {
		t.Fatal("revoked Full Access generation remained usable")
	}
	if authority.AllowsRunAuthorizationFence(run.RunID, fence) {
		t.Fatal("Thread revocation left a child Run authorization fence live")
	}
}

func TestRunAuthorizationFenceIsSharedBySiblingCapabilitiesUntilRevoked(t *testing.T) {
	_, run := runtimeAuthorityFixtures(t)
	authority := NewExecutionPermissionRuntimeAuthority()
	fullCDPFence, err := authority.IssueRunAuthorizationFence(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	mcpFence, err := authority.IssueRunAuthorizationFence(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if fullCDPFence == 0 || mcpFence != fullCDPFence ||
		!authority.AllowsRunAuthorizationFence(run.RunID, fullCDPFence) ||
		!authority.AllowsRunAuthorizationFence(run.RunID, mcpFence) {
		t.Fatalf("sibling child capabilities did not share one live Run epoch: full_cdp=%d mcp=%d",
			fullCDPFence, mcpFence)
	}
	authority.RevokeRun(run.RunID)
	if authority.AllowsRunAuthorizationFence(run.RunID, fullCDPFence) {
		t.Fatal("Run revocation left the shared child capability epoch live")
	}
	fresh, err := authority.IssueRunAuthorizationFence(run.RunID)
	if err != nil || fresh == 0 || fresh == fullCDPFence ||
		!authority.AllowsRunAuthorizationFence(run.RunID, fresh) {
		t.Fatalf("Run revocation did not create a fresh child epoch: old=%d fresh=%d err=%v",
			fullCDPFence, fresh, err)
	}
}

func TestRotateRunAuthorizationFenceInvalidatesChildrenWithoutRevokingParent(
	t *testing.T,
) {
	_, run := runtimeAuthorityFixtures(t)
	authority := NewExecutionPermissionRuntimeAuthority()
	grant, err := authority.ActivateRunFullAccess(run)
	if err != nil {
		t.Fatal(err)
	}
	first, err := authority.IssueRunAuthorizationFence(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := authority.RotateRunAuthorizationFence(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if rotated == 0 || rotated == first ||
		authority.AllowsRunAuthorizationFence(run.RunID, first) ||
		!authority.AllowsRunAuthorizationFence(run.RunID, rotated) {
		t.Fatalf("Run child fence did not rotate: first=%d rotated=%d", first, rotated)
	}
	if !authority.AllowsFullAccessGeneration(run, grant.Generation) {
		t.Fatal("rotating a child fence revoked the parent Full Access grant")
	}
	sibling, err := authority.IssueRunAuthorizationFence(run.RunID)
	if err != nil || sibling != rotated ||
		!authority.AllowsRunAuthorizationFence(run.RunID, sibling) {
		t.Fatalf("sibling Issue replaced the rotated shared epoch: rotated=%d sibling=%d err=%v",
			rotated, sibling, err)
	}
}

func TestDebugProcessDoesNotReactivateHistoricalFullAccess(t *testing.T) {
	_, full := runtimeAuthorityFixtures(t)
	authority := NewExecutionPermissionRuntimeAuthority()
	capabilities := ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		DebugMaximumAccessEnabled: true, FullAccessRequiresRuntimeGrant: true,
		RuntimeAuthority: authority,
	}
	if !capabilities.Allows(RunExecutionPermissionFullAccess) {
		t.Fatal("Debug process did not install the Full Access runtime adapter")
	}
	if capabilities.AllowsSnapshot(full) {
		t.Fatal("Debug startup reactivated a historical Full Access snapshot")
	}
	debug, err := full.Next("run-permission-debug",
		RunExecutionPermissionDebug, true, "test_operator", "confirm debug", full.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !capabilities.AllowsSnapshot(debug) {
		t.Fatal("Debug startup gate did not authorize an exact Debug snapshot")
	}
}

func TestExecutionPermissionRuntimeAuthorityRebindsSuccessorAndIsConcurrentSafe(t *testing.T) {
	thread, run := runtimeAuthorityFixtures(t)
	authority := NewExecutionPermissionRuntimeAuthority()
	first, err := authority.ActivateThreadFullAccess(thread, &run)
	if err != nil {
		t.Fatal(err)
	}
	successor := run
	successor.RunID = "run-runtime-successor"
	successor.ID = "run-permission-successor"
	successor.Revision = 1
	bound, active, err := authority.BindThreadRun(thread.ThreadID, successor)
	if err != nil || !active || bound.Generation == first.Generation {
		t.Fatalf("successor binding failed: grant=%+v active=%t err=%v", bound, active, err)
	}
	if _, allowed := authority.AllowsFullAccess(run); allowed {
		t.Fatal("predecessor Run survived successor generation binding")
	}
	if _, allowed := authority.AllowsFullAccess(successor); !allowed {
		t.Fatal("successor Run did not inherit the live Thread activation")
	}
	fence, err := authority.IssueRunAuthorizationFence(successor.RunID)
	if err != nil || !authority.AllowsRunAuthorizationFence(successor.RunID, fence) {
		t.Fatalf("Run authorization fence was not issued: fence=%d err=%v", fence, err)
	}
	authority.RevokeRun(successor.RunID)
	if authority.AllowsRunAuthorizationFence(successor.RunID, fence) {
		t.Fatal("revoked Run authorization fence remained usable")
	}

	var wait sync.WaitGroup
	for i := 0; i < 32; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for attempt := 0; attempt < 100; attempt++ {
				authority.AllowsFullAccess(successor)
			}
		}()
	}
	wait.Wait()
}
