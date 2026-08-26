//go:build windows

package desktop

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/artifact"
	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/credential"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/drydock"
	"cyberagent-workbench/internal/httpapi"
	"cyberagent-workbench/internal/packagede2e"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/sandbox"
	"cyberagent-workbench/internal/toolgateway"
)

//go:embed testdata/standard-code-security/probe.go.txt
var standardCodeSecurityProbe []byte

const standardCodeSecurityProbeProtocol = "standard_code_security_probe.v1"

type standardCodeSecurityDriver struct {
	dockerDigest         string
	config               packagede2e.SecurityDriverConfig
	local                sandbox.LocalBackend
	plane                *ControlPlane
	gateway              *toolgateway.Gateway
	readToken            string
	goExecutable         string
	localProbeExecutable string
	outsideSentinel      string
	available            map[string]bool
	runs                 map[string]*standardCodeSecurityRun
	recoveryJobsStarted  int
	recoveryJobsReaped   int
	opened               bool
	closed               bool
}

type standardCodeSecurityRun struct {
	backend     string
	fixtureID   string
	workspaceID string
	run         domain.Run
	root        domain.AgentNode
	lease       domain.RunExecutionLease
	adapter     commandruntimeadapter.Identity
}

type standardCodeSecurityProbeReceipt struct {
	Protocol string `json:"protocol"`
	Case     string `json:"case"`
	Blocked  bool   `json:"blocked"`
	Detail   string `json:"detail"`
}

type standardCodeSecurityObservation struct {
	outcome  toolgateway.Outcome
	job      runner.CommandRuntimeJob
	stdout   *artifact.Blob
	stderr   *artifact.Blob
	receipt  standardCodeSecurityProbeReceipt
	recovery *standardCodeSecurityRecoveryReceipt
	observed bool
	detail   string
}

// NewStandardCodeSecurityDriver returns the fixed packaged-only #181 driver.
// It accepts no command, environment, workspace, permission, or host path from
// the caller. The Docker value is an immutable digest already admitted by the
// existing Standard Code runtime.
func NewStandardCodeSecurityDriver(dockerDigest string) packagede2e.SecurityMatrixDriver {
	return &standardCodeSecurityDriver{dockerDigest: strings.TrimSpace(dockerDigest),
		available: make(map[string]bool), runs: make(map[string]*standardCodeSecurityRun)}
}

