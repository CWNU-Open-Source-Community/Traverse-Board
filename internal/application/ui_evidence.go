package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/browserruntime"
	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/toolgateway"
	"cyberagent-workbench/internal/uievidence"
	"cyberagent-workbench/internal/workspacecheckpoint"
)

const (
	UIEvidenceExecutionTimeout = 30 * time.Minute
	uiEvidenceCleanupTimeout   = 30 * time.Second
	uiEvidencePortReleaseWait  = 5 * time.Second
)

type UIEvidenceStore interface {
	GetRun(context.Context, string) (domain.Run, error)
	GetMission(context.Context, string) (domain.Mission, error)
	GetWorkspaceByID(context.Context, string) (session.WorkspaceRecord, error)
	GetRootAgent(context.Context, string) (domain.AgentNode, bool, error)
	GetRunExecutionPermission(context.Context, string) (
		domain.RunExecutionPermissionSnapshot, error)
	GetRunExecutionLease(context.Context, string) (domain.RunExecutionLease, bool, error)
	CreateUIEvidenceAttempt(context.Context, uievidence.Attempt) (
		uievidence.Attempt, bool, error)
	UpdateUIEvidenceAttempt(context.Context, uievidence.Attempt, int64) (
		uievidence.Attempt, error)
	GetUIEvidenceAttempt(context.Context, string) (uievidence.Attempt, error)
	ListUIEvidenceAttempts(context.Context, uievidence.ListFilter) (
		[]uievidence.Attempt, error)
	AddUIEvidenceStep(context.Context, uievidence.StepReceipt) error
	ListUIEvidenceSteps(context.Context, string) ([]uievidence.StepReceipt, error)
	AddUIEvidenceArtifact(context.Context, uievidence.Artifact) error
	ListUIEvidenceArtifacts(context.Context, string) ([]uievidence.ArtifactMetadata, error)
	GetUIEvidenceArtifact(context.Context, string, string) (uievidence.Artifact, error)
	UIEvidenceArtifactTotals(context.Context, string) (int, int64, error)
	ReconcileUIEvidenceAttempts(context.Context, time.Time) ([]uievidence.Attempt, error)
}

type UIEvidenceRuntimeStep struct {
	Step  uievidence.Step `json:"step"`
	Input string          `json:"input,omitempty"`
}

type UIEvidenceStartRequest struct {
	RunID         string                     `json:"run_id"`
	OperationKey  string                     `json:"operation_key"`
	Build         *runner.CommandRuntimeSpec `json:"build,omitempty"`
	Start         runner.CommandRuntimeSpec  `json:"start"`
	Readiness     uievidence.Readiness       `json:"readiness"`
	URL           string                     `json:"url"`
	Route         string                     `json:"route"`
	Browser       UIEvidenceBrowserSelection `json:"browser"`
	Environment   uievidence.Environment     `json:"environment"`
	Fixture       uievidence.Fixture         `json:"fixture"`
	Steps         []UIEvidenceRuntimeStep    `json:"steps"`
	Capture       uievidence.CapturePolicy   `json:"capture"`
	FailurePolicy uievidence.FailurePolicy   `json:"failure_policy"`
}

func (r UIEvidenceStartRequest) Validate() error {
	if !domain.ValidAgentID(r.RunID) || r.RunID != strings.TrimSpace(r.RunID) ||
		r.OperationKey == "" || r.OperationKey != strings.TrimSpace(r.OperationKey) ||
		len([]byte(r.OperationKey)) > 1024 || !utf8.ValidString(r.OperationKey) ||
		strings.ContainsRune(r.OperationKey, 0) || redact.String(r.OperationKey) != r.OperationKey ||
		!validUIEvidenceBrowserSelection(r.Browser) || len(r.Steps) == 0 ||
		len(r.Steps) > uievidence.MaxSteps || r.Capture.Video ||
		!r.Capture.Screenshot || !r.Capture.DOM || !r.Capture.Accessibility ||
		!r.Capture.Console || !r.Capture.Network || !r.Capture.Performance {
		return errors.New("UI evidence start request is invalid")
	}
	if _, err := runner.NormalizeCommandRuntimeIntent(r.Start); err != nil {
		return err
	}
	if uiEvidenceRecipeHasNetworkIntent(r.Start) {
		return errors.New("UI evidence start recipe contains network-client intent")
	}
	if r.Build != nil {
		if _, err := runner.NormalizeCommandRuntimeIntent(*r.Build); err != nil {
			return err
		}
		if uiEvidenceRecipeHasNetworkIntent(*r.Build) {
			return errors.New("UI evidence build recipe contains network-client intent")
		}
	}
	if r.Readiness.Validate() != nil || r.Environment.Validate() != nil ||
		r.Fixture.Validate() != nil || r.Capture.Validate() != nil ||
		r.FailurePolicy.Validate() != nil {
		return errors.New("UI evidence request contract is invalid")
	}
	seen := make(map[string]struct{}, len(r.Steps))
	for index, runtimeStep := range r.Steps {
		if runtimeStep.Step.Validate() != nil ||
			(index == 0 && runtimeStep.Step.Kind != uievidence.StepNavigate) {
			return errors.New("UI evidence step contract is invalid")
		}
		if _, exists := seen[runtimeStep.Step.ID]; exists {
			return errors.New("UI evidence step identity is duplicated")
		}
		seen[runtimeStep.Step.ID] = struct{}{}
		if runtimeStep.Step.Kind == uievidence.StepType {
			digest, err := uievidence.InputSHA256(runtimeStep.Input)
			if err != nil || digest != runtimeStep.Step.InputSHA256 {
				return errors.New("UI evidence step input does not match its digest")
			}
		} else if runtimeStep.Input != "" {
			return errors.New("UI evidence non-input step contains raw input")
		}
	}
	return nil
}

type UIEvidenceBundle struct {
	Attempt   uievidence.Attempt            `json:"attempt"`
	Steps     []uievidence.StepReceipt      `json:"steps"`
	Artifacts []uievidence.ArtifactMetadata `json:"artifacts"`
}

// uiEvidenceCommandRuntime deliberately keeps cleanup authority separate from
// ordinary command authority. Starting, reading, and waiting still require the
// live Run lease through ExecuteCommandRuntime. Cleanup can only reap the exact
// durable Job identity sealed into an Attempt before that lease expired.
type uiEvidenceCommandRuntime interface {
	toolgateway.CommandRuntimeExecutor
	toolgateway.CommandRuntimeAdvertiser
	cleanupUIEvidenceJob(context.Context, uiEvidenceCommandCleanupBinding) (
		runner.CommandRuntimeJobSnapshot, error)
}

