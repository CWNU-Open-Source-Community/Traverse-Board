package producte2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/drydock"
	"cyberagent-workbench/internal/fileedit"
	"cyberagent-workbench/internal/packagede2e"
	"cyberagent-workbench/internal/repository"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/standardcodedelivery"
	"cyberagent-workbench/internal/store"
)

const maximumManualEvidenceBytes int64 = 128 << 20

type ProduceOptions struct {
	Home          string
	EvidenceRoot  string
	Candidate     CandidateEvidence
	Fixture       FixtureEvidence
	Runbook       Runbook
	RunbookSHA256 string
	GeneratedAt   time.Time
}

type productStore struct {
	state    *store.SQLiteStore
	delivery *application.StandardCodeDeliveryService
	handoff  *application.CodeHandoffService
}

type runFacts struct {
	run        domain.Run
	mission    domain.Mission
	session    session.Session
	preset     domain.StandardCodePresetOperation
	permission domain.RunExecutionPermissionSnapshot
	supervisor domain.StandardCodeSupervisorSnapshot
	ledger     []domain.StandardCodeSupervisorLedgerEntry
	jobs       []runner.CommandRuntimeJob
	edits      []fileedit.Edit
	drydock    drydock.Workspace
	trust      drydock.Trust
	delivery   standardcodedelivery.Report
	handoff    application.CodeHandoff
	thread     domain.Thread
	threadRuns []domain.ThreadRun
	messages   []session.Message
	codeRoute  string
}

func Produce(ctx context.Context, options ProduceOptions) (Report, error) {
	if ctx == nil {
		return Report{}, errors.New("product E2E context is required")
	}
	if err := options.Runbook.Validate(); err != nil {
		return Report{}, err
	}
	if options.Runbook.CandidateSHA256 != options.Candidate.BinarySHA256 ||
		options.Runbook.FixtureManifestSHA256 != options.Fixture.ManifestSHA256 ||
		!validDigest(options.RunbookSHA256) {
		return Report{}, errors.New("runbook is not bound to the candidate and fixture oracle")
	}
	home, err := productHome(options.Home)
	if err != nil {
		return Report{}, err
	}
	evidenceRoot, err := regularDirectory(options.EvidenceRoot, "product evidence root")
	if err != nil {
		return Report{}, err
	}
	if err := validateEvidenceFiles(evidenceRoot, options.Runbook); err != nil {
		return Report{}, err
	}
	product, err := openProductStore(home)
	if err != nil {
		return Report{}, err
	}
	defer product.state.Close()
	definition, err := packagede2e.LoadDefinition()
	if err != nil {
		return Report{}, err
	}
	repositories := make(map[string]packagede2e.FixtureRepository,
		len(definition.Manifest.Repositories))
	for _, value := range definition.Manifest.Repositories {
		repositories[value.ID] = value
	}
	report := Report{ProtocolVersion: ReportProtocol, Issue: IssueNumber, Status: "pass",
		GeneratedAt: options.GeneratedAt.UTC(), Candidate: options.Candidate,
		Fixture: options.Fixture, RunbookSHA256: options.RunbookSHA256,
		Safeguards: Safeguards{NetworkDisabled: true, CredentialsAbsent: true}}
	if report.GeneratedAt.IsZero() {
		report.GeneratedAt = time.Now().UTC()
	}
	factsByRun := map[string]runFacts{}
	for _, backend := range options.Runbook.Backends {
		summary := BackendSummary{Backend: backend.Backend, State: backend.State}
		if backend.State == "approval_required" {
			fallback, validateErr := product.validateFallback(ctx, backend.Backend,
				*backend.Fallback)
			if validateErr != nil {
				return Report{}, validateErr
			}
			summary.ApprovalID = fallback.ApprovalID
			summary.FallbackReason = fallback.ReasonCode
			summary.EvidenceSHA256 = fallback.ReadinessEvidenceSHA
			report.Backends = append(report.Backends, summary)
			continue
		}
		for _, scenario := range backend.Runs {
			facts, collectErr := product.collectRun(ctx, scenario.RunID)
			if collectErr != nil {
				return Report{}, fmt.Errorf("scenario %q: %w", scenario.ID, collectErr)
			}
			scenarioSummary, validateErr := validateRunFacts(facts, scenario,
				backend.Backend, repositories[scenario.Language])
			if validateErr != nil {
				return Report{}, fmt.Errorf("scenario %q: %w", scenario.ID, validateErr)
			}
			factsByRun[scenario.RunID] = facts
			report.Scenarios = append(report.Scenarios, scenarioSummary)
			summary.PassedRuns++
			report.Coverage.RealFailureRetries += scenarioSummary.FailedJobs
			report.Coverage.RealProcessJobs += scenarioSummary.FailedJobs +
				scenarioSummary.PassedJobs
		}
		report.Backends = append(report.Backends, summary)
	}
	if err := validateEdgeFacts(options.Runbook.Edges, factsByRun, repositories); err != nil {
		return Report{}, err
	}
	for _, evidence := range options.Runbook.Continuity {
		summary, validateErr := product.validateContinuity(ctx, evidence)
		if validateErr != nil {
			return Report{}, validateErr
		}
		report.Continuity = append(report.Continuity, summary)
	}
	for _, evidence := range options.Runbook.Platforms {
		report.Platforms = append(report.Platforms, PlatformSummary{ID: evidence.ID,
			OS: evidence.OS, Build: evidence.Build, DPIPercent: evidence.DPIPercent,
			EvidenceSHA256: evidence.EvidenceSHA256})
	}
	report.Coverage = Coverage{Languages: append([]string(nil), requiredLanguages...),
		Backends:           append([]string(nil), requiredBackends...),
		Surfaces:           append([]string(nil), requiredSurfaces...),
		EdgeCases:          append([]string(nil), requiredEdgeCases...),
		ContinuityCases:    append([]string(nil), requiredContinuityCases...),
		OperatingSystems:   []string{"windows_10", "windows_11"},
		DPIPercents:        []int{100, 200},
		RealFailureRetries: report.Coverage.RealFailureRetries,
		RealProcessJobs:    report.Coverage.RealProcessJobs}
	sort.Slice(report.Backends, func(i, j int) bool {
		return report.Backends[i].Backend < report.Backends[j].Backend
	})
	sort.Slice(report.Scenarios, func(i, j int) bool {
		if report.Scenarios[i].Language == report.Scenarios[j].Language {
			return report.Scenarios[i].Backend < report.Scenarios[j].Backend
		}
		return report.Scenarios[i].Language < report.Scenarios[j].Language
	})
	sort.Slice(report.Continuity, func(i, j int) bool {
		return report.Continuity[i].Case < report.Continuity[j].Case
	})
	sort.Slice(report.Platforms, func(i, j int) bool {
		if report.Platforms[i].OS == report.Platforms[j].OS {
			return report.Platforms[i].DPIPercent < report.Platforms[j].DPIPercent
		}
		return report.Platforms[i].OS < report.Platforms[j].OS
	})
	return report.Seal()
}

