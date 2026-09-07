package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/browserruntime"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/toolgateway"
)

const (
	fullCDPBrowserArtifactDirectory     = ".cyberagent-workbench/browser-artifacts"
	fullCDPBrowserAuthorityPollInterval = 20 * time.Millisecond
)

type fullCDPBrowserActionRuntime interface {
	BrowserNavigate(context.Context, string) (browserruntime.RestrictedNavigationResult, error)
	BrowserSnapshot(context.Context) (browserruntime.FullCDPPageSnapshot, error)
	BrowserClick(context.Context, string) (browserruntime.FullCDPInteractionResult, error)
	BrowserType(context.Context, string, string) (browserruntime.FullCDPInteractionResult, error)
	BrowserScreenshot(context.Context) (browserruntime.FullCDPScreenshotCapture, error)
	CancelBrowserAction()
}

type fullCDPWorkspaceInfoStore interface {
	GetWorkspaceInfo(context.Context, string) (session.WorkspaceInfo, error)
}

type fullCDPBrowserActionBinding struct {
	view                      FullCDPSessionView
	runtime                   fullCDPBrowserActionRuntime
	missionID                 string
	runSessionID              string
	workspaceID               string
	executionPermission       domain.RunExecutionPermissionSnapshot
	executionActivation       uint64
	executionFence            uint64
	browserPermissionID       string
	browserPermissionRevision int64
}

// browserActionBinding proves the complete live authority immediately before
// advertisement and again before/after every action. It never opens a session:
// only the exact operator-opened Ready entry can produce a binding.
func (s *FullCDPProductionService) browserActionBinding(ctx context.Context,
	runID string,
) (fullCDPBrowserActionBinding, bool, error) {
	if s == nil || s.store == nil {
		return fullCDPBrowserActionBinding{}, false, apperror.New(
			apperror.CodeUnavailable, "full CDP browser action service is unavailable")
	}
	runID = strings.TrimSpace(runID)
	if !domain.ValidAgentID(runID) {
		return fullCDPBrowserActionBinding{}, false, apperror.New(
			apperror.CodeInvalidArgument, "full CDP browser action Run id is invalid")
	}
	run, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return fullCDPBrowserActionBinding{}, false, apperror.Normalize(err)
	}
	if !domain.CanChangeRunBrowserCDPPermission(run.Status) {
		return fullCDPBrowserActionBinding{}, false, nil
	}
	mission, err := s.store.GetMission(ctx, run.MissionID)
	if err != nil {
		return fullCDPBrowserActionBinding{}, false, apperror.Normalize(err)
	}
	browserPermission, err := s.store.GetRunBrowserCDPPermission(ctx, runID)
	if err != nil {
		return fullCDPBrowserActionBinding{}, false, apperror.Normalize(err)
	}
	executionPermission, err := s.store.GetRunExecutionPermission(ctx, runID)
	if err != nil {
		return fullCDPBrowserActionBinding{}, false, apperror.Normalize(err)
	}
	executionActivation, executionLive :=
		s.executionCapabilities.FullAccessGeneration(executionPermission)

	s.mu.Lock()
	entry := s.latestByRun[runID]
	if entry == nil || entry.view.State != FullCDPSessionReady || !entry.view.RuntimeAvailable ||
		entry.runtime == nil {
		s.mu.Unlock()
		return fullCDPBrowserActionBinding{}, false, nil
	}
	actionRuntime, runtimeOK := entry.runtime.(fullCDPBrowserActionRuntime)
	binding := fullCDPBrowserActionBinding{view: entry.view, runtime: actionRuntime,
		missionID: mission.ID, runSessionID: run.SessionID,
		workspaceID: mission.WorkspaceID, executionPermission: executionPermission,
		executionActivation: executionActivation, executionFence: entry.executionFence,
		browserPermissionID:       entry.browserPermissionID,
		browserPermissionRevision: entry.browserPermissionRevision}
	entryPermissionMatches := entry.executionPermissionID == executionPermission.ID &&
		entry.executionPermissionRevision == executionPermission.Revision &&
		entry.browserPermissionID == browserPermission.ID &&
		entry.browserPermissionRevision == browserPermission.Revision
	s.mu.Unlock()

	authorityLive := executionLive && s.executionCapabilities.RuntimeAuthority != nil &&
		binding.executionFence != 0 && s.executionCapabilities.RuntimeAuthority.
		AllowsRunAuthorizationFence(runID, binding.executionFence)
	if !runtimeOK || !entryPermissionMatches || !authorityLive ||
		browserPermission.Mode != domain.RunBrowserCDPPermissionFullDebug ||
		!browserPermission.OperatorConfirmed ||
		(executionPermission.Mode != domain.RunExecutionPermissionFullAccess &&
			executionPermission.Mode != domain.RunExecutionPermissionDebug) ||
		!s.executionCapabilities.AllowsSnapshot(executionPermission) {
		return fullCDPBrowserActionBinding{}, false, nil
	}
	return binding, true, nil
}