type uiEvidenceCommandCleanupBinding struct {
	JobID           string
	InvocationID    string
	OperationKey    string
	RunID           string
	MissionID       string
	SessionID       string
	WorkspaceID     string
	RootAgentID     string
	LeaseID         string
	LeaseGeneration int64
}

func (b uiEvidenceCommandCleanupBinding) Validate() error {
	for _, value := range []string{b.JobID, b.InvocationID, b.OperationKey,
		b.RunID, b.MissionID, b.SessionID, b.WorkspaceID, b.RootAgentID,
		b.LeaseID} {
		if value == "" || value != strings.TrimSpace(value) ||
			!utf8.ValidString(value) || len([]rune(value)) > 256 ||
			strings.ContainsRune(value, 0) {
			return errors.New("UI evidence command cleanup binding is invalid")
		}
	}
	if b.LeaseGeneration <= 0 {
		return errors.New("UI evidence command cleanup lease generation is invalid")
	}
	return nil
}

type UIEvidenceService struct {
	store       UIEvidenceStore
	commands    uiEvidenceCommandRuntime
	browsers    UIEvidenceBrowserProvider
	profileRoot string
	now         func() time.Time
	mu          sync.Mutex
	cancel      map[string]context.CancelFunc
	wg          sync.WaitGroup
	closed      bool
}

func NewUIEvidenceService(store UIEvidenceStore,
	commands uiEvidenceCommandRuntime, browsers UIEvidenceBrowserProvider,
	profileRoot string,
) (*UIEvidenceService, error) {
	service, err := NewUIEvidenceReadService(store)
	if err != nil {
		return nil, err
	}
	profileRoot = strings.TrimSpace(profileRoot)
	if commands == nil || browsers == nil || profileRoot == "" ||
		!filepath.IsAbs(profileRoot) {
		return nil, apperror.New(apperror.CodeFailedPrecondition,
			"UI evidence requires store, command runtime, browser runtime, and private Profile root")
	}
	service.commands = commands
	service.browsers = browsers
	service.profileRoot = filepath.Clean(profileRoot)
	return service, nil
}

// NewUIEvidenceReadService exposes persisted manifests, receipts, and
// hash-verified artifacts without constructing process, browser, Profile, or
// network authority. It also owns startup reconciliation for interrupted
// attempts when execution capability is disabled on a later launch.
func NewUIEvidenceReadService(store UIEvidenceStore) (*UIEvidenceService, error) {
	if store == nil {
		return nil, apperror.New(apperror.CodeFailedPrecondition,
			"UI evidence read service requires a store")
	}
	return &UIEvidenceService{store: store,
		now:    func() time.Time { return time.Now().UTC() },
		cancel: make(map[string]context.CancelFunc)}, nil
}

func (s *UIEvidenceService) Run(ctx context.Context,
	request UIEvidenceStartRequest,
) (uievidence.Attempt, error) {
	if err := s.requireExecutionOpen(); err != nil {
		return uievidence.Attempt{}, err
	}
	prepared, existing, err := s.prepare(ctx, request)
	if err != nil || existing.Status != "" {
		return existing, err
	}
	attemptID := prepared.attempt.Manifest.AttemptID
	runContext, cancel := uiEvidenceExecutionContext(ctx, prepared.lease.ExpiresAt)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		return uievidence.Attempt{}, apperror.New(apperror.CodeFailedPrecondition,
			"UI evidence service is closed")
	}
	if _, active := s.cancel[attemptID]; active {
		s.mu.Unlock()
		cancel()
		return uievidence.Attempt{}, apperror.New(apperror.CodeConflict,
			"UI evidence attempt is already active")
	}
	s.cancel[attemptID] = cancel
	s.wg.Add(1)
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.cancel, attemptID)
		s.mu.Unlock()
		s.wg.Done()
	}()
	return s.execute(runContext, prepared)
}

// Start persists not_run before returning and runs asynchronously under a
// bounded service-owned context. An HTTP request cancellation therefore cannot
// orphan the browser or application process tree.
func (s *UIEvidenceService) Start(ctx context.Context,
	request UIEvidenceStartRequest,
) (uievidence.Attempt, error) {
	if err := s.requireExecutionOpen(); err != nil {
		return uievidence.Attempt{}, err
	}
	prepared, existing, err := s.prepare(ctx, request)
	if err != nil {
		return uievidence.Attempt{}, err
	}
	if existing.Status != "" {
		return existing, nil
	}
	attemptID := prepared.attempt.Manifest.AttemptID
	runContext, cancel := uiEvidenceExecutionContext(context.Background(),
		prepared.lease.ExpiresAt)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		return uievidence.Attempt{}, apperror.New(apperror.CodeFailedPrecondition,
			"UI evidence service is closed")
	}
	if _, active := s.cancel[attemptID]; active {
		s.mu.Unlock()
		cancel()
		return prepared.attempt, nil
	}
	s.cancel[attemptID] = cancel
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer func() {
			cancel()
			s.mu.Lock()
			delete(s.cancel, attemptID)
			s.mu.Unlock()
			s.wg.Done()
		}()
		_, _ = s.execute(runContext, prepared)
	}()
	return prepared.attempt, nil
}

func (s *UIEvidenceService) requireExecutionOpen() error {
	if s == nil || s.store == nil || s.commands == nil || s.browsers == nil ||
		strings.TrimSpace(s.profileRoot) == "" {
		return apperror.New(apperror.CodeFailedPrecondition,
			"UI evidence execution capability is unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return apperror.New(apperror.CodeFailedPrecondition,
			"UI evidence service is closed")
	}
	return nil
}

func uiEvidenceExecutionContext(parent context.Context,
	leaseExpiresAt time.Time,
) (context.Context, context.CancelFunc) {
	deadline := time.Now().UTC().Add(UIEvidenceExecutionTimeout)
	if leaseExpiresAt.Before(deadline) {
		deadline = leaseExpiresAt.UTC()
	}
	return context.WithDeadline(parent, deadline)
}