func openProductStore(home string) (*productStore, error) {
	state, err := store.Open(filepath.Join(home, "cyberagent.db"))
	if err != nil {
		return nil, fmt.Errorf("open candidate store: %w", err)
	}
	executor, err := repository.NewDrydockExecutor(filepath.Join(home, "drydocks"))
	if err != nil {
		_ = state.Close()
		return nil, err
	}
	drydocks, err := application.NewDrydockService(state, executor)
	if err != nil {
		_ = state.Close()
		return nil, err
	}
	checkpoints, err := application.NewWorkspaceCheckpointService(state,
		domain.ExecutionPermissionRuntimeCapabilities{})
	if err != nil {
		_ = state.Close()
		return nil, err
	}
	drydocks.WithCheckpointService(checkpoints)
	delivery, err := application.NewStandardCodeDeliveryService(state, drydocks)
	if err != nil {
		_ = state.Close()
		return nil, err
	}
	handoff := application.NewCodeHandoffService(state).WithStandardCodeDelivery(delivery)
	return &productStore{state: state, delivery: delivery, handoff: handoff}, nil
}

func (p *productStore) collectRun(ctx context.Context, runID string) (runFacts, error) {
	var facts runFacts
	var err error
	if facts.run, err = p.state.GetRun(ctx, runID); err != nil {
		return runFacts{}, err
	}
	var found bool
	if facts.codeRoute, found, err = p.state.GetProviderSetting(ctx, "route.code"); err != nil || !found || strings.TrimSpace(facts.codeRoute) == "" {
		return runFacts{}, errors.Join(err, errors.New("configured Provider code route is missing"))
	}
	if facts.mission, err = p.state.GetMission(ctx, facts.run.MissionID); err != nil {
		return runFacts{}, err
	}
	if facts.session, err = p.state.GetSession(ctx, facts.run.SessionID); err != nil {
		return runFacts{}, err
	}
	if facts.preset, found, err = p.state.GetConfiguredStandardCodePresetOperation(ctx,
		runID); err != nil || !found {
		return runFacts{}, errors.Join(err, errors.New("configured Standard Code preset is missing"))
	}
	if facts.permission, err = p.state.GetRunExecutionPermission(ctx, runID); err != nil {
		return runFacts{}, err
	}
	if facts.supervisor, found, err = p.state.GetStandardCodeSupervisorSnapshot(ctx,
		runID); err != nil || !found {
		return runFacts{}, errors.Join(err, errors.New("Standard Code Supervisor evidence is missing"))
	}
	if facts.ledger, err = p.state.ListStandardCodeSupervisorLedger(ctx, runID,
		domain.StandardCodeSupervisorMaximumLedgerEntries); err != nil {
		return runFacts{}, err
	}
	if facts.jobs, err = p.state.ListCommandRuntimeJobs(ctx,
		runner.CommandRuntimeListFilter{RunID: runID, Limit: runner.MaxCommandRuntimeJobsPerRun}); err != nil {
		return runFacts{}, err
	}
	if facts.drydock, found, err = p.state.GetDrydockByRun(ctx, runID); err != nil || !found {
		return runFacts{}, errors.Join(err, errors.New("Run-owned Drydock is missing"))
	}
	if facts.trust, found, err = p.state.GetDrydockTrustByRun(ctx, runID); err != nil || !found {
		return runFacts{}, errors.Join(err, errors.New("Workspace Trust evidence is missing"))
	}
	if facts.edits, err = p.state.ListFileEdits(ctx, fileedit.ListFilter{
		SessionID: facts.run.SessionID, WorkspaceID: facts.drydock.WorkspaceID}); err != nil {
		return runFacts{}, err
	}
	if facts.delivery, found, err = p.delivery.Current(ctx, runID); err != nil || !found {
		return runFacts{}, errors.Join(err, errors.New("current delivery receipt is missing"))
	}
	if facts.handoff, err = p.handoff.Build(ctx, runID); err != nil {
		return runFacts{}, err
	}
	if facts.thread, err = p.state.GetThreadByRun(ctx, runID); err != nil {
		return runFacts{}, err
	}
	if facts.threadRuns, err = p.state.ListThreadRuns(ctx, facts.thread.ID); err != nil {
		return runFacts{}, err
	}
	if facts.messages, err = p.state.ListSessionMessages(ctx, facts.run.SessionID, true); err != nil {
		return runFacts{}, err
	}
	return facts, nil
}