func (d *standardCodeSecurityDriver) Open(ctx context.Context,
	config packagede2e.SecurityDriverConfig,
) ([]packagede2e.SecurityBackendEvidence, error) {
	if d == nil || d.opened || d.closed || ctx == nil || ctx.Err() != nil {
		return nil, errors.New("packaged security driver lifecycle is invalid")
	}
	runtimeRoot, err := filepath.Abs(config.OwnedRuntimeRoot)
	if err != nil || filepath.Clean(runtimeRoot) != runtimeRoot ||
		!pathInside(runtimeRoot, config.FixtureRoot) {
		return nil, errors.New("packaged security driver roots are invalid")
	}
	d.config = config
	d.goExecutable, err = exec.LookPath("go")
	if err != nil {
		return nil, fmt.Errorf("resolve fixed Go probe toolchain: %w", err)
	}
	d.goExecutable, err = filepath.Abs(d.goExecutable)
	if err != nil {
		return nil, err
	}
	d.localProbeExecutable, err = prepareStandardCodeSecurityLocalProbe(ctx,
		runtimeRoot, d.goExecutable)
	if err != nil {
		return nil, fmt.Errorf("build fixed Local Sandbox probe: %w", err)
	}
	d.outsideSentinel, err = prepareStandardCodeSecurityOutsideSentinel(runtimeRoot)
	if err != nil {
		return nil, fmt.Errorf("create fixed outside sentinel: %w", err)
	}
	d.local, err = sandbox.NewPlatformLocalBackend(sandbox.WithLocalOwnerRoot(
		filepath.Join(runtimeRoot, "local-owners")))
	if err != nil {
		return nil, fmt.Errorf("open packaged Local Sandbox: %w", err)
	}
	readiness, err := d.local.Readiness(ctx, sandbox.LocalRuntimeCapabilities{Enabled: true})
	if err != nil || !readiness.Ready {
		_ = d.local.Close()
		d.local = nil
		return nil, fmt.Errorf("prove packaged Local Sandbox readiness: %w", err)
	}
	d.readToken, err = randomDesktopSecurityToken()
	if err != nil {
		_ = d.local.Close()
		return nil, err
	}
	controlToken, err := randomDesktopSecurityToken()
	if err != nil {
		_ = d.local.Close()
		return nil, err
	}
	home := filepath.Join(runtimeRoot, "desktop-home")
	if err := os.Mkdir(home, 0o700); err != nil {
		_ = d.local.Close()
		return nil, err
	}
	d.plane, err = OpenControlPlane(ControlPlaneConfig{
		DatabasePath: filepath.Join(home, "security-matrix.db"), HomePath: home,
		ReadToken: d.readToken, ControlToken: controlToken,
		RunControlEnabled: true, RunCreationEnabled: true,
		ExecutionPermissionControlEnabled: true,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{
			WorkspaceSandboxEnabled: true, OperatorApprovalEnabled: true,
		},
		LocalSandboxReadiness: &readiness, LocalSandboxBackend: d.local,
		RunLifecycleEnabled: true, RunExecutionEnabled: true,
		PlanDeliveryControlEnabled: true, ApprovalControlEnabled: true,
		DockerExecutionEnabled:        d.dockerDigest != "",
		StandardCodeDockerImageDigest: d.dockerDigest,
		CredentialStore:               credential.NewMemoryStore(), AppVersion: "packaged-security-181",
	})
	if err != nil {
		_ = d.local.Close()
		d.local = nil
		return nil, fmt.Errorf("open packaged Desktop control plane: %w", err)
	}
	if d.plane.commandRuntime == nil || d.plane.standardCodeDrydocks == nil ||
		d.plane.standardCodePreset == nil || d.plane.policyChecker == nil {
		_ = d.plane.Close()
		_ = d.local.Close()
		return nil, errors.New("packaged Standard Code product path is incomplete")
	}
	d.gateway = toolgateway.New(d.plane.stateStore, d.plane.policyChecker).
		WithCommandRuntimeExecutor(d.plane.commandRuntime)

	adapters := d.plane.commandRuntime.InstalledCommandRuntimeAdapters()
	byMatrixBackend := make(map[string]commandruntimeadapter.Identity)
	for _, adapter := range adapters {
		switch adapter.Backend {
		case application.CommandRuntimeLocalSandboxBackend:
			byMatrixBackend["local"] = adapter
		case application.CommandRuntimeDockerSandboxBackend:
			byMatrixBackend["docker"] = adapter
		}
	}
	backends := make([]packagede2e.SecurityBackendEvidence, 0, 2)
	for _, name := range []string{"local", "docker"} {
		adapter, ready := byMatrixBackend[name]
		d.available[name] = ready
		identityHash, generationHash := hashSecurityValue(struct {
			Backend string                         `json:"backend"`
			Adapter commandruntimeadapter.Identity `json:"adapter"`
		}{Backend: name, Adapter: adapter}), hashSecurityValue(adapter.Generation)
		if !ready {
			identityHash = hashSecurityValue(struct {
				Backend string `json:"backend"`
				State   string `json:"state"`
			}{name, "unavailable"})
			generationHash = hashSecurityValue(struct {
				Backend string `json:"backend"`
				State   string `json:"state"`
			}{name, "no-generation"})
		}
		evidence := packagede2e.SecurityBackendEvidence{Backend: name,
			Availability:   packagede2e.SecurityBackendReady,
			IdentitySHA256: identityHash, GenerationSHA256: generationHash,
			Network: "disabled", Credentials: "none", FullAccessEnabled: false}
		if !ready {
			evidence.Availability = packagede2e.SecurityBackendUnavailable
			evidence.UnavailableSignal = "approval_required"
			evidence.ApprovalFallback = true
		}
		backends = append(backends, evidence)
	}
	d.opened = true
	return backends, nil
}

func (d *standardCodeSecurityDriver) Execute(ctx context.Context,
	request packagede2e.SecurityDriverCase,
) (packagede2e.SecurityCaseBackendEvidence, error) {
	started := time.Now().UTC()
	base := packagede2e.SecurityCaseBackendEvidence{Status: packagede2e.SecurityEvidenceFailed,
		ActualOutcome: "deny", ActualSignal: "failed_precondition",
		OperatorCode:   "standard_code.attack.failed_precondition",
		DiagnosticCode: "product.execution_failed", ActualExecution: true,
		StartedAt: started}
	if d == nil || !d.opened || d.closed || ctx == nil || ctx.Err() != nil ||
		!d.available[request.Backend] {
		base.ActualExecution = false
		base.CompletedAt = time.Now().UTC()
		return base, errors.New("packaged security backend is unavailable")
	}
	runInstance := ""
	if request.Attack.ID == "output_artifact_limit" {
		// This case deliberately leaves an over-limit tree behind so the product
		// checkpoint can retain the denial evidence. Keep it out of Runs reused by
		// later recovery cases; the packaged harness owns and removes the root.
		runInstance = request.Attack.ID
	}
	run, err := d.securityRun(ctx, request.Backend, request.FixtureID, runInstance)
	if err != nil {
		base.CompletedAt = time.Now().UTC()
		return base, err
	}
	observation, err := d.runProbe(ctx, run, request)
	if err == nil {
		err = d.verifyCaseSpecificBoundary(ctx, run, request, &observation)
	}
	refs, evidenceErr := d.securityEvidence(ctx, run, request, observation)
	base.Evidence = refs
	if err == nil {
		err = evidenceErr
	}
	if err == nil && observation.observed {
		base.Status = packagede2e.SecurityEvidencePassed
		base.ActualOutcome = request.Attack.ExpectedOutcome
		base.ActualSignal = request.Attack.ExpectedSignal
		base.OperatorCode = "standard_code.attack." + request.Attack.ExpectedSignal
		base.DiagnosticCode = "matrix." + request.Attack.ID
	}
	base.CompletedAt = time.Now().UTC()
	return base, err
}