// Close prevents new asynchronous attempts, cancels every service-owned
// attempt, and waits for their independent process/profile/network cleanup.
func (s *UIEvidenceService) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("UI evidence close context is required")
	}
	s.mu.Lock()
	s.closed = true
	cancellations := make([]context.CancelFunc, 0, len(s.cancel))
	for _, cancel := range s.cancel {
		cancellations = append(cancellations, cancel)
	}
	s.mu.Unlock()
	for _, cancel := range cancellations {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *UIEvidenceService) Cancel(ctx context.Context, attemptID string) (
	uievidence.Attempt, error,
) {
	if s == nil || s.store == nil || !domain.ValidAgentID(strings.TrimSpace(attemptID)) {
		return uievidence.Attempt{}, apperror.New(apperror.CodeInvalidArgument,
			"UI evidence attempt id is invalid")
	}
	if attemptID != strings.TrimSpace(attemptID) {
		return uievidence.Attempt{}, apperror.New(apperror.CodeInvalidArgument,
			"UI evidence attempt id must be normalized")
	}
	s.mu.Lock()
	cancel := s.cancel[attemptID]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	deadline := time.NewTimer(uiEvidenceCleanupTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		attempt, err := s.store.GetUIEvidenceAttempt(ctx, attemptID)
		if err != nil {
			return uievidence.Attempt{}, err
		}
		s.mu.Lock()
		_, active := s.cancel[attemptID]
		s.mu.Unlock()
		if attempt.Status.Terminal() || !active {
			return attempt, nil
		}
		select {
		case <-ctx.Done():
			return uievidence.Attempt{}, ctx.Err()
		case <-deadline.C:
			return attempt, apperror.New(apperror.CodeUnavailable,
				"UI evidence cancellation cleanup is still pending")
		case <-ticker.C:
		}
	}
}

func (s *UIEvidenceService) Get(ctx context.Context, attemptID string) (
	UIEvidenceBundle, error,
) {
	attempt, err := s.store.GetUIEvidenceAttempt(ctx, attemptID)
	if err != nil {
		return UIEvidenceBundle{}, err
	}
	steps, err := s.store.ListUIEvidenceSteps(ctx, attemptID)
	if err != nil {
		return UIEvidenceBundle{}, err
	}
	artifacts, err := s.store.ListUIEvidenceArtifacts(ctx, attemptID)
	if err != nil {
		return UIEvidenceBundle{}, err
	}
	return UIEvidenceBundle{Attempt: attempt, Steps: steps, Artifacts: artifacts}, nil
}

func (s *UIEvidenceService) List(ctx context.Context, filter uievidence.ListFilter) (
	[]uievidence.Attempt, error,
) {
	return s.store.ListUIEvidenceAttempts(ctx, filter)
}

func (s *UIEvidenceService) Artifact(ctx context.Context, attemptID,
	artifactID string,
) (uievidence.Artifact, error) {
	return s.store.GetUIEvidenceArtifact(ctx, attemptID, artifactID)
}

func (s *UIEvidenceService) Reconcile(ctx context.Context) ([]uievidence.Attempt, error) {
	return s.store.ReconcileUIEvidenceAttempts(ctx, s.now())
}

type preparedUIEvidence struct {
	request   UIEvidenceStartRequest
	attempt   uievidence.Attempt
	browser   UIEvidenceBrowserPreparation
	run       domain.Run
	mission   domain.Mission
	workspace session.WorkspaceRecord
	root      domain.AgentNode
	lease     domain.RunExecutionLease
	adapter   commandruntimeadapter.Identity
}