func sameFullCDPBrowserActionBinding(left, right fullCDPBrowserActionBinding) bool {
	return left.view.SessionID == right.view.SessionID &&
		left.view.State == FullCDPSessionReady && right.view.State == FullCDPSessionReady &&
		left.executionPermission.ID == right.executionPermission.ID &&
		left.executionPermission.Revision == right.executionPermission.Revision &&
		left.executionPermission.Mode == right.executionPermission.Mode &&
		left.executionActivation == right.executionActivation &&
		left.executionFence == right.executionFence &&
		left.browserPermissionID == right.browserPermissionID &&
		left.browserPermissionRevision == right.browserPermissionRevision &&
		left.missionID == right.missionID && left.runSessionID == right.runSessionID &&
		left.workspaceID == right.workspaceID && left.view.TargetOrigin == right.view.TargetOrigin
}

func (s *FullCDPProductionService) ExecuteBrowserAction(ctx context.Context,
	scope toolgateway.BrowserActionExecutionScope, name toolgateway.ToolName,
	raw json.RawMessage,
) (toolgateway.BrowserActionExecutionResult, error) {
	if err := scope.Validate(); err != nil {
		return toolgateway.BrowserActionExecutionResult{}, err
	}
	canonical, err := toolgateway.NormalizeBrowserActionPayload(name, raw)
	if err != nil {
		return toolgateway.BrowserActionExecutionResult{}, err
	}
	var payload toolgateway.BrowserActionPayload
	if err := json.Unmarshal(canonical, &payload); err != nil {
		return toolgateway.BrowserActionExecutionResult{}, err
	}
	binding, available, err := s.browserActionBinding(ctx, scope.RunID)
	if err != nil {
		return toolgateway.BrowserActionExecutionResult{}, err
	}
	expectedCapability := toolgateway.BrowserActionCapabilitySnapshot(
		toolgateway.BrowserActionCapabilityContext{RunID: scope.RunID,
			MissionID: scope.MissionID, SessionID: scope.SessionID,
			RootAgentID: scope.RootAgentID, WorkspaceID: scope.WorkspaceID,
			Surface: scope.Surface, Phase: scope.Phase, Role: scope.Role,
			Profile: scope.Profile, PermissionMode: scope.PermissionMode,
			ModeRevision:                scope.ModeRevision,
			PermissionSnapshotID:        scope.PermissionSnapshotID,
			PermissionRevision:          scope.PermissionRevision,
			PermissionActivation:        scope.PermissionActivation,
			RunAuthorizationFence:       scope.RunAuthorizationFence,
			FullCDPSessionID:            scope.FullCDPSessionID,
			BrowserPermissionSnapshotID: scope.BrowserPermissionSnapshotID,
			BrowserPermissionRevision:   scope.BrowserPermissionRevision,
			TargetOrigin:                binding.view.TargetOrigin, Ready: true, RuntimeAvailable: true})
	if !available || !expectedCapability.Available ||
		expectedCapability.Generation != scope.CapabilityGeneration ||
		binding.view.RunID != scope.RunID || binding.missionID != scope.MissionID ||
		binding.runSessionID != scope.SessionID || binding.workspaceID != scope.WorkspaceID ||
		binding.view.SessionID != scope.FullCDPSessionID ||
		binding.executionPermission.ID != scope.PermissionSnapshotID ||
		binding.executionPermission.Revision != scope.PermissionRevision ||
		binding.executionPermission.Mode != scope.PermissionMode ||
		binding.executionActivation != scope.PermissionActivation ||
		binding.executionFence != scope.RunAuthorizationFence ||
		binding.browserPermissionID != scope.BrowserPermissionSnapshotID ||
		binding.browserPermissionRevision != scope.BrowserPermissionRevision {
		return toolgateway.BrowserActionExecutionResult{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"browser action authority no longer matches the ready Full CDP session")
	}
	actionCtx, cancelAction := context.WithCancelCause(ctx)
	stopAuthorityMonitor := s.monitorFullCDPBrowserActionAuthority(
		actionCtx, cancelAction, scope.RunID, binding)
	defer func() {
		stopAuthorityMonitor()
		cancelAction(context.Canceled)
	}()

	var result any
	metadata := map[string]string{"full_cdp_session_id": binding.view.SessionID,
		"target_origin": binding.view.TargetOrigin}
	switch name {
	case toolgateway.BrowserStatusTool:
		result = struct {
			Version      string              `json:"version"`
			State        FullCDPSessionState `json:"state"`
			SessionID    string              `json:"session_id"`
			TargetOrigin string              `json:"target_origin"`
			ExpiresAt    *time.Time          `json:"expires_at,omitempty"`
		}{Version: "browser_status_result.v1", State: binding.view.State,
			SessionID: binding.view.SessionID, TargetOrigin: binding.view.TargetOrigin,
			ExpiresAt: binding.view.ExpiresAt}
	case toolgateway.BrowserNavigateTool:
		value, actionErr := binding.runtime.BrowserNavigate(actionCtx, payload.URL)
		if actionErr != nil {
			return toolgateway.BrowserActionExecutionResult{},
				normalizeFullCDPBrowserActionContextError(ctx, actionCtx, actionErr)
		}
		result = struct {
			Version            string `json:"version"`
			CanonicalURL       string `json:"canonical_url"`
			AllowedRequests    int    `json:"allowed_requests"`
			BlockedRequests    int    `json:"blocked_requests"`
			ScopeValidated     bool   `json:"scope_validated"`
			RedirectsValidated bool   `json:"redirects_validated"`
		}{Version: "browser_navigate_result.v1", CanonicalURL: value.CanonicalURL,
			AllowedRequests: value.AllowedRequests, BlockedRequests: value.BlockedRequests,
			ScopeValidated: value.ScopeValidated, RedirectsValidated: value.RedirectsValidated}
	case toolgateway.BrowserSnapshotTool:
		value, actionErr := binding.runtime.BrowserSnapshot(actionCtx)
		if actionErr != nil {
			return toolgateway.BrowserActionExecutionResult{},
				normalizeFullCDPBrowserActionContextError(ctx, actionCtx, actionErr)
		}
		result = value
	case toolgateway.BrowserClickTool:
		value, actionErr := binding.runtime.BrowserClick(actionCtx, payload.Selector)
		if actionErr != nil {
			return toolgateway.BrowserActionExecutionResult{},
				normalizeFullCDPBrowserActionContextError(ctx, actionCtx, actionErr)
		}
		result = value
	case toolgateway.BrowserTypeTool:
		value, actionErr := binding.runtime.BrowserType(actionCtx, payload.Selector, payload.Value)
		if actionErr != nil {
			return toolgateway.BrowserActionExecutionResult{},
				normalizeFullCDPBrowserActionContextError(ctx, actionCtx, actionErr)
		}
		result = value
	case toolgateway.BrowserScreenshotTool:
		capture, actionErr := binding.runtime.BrowserScreenshot(actionCtx)
		if actionErr != nil {
			return toolgateway.BrowserActionExecutionResult{},
				normalizeFullCDPBrowserActionContextError(ctx, actionCtx, actionErr)
		}
		locator, persistErr := s.persistFullCDPBrowserScreenshot(
			actionCtx, scope, binding, capture)
		if persistErr != nil {
			return toolgateway.BrowserActionExecutionResult{}, persistErr
		}
		result = struct {
			Version      string `json:"version"`
			CanonicalURL string `json:"canonical_url"`
			MediaType    string `json:"media_type"`
			Bytes        int    `json:"bytes"`
			SHA256       string `json:"sha256"`
			Artifact     string `json:"artifact_locator"`
		}{Version: "browser_screenshot_result.v1",
			CanonicalURL: capture.Metadata.CanonicalURL,
			MediaType:    capture.Metadata.MediaType, Bytes: capture.Metadata.Bytes,
			SHA256: capture.Metadata.SHA256, Artifact: locator}
		metadata["artifact_locator"] = locator
		metadata["artifact_sha256"] = capture.Metadata.SHA256
		metadata["artifact_bytes"] = fmt.Sprint(capture.Metadata.Bytes)
	default:
		return toolgateway.BrowserActionExecutionResult{}, apperror.New(
			apperror.CodeInvalidArgument, "unsupported Full CDP browser action")
	}
	if cause := context.Cause(actionCtx); cause != nil {
		return toolgateway.BrowserActionExecutionResult{},
			normalizeFullCDPBrowserActionContextError(ctx, actionCtx, cause)
	}
	stopAuthorityMonitor()

	postBinding, postAvailable, postErr := s.browserActionBinding(ctx, scope.RunID)
	if postErr != nil || !postAvailable || !sameFullCDPBrowserActionBinding(binding, postBinding) {
		if postErr != nil {
			return toolgateway.BrowserActionExecutionResult{}, postErr
		}
		return toolgateway.BrowserActionExecutionResult{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"browser action authority changed before the result was published")
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return toolgateway.BrowserActionExecutionResult{}, err
	}
	return toolgateway.BrowserActionExecutionResult{Content: string(encoded),
		Metadata: metadata}, nil
}