func validateRunFacts(facts runFacts, evidence RunEvidence, backend string,
	fixture packagede2e.FixtureRepository,
) (ScenarioSummary, error) {
	if facts.run.Validate() != nil || facts.mission.Validate() != nil ||
		facts.session.Validate() != nil || facts.preset.Validate() != nil ||
		facts.permission.Validate() != nil || facts.supervisor.Validate() != nil ||
		facts.drydock.Validate() != nil || facts.trust.Validate() != nil ||
		facts.delivery.Validate() != nil || facts.thread.Validate() != nil {
		return ScenarioSummary{}, errors.New("one or more persisted product facts are invalid")
	}
	if facts.run.ID != evidence.RunID || facts.run.Status != domain.RunCompleted ||
		facts.run.MissionID != facts.mission.ID || facts.run.SessionID != facts.session.ID ||
		(facts.run.Config.ModelRoute != "code" &&
			facts.run.Config.ModelRoute != facts.codeRoute) ||
		facts.mission.Profile != domain.ProfileCode || facts.mission.Scope.NetworkMode != "disabled" ||
		facts.mission.WorkspaceID != facts.preset.WorkspaceID ||
		facts.preset.RunID != facts.run.ID || facts.preset.MissionID != facts.mission.ID ||
		facts.preset.DrydockID != facts.drydock.ID ||
		facts.preset.DrydockGeneration != facts.drydock.Generation ||
		string(facts.preset.SelectedBackend) != backend ||
		facts.preset.Status != domain.StandardCodePresetConfigured ||
		facts.permission.RunID != facts.run.ID || facts.permission.MissionID != facts.mission.ID ||
		facts.permission.Mode != domain.RunExecutionPermissionWorkspaceAccess ||
		facts.permission.ProcessEnabled || facts.permission.ExecutionAuthorized ||
		facts.permission.CapabilityGrant {
		return ScenarioSummary{}, errors.New("Run, preset, or workspace-access binding is invalid")
	}
	if facts.drydock.RunID != facts.run.ID || facts.drydock.MissionID != facts.mission.ID ||
		facts.drydock.SessionID != facts.session.ID ||
		facts.drydock.SourceWorkspaceID != facts.mission.WorkspaceID ||
		facts.drydock.Source.WorkspaceID != facts.mission.WorkspaceID ||
		facts.drydock.Source.BaseCommit != fixture.ExpectedHead ||
		facts.drydock.BaseCommit != fixture.ExpectedHead ||
		(facts.drydock.State != drydock.StateReady && facts.drydock.State != drydock.StateDelivered) ||
		facts.trust.ID != facts.drydock.TrustID || facts.trust.RunID != facts.run.ID ||
		facts.trust.WorkspaceID != facts.mission.WorkspaceID ||
		facts.trust.Source.BaseCommit != fixture.ExpectedHead ||
		facts.trust.GrantsProcessAuthority {
		return ScenarioSummary{}, errors.New("Drydock or Workspace Trust escaped the fixed fixture")
	}
	sourceHead, err := gitValue(facts.drydock.Source.RootPath, "rev-parse", "HEAD")
	if err != nil || sourceHead != fixture.ExpectedHead {
		return ScenarioSummary{}, errors.New("source Workspace HEAD was overwritten")
	}
	if !facts.supervisor.InspectionComplete ||
		facts.supervisor.RunID != facts.run.ID || facts.supervisor.MissionID != facts.mission.ID ||
		facts.supervisor.WorkspaceID != facts.mission.WorkspaceID ||
		facts.supervisor.PresetOperationKeyDigest != facts.preset.KeyDigest ||
		facts.supervisor.PermissionSnapshotID != facts.permission.ID ||
		facts.supervisor.PermissionRevision != facts.permission.Revision ||
		facts.supervisor.ConsecutiveReadRounds < domain.StandardCodeSupervisorMinimumReadRounds ||
		facts.supervisor.State != domain.StandardCodeSupervisorDeliver ||
		facts.supervisor.MutationEpoch < 2 ||
		facts.supervisor.VerifiedMutationEpoch != facts.supervisor.MutationEpoch ||
		facts.supervisor.FixRounds < 1 || facts.supervisor.CommandsUsed < 2 ||
		facts.supervisor.DeliveryReceiptSHA256 != facts.delivery.ReceiptSHA256 ||
		facts.supervisor.DeliveryCheckpointID != facts.delivery.FinalCheckpoint.ID {
		return ScenarioSummary{}, errors.New("bounded Standard Code fail/diagnose/fix/deliver proof is incomplete")
	}
	readRounds, mutations, diagnosed, finished := 0, 0, false, false
	for _, entry := range facts.ledger {
		if err := entry.Validate(); err != nil || entry.Snapshot.RunID != facts.run.ID {
			return ScenarioSummary{}, errors.New("Standard Code ledger is invalid or cross-Run")
		}
		if entry.Snapshot.ConsecutiveReadRounds > readRounds {
			readRounds = entry.Snapshot.ConsecutiveReadRounds
		}
		if entry.Kind == domain.StandardCodeSupervisorCallObserved &&
			entry.ToolKind == domain.StandardCodeToolWorkspaceMutation &&
			entry.ResultStatus == domain.SupervisorToolCompleted {
			mutations++
		}
		if entry.ToState == domain.StandardCodeSupervisorDiagnose &&
			entry.ReasonCode == "command_verification_failed" {
			diagnosed = true
		}
		if entry.Kind == domain.StandardCodeSupervisorActionRecorded &&
			entry.Decision == domain.StandardCodeSupervisorAllowed &&
			entry.ToolAction == string(domain.RootActionFinish) {
			finished = true
		}
	}
	if readRounds < domain.StandardCodeSupervisorMinimumReadRounds || mutations < 2 ||
		!diagnosed || !finished {
		return ScenarioSummary{}, errors.New("read, reviewed mutation, diagnosis, or finish ledger proof is missing")
	}
	applied := 0
	for _, edit := range facts.edits {
		if edit.SessionID != facts.run.SessionID || edit.WorkspaceID != facts.drydock.WorkspaceID {
			return ScenarioSummary{}, errors.New("FileEdit escaped the Run-owned Workspace")
		}
		if edit.Status == fileedit.StatusApplied {
			applied++
		}
	}
	if applied < 2 {
		return ScenarioSummary{}, errors.New("at least two separately reviewed applies are required")
	}
	failedJobs, passedJobs, artifactCount, err := validateCommandJobs(facts, fixture, backend)
	if err != nil {
		return ScenarioSummary{}, err
	}
	if err := validateDeliveryFacts(facts, evidence.Projections, backend); err != nil {
		return ScenarioSummary{}, err
	}
	return ScenarioSummary{ID: evidence.ID, Language: evidence.Language, Backend: backend,
		RunID: facts.run.ID, ThreadID: facts.thread.ID, SessionID: facts.run.SessionID,
		FixtureHead: fixture.ExpectedHead, ReadRounds: readRounds, AppliedEdits: applied,
		FailedJobs: failedJobs, PassedJobs: passedJobs, FixRounds: facts.supervisor.FixRounds,
		ArtifactCount: artifactCount, ProjectionCount: len(evidence.Projections),
		ReceiptSHA256: facts.delivery.ReceiptSHA256, DiffSHA256: facts.delivery.Diff.SHA256,
		CheckpointID:        facts.delivery.FinalCheckpoint.ID,
		WorkspaceRevision:   facts.delivery.FinalCheckpoint.RevisionSHA256,
		SourceWorkPreserved: true}, nil
}