func (s *UIEvidenceService) prepare(ctx context.Context,
	request UIEvidenceStartRequest,
) (preparedUIEvidence, uievidence.Attempt, error) {
	if s == nil || s.store == nil || s.commands == nil || s.browsers == nil ||
		ctx == nil || ctx.Err() != nil || request.Validate() != nil {
		return preparedUIEvidence{}, uievidence.Attempt{}, apperror.New(
			apperror.CodeInvalidArgument, "UI evidence request is invalid")
	}
	runRecord, err := s.store.GetRun(ctx, request.RunID)
	if err != nil {
		return preparedUIEvidence{}, uievidence.Attempt{}, err
	}
	mission, err := s.store.GetMission(ctx, runRecord.MissionID)
	if err != nil {
		return preparedUIEvidence{}, uievidence.Attempt{}, err
	}
	workspace, err := s.store.GetWorkspaceByID(ctx, mission.WorkspaceID)
	if err != nil {
		return preparedUIEvidence{}, uievidence.Attempt{}, err
	}
	root, found, err := s.store.GetRootAgent(ctx, runRecord.ID)
	if err != nil || !found {
		return preparedUIEvidence{}, uievidence.Attempt{},
			apperror.New(apperror.CodeFailedPrecondition, "UI evidence root Agent is unavailable")
	}
	lease, found, err := s.store.GetRunExecutionLease(ctx, runRecord.ID)
	if err != nil || !found || lease.Status != domain.RunExecutionLeaseActive ||
		!lease.ExpiresAt.After(s.now()) {
		return preparedUIEvidence{}, uievidence.Attempt{}, apperror.New(
			apperror.CodeFailedPrecondition, "UI evidence requires the active Run execution lease")
	}
	if runRecord.Status != domain.RunRunning || root.Role != domain.AgentRoleRoot ||
		root.ParentID != "" || runRecord.SessionID == "" || mission.WorkspaceID == "" {
		return preparedUIEvidence{}, uievidence.Attempt{}, apperror.New(
			apperror.CodeFailedPrecondition, "UI evidence Run binding is not executable")
	}
	executionPermission, err := s.store.GetRunExecutionPermission(ctx, runRecord.ID)
	if err != nil {
		return preparedUIEvidence{}, uievidence.Attempt{}, err
	}
	if !executionPermission.Mode.IncludesFullAccess() {
		return preparedUIEvidence{}, uievidence.Attempt{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"UI evidence requires current Full Access or Debug permission")
	}
	adapter, available, err := s.commands.AdvertisedCommandRuntimeAdapter(ctx,
		runRecord.ID, executionPermission.Mode)
	if err != nil {
		return preparedUIEvidence{}, uievidence.Attempt{}, err
	}
	if !available || adapter.Kind != commandruntimeadapter.KindHostUnsandboxed ||
		!adapter.Executable() {
		return preparedUIEvidence{}, uievidence.Attempt{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"UI evidence requires the granted host Command Runtime adapter")
	}
	_, attemptID := uievidence.OperationIdentity(runRecord.ID, request.OperationKey)
	createdAt := s.now()
	if existing, getErr := s.store.GetUIEvidenceAttempt(ctx, attemptID); getErr == nil {
		createdAt = existing.Manifest.CreatedAt
	} else if apperror.CodeOf(apperror.Normalize(getErr)) != apperror.CodeNotFound {
		return preparedUIEvidence{}, uievidence.Attempt{}, getErr
	}
	startResolved, err := runner.NormalizeCommandRuntimeSpec(request.Start, workspace.RootPath)
	if err != nil {
		return preparedUIEvidence{}, uievidence.Attempt{}, err
	}
	startRecipe, err := uievidence.CommandRecipeFromResolved(startResolved)
	if err != nil {
		return preparedUIEvidence{}, uievidence.Attempt{}, err
	}
	var buildRecipe *uievidence.CommandRecipe
	if request.Build != nil {
		resolved, err := runner.NormalizeCommandRuntimeSpec(*request.Build, workspace.RootPath)
		if err != nil {
			return preparedUIEvidence{}, uievidence.Attempt{}, err
		}
		recipe, err := uievidence.CommandRecipeFromResolved(resolved)
		if err != nil {
			return preparedUIEvidence{}, uievidence.Attempt{}, err
		}
		buildRecipe = &recipe
	}
	snapshot, err := workspacecheckpoint.Capture(ctx, workspacecheckpoint.CaptureRequest{
		ID: "ui-source-" + attemptID[len("ui-attempt-"):], RunID: runRecord.ID,
		MissionID: mission.ID, SessionID: runRecord.SessionID, WorkspaceID: mission.WorkspaceID,
		WorkspaceRoot: workspace.RootPath, AttemptID: attemptID,
		CapabilityGeneration: strings.Repeat("0", 64), Trigger: workspacecheckpoint.TriggerManual,
		Phase: workspacecheckpoint.PhaseStandalone, TriggerReceiptID: attemptID,
		RequestedBy: toolgateway.CommandRuntimeRequestedByUIEvidenceOperator,
		Title:       "UI evidence source binding", CreatedAt: createdAt})
	if err != nil {
		return preparedUIEvidence{}, uievidence.Attempt{}, err
	}
	source, err := uievidence.BindSource(ctx, workspace.RootPath, snapshot)
	if err != nil {
		return preparedUIEvidence{}, uievidence.Attempt{}, err
	}
	browser, err := s.browsers.Prepare(ctx, request.Browser)
	if err != nil {
		return preparedUIEvidence{}, uievidence.Attempt{}, err
	}
	steps := make([]uievidence.Step, len(request.Steps))
	for index := range request.Steps {
		steps[index] = request.Steps[index].Step
	}
	manifest, err := uievidence.SealManifest(uievidence.Manifest{
		AttemptID: attemptID, RunID: runRecord.ID, MissionID: mission.ID,
		SessionID: runRecord.SessionID, WorkspaceID: mission.WorkspaceID,
		Source: source, Build: buildRecipe, Start: startRecipe,
		Readiness: request.Readiness, Browser: browser.ManifestIdentity,
		URL: request.URL, Route: request.Route, Environment: request.Environment,
		Fixture: request.Fixture, Steps: steps, Capture: request.Capture,
		FailurePolicy: request.FailurePolicy, CreatedAt: createdAt})
	if err != nil {
		return preparedUIEvidence{}, uievidence.Attempt{}, err
	}
	attempt, err := uievidence.NewAttempt(manifest, request.OperationKey, createdAt)
	if err != nil {
		return preparedUIEvidence{}, uievidence.Attempt{}, err
	}
	stored, replayed, err := s.store.CreateUIEvidenceAttempt(ctx, attempt)
	if err != nil {
		return preparedUIEvidence{}, uievidence.Attempt{}, err
	}
	if replayed {
		return preparedUIEvidence{}, stored, nil
	}
	return preparedUIEvidence{request: request, attempt: stored, browser: browser,
			run: runRecord, mission: mission, workspace: workspace, root: root, lease: lease,
			adapter: adapter},
		uievidence.Attempt{}, nil
}

type uiEvidenceOutcome struct {
	status      uievidence.Status
	stage       uievidence.FailureStage
	code        string
	message     string
	diagnostics uievidence.DiagnosticsSummary
}

type uiEvidenceResources struct {
	applicationJobs []uiEvidenceCommandCleanupBinding
	browser         *UIEvidenceBrowserRun
	cleanup         uievidence.CleanupReceipt
}

func (s *UIEvidenceService) execute(ctx context.Context,
	prepared preparedUIEvidence,
) (uievidence.Attempt, error) {
	started, err := uievidence.StartAttempt(prepared.attempt, s.now())
	if err != nil {
		return uievidence.Attempt{}, err
	}
	transitionContext, transitionCancel := context.WithTimeout(
		context.Background(), 5*time.Second)
	started, err = s.store.UpdateUIEvidenceAttempt(transitionContext, started,
		prepared.attempt.Version)
	transitionCancel()
	if err != nil {
		return uievidence.Attempt{}, err
	}
	prepared.attempt = started
	resources := uiEvidenceResources{cleanup: uievidence.CleanupReceipt{
		BrowserTreeReaped: true, ApplicationTreeReaped: true, ProfileRemoved: true,
		NetworkReleased: true, PortReleased: true}}
	outcome := s.perform(ctx, prepared, &resources)
	cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), uiEvidenceCleanupTimeout)
	cleanupErr := s.cleanup(cleanupContext, prepared, &resources)
	cleanupCancel()
	if cleanupErr != nil || !resources.cleanup.Complete() {
		outcome.status, outcome.stage, outcome.code = uievidence.StatusFailed,
			uievidence.FailureCleanup, "cleanup_incomplete"
		outcome.message = errors.Join(errors.New(outcome.message), cleanupErr).Error()
	}
	// A passing receipt is only valid after every owned process has stopped.
	// Rebind the source once more after cleanup so the application cannot mutate
	// tracked files between the last browser assertion and process-tree reaping.
	if cleanupErr == nil && resources.cleanup.Complete() &&
		outcome.status == uievidence.StatusPassed {
		if sourceErr := s.verifyPreparedSource(ctx, prepared); sourceErr != nil {
			status := uievidence.StatusFailed
			if errors.Is(ctx.Err(), context.Canceled) {
				status = uievidence.StatusCancelled
			}
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				status = uievidence.StatusTimedOut
			}
			outcome.status, outcome.stage = status, uievidence.FailureAssertion
			outcome.code, outcome.message = "source_changed_before_completion", sourceErr.Error()
		}
	}
	completionContext, completionCancel := context.WithTimeout(
		context.Background(), 5*time.Second)
	defer completionCancel()
	count, bytes, totalsErr := s.store.UIEvidenceArtifactTotals(completionContext,
		prepared.attempt.Manifest.AttemptID)
	if totalsErr != nil {
		return uievidence.Attempt{}, totalsErr
	}
	completed, err := uievidence.CompleteAttempt(started, outcome.status, outcome.stage,
		outcome.code, outcome.message, outcome.diagnostics, resources.cleanup,
		count, bytes, s.now())
	if err != nil {
		return uievidence.Attempt{}, err
	}
	completed, updateErr := s.store.UpdateUIEvidenceAttempt(completionContext,
		completed, started.Version)
	return completed, errors.Join(cleanupErr, updateErr)
}