func (d *standardCodeSecurityDriver) securityRun(ctx context.Context, backend,
	fixtureID, instance string,
) (*standardCodeSecurityRun, error) {
	key := backend + "/" + fixtureID
	operationSuffix := backend + "-" + fixtureID
	if instance != "" {
		key += "/" + instance
		operationSuffix += "-" + instance
	}
	if cached := d.runs[key]; cached != nil {
		return cached, nil
	}
	fixtureRoot := filepath.Join(d.config.FixtureRoot, fixtureID)
	workspace, err := d.plane.RegisterWorkspaceDirectory(ctx, fixtureRoot)
	if err != nil {
		return nil, err
	}
	goal := "execute the fixed packaged Standard Code security matrix"
	previewKey := "issue181-preview-" + operationSuffix
	preview, err := d.plane.standardCodePreset.Configure(ctx,
		application.ConfigureStandardCodeRequest{
			Version: domain.StandardCodePresetProtocolVersion, WorkspaceID: workspace.ID,
			Goal: goal, BackendIntent: backend, Action: "configure",
			OperationKey: previewKey, RequestedBy: "operator",
		})
	if err != nil || !preview.TrustRequired || preview.TrustDigest == "" {
		return nil, fmt.Errorf("preview fixed Standard Code workspace trust: %w", err)
	}
	configured, err := d.plane.standardCodePreset.Configure(ctx,
		application.ConfigureStandardCodeRequest{
			Version: domain.StandardCodePresetProtocolVersion, WorkspaceID: workspace.ID,
			Goal: goal, BackendIntent: backend, Action: "configure",
			OperationKey: "issue181-configure-" + operationSuffix,
			RequestedBy:  "operator", ConfirmWorkspaceTrust: true,
			ExpectedTrustDigest: preview.TrustDigest,
		})
	if err != nil || configured.Status != application.StandardCodeResultConfigured ||
		configured.Run == nil || configured.Permission == nil ||
		configured.Permission.Mode != domain.RunExecutionPermissionWorkspaceAccess ||
		configured.CapabilityGrant {
		return nil, fmt.Errorf("configure fixed Standard Code workspace: %w", err)
	}
	workspaceState, found, err := d.plane.stateStore.GetDrydockByRun(ctx, configured.RunID)
	if err != nil || !found || workspaceState.State != drydock.StateReady {
		return nil, fmt.Errorf("load configured Standard Code Drydock: %w", err)
	}
	initialCheckpoint := workspaceState.LastCheckpointID
	probeDirectory := filepath.Join(workspaceState.Path, ".standard-code-security")
	if err := os.Mkdir(probeDirectory, 0o700); err != nil {
		return nil, err
	}
	probePath := filepath.Join(probeDirectory, "probe.go")
	if err := writeExclusiveSecurityFile(probePath, standardCodeSecurityProbe); err != nil {
		return nil, err
	}
	_ = os.Symlink(d.outsideSentinel, filepath.Join(probeDirectory, "outside-link"))
	checkpoint, err := d.plane.standardCodeDrydocks.Checkpoint(ctx,
		application.DrydockCheckpointRequest{RunID: configured.RunID,
			ExpectedGeneration: workspaceState.Generation,
			OperationKey:       "issue181-probe-checkpoint-" + operationSuffix,
			RequestedBy:        "operator", Title: "Fixed packaged security probe",
			ConfirmObservedChanges: true})
	if err != nil {
		return nil, err
	}
	runs := application.NewRunService(d.plane.stateStore)
	if _, err := runs.ChangePhase(ctx, application.ChangeRunPhaseRequest{
		RunID: configured.RunID, Phase: string(domain.ExecutionPhaseDeliver),
		OperationKey: "issue181-deliver-" + operationSuffix,
		RequestedBy:  "operator", Reason: "execute frozen packaged security matrix",
	}); err != nil {
		return nil, err
	}
	runRecord, err := runs.Start(ctx, configured.RunID)
	if err != nil {
		return nil, err
	}
	root, found, err := d.plane.stateStore.GetRootAgent(ctx, runRecord.ID)
	if err != nil || !found {
		return nil, fmt.Errorf("load Standard Code root Agent: %w", err)
	}
	acquired, err := d.plane.stateStore.AcquireRunExecutionLease(ctx,
		domain.AcquireRunExecutionLeaseRequest{RunID: runRecord.ID,
			OwnerID: "packaged-security-181-" + operationSuffix,
			TTL:     2 * time.Hour})
	if err != nil {
		return nil, err
	}
	adapter, available, err := d.plane.commandRuntime.AdvertisedCommandRuntimeAdapter(
		ctx, runRecord.ID, domain.RunExecutionPermissionWorkspaceAccess)
	if err != nil || !available {
		return nil, fmt.Errorf("advertise exact Standard Code adapter: %w", err)
	}
	result := &standardCodeSecurityRun{backend: backend, fixtureID: fixtureID,
		workspaceID: workspace.ID, run: runRecord, root: root,
		lease: acquired.Lease, adapter: adapter}
	if checkpoint.Workspace.LastCheckpointID == initialCheckpoint {
		return nil, errors.New("fixed security probe checkpoint did not advance")
	}
	d.runs[key] = result
	return result, nil
}