func (s *FullCDPProductionService) monitorFullCDPBrowserActionAuthority(
	ctx context.Context, cancel context.CancelCauseFunc, runID string,
	expected fullCDPBrowserActionBinding,
) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(fullCDPBrowserAuthorityPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				current, available, err := s.browserActionBinding(ctx, runID)
				if err == nil && available &&
					sameFullCDPBrowserActionBinding(expected, current) {
					continue
				}
				cause := apperror.New(apperror.CodeFailedPrecondition,
					"browser action authority changed during the CDP operation")
				if err != nil {
					cause = apperror.Wrap(apperror.CodeFailedPrecondition,
						"browser action authority could not be revalidated", err)
				}
				cancel(cause)
				expected.runtime.CancelBrowserAction()
				return
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(stop) })
		<-done
	}
}

func normalizeFullCDPBrowserActionContextError(parent, action context.Context,
	err error,
) error {
	if parent != nil && parent.Err() == nil {
		if cause := context.Cause(action); cause != nil {
			if apperror.CodeOf(cause) == apperror.CodeFailedPrecondition {
				return cause
			}
			return apperror.Wrap(apperror.CodeFailedPrecondition,
				"Full CDP browser action was canceled after authority changed", cause)
		}
	}
	return normalizeFullCDPBrowserActionError(err)
}