func (s *UIEvidenceService) perform(ctx context.Context, prepared preparedUIEvidence,
	resources *uiEvidenceResources,
) uiEvidenceOutcome {
	fail := func(stage uievidence.FailureStage, code string, err error) uiEvidenceOutcome {
		status := uievidence.StatusFailed
		if errors.Is(ctx.Err(), context.Canceled) {
			status = uievidence.StatusCancelled
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status = uievidence.StatusTimedOut
		}
		return uiEvidenceOutcome{status: status, stage: stage, code: code,
			message: err.Error()}
	}
	if err := s.verifyPreparedSource(ctx, prepared); err != nil {
		return fail(uievidence.FailureBuild, "source_changed_before_build", err)
	}
	if prepared.request.Build != nil {
		job, err := s.startCommand(ctx, prepared, *prepared.request.Build, "build")
		if err == nil {
			resources.applicationJobs = append(resources.applicationJobs,
				s.commandCleanupBinding(prepared, "build", job.ID))
			resources.cleanup.ApplicationTreeReaped = false
			job, err = s.waitCommand(ctx, prepared, job.ID, "build:wait")
			if job.State.Terminal() && job.TreeReaped {
				resources.applicationJobs = resources.applicationJobs[:0]
				resources.cleanup.ApplicationTreeReaped = true
			}
		}
		if err != nil || job.State != runner.CommandRuntimeJobCompleted {
			if err == nil {
				err = fmt.Errorf("build Job ended as %s", job.State)
			}
			return fail(uievidence.FailureBuild, "build_failed", err)
		}
		if err := s.verifyPreparedSource(ctx, prepared); err != nil {
			return fail(uievidence.FailureBuild, "source_changed_by_build", err)
		}
	}
	if err := ensureUIEvidencePortFree(ctx, prepared.request.Readiness.URL); err != nil {
		return fail(uievidence.FailureLaunch, "preexisting_service", err)
	}
	job, err := s.startCommand(ctx, prepared, prepared.request.Start, "application")
	if err != nil {
		return fail(uievidence.FailureLaunch, "application_start_failed", err)
	}
	resources.applicationJobs = append(resources.applicationJobs,
		s.commandCleanupBinding(prepared, "application", job.ID))
	resources.cleanup.ApplicationTreeReaped = false
	resources.cleanup.PortReleased = false
	if err := s.waitReadiness(ctx, prepared, job.ID); err != nil {
		return fail(uievidence.FailureReadiness, "readiness_failed", err)
	}
	if err := s.verifyPreparedSource(ctx, prepared); err != nil {
		return fail(uievidence.FailureLaunch, "source_changed_by_start", err)
	}
	browserRun, err := s.browsers.Open(ctx, prepared.browser, BrowserRuntimeLaunchRequest{
		RunID: prepared.run.ID, Target: prepared.request.URL, ProfileRoot: s.profileRoot,
		OperationKey:       uiEvidenceRuntimeIdentity(prepared, "browser-prepare"),
		LeaseOwnerIdentity: "ui-evidence-runtime",
		ReviewerIdentity:   "ui-evidence-runtime",
		ReviewOperationKey: uiEvidenceRuntimeIdentity(prepared, "browser-review")})
	if err != nil {
		return fail(uievidence.FailureLaunch, "browser_launch_failed", err)
	}
	resources.browser = browserRun
	resources.cleanup.BrowserTreeReaped = false
	resources.cleanup.ProfileRemoved = false
	resources.cleanup.NetworkReleased = false
	if err := browserRun.Driver.ConfigureUIEvidence(ctx,
		prepared.request.Environment); err != nil {
		return fail(uievidence.FailureLaunch, "browser_configuration_failed", err)
	}
	for index, runtimeStep := range prepared.request.Steps {
		stepStarted := s.now()
		stage, stepErr := s.performStep(ctx, prepared, browserRun.Driver, runtimeStep)
		status := uievidence.StatusPassed
		message := ""
		if stepErr != nil {
			status, message = uiEvidenceStepFailureStatus(ctx, stepErr), stepErr.Error()
		}
		receipt, receiptErr := uievidence.SealStepReceipt(uievidence.StepReceipt{
			AttemptID: prepared.attempt.Manifest.AttemptID, StepID: runtimeStep.Step.ID,
			Sequence: index + 1, Kind: runtimeStep.Step.Kind, Status: status,
			FailureStage: stage, Message: message, StartedAt: stepStarted,
			CompletedAt: s.now()})
		receiptFailureStage := stage
		if receiptFailureStage == uievidence.FailureNone {
			receiptFailureStage = uievidence.FailureCapture
		}
		if receiptErr != nil {
			return fail(receiptFailureStage, "step_receipt_failed", receiptErr)
		}
		receiptContext, receiptCancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := s.store.AddUIEvidenceStep(receiptContext, receipt)
		receiptCancel()
		if err != nil {
			return fail(receiptFailureStage, "step_receipt_failed", err)
		}
		if stepErr != nil {
			return fail(stage, "step_failed", stepErr)
		}
		if runtimeStep.Step.CaptureAfter || runtimeStep.Step.Kind == uievidence.StepCapture {
			if err := s.captureVisual(ctx, prepared, browserRun.Driver,
				runtimeStep.Step.ID); err != nil {
				return fail(uievidence.FailureCapture, "capture_failed", err)
			}
		}
	}
	lastRuntimeStep := prepared.request.Steps[len(prepared.request.Steps)-1]
	lastStep := lastRuntimeStep.Step.ID
	if !lastRuntimeStep.Step.CaptureAfter && lastRuntimeStep.Step.Kind != uievidence.StepCapture {
		if err := s.captureVisual(ctx, prepared, browserRun.Driver, lastStep); err != nil {
			return fail(uievidence.FailureCapture, "final_capture_failed", err)
		}
	}
	diagnostics, _, err := browserRun.Driver.DiagnosticsUIEvidence(ctx)
	if err != nil {
		return fail(uievidence.FailureCapture, "diagnostics_failed", err)
	}
	if err := s.captureDiagnosticArtifacts(ctx, prepared, lastStep, diagnostics); err != nil {
		return fail(uievidence.FailureCapture, "final_capture_failed", err)
	}
	summary := diagnostics.Summary
	if summary.ConsoleErrors > 0 || summary.PageErrors > 0 {
		return uiEvidenceOutcome{status: uievidence.StatusFailed,
			stage: uievidence.FailureConsole, code: "browser_console_failed",
			message: "browser console or page errors were observed", diagnostics: summary}
	}
	if summary.FailedRequests > 0 || summary.HTTPFailures > 0 || summary.BlockedRequests > 0 {
		return uiEvidenceOutcome{status: uievidence.StatusFailed,
			stage: uievidence.FailureNetwork, code: "browser_network_failed",
			message:     "failed, blocked, or unsuccessful browser requests were observed",
			diagnostics: summary}
	}
	if err := s.verifyPreparedSource(ctx, prepared); err != nil {
		return fail(uievidence.FailureAssertion, "source_changed_during_evidence", err)
	}
	return uiEvidenceOutcome{status: uievidence.StatusPassed,
		stage: uievidence.FailureNone, diagnostics: summary}
}