func (d *standardCodeSecurityDriver) runProbe(ctx context.Context,
	run *standardCodeSecurityRun, request packagede2e.SecurityDriverCase,
) (observation standardCodeSecurityObservation, returnErr error) {
	probeArgument, cleanup, verifyHostBoundary, err :=
		d.prepareStandardCodeSecurityProbeBoundary(request)
	if err != nil {
		return standardCodeSecurityObservation{}, err
	}
	defer func() {
		if cleanup != nil {
			returnErr = errors.Join(returnErr, cleanup())
		}
	}()
	artifactBytes := 128 * 1024
	if strings.HasPrefix(request.Attack.ID, "output_") {
		artifactBytes = 64 * 1024
	}
	maxBytes := 32 * 1024
	executable := d.goExecutable
	arguments := []string{"run", ".standard-code-security/probe.go",
		request.Attack.ID, probeArgument}
	if request.Backend == "local" {
		executable = d.localProbeExecutable
		arguments = []string{request.Attack.ID, probeArgument}
	}
	input := toolgateway.CommandRuntimeInput{
		Version:       toolgateway.CommandRuntimeToolProtocolVersion,
		Action:        toolgateway.CommandRuntimeActionRun,
		FailurePolicy: toolgateway.CommandRuntimeFailFast, MaxBytes: &maxBytes,
		Commands: []runner.CommandRuntimeSpec{{
			Version: runner.CommandRuntimeProtocolVersion,
			Profile: runner.CommandRuntimeProcess, Executable: executable,
			Arguments:        arguments,
			WorkingDirectory: ".", Environment: []runner.CommandRuntimeEnvironment{},
			StdinPolicy: runner.CommandRuntimeStdinClosed, CloseInitialStdin: true,
			TimeoutMilliseconds: 24_000,
			Output: runner.CommandRuntimeOutputPolicy{InlineBytes: 32 * 1024,
				ArtifactBytes: artifactBytes},
			Network:     runner.CommandRuntimeNetworkDisabled,
			Credentials: runner.CommandRuntimeCredentialsNone,
			Purpose:     "execute one fixed packaged Standard Code matrix probe",
		}},
	}
	call, err := d.commandRuntimeCall(ctx, run, request.Ordinal, input)
	if err != nil {
		return standardCodeSecurityObservation{}, err
	}
	outcome, err := d.gateway.Invoke(ctx, call)
	if err != nil {
		return standardCodeSecurityObservation{}, err
	}
	observation = standardCodeSecurityObservation{outcome: outcome}
	if outcome.Result == nil || outcome.Result.Status != toolgateway.StatusCompleted {
		return observation, errors.New("fixed probe did not produce a completed tool receipt")
	}
	jobID := outcome.Result.Metadata["job_1_id"]
	if jobID == "" {
		return observation, errors.New("fixed probe omitted its durable Job identity")
	}
	observation.job, err = d.plane.stateStore.GetCommandRuntimeJob(ctx, jobID)
	if err != nil {
		return observation, err
	}
	if id := outcome.Result.Metadata["job_1_artifact_stdout_id"]; id != "" {
		blob, blobErr := d.plane.stateStore.GetRunArtifact(ctx, id)
		if blobErr != nil {
			return observation, blobErr
		}
		observation.stdout = &blob
	}
	if id := outcome.Result.Metadata["job_1_artifact_stderr_id"]; id != "" {
		blob, blobErr := d.plane.stateStore.GetRunArtifact(ctx, id)
		if blobErr != nil {
			return observation, blobErr
		}
		observation.stderr = &blob
	}
	if !observation.job.State.Terminal() || !observation.job.TreeReaped ||
		observation.job.Network != runner.CommandRuntimeNetworkDisabled ||
		observation.job.Credentials != runner.CommandRuntimeCredentialsNone {
		return observation, errors.New("fixed probe process boundary is incomplete")
	}
	observation.receipt = parseSecurityProbeReceipt(observation.stdout, request.Attack.ID)
	observation.observed = observation.receipt.Protocol == standardCodeSecurityProbeProtocol &&
		observation.receipt.Case == request.Attack.ID && observation.receipt.Blocked
	observation.detail = observation.receipt.Detail
	if verifyHostBoundary != nil {
		if err := verifyHostBoundary(); err != nil {
			return observation, err
		}
	}
	return observation, nil
}

func (d *standardCodeSecurityDriver) commandRuntimeCall(ctx context.Context,
	run *standardCodeSecurityRun, ordinal int, input toolgateway.CommandRuntimeInput,
) (toolgateway.ToolCall, error) {
	mode, err := d.plane.stateStore.GetRunMode(ctx, run.run.ID)
	if err != nil {
		return toolgateway.ToolCall{}, err
	}
	permission, err := d.plane.stateStore.GetRunExecutionPermission(ctx, run.run.ID)
	if err != nil {
		return toolgateway.ToolCall{}, err
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return toolgateway.ToolCall{}, err
	}
	return toolgateway.ToolCall{Name: toolgateway.CommandRuntimeTool,
		Arguments: map[string]string{}, Payload: payload,
		OperationKey: fmt.Sprintf("issue181-command-%03d", ordinal),
		RunID:        run.run.ID, MissionID: run.run.MissionID, AgentID: run.root.ID,
		SessionID: run.run.SessionID, WorkspaceID: run.workspaceID,
		Surface: mode.Surface, Phase: mode.Phase, Role: run.root.Role,
		Profile: mode.Profile, PermissionMode: permission.Mode,
		ModeRevision: mode.Revision, PermissionRevision: permission.Revision,
		CapabilityGeneration: run.adapter.Generation,
		LeaseID:              run.lease.LeaseID, LeaseGeneration: run.lease.Generation,
		RequestedBy: "run_supervisor", CommandRuntimeAdapter: run.adapter}, nil
}