func validateEvidenceFiles(root string, runbook Runbook) error {
	type binding struct {
		name   string
		path   string
		digest string
	}
	bindings := []binding{{name: "zero-argument launch", path: runbook.DefaultLaunch.EvidencePath,
		digest: runbook.DefaultLaunch.EvidenceSHA256}}
	for _, backend := range runbook.Backends {
		if backend.Fallback != nil {
			bindings = append(bindings,
				binding{name: backend.Backend + " readiness", path: backend.Fallback.ReadinessEvidencePath,
					digest: backend.Fallback.ReadinessEvidenceSHA},
				binding{name: backend.Backend + " Approval UI", path: backend.Fallback.UIEvidencePath,
					digest: backend.Fallback.UIEvidenceSHA256})
		}
		for _, run := range backend.Runs {
			for _, projection := range run.Projections {
				bindings = append(bindings, binding{name: run.ID + "/" + projection.Surface,
					path: projection.EvidencePath, digest: projection.EvidenceSHA256})
			}
		}
	}
	for _, evidence := range runbook.Continuity {
		bindings = append(bindings, binding{name: "continuity/" + evidence.Case,
			path: evidence.EvidencePath, digest: evidence.EvidenceSHA256})
	}
	for _, evidence := range runbook.Platforms {
		bindings = append(bindings, binding{name: "platform/" + evidence.ID,
			path: evidence.EvidencePath, digest: evidence.EvidenceSHA256})
	}
	seen := map[string]bool{}
	for _, current := range bindings {
		if seen[current.path] {
			return fmt.Errorf("product evidence path %q is reused", current.path)
		}
		seen[current.path] = true
		target, err := withinRoot(root, current.path)
		if err != nil {
			return fmt.Errorf("%s evidence: %w", current.name, err)
		}
		digest, _, err := fileDigestLimit(target, maximumManualEvidenceBytes)
		if err != nil || digest != current.digest {
			return fmt.Errorf("%s evidence digest differs", current.name)
		}
		if current.name == "zero-argument launch" {
			if err := validateLaunchRecord(target, runbook.CandidateSHA256); err != nil {
				return err
			}
		}
	}
	return nil
}