func (s *UIEvidenceService) verifyPreparedSource(ctx context.Context,
	prepared preparedUIEvidence,
) error {
	manifest := prepared.attempt.Manifest
	suffix := strings.TrimPrefix(manifest.AttemptID, "ui-attempt-")
	snapshot, err := workspacecheckpoint.Capture(ctx, workspacecheckpoint.CaptureRequest{
		ID: "ui-source-verify-" + suffix, RunID: prepared.run.ID,
		MissionID: prepared.mission.ID, SessionID: prepared.run.SessionID,
		WorkspaceID: prepared.mission.WorkspaceID, WorkspaceRoot: prepared.workspace.RootPath,
		AttemptID: manifest.AttemptID, CapabilityGeneration: manifest.Fingerprint,
		Trigger: workspacecheckpoint.TriggerManual, Phase: workspacecheckpoint.PhaseStandalone,
		TriggerReceiptID: manifest.AttemptID, RequestedBy: "run_supervisor",
		Title: "UI evidence source revalidation", CreatedAt: s.now()})
	if err != nil {
		return fmt.Errorf("revalidate UI evidence source: %w", err)
	}
	current, err := uievidence.BindSource(ctx, prepared.workspace.RootPath, snapshot)
	if err != nil {
		return fmt.Errorf("bind revalidated UI evidence source: %w", err)
	}
	if current != manifest.Source {
		return errors.New("UI evidence source changed after its manifest was sealed")
	}
	return nil
}

func (s *UIEvidenceService) performStep(ctx context.Context,
	prepared preparedUIEvidence, driver UIEvidenceBrowserDriver,
	runtimeStep UIEvidenceRuntimeStep,
) (uievidence.FailureStage, error) {
	switch runtimeStep.Step.Kind {
	case uievidence.StepNavigate:
		_, err := driver.Navigate(ctx, prepared.request.URL)
		return stageForError(uievidence.FailureNavigation, err), err
	case uievidence.StepClick:
		err := driver.ClickUIEvidence(ctx, runtimeStep.Step.Selector)
		return stageForError(uievidence.FailureSelector, err), err
	case uievidence.StepType:
		err := driver.TypeUIEvidence(ctx, runtimeStep.Step.Selector,
			runtimeStep.Input, runtimeStep.Step.InputSHA256)
		return stageForError(uievidence.FailureSelector, err), err
	case uievidence.StepAssertPresent:
		err := driver.AssertUIEvidenceSelector(ctx, runtimeStep.Step.Selector, true)
		return stageForError(uievidence.FailureAssertion, err), err
	case uievidence.StepAssertAbsent:
		err := driver.AssertUIEvidenceSelector(ctx, runtimeStep.Step.Selector, false)
		return stageForError(uievidence.FailureAssertion, err), err
	case uievidence.StepCapture:
		return uievidence.FailureNone, nil
	default:
		return uievidence.FailureAssertion, errors.New("unsupported UI evidence step")
	}
}

func stageForError(stage uievidence.FailureStage, err error) uievidence.FailureStage {
	if err == nil {
		return uievidence.FailureNone
	}
	return stage
}

func uiEvidenceStepFailureStatus(ctx context.Context, err error) uievidence.Status {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) ||
		errors.Is(err, context.DeadlineExceeded) {
		return uievidence.StatusTimedOut
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return uievidence.StatusCancelled
	}
	return uievidence.StatusFailed
}

func (s *UIEvidenceService) captureVisual(ctx context.Context,
	prepared preparedUIEvidence, driver UIEvidenceBrowserDriver, stepID string,
) error {
	policy := prepared.request.Capture
	if policy.Screenshot {
		screenshot, width, height, err := driver.ScreenshotUIEvidence(ctx,
			policy.MaskSelectors, prepared.request.Environment.Viewport.DPR)
		if err != nil {
			return err
		}
		if err := s.addArtifact(ctx, prepared, stepID, uievidence.ArtifactScreenshot,
			screenshot.MediaType, screenshot.PNG, len(policy.MaskSelectors) > 0,
			width, height, screenshot.CompletedAt); err != nil {
			return err
		}
	}
	if policy.DOM {
		capture, err := driver.DOMUIEvidence(ctx)
		if err != nil {
			return err
		}
		if err := s.addArtifact(ctx, prepared, stepID, uievidence.ArtifactDOM,
			capture.MIME, capture.Content, capture.Redacted, 0, 0, capture.CapturedAt); err != nil {
			return err
		}
	}
	if policy.Accessibility {
		capture, err := driver.AccessibilityUIEvidence(ctx)
		if err != nil {
			return err
		}
		if err := s.addArtifact(ctx, prepared, stepID, uievidence.ArtifactAccessibility,
			capture.MIME, capture.Content, capture.Redacted, 0, 0, capture.CapturedAt); err != nil {
			return err
		}
	}
	if policy.Performance {
		capture, err := driver.PerformanceUIEvidence(ctx)
		if err != nil {
			return err
		}
		if err := s.addArtifact(ctx, prepared, stepID, uievidence.ArtifactPerformance,
			capture.MIME, capture.Content, capture.Redacted, 0, 0, capture.CapturedAt); err != nil {
			return err
		}
	}
	return nil
}