func (d *standardCodeSecurityDriver) verifyCaseSpecificBoundary(ctx context.Context,
	run *standardCodeSecurityRun, request packagede2e.SecurityDriverCase,
	observation *standardCodeSecurityObservation,
) error {
	if observation == nil {
		return errors.New("security observation is required")
	}
	switch request.Attack.Category {
	case "process_escape":
		if request.Attack.ID != "process_inherited_handle" {
			time.Sleep(1700 * time.Millisecond)
			workspace, found, err := d.plane.stateStore.GetDrydockByRun(ctx, run.run.ID)
			if err != nil || !found {
				return errors.New("reload process-escape Drydock")
			}
			marker := filepath.Join(workspace.Path, ".standard-code-security",
				request.Attack.ID+".marker")
			if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
				return errors.New("owned child escaped the command runtime tree")
			}
		}
	case "output_safety":
		switch request.Attack.ID {
		case "output_secret_redaction":
			observation.observed = observation.stdout != nil && observation.stderr != nil &&
				observation.stdout.Redacted && observation.stderr.Redacted &&
				!strings.Contains(observation.stdout.Content, "Issue181SyntheticSentinel") &&
				!strings.Contains(observation.stderr.Content, "Issue181SyntheticSentinel")
		case "output_control_ansi":
			observation.observed = observation.stdout != nil &&
				!strings.ContainsAny(observation.stdout.Content, "\x1b\r") &&
				!strings.ContainsRune(observation.stdout.Content, '\u202e')
		case "output_stream_limit", "output_artifact_limit":
			if request.Attack.ID == "output_stream_limit" {
				observation.observed = observation.observed &&
					observation.job.TruncationReason == "inline_window" &&
					observation.job.StdoutObservedBytes > int64(observation.job.InlineLimitBytes) &&
					observation.job.StdoutObservedBytes <= int64(observation.job.ArtifactLimitBytes)
			} else {
				observation.observed = observation.observed &&
					observation.receipt.Detail == "workspace-growth-submitted" &&
					observation.job.State == runner.CommandRuntimeJobFailed &&
					observation.job.ExitCode != nil && *observation.job.ExitCode == 0
			}
		}
	case "authority_replay":
		return d.verifyStaleAuthority(ctx, run, request)
	case "prompt_injection":
		if request.Attack.ID == "prompt_forged_receipt" {
			return d.verifyStaleAuthority(ctx, run, request)
		}
		if err := d.verifyPromptInjectionDenial(ctx, run, request); err != nil {
			return err
		}
		observation.observed = observation.observed &&
			!observation.outcome.Call.CommandRuntimeAdapter.IsZero()
	case "approval_fallback":
		if err := d.verifyApprovalFallback(ctx, run, request); err != nil {
			return err
		}
		observation.observed = observation.observed &&
			!run.adapter.AllowsPermission(domain.RunExecutionPermissionFullAccess)
	case "recovery":
		receipt, err := d.verifyPackagedRecovery(ctx, request)
		if err != nil {
			return err
		}
		observation.recovery = &receipt
		observation.observed = observation.observed && receipt.Status == "passed" &&
			receipt.Signal == request.Attack.ExpectedSignal && receipt.TreeReaped &&
			receipt.Preserved && receipt.RecoveryCycles == 2
	}
	if !observation.observed {
		return errors.New("fixed product observation did not prove the expected boundary")
	}
	return nil
}

func (d *standardCodeSecurityDriver) verifyPromptInjectionDenial(ctx context.Context,
	run *standardCodeSecurityRun, request packagede2e.SecurityDriverCase,
) error {
	before, err := d.plane.stateStore.ListCommandRuntimeJobs(ctx,
		runner.CommandRuntimeListFilter{Limit: 500})
	if err != nil {
		return err
	}
	outcome, err := d.gateway.Invoke(ctx, toolgateway.ToolCall{
		Name:      toolgateway.ShellTool,
		Arguments: map[string]string{"command": "masscan 0.0.0.0/0"},
		RunID:     run.run.ID, MissionID: run.run.MissionID, AgentID: run.root.ID,
		SessionID: run.run.SessionID, WorkspaceID: run.workspaceID,
		LeaseID: run.lease.LeaseID, LeaseGeneration: run.lease.Generation,
		RequestedBy: "run_supervisor",
	})
	after, listErr := d.plane.stateStore.ListCommandRuntimeJobs(ctx,
		runner.CommandRuntimeListFilter{Limit: 500})
	if err != nil || listErr != nil || len(after) != len(before) ||
		outcome.Decision.Allowed || outcome.Decision.Approval != toolgateway.ApprovalNever ||
		outcome.Result == nil || outcome.Result.Status != toolgateway.StatusDenied ||
		outcome.Execution != nil {
		return errors.Join(err, listErr,
			errors.New("untrusted repository prompt did not remain a non-executing policy denial"))
	}
	return nil
}