func normalizeFullCDPBrowserActionError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return apperror.Normalize(err)
	}
	return apperror.Wrap(apperror.CodeFailedPrecondition,
		"Full CDP browser action failed closed", err)
}

func (s *FullCDPProductionService) persistFullCDPBrowserScreenshot(ctx context.Context,
	scope toolgateway.BrowserActionExecutionScope, binding fullCDPBrowserActionBinding,
	capture browserruntime.FullCDPScreenshotCapture,
) (string, error) {
	if len(capture.PNG) == 0 || len(capture.PNG) != capture.Metadata.Bytes ||
		capture.Metadata.MediaType != "image/png" {
		return "", apperror.New(apperror.CodeFailedPrecondition,
			"Full CDP screenshot payload does not match its metadata")
	}
	digest := sha256.Sum256(capture.PNG)
	if hex.EncodeToString(digest[:]) != capture.Metadata.SHA256 {
		return "", apperror.New(apperror.CodeFailedPrecondition,
			"Full CDP screenshot payload failed integrity validation")
	}
	workspaceStore, ok := s.store.(fullCDPWorkspaceInfoStore)
	if !ok || strings.TrimSpace(binding.workspaceID) == "" {
		return "", apperror.New(apperror.CodeUnavailable,
			"Full CDP screenshot requires a registered Workspace artifact root")
	}
	workspace, err := workspaceStore.GetWorkspaceInfo(ctx, binding.workspaceID)
	if err != nil {
		return "", apperror.Normalize(err)
	}
	rootPath, err := filepath.Abs(filepath.Clean(workspace.RootPath))
	if err != nil || !filepath.IsAbs(rootPath) {
		return "", apperror.New(apperror.CodeFailedPrecondition,
			"Full CDP screenshot Workspace root is invalid")
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return "", apperror.Wrap(apperror.CodeUnavailable,
			"Full CDP screenshot Workspace root could not be opened", err)
	}
	defer root.Close()
	operationDigest := sha256.Sum256([]byte(scope.OperationKey))
	relative := filepath.Join(fullCDPBrowserArtifactDirectory, scope.RunID,
		hex.EncodeToString(operationDigest[:])+".png")
	if !pathInsideFullCDPArtifactRoot(rootPath, filepath.Join(rootPath, relative)) {
		return "", apperror.New(apperror.CodeFailedPrecondition,
			"Full CDP screenshot artifact path escaped the Workspace")
	}
	if err := root.MkdirAll(filepath.Dir(relative), 0o700); err != nil {
		return "", apperror.Wrap(apperror.CodeUnavailable,
			"Full CDP screenshot artifact directory could not be created", err)
	}
	file, err := root.OpenFile(relative, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		existing, readErr := readBoundedFullCDPArtifact(root, relative,
			browserruntime.MaxScreenshotBytes)
		if readErr != nil || !bytesEqual(existing, capture.PNG) {
			return "", apperror.New(apperror.CodeConflict,
				"Full CDP screenshot artifact locator already has different content")
		}
	} else if err != nil {
		return "", apperror.Wrap(apperror.CodeUnavailable,
			"Full CDP screenshot artifact could not be created", err)
	} else {
		writeErr := writeFullCDPArtifact(file, capture.PNG)
		if writeErr != nil {
			_ = root.Remove(relative)
			return "", apperror.Wrap(apperror.CodeUnavailable,
				"Full CDP screenshot artifact could not be persisted", writeErr)
		}
	}
	return "workspace:///" + filepath.ToSlash(relative), nil
}

