package application

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/artifact"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/drydock"
	"cyberagent-workbench/internal/outputsafe"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/repository"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/standardcodedelivery"
	"cyberagent-workbench/internal/workspacecheckpoint"
)

const standardCodeDeliveryCaptureAttempts = 3

type StandardCodeDeliveryStore interface {
	GetConfiguredStandardCodePresetOperation(context.Context, string) (
		domain.StandardCodePresetOperation, bool, error)
	GetStandardCodeSupervisorSnapshot(context.Context, string) (
		domain.StandardCodeSupervisorSnapshot, bool, error)
	GetDrydockByRun(context.Context, string) (drydock.Workspace, bool, error)
	GetCommandRuntimeJob(context.Context, string) (runner.CommandRuntimeJob, error)
	ListCommandRuntimeJobs(context.Context, runner.CommandRuntimeListFilter) (
		[]runner.CommandRuntimeJob, error)
	ListRunArtifacts(context.Context, artifact.ListFilter) ([]artifact.Descriptor, error)
	ListWorkspaceCheckpointTransactions(context.Context, string, int) (
		[]workspacecheckpoint.Transaction, error)
	GetWorkspaceCheckpointSnapshot(context.Context, string) (
		workspacecheckpoint.Snapshot, error)
	CreateStandardCodeDelivery(context.Context, standardcodedelivery.Report) (
		standardcodedelivery.Report, bool, error)
	GetLatestStandardCodeDelivery(context.Context, string) (
		standardcodedelivery.Report, bool, error)
}

type StandardCodeDeliveryService struct {
	store    StandardCodeDeliveryStore
	drydocks *DrydockService
	now      func() time.Time
}

type StandardCodeDeliveryRecordRequest struct {
	RunID              string                           `json:"run_id"`
	OperationKey       string                           `json:"operation_key"`
	RequestedBy        string                           `json:"requested_by"`
	Declaration        standardcodedelivery.Declaration `json:"declaration,omitempty"`
	VerificationJobIDs []string                         `json:"verification_job_ids"`
	UncoveredItems     []string                         `json:"uncovered_items"`
}

type StandardCodeDeliveryRecordResult struct {
	Report   standardcodedelivery.Report `json:"report"`
	Replayed bool                        `json:"replayed"`
}