type launchRecord struct {
	ProtocolVersion  string    `json:"protocol_version"`
	CandidateSHA256  string    `json:"candidate_sha256"`
	ExecutableName   string    `json:"executable_name"`
	ExecutableSHA256 string    `json:"executable_sha256"`
	Arguments        []string  `json:"arguments"`
	ProcessID        int       `json:"process_id"`
	StartedAt        time.Time `json:"started_at"`
}

func validateLaunchRecord(path, candidate string) error {
	content, err := readBoundedFile(path, maximumManualEvidenceBytes)
	if err != nil {
		return err
	}
	var record launchRecord
	if err := decodeStrictJSON(content, &record); err != nil {
		return fmt.Errorf("decode zero-argument launch record: %w", err)
	}
	if record.ProtocolVersion != "standard_code_product_launch.v1" ||
		record.CandidateSHA256 != candidate || record.ExecutableSHA256 != candidate ||
		record.ExecutableName != "TraverseBoard.exe" ||
		len(record.Arguments) != 0 || record.ProcessID <= 0 || record.StartedAt.IsZero() {
		return errors.New("zero-argument launch record is invalid or candidate-mismatched")
	}
	return nil
}

type commandIntent struct {
	Version          string   `json:"version"`
	Profile          string   `json:"profile"`
	ExecutablePath   string   `json:"executable_path"`
	Argv             []string `json:"argv"`
	WorkingDirectory string   `json:"working_directory"`
	Network          string   `json:"network"`
	Credentials      string   `json:"credentials"`
}

func validateCommandJobs(facts runFacts, fixture packagede2e.FixtureRepository,
	backend string,
) (int, int, int, error) {
	failed, passed, artifacts := 0, 0, 0
	var failureAt time.Time
	var successAt time.Time
	jobsByID := map[string]runner.CommandRuntimeJob{}
	for _, job := range facts.jobs {
		if err := job.Validate(); err != nil || job.RunID != facts.run.ID ||
			job.SessionID != facts.run.SessionID || job.WorkspaceID != facts.drydock.WorkspaceID {
			return 0, 0, 0, errors.New("Command Runtime Job is invalid or cross-Run")
		}
		var intent commandIntent
		if err := json.Unmarshal([]byte(job.IntentJSON), &intent); err != nil {
			return 0, 0, 0, errors.New("Command Runtime intent is invalid")
		}
		if !commandMatchesFixture(job, intent, fixture) {
			continue
		}
		if job.Adapter.Kind != commandruntimeadapter.KindSandboxedWorkspace ||
			job.Adapter.Backend != backend ||
			job.Network != runner.CommandRuntimeNetworkDisabled ||
			job.Credentials != runner.CommandRuntimeCredentialsNone ||
			job.PermissionMode != domain.RunExecutionPermissionWorkspaceAccess ||
			!job.State.Terminal() || !job.TreeReaped || job.ExitCode == nil ||
			job.TruncationReason != "" || job.StartedAt == nil || job.CompletedAt == nil {
			return 0, 0, 0, errors.New("verification Job is not a complete sandboxed real-process receipt")
		}
		jobsByID[job.ID] = job
		if *job.ExitCode == 0 && job.State == runner.CommandRuntimeJobCompleted {
			passed++
			if successAt.IsZero() || job.CompletedAt.After(successAt) {
				successAt = *job.CompletedAt
			}
		} else if *job.ExitCode != 0 {
			failed++
			if failureAt.IsZero() || job.CompletedAt.Before(failureAt) {
				failureAt = *job.CompletedAt
			}
		}
	}
	if failed < 1 || passed < 1 || failureAt.IsZero() || successAt.IsZero() ||
		!failureAt.Before(successAt) {
		return 0, 0, 0, errors.New("real failure followed by a passing retry was not observed")
	}
	for _, verification := range facts.delivery.Verifications {
		job, found := jobsByID[verification.JobID]
		if !found || verification.Conclusion != standardcodedelivery.StatusPassed ||
			verification.ExitCode == nil || *verification.ExitCode != 0 ||
			verification.State != string(runner.CommandRuntimeJobCompleted) ||
			!verification.CurrentRevision || verification.OutputTruncated ||
			!verification.TreeReaped || verification.SpecSHA256 != job.SpecFingerprint ||
			verification.StdoutSHA256 != job.StdoutSHA256 ||
			verification.StderrSHA256 != job.StderrSHA256 ||
			verification.Backend != backend || len(verification.Artifacts) == 0 {
			return 0, 0, 0, errors.New("delivery verification is not bound to a passing real Job")
		}
		artifacts += len(verification.Artifacts)
	}
	return failed, passed, artifacts, nil
}

func commandMatchesFixture(job runner.CommandRuntimeJob, intent commandIntent,
	fixture packagede2e.FixtureRepository,
) bool {
	executable := strings.TrimSuffix(strings.ToLower(filepath.Base(job.ExecutablePath)), ".exe")
	expected := fixture.Command.Executable
	if expected == "python" && executable == "python3" {
		executable = "python"
	}
	return intent.Version == runner.CommandRuntimeProtocolVersion &&
		intent.Profile == string(runner.CommandRuntimeProcess) &&
		intent.ExecutablePath == job.ExecutablePath && job.Profile == runner.CommandRuntimeProcess &&
		executable == expected && reflect.DeepEqual(intent.Argv, fixture.Command.Arguments) &&
		intent.WorkingDirectory == "." && job.WorkingDirectory == "." &&
		intent.Network == string(runner.CommandRuntimeNetworkDisabled) &&
		intent.Credentials == string(runner.CommandRuntimeCredentialsNone)
}