func (s *UIEvidenceService) captureDiagnosticArtifacts(ctx context.Context,
	prepared preparedUIEvidence, stepID string,
	diagnostics browserruntime.UIEvidenceDiagnostics,
) error {
	policy := prepared.request.Capture
	if policy.Console {
		content, err := json.Marshal(struct {
			Console           []browserruntime.UIEvidenceConsoleEntry `json:"console"`
			PageErrors        []browserruntime.UIEvidencePageError    `json:"page_errors"`
			Summary           uievidence.DiagnosticsSummary           `json:"summary"`
			UntrustedEvidence bool                                    `json:"untrusted_evidence"`
			CapturedAt        time.Time                               `json:"captured_at"`
		}{Console: diagnostics.Console, PageErrors: diagnostics.PageErrors,
			Summary: diagnostics.Summary, UntrustedEvidence: true,
			CapturedAt: diagnostics.CapturedAt})
		if err != nil {
			return err
		}
		if err := s.addArtifact(ctx, prepared, stepID, uievidence.ArtifactConsole,
			"application/json", content, true, 0, 0,
			diagnostics.CapturedAt); err != nil {
			return err
		}
	}
	if policy.Network {
		content, err := json.Marshal(struct {
			Network           []browserruntime.UIEvidenceNetworkEntry `json:"network"`
			Summary           uievidence.DiagnosticsSummary           `json:"summary"`
			UntrustedEvidence bool                                    `json:"untrusted_evidence"`
			CapturedAt        time.Time                               `json:"captured_at"`
		}{Network: diagnostics.Network, Summary: diagnostics.Summary,
			UntrustedEvidence: true, CapturedAt: diagnostics.CapturedAt})
		if err != nil {
			return err
		}
		if err := s.addArtifact(ctx, prepared, stepID, uievidence.ArtifactNetwork,
			"application/json", content, true, 0, 0,
			diagnostics.CapturedAt); err != nil {
			return err
		}
	}
	return nil
}

func (s *UIEvidenceService) addArtifact(ctx context.Context,
	prepared preparedUIEvidence, stepID string, kind uievidence.ArtifactKind,
	mime string, content []byte, redacted bool, width, height int, at time.Time,
) error {
	artifact, err := uievidence.SealArtifact(uievidence.ArtifactMetadata{
		ID: idgen.New("ui_artifact"), AttemptID: prepared.attempt.Manifest.AttemptID,
		RunID: prepared.run.ID, StepID: stepID, Kind: kind, MIME: mime,
		Width: width, Height: height, Viewport: prepared.request.Environment.Viewport,
		SourceCommit:    prepared.attempt.Manifest.Source.Commit,
		RetentionPolicy: uievidence.ArtifactRetentionRunHistory, Redacted: redacted,
		Untrusted: true, CreatedAt: at.UTC()}, content)
	if err != nil {
		return err
	}
	return s.store.AddUIEvidenceArtifact(ctx, artifact)
}

func (s *UIEvidenceService) cleanup(ctx context.Context,
	prepared preparedUIEvidence, resources *uiEvidenceResources,
) error {
	var cleanupErrors []error
	if resources.browser != nil {
		receipt, err := s.browsers.Close(ctx, resources.browser)
		if err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
		resources.cleanup.BrowserTreeReaped = receipt.ProcessTreeQuiescent
		resources.cleanup.ProfileRemoved = receipt.ProfileReleased && receipt.ProfileCleaned
		resources.cleanup.NetworkReleased = receipt.NetworkCleanupVerified
	}
	if len(resources.applicationJobs) > 0 {
		allReaped := true
		for index := len(resources.applicationJobs) - 1; index >= 0; index-- {
			job, err := s.stopCommand(ctx, resources.applicationJobs[index])
			if err != nil {
				cleanupErrors = append(cleanupErrors, err)
			}
			allReaped = allReaped && err == nil && job.TreeReaped
		}
		resources.cleanup.ApplicationTreeReaped = allReaped
	}
	// Port release is an ownership assertion, not a general availability check.
	// A pre-existing listener rejected before application start is intentionally
	// left alone and must not turn the stable launch failure into cleanup_failed.
	if !resources.cleanup.PortReleased {
		if err := waitUIEvidencePortReleased(ctx, prepared.request.Readiness.URL); err != nil {
			cleanupErrors = append(cleanupErrors, err)
			resources.cleanup.PortReleased = false
		} else {
			resources.cleanup.PortReleased = true
		}
	}
	return errors.Join(cleanupErrors...)
}

func (s *UIEvidenceService) commandScope(prepared preparedUIEvidence,
	suffix string,
) toolgateway.CommandRuntimeContext {
	identity := uiEvidenceRuntimeIdentity(prepared, suffix)
	return toolgateway.CommandRuntimeContext{
		InvocationID: identity,
		OperationKey: identity,
		RunID:        prepared.run.ID, MissionID: prepared.mission.ID,
		RootAgentID: prepared.root.ID, AgentID: prepared.root.ID,
		SessionID: prepared.run.SessionID, WorkspaceID: prepared.mission.WorkspaceID,
		CapabilityGeneration: prepared.adapter.Generation,
		LeaseID:              prepared.lease.LeaseID, LeaseGeneration: prepared.lease.Generation,
		RequestedBy: toolgateway.CommandRuntimeRequestedByUIEvidenceOperator,
		PolicyDecision: toolgateway.Decision{
			Allowed: true, Approval: toolgateway.ApprovalAutomatic, Risk: "high",
			Reason: "exact ui-evidence.v1 recipe passed the Run execution boundary"},
		Adapter: prepared.adapter}
}

func (s *UIEvidenceService) startCommand(ctx context.Context,
	prepared preparedUIEvidence, spec runner.CommandRuntimeSpec, suffix string,
) (runner.CommandRuntimeJobSnapshot, error) {
	result, err := s.commands.ExecuteCommandRuntime(ctx, s.commandScope(prepared, suffix),
		toolgateway.CommandRuntimeInput{Version: toolgateway.CommandRuntimeToolProtocolVersion,
			Action:   toolgateway.CommandRuntimeActionStart,
			Commands: []runner.CommandRuntimeSpec{spec}})
	if err != nil || len(result.Jobs) != 1 {
		if err == nil {
			err = errors.New("command runtime did not return one Job")
		}
		return runner.CommandRuntimeJobSnapshot{}, err
	}
	return result.Jobs[0], nil
}