func (d *standardCodeSecurityDriver) verifyApprovalFallback(ctx context.Context,
	run *standardCodeSecurityRun, request packagede2e.SecurityDriverCase,
) error {
	maxBytes := 4096
	input := toolgateway.CommandRuntimeInput{Version: toolgateway.CommandRuntimeToolProtocolVersion,
		Action:        toolgateway.CommandRuntimeActionRun,
		FailurePolicy: toolgateway.CommandRuntimeFailFast, MaxBytes: &maxBytes,
		Commands: []runner.CommandRuntimeSpec{{Version: runner.CommandRuntimeProtocolVersion,
			Profile: runner.CommandRuntimeProcess, Executable: d.goExecutable,
			Arguments: []string{"version"}, WorkingDirectory: ".",
			Environment: []runner.CommandRuntimeEnvironment{},
			StdinPolicy: runner.CommandRuntimeStdinClosed, CloseInitialStdin: true,
			TimeoutMilliseconds: 5000,
			Output:              runner.CommandRuntimeOutputPolicy{InlineBytes: 4096, ArtifactBytes: 4096},
			Network:             runner.CommandRuntimeNetworkDisabled,
			Credentials:         runner.CommandRuntimeCredentialsNone,
			Purpose:             "prove unavailable packaged adapter fails closed"}}}
	stale, err := d.commandRuntimeCall(ctx, run, 2000+request.Ordinal, input)
	if err != nil {
		return err
	}
	stale.CapabilityGeneration = strings.Repeat("0", 64)
	before, err := d.plane.stateStore.ListCommandRuntimeJobs(ctx,
		runner.CommandRuntimeListFilter{Limit: 500})
	if err != nil {
		return err
	}
	if _, err := d.gateway.Invoke(ctx, stale); err == nil ||
		apperror.CodeOf(apperror.Normalize(err)) != apperror.CodeConflict {
		return errors.New("unavailable packaged adapter did not fail with a stable conflict")
	}
	proposal, proposalErr := d.gateway.Invoke(ctx, toolgateway.ToolCall{
		Name:      toolgateway.ShellTool,
		Arguments: map[string]string{"command": "go version"},
		RunID:     run.run.ID, MissionID: run.run.MissionID, AgentID: run.root.ID,
		SessionID: run.run.SessionID, WorkspaceID: run.workspaceID,
		LeaseID: run.lease.LeaseID, LeaseGeneration: run.lease.Generation,
		RequestedBy: "run_supervisor",
	})
	after, listErr := d.plane.stateStore.ListCommandRuntimeJobs(ctx,
		runner.CommandRuntimeListFilter{Limit: 500})
	if proposalErr != nil || listErr != nil || len(after) != len(before) ||
		proposal.Proposal == nil || proposal.Proposal.Status != toolgateway.StatusProposed ||
		proposal.Execution != nil || proposal.Result != nil ||
		(proposal.Decision.Approval != toolgateway.ApprovalPerCall &&
			proposal.Decision.Approval != toolgateway.ApprovalSession) ||
		run.adapter.AllowsPermission(domain.RunExecutionPermissionFullAccess) {
		return errors.Join(proposalErr, listErr,
			errors.New("backend failure did not remain an explicit unapproved host proposal"))
	}
	return nil
}

func (d *standardCodeSecurityDriver) verifyStaleAuthority(ctx context.Context,
	run *standardCodeSecurityRun, request packagede2e.SecurityDriverCase,
) error {
	maxBytes := 4096
	input := toolgateway.CommandRuntimeInput{Version: toolgateway.CommandRuntimeToolProtocolVersion,
		Action:        toolgateway.CommandRuntimeActionRun,
		FailurePolicy: toolgateway.CommandRuntimeFailFast, MaxBytes: &maxBytes,
		Commands: []runner.CommandRuntimeSpec{{Version: runner.CommandRuntimeProtocolVersion,
			Profile: runner.CommandRuntimeProcess, Executable: d.goExecutable,
			Arguments: []string{"version"}, WorkingDirectory: ".",
			Environment: []runner.CommandRuntimeEnvironment{},
			StdinPolicy: runner.CommandRuntimeStdinClosed, CloseInitialStdin: true,
			TimeoutMilliseconds: 5000,
			Output:              runner.CommandRuntimeOutputPolicy{InlineBytes: 4096, ArtifactBytes: 4096},
			Network:             runner.CommandRuntimeNetworkDisabled,
			Credentials:         runner.CommandRuntimeCredentialsNone,
			Purpose:             "prove stale packaged authority fails closed"}}}
	call, err := d.commandRuntimeCall(ctx, run, 1000+request.Ordinal, input)
	if err != nil {
		return err
	}
	switch request.Attack.ID {
	case "authority_stale_permission":
		call.PermissionRevision++
	case "authority_stale_profile":
		call.ModeRevision++
	case "authority_stale_root":
		call.LeaseGeneration++
	case "authority_stale_backend":
		call.CapabilityGeneration = strings.Repeat("0", 64)
	case "authority_cross_run_replay", "prompt_forged_receipt":
		other := d.otherSecurityRun(run)
		if other == nil {
			return errors.New("cross-Run security replay has no second real Run")
		}
		call.MissionID = other.run.MissionID
	default:
		call.PermissionRevision++
	}
	before, err := d.plane.stateStore.ListCommandRuntimeJobs(ctx,
		runner.CommandRuntimeListFilter{Limit: 500})
	if err != nil {
		return err
	}
	_, invokeErr := d.gateway.Invoke(ctx, call)
	after, listErr := d.plane.stateStore.ListCommandRuntimeJobs(ctx,
		runner.CommandRuntimeListFilter{Limit: 500})
	if listErr != nil {
		return listErr
	}
	if invokeErr == nil || len(after) != len(before) {
		return errors.New("stale packaged authority reached process execution")
	}
	code := apperror.CodeOf(apperror.Normalize(invokeErr))
	if code != apperror.CodeConflict {
		return fmt.Errorf("stale packaged authority returned unstable category %q", code)
	}
	return nil
}