func NewStandardCodeDeliveryService(store StandardCodeDeliveryStore,
	drydocks *DrydockService,
) (*StandardCodeDeliveryService, error) {
	if store == nil || drydocks == nil || drydocks.executor == nil ||
		!drydocks.executor.Available() || drydocks.checkpoints == nil {
		return nil, apperror.New(apperror.CodeFailedPrecondition,
			"Standard Code delivery requires durable Drydock, Diff, and Checkpoint services")
	}
	return &StandardCodeDeliveryService{store: store, drydocks: drydocks,
		now: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *StandardCodeDeliveryService) Record(ctx context.Context,
	request StandardCodeDeliveryRecordRequest,
) (StandardCodeDeliveryRecordResult, error) {
	request = normalizeStandardCodeDeliveryRequest(request)
	if s == nil || s.store == nil || s.drydocks == nil || s.now == nil ||
		request.RunID == "" || request.OperationKey == "" || request.RequestedBy == "" ||
		!request.Declaration.Valid() ||
		(request.Declaration != standardcodedelivery.DeclarationNone &&
			len(request.VerificationJobIDs) > 0) ||
		len(request.VerificationJobIDs) > standardcodedelivery.MaxVerifications ||
		len(request.UncoveredItems) > standardcodedelivery.MaxUncoveredItems {
		return StandardCodeDeliveryRecordResult{}, apperror.New(
			apperror.CodeInvalidArgument, "Standard Code delivery request is invalid")
	}
	preset, configured, err := s.store.GetConfiguredStandardCodePresetOperation(ctx,
		request.RunID)
	if err != nil {
		return StandardCodeDeliveryRecordResult{}, apperror.Normalize(err)
	}
	if !configured || preset.Status != domain.StandardCodePresetConfigured {
		return StandardCodeDeliveryRecordResult{}, apperror.New(
			apperror.CodeFailedPrecondition, "Run has no configured Standard Code preset")
	}
	supervisor, found, err := s.store.GetStandardCodeSupervisorSnapshot(ctx,
		request.RunID)
	if err != nil {
		return StandardCodeDeliveryRecordResult{}, apperror.Normalize(err)
	}
	if !found || supervisor.RunID != preset.RunID ||
		supervisor.MissionID != preset.MissionID ||
		supervisor.WorkspaceID != preset.WorkspaceID ||
		supervisor.PresetOperationKeyDigest != preset.KeyDigest {
		return StandardCodeDeliveryRecordResult{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"Standard Code delivery requires an exact Supervisor binding")
	}
	workspace, found, err := s.store.GetDrydockByRun(ctx, request.RunID)
	if err != nil {
		return StandardCodeDeliveryRecordResult{}, apperror.Normalize(err)
	}
	if !found || workspace.ID != preset.DrydockID ||
		workspace.RunID != preset.RunID || workspace.MissionID != preset.MissionID ||
		workspace.SessionID == "" || workspace.SourceWorkspaceID != preset.WorkspaceID ||
		(workspace.State != drydock.StateReady && workspace.State != drydock.StateDelivered) {
		return StandardCodeDeliveryRecordResult{}, apperror.New(
			apperror.CodeFailedPrecondition, "Standard Code Drydock binding is unavailable")
	}
	if len(request.VerificationJobIDs) == 0 && request.Declaration == standardcodedelivery.DeclarationNone {
		request.VerificationJobIDs = append([]string(nil), supervisor.VerificationJobIDs...)
	}
	operationDigest := runmutation.OperationKeyDigest(
		"standard_code_delivery_record.v1", request.RunID, request.OperationKey)
	requestFingerprint := standardCodeDeliveryRequestFingerprint(request, preset,
		supervisor, workspace, operationDigest)
	checkpoint, evidence, err := s.captureAlignedDelivery(ctx, workspace,
		operationDigest, request.RequestedBy)
	if err != nil {
		return StandardCodeDeliveryRecordResult{}, err
	}
	transactions, err := s.store.ListWorkspaceCheckpointTransactions(ctx,
		request.RunID, 2_000)
	if err != nil {
		return StandardCodeDeliveryRecordResult{}, apperror.Normalize(err)
	}
	artifacts, err := s.store.ListRunArtifacts(ctx, artifact.ListFilter{
		RunID: request.RunID, Limit: artifact.MaxListLimit})
	if err != nil {
		return StandardCodeDeliveryRecordResult{}, apperror.Normalize(err)
	}
	verification, backend, backendGeneration, err := s.projectVerifications(ctx,
		request, preset, supervisor, workspace, checkpoint, transactions, artifacts)
	if err != nil {
		return StandardCodeDeliveryRecordResult{}, err
	}
	uncovered := projectStandardCodeUncoveredItems(request.UncoveredItems)
	status, reasonCode := standardcodedelivery.Evaluate(standardcodedelivery.Evaluation{
		Declaration: request.Declaration, Verifications: verification,
		CheckpointIncomplete: checkpoint.Checkpoint.RecoveryLevel != workspacecheckpoint.RecoveryComplete,
		UncoveredCount:       len(uncovered),
	})
	report := standardcodedelivery.Report{
		ID:                 "standard-code-delivery-" + operationDigest[:32],
		ProtocolVersion:    standardcodedelivery.ProtocolVersion,
		OperationKeySHA256: operationDigest, RequestFingerprint: requestFingerprint,
		Status: status, ReceiptStatus: status, Verified: status == standardcodedelivery.StatusPassed,
		Declaration: request.Declaration,
		Binding: standardcodedelivery.Binding{RunID: preset.RunID,
			MissionID: preset.MissionID, SessionID: workspace.SessionID,
			SourceWorkspaceID:  preset.WorkspaceID,
			DrydockWorkspaceID: workspace.WorkspaceID, DrydockID: workspace.ID,
			DrydockGeneration:     workspace.Generation,
			PresetOperationSHA256: preset.KeyDigest,
			PermissionSnapshotID:  supervisor.PermissionSnapshotID,
			PermissionRevision:    supervisor.PermissionRevision,
			Backend:               backend, BackendGenerationSHA256: backendGeneration,
			CapabilityGenerationSHA256: supervisor.CapabilityGeneration,
			SupervisorMutationEpoch:    supervisor.MutationEpoch},
		BaseCommit: workspace.BaseCommit, HeadCommit: evidence.HeadCommit,
		Diff:            projectStandardCodeDiff(workspace, evidence),
		FinalCheckpoint: projectStandardCodeCheckpoint(checkpoint.Checkpoint),
		Verifications:   verification, UncoveredItems: uncovered,
		Links:      standardCodeDeliveryLinks(request.RunID, checkpoint.Checkpoint.ID),
		Safeguards: standardcodedelivery.Safeguards{}, CreatedAt: s.now().UTC(),
	}
	report.Reasons = projectStandardCodeReasons(reasonCode, report)
	stored, replayed, err := s.store.CreateStandardCodeDelivery(ctx, report)
	if err != nil {
		return StandardCodeDeliveryRecordResult{}, apperror.Normalize(err)
	}
	current, err := s.observeStored(ctx, workspace, stored)
	if err != nil {
		return StandardCodeDeliveryRecordResult{}, err
	}
	return StandardCodeDeliveryRecordResult{Report: current, Replayed: replayed}, nil
}

func (s *StandardCodeDeliveryService) Current(ctx context.Context,
	runID string,
) (standardcodedelivery.Report, bool, error) {
	runID = strings.TrimSpace(runID)
	if s == nil || s.store == nil || s.drydocks == nil || runID == "" {
		return standardcodedelivery.Report{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Standard Code delivery Run id is invalid")
	}
	report, found, err := s.store.GetLatestStandardCodeDelivery(ctx, runID)
	if err != nil || !found {
		return standardcodedelivery.Report{}, found, apperror.Normalize(err)
	}
	workspace, workspaceFound, err := s.store.GetDrydockByRun(ctx, runID)
	if err != nil {
		return standardcodedelivery.Report{}, false, apperror.Normalize(err)
	}
	if !workspaceFound || workspace.ID != report.Binding.DrydockID ||
		workspace.WorkspaceID != report.Binding.DrydockWorkspaceID {
		stale := report.WithObservation("", "drydock_binding_unavailable", s.now())
		return stale, true, stale.Validate()
	}
	current, err := s.observeStored(ctx, workspace, report)
	return current, true, err
}

func (s *StandardCodeDeliveryService) observeStored(ctx context.Context,
	workspace drydock.Workspace, report standardcodedelivery.Report,
) (standardcodedelivery.Report, error) {
	preset, configured, err := s.store.GetConfiguredStandardCodePresetOperation(ctx,
		report.Binding.RunID)
	if err != nil {
		return standardcodedelivery.Report{}, apperror.Normalize(err)
	}
	if !configured || preset.KeyDigest != report.Binding.PresetOperationSHA256 ||
		string(preset.SelectedBackend) != report.Binding.Backend {
		return s.staleStored(report, standardcodedelivery.ReasonBackendDrift)
	}
	supervisor, found, err := s.store.GetStandardCodeSupervisorSnapshot(ctx,
		report.Binding.RunID)
	if err != nil {
		return standardcodedelivery.Report{}, apperror.Normalize(err)
	}
	if !found || supervisor.PermissionSnapshotID != report.Binding.PermissionSnapshotID ||
		supervisor.PermissionRevision != report.Binding.PermissionRevision ||
		supervisor.CapabilityGeneration != report.Binding.CapabilityGenerationSHA256 {
		return s.staleStored(report, standardcodedelivery.ReasonPermissionDrift)
	}
	if supervisor.MutationEpoch != report.Binding.SupervisorMutationEpoch {
		return s.staleStored(report,
			standardcodedelivery.ReasonWorkspaceModifiedAfterVerification)
	}
	revision, err := s.observeWorkspaceRevision(ctx, workspace, report.Binding)
	if err != nil {
		current := report.WithObservation("", "workspace_revision_unavailable", s.now())
		if validateErr := current.Validate(); validateErr != nil {
			return standardcodedelivery.Report{}, validateErr
		}
		return current, nil
	}
	reason := ""
	if revision != report.FinalCheckpoint.RevisionSHA256 {
		reason = standardcodedelivery.ReasonWorkspaceModifiedAfterVerification
	}
	current := report.WithObservation(revision, reason, s.now())
	if err := current.Validate(); err != nil {
		return standardcodedelivery.Report{}, err
	}
	return current, nil
}

func (s *StandardCodeDeliveryService) staleStored(report standardcodedelivery.Report,
	reason string,
) (standardcodedelivery.Report, error) {
	current := report.WithObservation("", reason, s.now())
	if err := current.Validate(); err != nil {
		return standardcodedelivery.Report{}, err
	}
	return current, nil
}

func (s *StandardCodeDeliveryService) captureAlignedDelivery(ctx context.Context,
	workspace drydock.Workspace, operationDigest, requestedBy string,
) (workspacecheckpoint.Snapshot, repository.DrydockDeliveryEvidence, error) {
	for attempt := 1; attempt <= standardCodeDeliveryCaptureAttempts; attempt++ {
		key := "standard-code-delivery-" + operationDigest[:24] + "-" + strconv.Itoa(attempt)
		checkpoint, _, err := s.drydocks.checkpoints.Capture(ctx,
			WorkspaceCheckpointCaptureRequest{RunID: workspace.RunID,
				OperationKey: key, RequestedBy: requestedBy,
				Title: "Standard Code final delivery checkpoint"})
		if err != nil {
			return workspacecheckpoint.Snapshot{}, repository.DrydockDeliveryEvidence{}, err
		}
		snapshot, err := s.store.GetWorkspaceCheckpointSnapshot(ctx, checkpoint.ID)
		if err != nil {
			return workspacecheckpoint.Snapshot{}, repository.DrydockDeliveryEvidence{},
				apperror.Normalize(err)
		}
		evidence, err := s.drydocks.executor.CaptureDelivery(ctx, workspace.Path,
			workspace.BaseCommit)
		if err != nil {
			return workspacecheckpoint.Snapshot{}, repository.DrydockDeliveryEvidence{},
				apperror.Normalize(err)
		}
		observed, err := s.captureWorkspaceObservation(ctx, workspace,
			"standard-code-delivery-align-"+operationDigest[:16]+"-"+strconv.Itoa(attempt))
		if err != nil {
			return workspacecheckpoint.Snapshot{}, repository.DrydockDeliveryEvidence{},
				apperror.Normalize(err)
		}
		if checkpointRevision(snapshot.Checkpoint) == checkpointRevision(observed.Checkpoint) &&
			snapshot.Checkpoint.BaseCommit == evidence.HeadCommit &&
			snapshot.Checkpoint.IndexSHA256 == evidence.Binding.IndexSHA256 &&
			snapshot.Checkpoint.Branch == evidence.Binding.Branch {
			return snapshot, evidence, nil
		}
	}
	return workspacecheckpoint.Snapshot{}, repository.DrydockDeliveryEvidence{},
		apperror.New(apperror.CodeConflict,
			"Drydock changed during the bounded final delivery capture")
}

func (s *StandardCodeDeliveryService) observeWorkspaceRevision(ctx context.Context,
	workspace drydock.Workspace, binding standardcodedelivery.Binding,
) (string, error) {
	if workspace.Generation != binding.DrydockGeneration {
		return "", errors.New("Drydock generation changed")
	}
	snapshot, err := s.captureWorkspaceObservation(ctx, workspace,
		"standard-code-delivery-current-"+standardcodedelivery.Hash(
			workspace.ID + strconv.FormatInt(s.now().UnixNano(), 10))[:20])
	if err != nil {
		return "", err
	}
	return checkpointRevision(snapshot.Checkpoint), nil
}

func (s *StandardCodeDeliveryService) captureWorkspaceObservation(ctx context.Context,
	workspace drydock.Workspace, id string,
) (workspacecheckpoint.Snapshot, error) {
	return workspacecheckpoint.Capture(ctx, workspacecheckpoint.CaptureRequest{
		ID: id, RunID: workspace.RunID, MissionID: workspace.MissionID,
		SessionID: workspace.SessionID, WorkspaceID: workspace.WorkspaceID,
		WorkspaceRoot: workspace.Path, Trigger: workspacecheckpoint.TriggerManual,
		Phase:            workspacecheckpoint.PhaseStandalone,
		TriggerReceiptID: id, RequestedBy: "run_supervisor",
		Title: "Standard Code delivery revision observation", CreatedAt: s.now().UTC(),
	})
}

func (s *StandardCodeDeliveryService) projectVerifications(ctx context.Context,
	request StandardCodeDeliveryRecordRequest, preset domain.StandardCodePresetOperation,
	supervisor domain.StandardCodeSupervisorSnapshot, workspace drydock.Workspace,
	final workspacecheckpoint.Snapshot, transactions []workspacecheckpoint.Transaction,
	artifacts []artifact.Descriptor,
) ([]standardcodedelivery.Verification, string, string, error) {
	jobs := make([]runner.CommandRuntimeJob, 0, len(request.VerificationJobIDs))
	for _, id := range request.VerificationJobIDs {
		job, err := s.store.GetCommandRuntimeJob(ctx, id)
		if err != nil {
			return nil, "", "", apperror.Normalize(err)
		}
		if job.RunID != preset.RunID || job.MissionID != preset.MissionID ||
			job.SessionID != workspace.SessionID || job.WorkspaceID != preset.WorkspaceID {
			return nil, "", "", apperror.New(apperror.CodeConflict,
				"verification Job escaped its Standard Code Run binding")
		}
		jobs = append(jobs, job)
	}
	backend := string(preset.SelectedBackend)
	backendGeneration := standardcodedelivery.Hash(strings.Join([]string{
		"standard-code-backend.v1", backend, preset.KeyDigest}, "\x00"))
	if len(jobs) > 0 {
		backend = jobs[0].Adapter.Backend
		backendGeneration = commandRuntimeBackendGeneration(jobs[0])
	}
	allJobs, err := s.store.ListCommandRuntimeJobs(ctx,
		runner.CommandRuntimeListFilter{RunID: preset.RunID, Limit: 500})
	if err != nil {
		return nil, "", "", apperror.Normalize(err)
	}
	artifactBySource := make(map[string][]artifact.Descriptor)
	for _, descriptor := range artifacts {
		if descriptor.RunID == preset.RunID {
			artifactBySource[descriptor.SourceID] = append(
				artifactBySource[descriptor.SourceID], descriptor)
		}
	}
	result := make([]standardcodedelivery.Verification, 0, len(jobs))
	finalRevision := checkpointRevision(final.Checkpoint)
	for _, job := range jobs {
		checkpoint, found, err := s.verificationCheckpoint(ctx, job, transactions)
		if err != nil {
			return nil, "", "", err
		}
		revision := standardcodedelivery.Hash("missing-verification-checkpoint")
		checkpointID := "missing-verification-checkpoint"
		if found {
			revision = checkpointRevision(checkpoint.Checkpoint)
			checkpointID = checkpoint.Checkpoint.ID
		}
		projectedArtifacts, artifactsComplete := projectCommandArtifacts(job,
			artifactBySource[job.ID])
		current := found && revision == finalRevision
		conclusion, reason := commandVerificationConclusion(job, current,
			artifactsComplete, supervisor, backend, backendGeneration)
		result = append(result, standardcodedelivery.Verification{
			JobID: job.ID, Conclusion: conclusion, ReasonCode: reason,
			State: string(job.State), ExitCode: cloneDeliveryExitCode(job.ExitCode),
			SpecSHA256: job.SpecFingerprint, ExecutableSHA256: job.ExecutableSHA256,
			EnvironmentSHA256:  job.EnvironmentSHA256,
			PermissionRevision: job.PermissionRevision, Backend: job.Adapter.Backend,
			BackendGenerationSHA256: commandRuntimeBackendGeneration(job),
			CheckpointID:            checkpointID, RevisionSHA256: revision,
			CurrentRevision: current, RetryCount: commandRetryCount(allJobs, job),
			StdoutSHA256: job.StdoutSHA256, StderrSHA256: job.StderrSHA256,
			StdoutObservedBytes: job.StdoutObservedBytes,
			StderrObservedBytes: job.StderrObservedBytes,
			OutputTruncated:     job.TruncationReason != "", TreeReaped: job.TreeReaped,
			Artifacts: projectedArtifacts,
			StartedAt: cloneDeliveryTime(job.StartedAt), CompletedAt: cloneDeliveryTime(job.CompletedAt),
		})
	}
	return result, backend, backendGeneration, nil
}

func (s *StandardCodeDeliveryService) verificationCheckpoint(ctx context.Context,
	job runner.CommandRuntimeJob, transactions []workspacecheckpoint.Transaction,
) (workspacecheckpoint.Snapshot, bool, error) {
	for _, transaction := range transactions {
		if transaction.RunID != job.RunID ||
			transaction.Kind != workspacecheckpoint.TransactionCommandBatch ||
			(transaction.TriggerReceiptID != job.ID &&
				transaction.TriggerReceiptID != job.InvocationID) ||
			!transaction.Status.Terminal() || transaction.AfterCheckpointID == "" {
			continue
		}
		checkpoint, err := s.store.GetWorkspaceCheckpointSnapshot(ctx,
			transaction.AfterCheckpointID)
		if err != nil {
			return workspacecheckpoint.Snapshot{}, false, apperror.Normalize(err)
		}
		return checkpoint, true, nil
	}
	return workspacecheckpoint.Snapshot{}, false, nil
}

func commandVerificationConclusion(job runner.CommandRuntimeJob, current,
	artifactsComplete bool, supervisor domain.StandardCodeSupervisorSnapshot,
	backend, backendGeneration string,
) (standardcodedelivery.Status, string) {
	if !current {
		return standardcodedelivery.StatusStale,
			standardcodedelivery.ReasonWorkspaceModifiedAfterVerification
	}
	if job.PermissionRevision != supervisor.PermissionRevision ||
		job.PermissionSnapshotID != supervisor.PermissionSnapshotID {
		return standardcodedelivery.StatusStale, standardcodedelivery.ReasonPermissionDrift
	}
	if job.Adapter.Backend != backend || commandRuntimeBackendGeneration(job) != backendGeneration {
		return standardcodedelivery.StatusStale, standardcodedelivery.ReasonBackendDrift
	}
	if job.TruncationReason != "" {
		return standardcodedelivery.StatusPartial, standardcodedelivery.ReasonOutputTruncated
	}
	if !artifactsComplete {
		return standardcodedelivery.StatusPartial, standardcodedelivery.ReasonArtifactMissing
	}
	switch job.State {
	case runner.CommandRuntimeJobTimedOut:
		return standardcodedelivery.StatusBlocked, standardcodedelivery.ReasonCommandTimedOut
	case runner.CommandRuntimeJobCancelled, runner.CommandRuntimeJobKilled:
		return standardcodedelivery.StatusBlocked, standardcodedelivery.ReasonCommandCancelled
	case runner.CommandRuntimeJobInterrupted:
		return standardcodedelivery.StatusBlocked, standardcodedelivery.ReasonCommandInterrupted
	case runner.CommandRuntimeJobCompleted:
		if job.ExitCode != nil && *job.ExitCode == 0 && job.TreeReaped {
			return standardcodedelivery.StatusPassed, standardcodedelivery.ReasonPassed
		}
		return standardcodedelivery.StatusFailed, standardcodedelivery.ReasonVerificationFailed
	case runner.CommandRuntimeJobPrepared, runner.CommandRuntimeJobRunning,
		runner.CommandRuntimeJobStopping:
		return standardcodedelivery.StatusBlocked, standardcodedelivery.ReasonCommandNotTerminal
	default:
		return standardcodedelivery.StatusFailed, standardcodedelivery.ReasonVerificationFailed
	}
}

func projectCommandArtifacts(job runner.CommandRuntimeJob,
	descriptors []artifact.Descriptor,
) ([]standardcodedelivery.Artifact, bool) {
	result := make([]standardcodedelivery.Artifact, 0, len(descriptors))
	stdoutFound := job.StdoutObservedBytes == 0
	stderrFound := job.StderrObservedBytes == 0
	for _, descriptor := range descriptors {
		if descriptor.SourceID != job.ID || len(result) >= standardcodedelivery.MaxArtifactsPerCommand {
			continue
		}
		switch descriptor.Stream {
		case artifact.StreamStdout:
			if descriptor.SHA256 != job.StdoutSHA256 {
				continue
			}
			stdoutFound = true
		case artifact.StreamStderr:
			if descriptor.SHA256 != job.StderrSHA256 {
				continue
			}
			stderrFound = true
		default:
			continue
		}
		result = append(result, standardcodedelivery.Artifact{ID: descriptor.ID,
			Stream: string(descriptor.Stream), SHA256: descriptor.SHA256,
			SizeBytes: descriptor.SizeBytes, Redacted: descriptor.Redacted,
			URL: "/api/v1/artifacts/" + url.PathEscape(descriptor.ID)})
	}
	slices.SortFunc(result, func(left, right standardcodedelivery.Artifact) int {
		return strings.Compare(left.ID, right.ID)
	})
	return result, stdoutFound && stderrFound
}

func projectStandardCodeDiff(workspace drydock.Workspace,
	evidence repository.DrydockDeliveryEvidence,
) standardcodedelivery.Diff {
	value := standardcodedelivery.Diff{SHA256: standardcodedelivery.HashBytes(
		[]byte(evidence.Patch)), Bytes: len([]byte(evidence.Patch)),
		Files: make([]standardcodedelivery.ChangedFile, 0, len(evidence.PathStates))}
	for _, state := range evidence.PathStates {
		publicPath, safe := repository.PublicPath(state.Path)
		file := standardcodedelivery.ChangedFile{
			PathSHA256: standardcodedelivery.Hash(state.Path), Tracked: state.Tracked,
			Committed: state.Committed, IndexChanged: state.IndexChanged,
			WorktreeChanged: state.WorktreeChanged, Untracked: state.Untracked,
			Conflicted: state.Conflicted, PathRedacted: !safe}
		if safe {
			file.Path = publicPath
			file.FileURL = "/api/v1/workspaces/" + url.PathEscape(workspace.WorkspaceID) +
				"/explore?path=" + url.QueryEscape(publicPath)
		}
		value.Files = append(value.Files, file)
		if file.Tracked {
			value.TrackedCount++
		}
		if file.Committed {
			value.CommittedCount++
		}
		if file.IndexChanged {
			value.IndexCount++
		}
		if file.WorktreeChanged {
			value.WorktreeCount++
		}
		if file.Untracked {
			value.UntrackedCount++
		}
		if file.Conflicted {
			value.ConflictCount++
		}
		if file.PathRedacted {
			value.RedactedCount++
		}
	}
	value.ChangedCount = len(value.Files)
	return value
}

func projectStandardCodeCheckpoint(checkpoint workspacecheckpoint.Checkpoint) standardcodedelivery.Checkpoint {
	incomplete := make([]string, 0, len(checkpoint.IncompleteReasons))
	for _, reason := range checkpoint.IncompleteReasons {
		incomplete = append(incomplete, standardcodedelivery.Hash(reason))
	}
	return standardcodedelivery.Checkpoint{ID: checkpoint.ID,
		ManifestSHA256: checkpoint.ManifestSHA256, IndexSHA256: checkpoint.IndexSHA256,
		RootFingerprint: checkpoint.RootFingerprint,
		RootPathSHA256:  checkpoint.RootPathSHA256, HeadCommit: checkpoint.BaseCommit,
		BranchSHA256:           standardcodedelivery.Hash(checkpoint.Branch),
		RevisionSHA256:         checkpointRevision(checkpoint),
		RecoveryLevel:          string(checkpoint.RecoveryLevel),
		IncompleteReasonSHA256: incomplete, CreatedAt: checkpoint.CreatedAt}
}

func checkpointRevision(checkpoint workspacecheckpoint.Checkpoint) string {
	return standardcodedelivery.RevisionSHA256(checkpoint.ManifestSHA256,
		checkpoint.IndexSHA256, checkpoint.RootFingerprint, checkpoint.RootPathSHA256,
		checkpoint.BaseCommit, checkpoint.Branch)
}

func projectStandardCodeReasons(primary string,
	report standardcodedelivery.Report,
) []standardcodedelivery.Reason {
	reasons := []standardcodedelivery.Reason{standardcodedelivery.ReasonFact(primary,
		report.FinalCheckpoint.RevisionSHA256, report.Diff.SHA256,
		report.RequestFingerprint)}
	seen := map[string]struct{}{primary: {}}
	for _, verification := range report.Verifications {
		if _, exists := seen[verification.ReasonCode]; exists {
			continue
		}
		seen[verification.ReasonCode] = struct{}{}
		reasons = append(reasons, standardcodedelivery.ReasonFact(
			verification.ReasonCode, verification.JobID, verification.SpecSHA256,
			verification.RevisionSHA256))
	}
	return reasons
}

func projectStandardCodeUncoveredItems(values []string) []standardcodedelivery.UncoveredItem {
	result := make([]standardcodedelivery.UncoveredItem, 0, len(values))
	seen := map[string]struct{}{}
	for _, raw := range values {
		value := strings.TrimSpace(redact.String(outputsafe.Sanitize([]byte(raw))))
		value = standardCodePrivateHostPath.ReplaceAllString(value,
			`${1}[REDACTED:host-path]`)
		runes := []rune(value)
		if len(runes) > standardcodedelivery.MaxTextRunes {
			value = strings.TrimSpace(string(runes[:standardcodedelivery.MaxTextRunes]))
		}
		if value == "" || !utf8.ValidString(value) {
			value = "uncovered item details omitted"
		}
		digest := standardcodedelivery.Hash(value)
		if _, exists := seen[digest]; exists {
			continue
		}
		seen[digest] = struct{}{}
		result = append(result, standardcodedelivery.UncoveredItem{
			Summary: value, SummarySHA256: digest})
	}
	return result
}

var standardCodePrivateHostPath = regexp.MustCompile(
	`(?i)(^|[\s"'(=])(?:[a-z]:[\\/][^\s"'<>]*|\\\\[^\\/\s"'<>]+[\\/][^\s"'<>]*|/[^\s"'<>]+)`)

func standardCodeDeliveryLinks(runID, checkpointID string) standardcodedelivery.Links {
	base := "/api/v1/runs/" + url.PathEscape(runID) + "/workspace-checkpoints"
	return standardcodedelivery.Links{
		Self:               "/api/v1/runs/" + url.PathEscape(runID) + "/standard-code-delivery",
		Checkpoint:         base + "?checkpoint_id=" + url.QueryEscape(checkpointID),
		CheckpointTimeline: base, Undo: base + "/undo", Rewind: base + "/rewind",
		Fork: base + "/fork"}
}

func standardCodeDeliveryRequestFingerprint(request StandardCodeDeliveryRecordRequest,
	preset domain.StandardCodePresetOperation, supervisor domain.StandardCodeSupervisorSnapshot,
	workspace drydock.Workspace, operationDigest string,
) string {
	parts := []string{"standard_code_delivery_request.v1", operationDigest,
		request.RunID, request.RequestedBy, string(request.Declaration), preset.KeyDigest,
		supervisor.PermissionSnapshotID, strconv.FormatInt(supervisor.PermissionRevision, 10),
		supervisor.CapabilityGeneration, strconv.Itoa(supervisor.MutationEpoch),
		workspace.ID, strconv.FormatInt(workspace.Generation, 10)}
	parts = append(parts, request.VerificationJobIDs...)
	for _, item := range projectStandardCodeUncoveredItems(request.UncoveredItems) {
		parts = append(parts, item.SummarySHA256)
	}
	return runmutation.Fingerprint(parts...)
}

func normalizeStandardCodeDeliveryRequest(request StandardCodeDeliveryRecordRequest) StandardCodeDeliveryRecordRequest {
	request.RunID = strings.TrimSpace(request.RunID)
	request.OperationKey = strings.TrimSpace(request.OperationKey)
	request.RequestedBy = normalizeWorkspaceCheckpointOperator(request.RequestedBy)
	seen := map[string]struct{}{}
	jobs := make([]string, 0, len(request.VerificationJobIDs))
	for _, id := range request.VerificationJobIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		jobs = append(jobs, id)
	}
	slices.Sort(jobs)
	request.VerificationJobIDs = jobs
	return request
}

func commandRuntimeBackendGeneration(job runner.CommandRuntimeJob) string {
	return standardcodedelivery.Hash(strings.Join([]string{
		"command-runtime-backend-generation.v1", string(job.Adapter.Kind),
		job.Adapter.Backend, job.Adapter.BackendIdentity, job.Adapter.Generation}, "\x00"))
}

func commandRetryCount(jobs []runner.CommandRuntimeJob, current runner.CommandRuntimeJob) int {
	count := 0
	for _, job := range jobs {
		if job.ID != current.ID && job.SpecFingerprint == current.SpecFingerprint &&
			job.CreatedAt.Before(current.CreatedAt) {
			count++
		}
	}
	return count
}

func cloneDeliveryExitCode(value *int) *int {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneDeliveryTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := value.UTC()
	return &clone
}
