package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/browserruntime"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/toolgateway"
)

type fakeFullCDPBrowserActionStore struct {
	*fakeFullCDPProductionStore
	workspace session.WorkspaceInfo
}

func (s *fakeFullCDPBrowserActionStore) GetWorkspaceInfo(context.Context, string) (
	session.WorkspaceInfo, error,
) {
	return s.workspace, nil
}

type fakeFullCDPBrowserActionRuntime struct {
	*fakeManagedFullCDPRuntime
	mu                sync.Mutex
	screenshots       int
	png               []byte
	blockScreenshot   bool
	screenshotEntered chan struct{}
	enteredOnce       sync.Once
	cancelCalls       int
}

func (f *fakeFullCDPBrowserActionRuntime) CancelBrowserAction() {
	f.mu.Lock()
	f.cancelCalls++
	f.mu.Unlock()
}

func (f *fakeFullCDPBrowserActionRuntime) BrowserNavigate(_ context.Context, value string) (
	browserruntime.RestrictedNavigationResult, error,
) {
	return browserruntime.RestrictedNavigationResult{CanonicalURL: value,
		ScopeValidated: true, RedirectsValidated: true}, nil
}

func (f *fakeFullCDPBrowserActionRuntime) BrowserSnapshot(context.Context) (
	browserruntime.FullCDPPageSnapshot, error,
) {
	return browserruntime.FullCDPPageSnapshot{
		ProtocolVersion: browserruntime.FullCDPPageSnapshotProtocolVersion,
		CanonicalURL:    "http://127.0.0.1:18080/", Text: "bounded page",
		Elements: []browserruntime.FullCDPPageElement{}, UntrustedEvidence: true,
		CompletedAt: time.Now().UTC(), Fingerprint: strings.Repeat("1", 64)}, nil
}

func (f *fakeFullCDPBrowserActionRuntime) BrowserClick(_ context.Context, selector string) (
	browserruntime.FullCDPInteractionResult, error,
) {
	return browserruntime.FullCDPInteractionResult{
		ProtocolVersion: browserruntime.FullCDPInteractionProtocolVersion,
		CanonicalURL:    "http://127.0.0.1:18080/", Action: "click",
		Selector: selector, CompletedAt: time.Now().UTC(),
		Fingerprint: strings.Repeat("2", 64)}, nil
}

func (f *fakeFullCDPBrowserActionRuntime) BrowserType(_ context.Context, selector, _ string) (
	browserruntime.FullCDPInteractionResult, error,
) {
	return browserruntime.FullCDPInteractionResult{
		ProtocolVersion: browserruntime.FullCDPInteractionProtocolVersion,
		CanonicalURL:    "http://127.0.0.1:18080/", Action: "type",
		Selector: selector, CompletedAt: time.Now().UTC(),
		Fingerprint: strings.Repeat("3", 64)}, nil
}

func (f *fakeFullCDPBrowserActionRuntime) BrowserScreenshot(ctx context.Context) (
	browserruntime.FullCDPScreenshotCapture, error,
) {
	f.mu.Lock()
	f.screenshots++
	blocked := f.blockScreenshot
	entered := f.screenshotEntered
	png := append([]byte(nil), f.png...)
	f.mu.Unlock()
	if blocked {
		if entered != nil {
			f.enteredOnce.Do(func() { close(entered) })
		}
		<-ctx.Done()
		return browserruntime.FullCDPScreenshotCapture{}, ctx.Err()
	}
	digest := sha256.Sum256(png)
	return browserruntime.FullCDPScreenshotCapture{Metadata: browserruntime.FullCDPScreenshotMetadata{
		ProtocolVersion: browserruntime.FullCDPScreenshotProtocolVersion,
		CanonicalURL:    "http://127.0.0.1:18080/", MediaType: "image/png",
		Bytes: len(png), SHA256: hex.EncodeToString(digest[:]),
		UntrustedEvidence: true, CompletedAt: time.Now().UTC(),
		Fingerprint: strings.Repeat("4", 64)}, PNG: png}, nil
}