func (d *standardCodeSecurityDriver) otherSecurityRun(
	current *standardCodeSecurityRun,
) *standardCodeSecurityRun {
	if d == nil || current == nil {
		return nil
	}
	keys := make([]string, 0, len(d.runs))
	for key := range d.runs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		candidate := d.runs[key]
		if candidate != nil && candidate.backend == current.backend &&
			candidate.run.ID != current.run.ID {
			return candidate
		}
	}
	return nil
}

func (d *standardCodeSecurityDriver) securityEvidence(ctx context.Context,
	run *standardCodeSecurityRun, request packagede2e.SecurityDriverCase,
	observation standardCodeSecurityObservation,
) ([]packagede2e.SecurityEvidenceRef, error) {
	events, err := d.plane.stateStore.ListRunEvents(ctx, run.run.ID)
	if err != nil {
		return nil, err
	}
	projection, err := d.plane.standardCodeDrydocks.Projection(ctx, run.run.ID, 50)
	if err != nil {
		return nil, err
	}
	workspace, found, err := d.plane.stateStore.GetDrydockByRun(ctx, run.run.ID)
	if err != nil || !found {
		return nil, errors.New("security evidence Drydock is unavailable")
	}
	checkpoint, err := d.plane.stateStore.GetWorkspaceCheckpointSnapshot(ctx,
		workspace.LastCheckpointID)
	if err != nil {
		return nil, err
	}
	uiHash, err := d.operatorProjectionHash(run.run.ID)
	if err != nil {
		return nil, err
	}
	values := map[string]struct {
		Source string
		Hash   string
	}{
		"operator_ui":      {"desktop.projection", uiHash},
		"immutable_event":  {"run.event", hashSecurityValue(events)},
		"workspace_digest": {"drydock.observation", hashSecurityValue(projection)},
		"process_receipt":  {"command_runtime.receipt", hashSecurityValue(observation.job)},
		"network_observation": {"sandbox.network", hashSecurityValue(struct {
			Adapter commandruntimeadapter.Identity `json:"adapter"`
			Job     runner.CommandRuntimeJob       `json:"job"`
		}{run.adapter, observation.job})},
		"artifact_digest": {"artifact.store", hashSecurityValue(struct {
			Stdout *artifact.Blob `json:"stdout,omitempty"`
			Stderr *artifact.Blob `json:"stderr,omitempty"`
		}{observation.stdout, observation.stderr})},
		"thread_transcript": {"thread.projection", hashSecurityValue(events)},
		"checkpoint":        {"workspace.checkpoint", hashSecurityValue(checkpoint)},
	}
	if observation.recovery != nil {
		values["immutable_event"] = struct {
			Source string
			Hash   string
		}{"recovery.event", observation.recovery.EventSHA256}
		values["process_receipt"] = struct {
			Source string
			Hash   string
		}{"recovery.command_runtime", observation.recovery.JobSHA256}
		values["checkpoint"] = struct {
			Source string
			Hash   string
		}{"recovery.checkpoint", observation.recovery.CheckpointSHA256}
		values["workspace_digest"] = struct {
			Source string
			Hash   string
		}{"recovery.drydock", observation.recovery.WorkspaceSHA256}
		values["thread_transcript"] = struct {
			Source string
			Hash   string
		}{"recovery.transcript", observation.recovery.TranscriptSHA256}
	}
	refs := make([]packagede2e.SecurityEvidenceRef, 0,
		len(request.Attack.RequiredEvidence))
	for _, kind := range request.Attack.RequiredEvidence {
		value, ok := values[kind]
		if !ok || value.Hash == "" {
			return nil, fmt.Errorf("security evidence kind %q is unavailable", kind)
		}
		refs = append(refs, packagede2e.SecurityEvidenceRef{Kind: kind,
			Source: value.Source, SHA256: value.Hash})
	}
	return refs, nil
}

