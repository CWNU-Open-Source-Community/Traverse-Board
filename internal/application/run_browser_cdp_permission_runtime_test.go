package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
)

type browserCDPRuntimeFenceStore struct {
	run            domain.Run
	execution      domain.RunExecutionPermissionSnapshot
	browser        domain.RunBrowserCDPPermissionSnapshot
	transitionHook func() error
}

func (store *browserCDPRuntimeFenceStore) GetRun(context.Context, string) (
	domain.Run, error,
) {
	return store.run, nil
}

func (store *browserCDPRuntimeFenceStore) GetRunExecutionPermission(
	context.Context, string,
) (domain.RunExecutionPermissionSnapshot, error) {
	return store.execution, nil
}

func (store *browserCDPRuntimeFenceStore) GetRunBrowserCDPPermission(
	context.Context, string,
) (domain.RunBrowserCDPPermissionSnapshot, error) {
	return store.browser, nil
}

func (store *browserCDPRuntimeFenceStore) GetRunBrowserCDPPermissionSnapshot(
	_ context.Context, id string,
) (domain.RunBrowserCDPPermissionSnapshot, error) {
	if store.browser.ID != id {
		return domain.RunBrowserCDPPermissionSnapshot{}, errors.New(
			"browser CDP permission snapshot not found")
	}
	return store.browser, nil
}

func (*browserCDPRuntimeFenceStore) GetRunBrowserCDPPermissionOperation(
	context.Context, string,
) (domain.RunBrowserCDPPermissionOperation, bool, error) {
	return domain.RunBrowserCDPPermissionOperation{}, false, nil
}

func (store *browserCDPRuntimeFenceStore) TransitionRunBrowserCDPPermission(
	_ context.Context, snapshot domain.RunBrowserCDPPermissionSnapshot,
	_ domain.RunBrowserCDPPermissionOperation, _ events.Event,
	_ domain.RunExecutionPermissionSnapshot,
) (domain.RunBrowserCDPPermissionSnapshot, bool, error) {
	if store.transitionHook != nil {
		if err := store.transitionHook(); err != nil {
			return domain.RunBrowserCDPPermissionSnapshot{}, false, err
		}
	}
	store.browser = snapshot
	return snapshot, false, nil
}

func browserCDPRuntimeFenceFixture(t *testing.T) (*browserCDPRuntimeFenceStore,
	*domain.ExecutionPermissionRuntimeAuthority,
	domain.ExecutionPermissionRuntimeCapabilities,
) {
	t.Helper()
	now := time.Now().UTC()
	mission := domain.Mission{ID: "mission-browser-cdp-fence", CreatedAt: now}
	run := domain.Run{ID: "run-browser-cdp-fence", MissionID: mission.ID,
		Status: domain.RunCreated, CreatedAt: now, UpdatedAt: now}
	initialExecution, err := domain.NewInitialRunExecutionPermissionSnapshot(
		"execution-browser-cdp-initial", run, mission, "test_operator", now)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := initialExecution.Next("execution-browser-cdp-full",
		domain.RunExecutionPermissionFullAccess, true, "test_operator",
		"confirm full access", now)
	if err != nil {
		t.Fatal(err)
	}
	initialBrowser, err := domain.NewInitialRunBrowserCDPPermissionSnapshot(
		"browser-cdp-initial", run, mission, "test_operator", now)
	if err != nil {
		t.Fatal(err)
	}
	browser, err := initialBrowser.Next("browser-cdp-full",
		domain.RunBrowserCDPPermissionFullDebug, true, "test_operator",
		"confirm full CDP", now)
	if err != nil {
		t.Fatal(err)
	}
	authority := domain.NewExecutionPermissionRuntimeAuthority()
	if _, err := authority.ActivateRunFullAccess(execution); err != nil {
		t.Fatal(err)
	}
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		FullAccessRequiresRuntimeGrant: true, RuntimeAuthority: authority,
	}
	return &browserCDPRuntimeFenceStore{run: run, execution: execution,
		browser: browser}, authority, capabilities
}

func TestBrowserCDPPermissionChangeRotatesRuntimeFenceBeforeAndAfterCommit(
	t *testing.T,
) {
	store, authority, executionCapabilities := browserCDPRuntimeFenceFixture(t)
	oldFence, err := authority.IssueRunAuthorizationFence(store.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var duringCommitFence uint64
	store.transitionHook = func() error {
		if authority.AllowsRunAuthorizationFence(store.run.ID, oldFence) {
			t.Fatal("durable browser permission transition began before old child fence was revoked")
		}
		var issueErr error
		duringCommitFence, issueErr = authority.IssueRunAuthorizationFence(store.run.ID)
		return issueErr
	}
	service := NewRunBrowserCDPPermissionServiceWithExecutionCapabilities(store,
		domain.BrowserCDPPermissionRuntimeCapabilities{
			ControlEnabled: true, FullDebugEnabled: true,
		}, executionCapabilities)
	result, err := service.Change(t.Context(), ChangeRunBrowserCDPPermissionRequest{
		RunID: store.run.ID, Mode: string(domain.RunBrowserCDPPermissionRestricted),
		OperationKey: "browser-cdp-fence-success", RequestedBy: "test_operator",
		Reason: "disable full CDP immediately",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Permission.Mode != domain.RunBrowserCDPPermissionRestricted ||
		duringCommitFence == 0 ||
		authority.AllowsRunAuthorizationFence(store.run.ID, oldFence) ||
		authority.AllowsRunAuthorizationFence(store.run.ID, duringCommitFence) {
		t.Fatalf("permission commit left a pre-commit child fence live: result=%+v old=%d during=%d",
			result, oldFence, duringCommitFence)
	}
	if !executionCapabilities.AllowsSnapshot(store.execution) {
		t.Fatal("browser child-permission rotation revoked the parent Full Access grant")
	}
}

func TestBrowserCDPPermissionFailedChangeStillInvalidatesExistingRuntimeFence(
	t *testing.T,
) {
	store, authority, executionCapabilities := browserCDPRuntimeFenceFixture(t)
	oldFence, err := authority.IssueRunAuthorizationFence(store.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	store.transitionHook = func() error { return errors.New("injected transition failure") }
	service := NewRunBrowserCDPPermissionServiceWithExecutionCapabilities(store,
		domain.BrowserCDPPermissionRuntimeCapabilities{
			ControlEnabled: true, FullDebugEnabled: true,
		}, executionCapabilities)
	if _, err := service.Change(t.Context(), ChangeRunBrowserCDPPermissionRequest{
		RunID: store.run.ID, Mode: string(domain.RunBrowserCDPPermissionRestricted),
		OperationKey: "browser-cdp-fence-failure", RequestedBy: "test_operator",
		Reason: "fail closed while disabling full CDP",
	}); err == nil {
		t.Fatal("injected browser permission transition unexpectedly succeeded")
	}
	if authority.AllowsRunAuthorizationFence(store.run.ID, oldFence) {
		t.Fatal("failed durable transition left the existing Full CDP child fence live")
	}
	if !executionCapabilities.AllowsSnapshot(store.execution) {
		t.Fatal("failed browser child-permission transition revoked parent Full Access")
	}
}