func browserActionTestScope(binding fullCDPBrowserActionBinding,
	operationKey string,
) toolgateway.BrowserActionExecutionScope {
	context := toolgateway.BrowserActionCapabilityContext{RunID: binding.view.RunID,
		MissionID: binding.missionID, SessionID: binding.runSessionID,
		RootAgentID: "agent-browser-root", WorkspaceID: binding.workspaceID,
		Surface: domain.ExecutionSurfaceCode, Phase: domain.ExecutionPhaseDeliver,
		Role: domain.AgentRoleRoot, Profile: domain.ProfileCode,
		PermissionMode: binding.executionPermission.Mode, ModeRevision: 1,
		PermissionSnapshotID:        binding.executionPermission.ID,
		PermissionRevision:          binding.executionPermission.Revision,
		PermissionActivation:        binding.executionActivation,
		RunAuthorizationFence:       binding.executionFence,
		FullCDPSessionID:            binding.view.SessionID,
		BrowserPermissionSnapshotID: binding.browserPermissionID,
		BrowserPermissionRevision:   binding.browserPermissionRevision,
		TargetOrigin:                binding.view.TargetOrigin, Ready: true, RuntimeAvailable: true}
	return toolgateway.BrowserActionExecutionScope{InvocationID: "browser-invocation-1",
		OperationKey: operationKey, RunID: context.RunID, MissionID: context.MissionID,
		SessionID: context.SessionID, WorkspaceID: context.WorkspaceID,
		RootAgentID: context.RootAgentID, Surface: context.Surface, Phase: context.Phase,
		Role: context.Role, Profile: context.Profile, PermissionMode: context.PermissionMode,
		ModeRevision: context.ModeRevision, PermissionSnapshotID: context.PermissionSnapshotID,
		PermissionRevision:          context.PermissionRevision,
		PermissionActivation:        context.PermissionActivation,
		RunAuthorizationFence:       context.RunAuthorizationFence,
		FullCDPSessionID:            context.FullCDPSessionID,
		BrowserPermissionSnapshotID: context.BrowserPermissionSnapshotID,
		BrowserPermissionRevision:   context.BrowserPermissionRevision,
		CapabilityGeneration:        toolgateway.BrowserActionCapabilitySnapshot(context).Generation,
		LeaseID:                     "lease-browser-action-1", LeaseGeneration: 1,
		RequestedBy: "run_supervisor", PolicyDecision: toolgateway.Decision{Allowed: true,
			Approval: toolgateway.ApprovalAutomatic, Risk: "high", Reason: "test policy"}}
}

func TestFullCDPBrowserActionExecutorPersistsScreenshotAndRechecksAuthority(t *testing.T) {
	service, baseStore, _, latest := newFullCDPProductionServiceFixture(t)
	workspaceRoot := t.TempDir()
	store := &fakeFullCDPBrowserActionStore{fakeFullCDPProductionStore: baseStore,
		workspace: session.WorkspaceInfo{ID: baseStore.mission.WorkspaceID,
			Name: "browser action test", RootPath: workspaceRoot}}
	service.store = store
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := service.Close(ctx); err != nil {
			t.Errorf("close Full CDP service: %v", err)
		}
	}()

	opened, err := service.OpenFullCDPSession(t.Context(),
		fullCDPOpenFixture(service, baseStore, "open-browser-action-test"))
	if err != nil || opened.Session.State != FullCDPSessionReady {
		t.Fatalf("opened=%#v err=%v", opened, err)
	}
	png := []byte("\x89PNG\r\n\x1a\ncontrolled-browser-artifact")
	actionRuntime := &fakeFullCDPBrowserActionRuntime{
		fakeManagedFullCDPRuntime: *latest, png: png}
	service.mu.Lock()
	service.latestByRun[baseStore.run.ID].runtime = actionRuntime
	service.mu.Unlock()

	binding, available, err := service.browserActionBinding(t.Context(), baseStore.run.ID)
	if err != nil || !available {
		t.Fatalf("binding available=%t err=%v", available, err)
	}
	scope := browserActionTestScope(binding, "browser-screenshot-operation")
	result, err := service.ExecuteBrowserAction(t.Context(), scope,
		toolgateway.BrowserScreenshotTool,
		json.RawMessage(`{"version":"browser_screenshot.v1"}`))
	if err != nil || strings.Contains(result.Content, "iVBOR") ||
		result.Metadata["artifact_locator"] == "" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	var receipt struct {
		Artifact string `json:"artifact_locator"`
		SHA256   string `json:"sha256"`
		Bytes    int    `json:"bytes"`
	}
	if err := json.Unmarshal([]byte(result.Content), &receipt); err != nil {
		t.Fatal(err)
	}
	relative := strings.TrimPrefix(receipt.Artifact, "workspace:///")
	content, err := os.ReadFile(filepath.Join(workspaceRoot, filepath.FromSlash(relative)))
	digest := sha256.Sum256(png)
	if err != nil || !bytesEqual(content, png) || receipt.Bytes != len(png) ||
		receipt.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("receipt=%#v content=%x err=%v", receipt, content, err)
	}

	replayScope := scope
	replayScope.InvocationID = "browser-invocation-replay"
	if _, err := service.ExecuteBrowserAction(t.Context(), replayScope,
		toolgateway.BrowserScreenshotTool,
		json.RawMessage(`{"version":"browser_screenshot.v1"}`)); err != nil {
		t.Fatalf("exact artifact replay failed: %v", err)
	}
	actionRuntime.mu.Lock()
	screenshotCalls := actionRuntime.screenshots
	actionRuntime.mu.Unlock()
	if screenshotCalls != 2 {
		t.Fatalf("screenshot calls=%d", screenshotCalls)
	}

	stale := scope
	stale.InvocationID = "browser-invocation-stale"
	stale.OperationKey = "browser-stale-operation"
	stale.CapabilityGeneration = strings.Repeat("a", 64)
	if _, err := service.ExecuteBrowserAction(t.Context(), stale,
		toolgateway.BrowserStatusTool,
		json.RawMessage(`{"version":"browser_status.v1"}`)); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("stale capability code=%s err=%v", apperror.CodeOf(err), err)
	}

	baseStore.mu.Lock()
	baseStore.browserPermission.Revision++
	baseStore.mu.Unlock()
	stale = scope
	stale.InvocationID = "browser-invocation-revoked"
	stale.OperationKey = "browser-revoked-operation"
	if _, err := service.ExecuteBrowserAction(t.Context(), stale,
		toolgateway.BrowserStatusTool,
		json.RawMessage(`{"version":"browser_status.v1"}`)); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("revoked permission code=%s err=%v", apperror.CodeOf(err), err)
	}
}