func pathInsideFullCDPArtifactRoot(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != "." && relative != "" &&
		relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(relative)
}

func writeFullCDPArtifact(file *os.File, content []byte) error {
	if file == nil {
		return errors.New("artifact file is required")
	}
	_, writeErr := file.Write(content)
	syncErr := file.Sync()
	closeErr := file.Close()
	return errors.Join(writeErr, syncErr, closeErr)
}

func readBoundedFullCDPArtifact(root *os.Root, path string, limit int) ([]byte, error) {
	if root == nil {
		return nil, errors.New("artifact root is required")
	}
	identity, err := root.Lstat(path)
	if err != nil || !identity.Mode().IsRegular() || identity.Mode()&os.ModeSymlink != 0 {
		return nil, errors.Join(err, errors.New("artifact replay target is not a regular file"))
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	opened, statErr := file.Stat()
	if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(identity, opened) {
		_ = file.Close()
		return nil, errors.Join(statErr, errors.New("artifact replay target changed while opening"))
	}
	content, readErr := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(content) > limit {
		return nil, errors.Join(readErr, closeErr,
			errors.New("artifact content exceeds its bound"))
	}
	return content, nil
}

func bytesEqual(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var difference byte
	for index := range left {
		difference |= left[index] ^ right[index]
	}
	return difference == 0
}