func validateDeliveryFacts(facts runFacts, projections []SurfaceProjection,
	backend string,
) error {
	report := facts.delivery
	checkpointBase := "/api/v1/runs/" + url.PathEscape(facts.run.ID) +
		"/workspace-checkpoints"
	expectedLinks := standardcodedelivery.Links{
		Self:               "/api/v1/runs/" + url.PathEscape(facts.run.ID) + "/standard-code-delivery",
		Checkpoint:         checkpointBase + "?checkpoint_id=" + url.QueryEscape(report.FinalCheckpoint.ID),
		CheckpointTimeline: checkpointBase,
		Undo:               checkpointBase + "/undo",
		Rewind:             checkpointBase + "/rewind",
		Fork:               checkpointBase + "/fork",
	}
	if report.Status != standardcodedelivery.StatusPassed ||
		report.ReceiptStatus != standardcodedelivery.StatusPassed || !report.Verified ||
		report.Declaration != standardcodedelivery.DeclarationNone ||
		report.Binding.RunID != facts.run.ID || report.Binding.MissionID != facts.run.MissionID ||
		report.Binding.SessionID != facts.run.SessionID || report.Binding.Backend != backend ||
		report.Binding.DrydockID != facts.drydock.ID || report.Diff.ChangedCount < 1 ||
		report.FinalCheckpoint.RecoveryLevel != "complete" ||
		report.Observation.RevisionSHA256 != report.FinalCheckpoint.RevisionSHA256 ||
		report.Observation.ReasonCode != "" || len(report.Verifications) == 0 ||
		report.Links != expectedLinks ||
		report.Safeguards != (standardcodedelivery.Safeguards{}) {
		return errors.New("delivery receipt is not a fresh, complete, passed projection")
	}
	if facts.handoff.StandardCodeDelivery == nil ||
		!sameDelivery(*facts.handoff.StandardCodeDelivery, report) {
		return errors.New("Code Handoff does not expose the current delivery receipt")
	}
	finalMessageFound := false
	for _, message := range facts.messages {
		if message.Role == "assistant" && !message.CreatedAt.Before(report.CreatedAt) &&
			strings.Contains(message.Content,
				"Standard Code delivery verified:") && strings.Contains(message.Content,
			report.Links.Self) {
			finalMessageFound = true
		}
	}
	if !finalMessageFound {
		return errors.New("final response is not linked to the delivery report")
	}
	for _, projection := range projections {
		if projection.RunID != report.Binding.RunID || projection.Status != string(report.Status) ||
			projection.ReceiptSHA256 != report.ReceiptSHA256 ||
			projection.DiffSHA256 != report.Diff.SHA256 ||
			projection.CheckpointID != report.FinalCheckpoint.ID {
			return fmt.Errorf("%s projection diverges from the delivery receipt", projection.Surface)
		}
	}
	return nil
}

func sameDelivery(left, right standardcodedelivery.Report) bool {
	return left.ID == right.ID && left.Status == right.Status &&
		left.Verified == right.Verified && left.ReceiptSHA256 == right.ReceiptSHA256 &&
		left.Diff.SHA256 == right.Diff.SHA256 &&
		left.FinalCheckpoint.ID == right.FinalCheckpoint.ID &&
		left.FinalCheckpoint.RevisionSHA256 == right.FinalCheckpoint.RevisionSHA256
}

func (p *productStore) validateFallback(ctx context.Context, backend string,
	evidence FallbackEvidence,
) (FallbackEvidence, error) {
	run, err := p.state.GetRun(ctx, evidence.RunID)
	if err != nil {
		return FallbackEvidence{}, err
	}
	permission, err := p.state.GetRunExecutionPermission(ctx, evidence.RunID)
	if err != nil {
		return FallbackEvidence{}, err
	}
	decision, err := p.state.GetApproval(ctx, evidence.ApprovalID)
	if err != nil {
		return FallbackEvidence{}, err
	}
	proposal, err := p.state.GetRiskEscalationProposal(ctx, decision.ProposalID)
	if err != nil {
		return FallbackEvidence{}, err
	}
	if run.Status != domain.RunWaitingApproval || permission.Validate() != nil ||
		permission.Mode != domain.RunExecutionPermissionWorkspaceAccess ||
		permission.ProcessEnabled || permission.ExecutionAuthorized || permission.CapabilityGrant ||
		decision.Validate() != nil || decision.RunID != run.ID ||
		decision.SessionID != run.SessionID || decision.Status != approval.StatusPending ||
		proposal.Validate() != nil || proposal.ID != decision.ProposalID ||
		proposal.RunID != run.ID || proposal.MissionID != run.MissionID ||
		proposal.SessionID != run.SessionID || proposal.WorkspaceID != decision.WorkspaceID ||
		proposal.PermissionSnapshotID != permission.ID ||
		proposal.PermissionRevision != permission.Revision ||
		proposal.PermissionMode != domain.RunExecutionPermissionWorkspaceAccess ||
		decision.RequestFingerprint != proposal.Fingerprint ||
		decision.RequestedBy != proposal.RequestedBy {
		return FallbackEvidence{}, fmt.Errorf("backend %q did not enter explicit Approval", backend)
	}
	if decision.ToolName != "host_command_propose" ||
		decision.ActionClass != "risk_escalation" || decision.Mode != "per_call" {
		return FallbackEvidence{}, fmt.Errorf("backend %q Approval is not a bounded host fallback", backend)
	}
	jobs, err := p.state.ListCommandRuntimeJobs(ctx,
		runner.CommandRuntimeListFilter{RunID: run.ID, Limit: runner.MaxCommandRuntimeJobsPerRun})
	if err != nil {
		return FallbackEvidence{}, err
	}
	for _, job := range jobs {
		if job.Adapter.Kind == commandruntimeadapter.KindHostUnsandboxed ||
			job.PermissionMode == domain.RunExecutionPermissionFullAccess ||
			job.PermissionMode == domain.RunExecutionPermissionDebug {
			return FallbackEvidence{}, errors.New("backend fallback silently used host or maximum access")
		}
	}
	if report, found, getErr := p.state.GetLatestStandardCodeDelivery(ctx, run.ID); getErr != nil {
		return FallbackEvidence{}, getErr
	} else if found && report.Status == standardcodedelivery.StatusPassed {
		return FallbackEvidence{}, errors.New("Approval fallback was incorrectly projected as passed")
	}
	return evidence, nil
}

