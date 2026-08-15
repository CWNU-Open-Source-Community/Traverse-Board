package application

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"cyberagent-workbench/internal/browserruntime"
	"cyberagent-workbench/internal/domain"
)

type fakeBrowserRuntimeStore struct {
	run         domain.Run
	mission     domain.Mission
	permission  domain.RunBrowserCDPPermissionSnapshot
	evidenceErr error
}

func (f *fakeBrowserRuntimeStore) GetRun(context.Context, string) (domain.Run, error) {
	return f.run, nil
}

func (f *fakeBrowserRuntimeStore) GetMission(context.Context, string) (domain.Mission, error) {
	return f.mission, nil
}

func (f *fakeBrowserRuntimeStore) GetRunBrowserCDPPermission(context.Context,
	string,
) (domain.RunBrowserCDPPermissionSnapshot, error) {
	return f.permission, nil
}

func (f *fakeBrowserRuntimeStore) LoadLatestBrowserNetworkEvidence(context.Context,
	string,
) (browserruntime.BrowserNetworkContainmentEvidence, error) {
	if f.evidenceErr != nil {
		return browserruntime.BrowserNetworkContainmentEvidence{}, f.evidenceErr
	}
	return browserruntime.BrowserNetworkContainmentEvidence{}, sql.ErrNoRows
}

func (f *fakeBrowserRuntimeStore) LoadBrowserNetworkReview(context.Context,
	string,
) (browserruntime.BrowserNetworkContainmentReview, error) {
	return browserruntime.BrowserNetworkContainmentReview{}, sql.ErrNoRows
}

func (f *fakeBrowserRuntimeStore) PrepareBrowserLaunch(context.Context,
	browserruntime.SessionPlan, browserruntime.BrowserExecutableIdentity,
	browserruntime.BrowserAcceptanceCandidate, browserruntime.ProfileOwnershipPlan,
	string, string,
) (browserruntime.BrowserLaunchAttempt, browserruntime.BrowserLaunchLease, bool, error) {
	panic("PrepareBrowserLaunch must not be reached in fail-closed tests")
}

func (f *fakeBrowserRuntimeStore) RecordBrowserLaunchReview(context.Context,
	browserruntime.SessionPlan, browserruntime.BrowserExecutableIdentity,
	browserruntime.BrowserAcceptanceCandidate, browserruntime.ProfileOwnershipPlan,
	browserruntime.BrowserLaunchAttempt, browserruntime.BrowserLaunchLease,
	browserruntime.BrowserLaunchReviewDecision, string, string,
) (browserruntime.BrowserLaunchReview, bool, error) {
	panic("RecordBrowserLaunchReview must not be reached in fail-closed tests")
}

func (f *fakeBrowserRuntimeStore) RecordBrowserRuntimeCheckpoint(context.Context,
	browserruntime.BrowserRuntimeCheckpoint,
) error {
	panic("RecordBrowserRuntimeCheckpoint must not be reached in fail-closed tests")
}

func (f *fakeBrowserRuntimeStore) RecordBrowserRuntimeReceipt(context.Context,
	browserruntime.BrowserRuntimeReceipt,
) error {
	panic("RecordBrowserRuntimeReceipt must not be reached in fail-closed tests")
}

func TestBrowserRuntimeLaunchFailsClosedWithZeroIdentity(t *testing.T) {
	controller, err := browserruntime.NewPlatformBrowserProcessController()
	if err != nil {
		t.Fatal(err)
	}
	service := NewBrowserRuntimeService(&fakeBrowserRuntimeStore{}, controller,
		browserruntime.ProductionRuntimeCapabilities{
			SafeWebStartEnabled: true, DisposableProfileEnabled: true,
			NetworkContainmentEnabled: true,
		},
		domain.BrowserCDPPermissionRuntimeCapabilities{ControlEnabled: true})

	if _, err := service.Launch(t.Context(), BrowserRuntimeLaunchRequest{
		RunID: "run-1", Target: "http://127.0.0.1:8080",
	}); err == nil {
		t.Fatal("zero browser identity unexpectedly launched a runtime")
	}
}

func TestBrowserRuntimeLaunchRefusesWhenNotReady(t *testing.T) {
	now := time.Now().UTC()
	identity, acceptance, _, _ := safeWebRuntimeFixture(t)
	permission, err := domain.NewInitialRunBrowserCDPPermissionSnapshot(
		"browser-cdp-runtime", domain.Run{ID: "run-runtime", MissionID: "mission-runtime",
			SessionID: "session-runtime", Status: domain.RunCreated, CreatedAt: now},
		domain.Mission{ID: "mission-runtime", WorkspaceID: "workspace-runtime", CreatedAt: now},
		"runtime-operator", now)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeBrowserRuntimeStore{
		run: domain.Run{ID: "run-runtime", MissionID: "mission-runtime",
			SessionID: "session-runtime", Status: domain.RunCreated, CreatedAt: now},
		mission:    domain.Mission{ID: "mission-runtime", WorkspaceID: "workspace-runtime", CreatedAt: now},
		permission: permission,
	}
	controller, err := browserruntime.NewPlatformBrowserProcessController()
	if err != nil {
		t.Fatal(err)
	}
	service := NewBrowserRuntimeService(store, controller,
		browserruntime.ProductionRuntimeCapabilities{
			SafeWebStartEnabled: true, DisposableProfileEnabled: true,
			NetworkContainmentEnabled: true,
		},
		domain.BrowserCDPPermissionRuntimeCapabilities{ControlEnabled: true})
	_, err = service.Launch(t.Context(), BrowserRuntimeLaunchRequest{
		RunID: "run-runtime", Target: "http://127.0.0.1:8080",
		Identity: identity, Acceptance: acceptance, ProfileRoot: t.TempDir(),
		OperationKey: "launch-op-1", LeaseOwnerIdentity: "runtime-worker",
		ReviewerIdentity: "runtime-reviewer", ReviewOperationKey: "review-op-1",
	})
	if err == nil {
		t.Fatal("not-ready Safe Web unexpectedly launched a runtime")
	}
}