func (s *UIEvidenceService) waitCommand(ctx context.Context,
	prepared preparedUIEvidence, jobID, suffix string,
) (runner.CommandRuntimeJobSnapshot, error) {
	for {
		cursor, maxBytes, wait := uint64(0), runner.MinCommandRuntimeOutputRead, 1000
		result, err := s.commands.ExecuteCommandRuntime(ctx, s.commandScope(prepared, suffix),
			toolgateway.CommandRuntimeInput{Version: toolgateway.CommandRuntimeToolProtocolVersion,
				Action: toolgateway.CommandRuntimeActionWait, JobID: jobID,
				Cursor: &cursor, MaxBytes: &maxBytes, WaitMilliseconds: &wait})
		if err != nil || len(result.Jobs) != 1 {
			if err == nil {
				err = errors.New("command runtime wait returned no Job")
			}
			return runner.CommandRuntimeJobSnapshot{}, err
		}
		if result.Jobs[0].State.Terminal() {
			return result.Jobs[0], nil
		}
	}
}

func uiEvidenceRuntimeIdentity(prepared preparedUIEvidence, suffix string) string {
	return prepared.attempt.Manifest.AttemptID + ":" + suffix
}

func (s *UIEvidenceService) commandCleanupBinding(prepared preparedUIEvidence,
	suffix, jobID string,
) uiEvidenceCommandCleanupBinding {
	identity := uiEvidenceRuntimeIdentity(prepared, suffix)
	return uiEvidenceCommandCleanupBinding{JobID: jobID,
		InvocationID: identity, OperationKey: identity,
		RunID: prepared.run.ID, MissionID: prepared.mission.ID,
		SessionID: prepared.run.SessionID, WorkspaceID: prepared.mission.WorkspaceID,
		RootAgentID: prepared.root.ID, LeaseID: prepared.lease.LeaseID,
		LeaseGeneration: prepared.lease.Generation}
}

func (s *UIEvidenceService) readCommand(ctx context.Context,
	prepared preparedUIEvidence, jobID string,
) (runner.CommandRuntimeJobSnapshot, error) {
	cursor, maxBytes, wait := uint64(0), runner.MinCommandRuntimeOutputRead, 0
	result, err := s.commands.ExecuteCommandRuntime(ctx,
		s.commandScope(prepared, "readiness-read"), toolgateway.CommandRuntimeInput{
			Version: toolgateway.CommandRuntimeToolProtocolVersion,
			Action:  toolgateway.CommandRuntimeActionRead, JobID: jobID,
			Cursor: &cursor, MaxBytes: &maxBytes, WaitMilliseconds: &wait})
	if err != nil || len(result.Jobs) != 1 {
		if err == nil {
			err = errors.New("command runtime read returned no Job")
		}
		return runner.CommandRuntimeJobSnapshot{}, err
	}
	return result.Jobs[0], nil
}

func (s *UIEvidenceService) stopCommand(ctx context.Context,
	binding uiEvidenceCommandCleanupBinding,
) (runner.CommandRuntimeJobSnapshot, error) {
	return s.commands.cleanupUIEvidenceJob(ctx, binding)
}

func (s *UIEvidenceService) waitReadiness(ctx context.Context,
	prepared preparedUIEvidence, jobID string,
) error {
	contract := prepared.request.Readiness
	deadline := time.NewTimer(time.Duration(contract.TimeoutMilliseconds) * time.Millisecond)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Duration(contract.IntervalMilliseconds) * time.Millisecond)
	defer ticker.Stop()
	client, err := newUIEvidenceHTTPClient(contract.URL)
	if err != nil {
		return err
	}
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, contract.URL, nil)
		if err != nil {
			return err
		}
		response, requestErr := client.Do(request)
		ready := false
		if requestErr == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
			_ = response.Body.Close()
			for _, expected := range contract.ExpectedStatus {
				if response.StatusCode == expected {
					ready = true
					break
				}
			}
		}
		job, readErr := s.readCommand(ctx, prepared, jobID)
		if readErr != nil {
			return readErr
		}
		if job.State.Terminal() {
			return fmt.Errorf("application Job exited before readiness with state %s", job.State)
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("UI evidence readiness deadline expired")
		case <-ticker.C:
		}
	}
}

func newUIEvidenceHTTPClient(rawURL string) (*http.Client, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	expected := parsed.Host
	dialer := &net.Dialer{Timeout: time.Second}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if network != "tcp" || !strings.EqualFold(address, expected) {
				return nil, errors.New("UI evidence readiness attempted to leave its literal loopback endpoint")
			}
			return dialer.DialContext(ctx, network, address)
		}}
	return &http.Client{Transport: transport, Timeout: 2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("UI evidence readiness redirects are forbidden")
		}}, nil
}

func ensureUIEvidencePortFree(ctx context.Context, rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	connection, err := (&net.Dialer{Timeout: 200 * time.Millisecond}).
		DialContext(ctx, "tcp", parsed.Host)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if uiEvidenceConnectionRefused(err) {
			return nil
		}
		return fmt.Errorf("UI evidence cannot prove the application port is free: %w", err)
	}
	_ = connection.Close()
	return errors.New("UI evidence refuses to adopt a pre-existing service")
}

func waitUIEvidencePortReleased(ctx context.Context, rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	deadline := time.NewTimer(uiEvidencePortReleaseWait)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		connection, dialErr := (&net.Dialer{Timeout: 100 * time.Millisecond}).
			DialContext(ctx, "tcp", parsed.Host)
		if dialErr != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		if uiEvidenceConnectionRefused(dialErr) {
			return nil
		}
		if connection != nil {
			_ = connection.Close()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("UI evidence application port remained open after cleanup")
		case <-ticker.C:
		}
	}
}

func uiEvidenceRecipeHasNetworkIntent(spec runner.CommandRuntimeSpec) bool {
	value := strings.ToLower(spec.Script + " " + spec.Executable + " " +
		strings.Join(spec.Arguments, " "))
	base := strings.ToLower(filepath.Base(spec.Executable))
	for _, executable := range []string{"curl", "curl.exe", "wget", "wget.exe",
		"ssh", "ssh.exe", "scp", "scp.exe", "sftp", "ftp", "nc", "netcat",
		"nmap", "telnet", "ping", "ping.exe"} {
		if base == executable {
			return true
		}
	}
	for _, marker := range []string{"http://", "https://", "invoke-webrequest",
		"invoke-restmethod", "test-netconnection", "start-bitstransfer", "git clone",
		"git fetch", "git pull", "git push", "git ls-remote", "npm install",
		"pnpm install", "yarn install", "go get", "cargo install", "pip install"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

var _ toolgateway.CommandRuntimeExecutor = (*CommandRuntimeService)(nil)
var _ UIEvidenceBrowserDriver = (*browserruntime.RestrictedBrowserSession)(nil)