func (p *productStore) validateContinuity(ctx context.Context,
	evidence ContinuityEvidence,
) (ContinuitySummary, error) {
	run, err := p.state.GetRun(ctx, evidence.RunID)
	if err != nil {
		return ContinuitySummary{}, err
	}
	thread, err := p.state.GetThreadByRun(ctx, run.ID)
	if err != nil || thread.ID != evidence.ThreadID {
		return ContinuitySummary{}, errors.Join(err,
			errors.New("Thread continuity identity does not match"))
	}
	runs, err := p.state.ListThreadRuns(ctx, thread.ID)
	if err != nil {
		return ContinuitySummary{}, err
	}
	summary := ContinuitySummary{Case: evidence.Case, ThreadID: thread.ID,
		RunID: run.ID, SuccessorRunID: evidence.SuccessorRunID,
		Verified: true, EvidenceSHA256: evidence.EvidenceSHA256}
	switch evidence.Case {
	case "completed", "failed":
		expected := domain.RunCompleted
		if evidence.Case == "failed" {
			expected = domain.RunFailed
		}
		if run.Status != expected || !threadSuccessor(runs, run.ID, evidence.SuccessorRunID) {
			return ContinuitySummary{}, fmt.Errorf("%s Run has no same-Thread successor", evidence.Case)
		}
	case "approval_wait":
		if run.Status != domain.RunWaitingApproval {
			return ContinuitySummary{}, errors.New("approval-wait continuity Run is not waiting")
		}
		messages, listErr := p.state.ListThreadMessagesPage(ctx, thread.ID, true, 0, 0)
		if listErr != nil {
			return ContinuitySummary{}, listErr
		}
		found := false
		for _, message := range messages {
			if message.RunID == run.ID && message.ContentSHA256 == evidence.QueuedMessageSHA256 &&
				message.Role == "user" {
				found = true
			}
		}
		if !found {
			return ContinuitySummary{}, errors.New("approval-wait queued message is missing")
		}
	case "restart":
		if !evidence.ProcessRestarted || len(runs) == 0 {
			return ContinuitySummary{}, errors.New("restart continuity evidence is incomplete")
		}
	default:
		return ContinuitySummary{}, errors.New("unknown Thread continuity case")
	}
	return summary, nil
}

func threadSuccessor(values []domain.ThreadRun, predecessor, successor string) bool {
	for _, value := range values {
		if value.RunID == successor && value.PredecessorRunID == predecessor {
			return true
		}
	}
	return false
}

func validateEdgeFacts(values []EdgeEvidence, factsByRun map[string]runFacts,
	fixtures map[string]packagede2e.FixtureRepository,
) error {
	for _, value := range values {
		facts, found := factsByRun[value.RunID]
		if !found {
			return fmt.Errorf("edge %q references an unverified Run", value.Kind)
		}
		root := facts.drydock.Path
		if value.Scope == "source" {
			root = facts.drydock.Source.RootPath
		}
		target, err := withinRoot(root, value.Path)
		if err != nil {
			return fmt.Errorf("edge %q: %w", value.Kind, err)
		}
		content, err := readBoundedFile(target, maximumManualEvidenceBytes)
		if err != nil || digestBytes(content) != value.ExpectedSHA256 {
			return fmt.Errorf("edge %q content was not preserved", value.Kind)
		}
		switch value.Kind {
		case "chinese_path":
			if asciiOnly(root + "/" + value.Path) {
				return errors.New("Chinese path evidence contains no non-ASCII character")
			}
		case "space_path":
			if !strings.Contains(root+"/"+value.Path, " ") {
				return errors.New("space path evidence contains no space")
			}
		case "long_path":
			if utf8.RuneCountInString(filepath.Join(root, filepath.FromSlash(value.Path))) < 100 {
				return errors.New("long path evidence is shorter than 100 characters")
			}
		case "crlf":
			if !validCRLF(content) {
				return errors.New("CRLF evidence was normalized or corrupted")
			}
		case "binary":
			if utf8.Valid(content) && !bytes.ContainsRune(content, 0) {
				return errors.New("binary evidence is not binary")
			}
		case "dirty_tracked":
			if value.Scope != "source" || !facts.trust.SourceState.DirtyTracked {
				return errors.New("dirty tracked source was not captured by Workspace Trust")
			}
		case "untracked":
			if value.Scope != "source" || !facts.trust.SourceState.DirtyUntracked {
				return errors.New("untracked source was not captured by Workspace Trust")
			}
		case "concurrent_edit":
			if value.Scope != "source" || value.BaselineSHA256 == value.ExpectedSHA256 ||
				!baselineDigestKnown(value.Path, value.BaselineSHA256,
					facts.drydock.Source.BaseCommit, fixtures) ||
				facts.delivery.Safeguards.SourceOverwrite {
				return errors.New("concurrent source edit was not proven preserved")
			}
		}
	}
	return nil
}