func (d *standardCodeSecurityDriver) operatorProjectionHash(runID string) (string, error) {
	request := httptest.NewRequest(http.MethodGet,
		"http://127.0.0.1/api/v1/runs/"+runID+"/capability-readiness", nil)
	request.Host = "127.0.0.1:8765"
	request.RemoteAddr = "127.0.0.1:45678"
	request.Header.Set("Authorization", "Bearer "+d.readToken)
	response := httptest.NewRecorder()
	d.plane.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		return "", fmt.Errorf("operator projection status %d", response.Code)
	}
	return hashSecurityValue(json.RawMessage(response.Body.Bytes())), nil
}

func (d *standardCodeSecurityDriver) Close(ctx context.Context) (
	packagede2e.SecurityCleanupEvidence, error,
) {
	cleanup := packagede2e.SecurityCleanupEvidence{OwnedRootSHA256: hashSecurityValue(
		struct {
			Owner string `json:"owner"`
		}{"standard-code-attack-181"}),
		OwnedProcessesStarted: d.recoveryJobsStarted,
		OwnedProcessesReaped:  d.recoveryJobsReaped, OwnedDirectoriesOnly: true}
	if d == nil || !d.opened || d.closed || ctx == nil {
		return cleanup, errors.New("packaged security driver close lifecycle is invalid")
	}
	var closeErr error
	jobs, err := d.plane.stateStore.ListCommandRuntimeJobs(ctx,
		runner.CommandRuntimeListFilter{Limit: 500})
	if err != nil {
		closeErr = errors.Join(closeErr, errors.New("list harness-owned command runtime Jobs"))
	}
	cleanup.OwnedProcessesStarted += len(jobs)
	for _, job := range jobs {
		if job.State.Terminal() && job.TreeReaped {
			cleanup.OwnedProcessesReaped++
		} else {
			closeErr = errors.Join(closeErr, fmt.Errorf("owned Job cleanup %s", job.ID))
		}
	}
	for _, run := range d.runs {
		_, _, _ = d.plane.stateStore.ReleaseRunExecutionLease(ctx, run.lease)
		_, _ = application.NewRunService(d.plane.stateStore).Pause(ctx, run.run.ID)
	}
	closeErr = errors.Join(closeErr, d.plane.Close())
	closeErr = errors.Join(closeErr, d.local.Close())
	d.closed = true
	return cleanup, closeErr
}

func prepareStandardCodeSecurityLocalProbe(ctx context.Context, runtimeRoot,
	goExecutable string,
) (string, error) {
	buildRoot := filepath.Join(runtimeRoot, "local-probe-build")
	toolchainRoot := filepath.Join(runtimeRoot, "local-probe-toolchain")
	if err := os.Mkdir(buildRoot, 0o700); err != nil {
		return "", err
	}
	if err := os.Mkdir(toolchainRoot, 0o700); err != nil {
		return "", err
	}
	source := filepath.Join(buildRoot, "probe.go")
	if err := writeExclusiveSecurityFile(source, standardCodeSecurityProbe); err != nil {
		return "", err
	}
	output := filepath.Join(toolchainRoot, "standard-code-security-probe.exe")
	command := exec.CommandContext(ctx, goExecutable, "build", "-trimpath",
		"-buildvcs=false", "-o", output, source)
	configureStandardCodeSecuritySubprocess(command)
	command.Dir = buildRoot
	command.Env = append(standardCodeSecurityWorkerEnvironment(""),
		"GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off")
	combined, err := command.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(combined))
		if len(detail) > 4096 {
			detail = detail[:4096]
		}
		return "", fmt.Errorf("compile fixed probe: %w: %s", err, detail)
	}
	info, err := os.Lstat(output)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() <= 0 || info.Size() > 64<<20 {
		return "", errors.New("compiled fixed probe is unavailable, indirect, or oversized")
	}
	return output, nil
}

func prepareStandardCodeSecurityOutsideSentinel(runtimeRoot string) (string, error) {
	directory := filepath.Join(runtimeRoot, "outside")
	if err := os.Mkdir(directory, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(directory, "sentinel.txt")
	if err := writeExclusiveSecurityFile(path, []byte("issue181-owned-sentinel\n")); err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() != int64(len("issue181-owned-sentinel\n")) {
		return "", errors.New("fixed outside sentinel is unavailable or indirect")
	}
	return path, nil
}

func parseSecurityProbeReceipt(blob *artifact.Blob,
	caseID string,
) standardCodeSecurityProbeReceipt {
	if blob == nil {
		return standardCodeSecurityProbeReceipt{}
	}
	lines := strings.Split(blob.Content, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		var value standardCodeSecurityProbeReceipt
		if json.Unmarshal([]byte(lines[index]), &value) == nil &&
			value.Protocol == standardCodeSecurityProbeProtocol && value.Case == caseID {
			return value
		}
	}
	return standardCodeSecurityProbeReceipt{}
}

func hashSecurityValue(value any) string {
	raw, _ := json.Marshal(value)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func randomDesktopSecurityToken() (string, error) {
	// The HTTP package owns the exact token entropy and formatting contract.
	return httpapi.GenerateAccessToken()
}

func writeExclusiveSecurityFile(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(content); err == nil {
		err = file.Sync()
	}
	return errors.Join(err, file.Close())
}

func pathInside(parent, child string) bool {
	parent, parentErr := filepath.Abs(parent)
	child, childErr := filepath.Abs(child)
	if parentErr != nil || childErr != nil {
		return false
	}
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