func TestFullCDPBrowserActionCancelsInFlightOnDurablePermissionChange(t *testing.T) {
	service, baseStore, _, latest := newFullCDPProductionServiceFixture(t)
	workspaceRoot := t.TempDir()
	service.store = &fakeFullCDPBrowserActionStore{
		fakeFullCDPProductionStore: baseStore,
		workspace: session.WorkspaceInfo{ID: baseStore.mission.WorkspaceID,
			Name: "browser cancellation test", RootPath: workspaceRoot}}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := service.Close(ctx); err != nil {
			t.Errorf("close Full CDP service: %v", err)
		}
	}()
	opened, err := service.OpenFullCDPSession(t.Context(),
		fullCDPOpenFixture(service, baseStore, "open-browser-cancel-test"))
	if err != nil || opened.Session.State != FullCDPSessionReady {
		t.Fatalf("opened=%#v err=%v", opened, err)
	}
	entered := make(chan struct{})
	actionRuntime := &fakeFullCDPBrowserActionRuntime{
		fakeManagedFullCDPRuntime: *latest, png: []byte("unused"),
		blockScreenshot: true, screenshotEntered: entered}
	service.mu.Lock()
	service.latestByRun[baseStore.run.ID].runtime = actionRuntime
	service.mu.Unlock()
	binding, available, err := service.browserActionBinding(t.Context(), baseStore.run.ID)
	if err != nil || !available {
		t.Fatalf("binding available=%t err=%v", available, err)
	}
	scope := browserActionTestScope(binding, "browser-cancel-operation")
	result := make(chan error, 1)
	startedAt := time.Now()
	go func() {
		_, executeErr := service.ExecuteBrowserAction(context.Background(), scope,
			toolgateway.BrowserScreenshotTool,
			json.RawMessage(`{"version":"browser_screenshot.v1"}`))
		result <- executeErr
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("browser action did not enter the blocked runtime")
	}
	baseStore.mu.Lock()
	baseStore.browserPermission.Revision++
	baseStore.mu.Unlock()
	select {
	case executeErr := <-result:
		if apperror.CodeOf(executeErr) != apperror.CodeFailedPrecondition {
			t.Fatalf("canceled action code=%s err=%v", apperror.CodeOf(executeErr), executeErr)
		}
		if elapsed := time.Since(startedAt); elapsed > 500*time.Millisecond {
			t.Fatalf("durable permission cancellation took %s", elapsed)
		}
	case <-time.After(time.Second):
		t.Fatal("durable permission change did not cancel the in-flight action")
	}
	actionRuntime.mu.Lock()
	cancelCalls := actionRuntime.cancelCalls
	actionRuntime.mu.Unlock()
	if cancelCalls == 0 {
		t.Fatal("permission monitor did not interrupt the CDP runtime")
	}
}