func baselineDigestKnown(path, digest, baseCommit string,
	fixtures map[string]packagede2e.FixtureRepository,
) bool {
	for _, fixture := range fixtures {
		if fixture.ExpectedHead != baseCommit {
			continue
		}
		for _, file := range fixture.Files {
			if file.Path == path && file.SHA256 == digest {
				return true
			}
		}
	}
	return false
}

func withinRoot(root, relative string) (string, error) {
	if !filepath.IsAbs(root) || !safeRelativePath(relative) {
		return "", errors.New("edge path boundary is invalid")
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("edge root is not a regular directory")
	}
	target := filepath.Join(root, filepath.FromSlash(relative))
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", errors.New("edge root cannot be resolved")
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", errors.New("edge target cannot be resolved")
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedTarget)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("edge path escaped its Workspace")
	}
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("edge target is not a regular file")
	}
	return target, nil
}

func productHome(value string) (string, error) {
	path, err := regularDirectory(value, "candidate home")
	if err != nil {
		return "", err
	}
	database, err := regularFile(filepath.Join(path, "cyberagent.db"))
	if err != nil || filepath.Dir(database) != path {
		return "", errors.New("candidate home has no regular product database")
	}
	if _, err := regularDirectory(filepath.Join(path, "drydocks"),
		"candidate Drydock root"); err != nil {
		return "", err
	}
	return path, nil
}

func regularDirectory(value, label string) (string, error) {
	trimmed := strings.TrimSpace(value)
	path, err := filepath.Abs(trimmed)
	if trimmed == "" || err != nil || filepath.Clean(path) != path {
		return "", fmt.Errorf("%s is invalid", label)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s is not a regular directory", label)
	}
	return path, nil
}

func validCRLF(content []byte) bool {
	if !bytes.Contains(content, []byte("\r\n")) {
		return false
	}
	for index, value := range content {
		if value == '\n' && (index == 0 || content[index-1] != '\r') {
			return false
		}
	}
	return true
}

func asciiOnly(value string) bool {
	for _, current := range value {
		if current > 127 {
			return false
		}
	}
	return true
}

func gitValue(root string, arguments ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", append([]string{
		"-c", "core.autocrlf=false", "-c", "core.hooksPath=" + os.DevNull,
		"-c", "core.fsmonitor=false", "-c", "commit.gpgsign=false",
	}, arguments...)...)
	command.Dir = root
	command.Env = append(filteredGitEnvironment(), "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never")
	output, err := command.CombinedOutput()
	if err != nil || ctx.Err() != nil {
		return "", fmt.Errorf("read fixed Git identity: %w", errors.Join(err, ctx.Err()))
	}
	return strings.TrimSpace(string(output)), nil
}

func filteredGitEnvironment() []string {
	blocked := map[string]bool{"GIT_CONFIG_GLOBAL": true, "GIT_CONFIG_SYSTEM": true,
		"GIT_CONFIG_COUNT": true, "GIT_ASKPASS": true, "SSH_ASKPASS": true,
		"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_COMMON_DIR": true,
		"GIT_OBJECT_DIRECTORY": true, "GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
		"GIT_REPLACE_REF_BASE": true, "GIT_CEILING_DIRECTORIES": true,
		"GIT_TERMINAL_PROMPT": true, "GCM_INTERACTIVE": true,
		"GH_TOKEN": true, "GITHUB_TOKEN": true, "OPENAI_API_KEY": true,
		"HTTP_PROXY": true, "HTTPS_PROXY": true, "ALL_PROXY": true, "NO_PROXY": true}
	values := make([]string, 0, len(os.Environ()))
	for _, value := range os.Environ() {
		key, _, found := strings.Cut(value, "=")
		if found && !blocked[strings.ToUpper(key)] {
			values = append(values, value)
		}
	}
	return values
}

func WriteReport(path string, report Report) error {
	if err := report.Validate(); err != nil {
		return err
	}
	target := strings.TrimSpace(path)
	if target == "" {
		return errors.New("product E2E report path is required")
	}
	target, err := filepath.Abs(target)
	if err != nil || filepath.Clean(target) != target {
		return errors.New("product E2E report path is invalid")
	}
	parent := filepath.Dir(target)
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("product E2E report parent must be a regular directory")
	}
	content, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(content); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	return errors.Join(err, closeErr)
}
