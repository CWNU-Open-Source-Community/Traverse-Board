package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/artifact"
	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/drydock"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/standardcodedelivery"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/workspacecheckpoint"
)

func TestStandardCodeDeliveryLostSuccessReplaysSealedIntentReadOnly(t *testing.T) {
	f := newStandardCodeDeliveryReplayFixture(t, 0)
	request := StandardCodeDeliveryRecordRequest{RunID: f.base.run.ID,
		OperationKey: "lost-response", RequestedBy: "api_operator"}
	first, err := f.service.Record(t.Context(), request)
	if err != nil || first.Replayed || first.Report.Status != standardcodedelivery.StatusPassed {
		t.Fatalf("record status=%s replay=%t err=%v", first.Report.Status, first.Replayed, err)
	}
	sealed, found, err := f.base.state.GetStandardCodeDelivery(t.Context(), first.Report.ID)
	if err != nil || !found {
		t.Fatalf("sealed found=%t err=%v", found, err)
	}
	assertReadOnlyReplay := func(service *StandardCodeDeliveryService, req StandardCodeDeliveryRecordRequest,
		wantStatus standardcodedelivery.Status) {
		t.Helper()
		before := deliveryReplayDurableState(t, f.base.state, request.RunID)
		result, err := service.Record(t.Context(), req)
		if err != nil || !result.Replayed || result.Report.ID != sealed.ID ||
			result.Report.ReceiptSHA256 != sealed.ReceiptSHA256 || result.Report.Status != wantStatus ||
			result.Report.ReceiptStatus != standardcodedelivery.StatusPassed ||
			!reflect.DeepEqual(result.Report.Verifications, sealed.Verifications) {
			t.Fatalf("replay status=%s replay=%t id=%s err=%v", result.Report.Status,
				result.Replayed, result.Report.ID, err)
		}
		if after := deliveryReplayDurableState(t, f.base.state, request.RunID); after != before {
			t.Fatal("read-only replay changed durable checkpoints, cursor, events, or reports")
		}
		stored, _, err := f.base.state.GetStandardCodeDelivery(t.Context(), sealed.ID)
		if err != nil || !reflect.DeepEqual(stored, sealed) {
			t.Fatalf("replay changed sealed receipt: err=%v", err)
		}
	}
	assertReadOnlyReplay(f.service, request, standardcodedelivery.StatusPassed)
	explicit := request
	explicit.VerificationJobIDs = []string{f.job.ID, f.job.ID}
	assertReadOnlyReplay(f.service, explicit, standardcodedelivery.StatusPassed)

	// The lost response may be retried after a newer report: look up the exact
	// operation, not the latest report for the Run.
	later := request
	later.OperationKey = "later-report"
	if result, err := f.service.Record(t.Context(), later); err != nil || result.Report.ID == sealed.ID {
		t.Fatalf("later report err=%v", err)
	}
	writeDrydockTestFile(t, filepath.Join(f.workspace.Path, "tracked.txt"), "edited after verification\n")
	assertReadOnlyReplay(f.service, request, standardcodedelivery.StatusStale)
	current, found, err := f.service.Current(t.Context(), request.RunID)
	if err != nil || !found || current.Status != standardcodedelivery.StatusStale || current.ID == sealed.ID {
		t.Fatalf("current latest report status=%s found=%t err=%v", current.Status, found, err)
	}
	writeDrydockTestFile(t, filepath.Join(f.workspace.Path, "tracked.txt"), "verified revision\n")
	assertReadOnlyReplay(f.service, request, standardcodedelivery.StatusPassed)

	// Advancing the Supervisor must not change the original automatic Job list
	// or the immutable authority tuple used to compare the original request.
	f.machine.snapshot.MutationEpoch++
	f.machine.snapshot.State = domain.StandardCodeSupervisorExecute
	f.machine.snapshot.VerificationJobIDs = nil
	if err := f.machine.append(t.Context(), domain.StandardCodeSupervisorTurnPrepared,
		domain.StandardCodeSupervisorRecorded, "", "", "epoch-change", domain.StandardCodeToolOther,
		"", "", "", "", "", "integration_epoch_change"); err != nil {
		t.Fatal(err)
	}
	assertReadOnlyReplay(f.service, request, standardcodedelivery.StatusStale)
	for name, change := range map[string]func(*StandardCodeDeliveryRecordRequest){
		"operator": func(r *StandardCodeDeliveryRecordRequest) { r.RequestedBy = "cli_operator" },
		"jobs":     func(r *StandardCodeDeliveryRecordRequest) { r.VerificationJobIDs = []string{"different-job"} },
		"declaration": func(r *StandardCodeDeliveryRecordRequest) {
			r.Declaration = standardcodedelivery.DeclarationUserSkipped
		},
		"uncovered": func(r *StandardCodeDeliveryRecordRequest) { r.UncoveredItems = []string{"another scenario"} },
	} {
		t.Run(name, func(t *testing.T) {
			changed := request
			change(&changed)
			before := deliveryReplayDurableState(t, f.base.state, request.RunID)
			if _, err := f.service.Record(t.Context(), changed); apperror.CodeOf(err) != apperror.CodeConflict {
				t.Fatalf("different intent error=%v", err)
			}
			if after := deliveryReplayDurableState(t, f.base.state, request.RunID); before != after {
				t.Fatal("conflicting request wrote durable state")
			}
		})
	}
	if _, _, err := f.base.state.ReleaseRunExecutionLease(t.Context(), f.lease); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRunService(f.base.state).Cancel(t.Context(), request.RunID); err != nil {
		t.Fatal(err)
	}
	// Another SQLite connection and service represent a retry after process
	// restart. The Run is terminal and cannot accept a new capture.
	reopened, err := store.Open(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restartedDrydocks, err := NewDrydockService(reopened, f.base.executor)
	if err != nil {
		t.Fatal(err)
	}
	checkpoints, err := NewWorkspaceCheckpointService(reopened, f.capabilities)
	if err != nil {
		t.Fatal(err)
	}
	restartedDrydocks.WithCheckpointService(checkpoints)
	restarted, err := NewStandardCodeDeliveryService(reopened, restartedDrydocks)
	if err != nil {
		t.Fatal(err)
	}
	assertReadOnlyReplay(restarted, request, standardcodedelivery.StatusStale)
}

func TestStandardCodeDeliveryAutomaticSelectionPreservesFailedVerification(t *testing.T) {
	f := newStandardCodeDeliveryReplayFixture(t, 2)
	result, err := f.service.Record(t.Context(), StandardCodeDeliveryRecordRequest{
		RunID: f.base.run.ID, OperationKey: "failed-evidence", RequestedBy: "api_operator"})
	if err != nil || result.Report.Status != standardcodedelivery.StatusFailed ||
		len(result.Report.Verifications) != 1 || result.Report.Verifications[0].JobID != f.job.ID ||
		!result.Report.Verifications[0].CurrentRevision || result.Report.Verified {
		t.Fatalf("failed selection result=%+v err=%v", result, err)
	}
}

type standardCodeDeliveryReplayFixture struct {
	base         drydockApplicationFixture
	service      *StandardCodeDeliveryService
	workspace    drydock.Workspace
	machine      *standardCodeSupervisorTurn
	job          runner.CommandRuntimeJob
	lease        domain.RunExecutionLease
	capabilities domain.ExecutionPermissionRuntimeCapabilities
	dbPath       string
}

func newStandardCodeDeliveryReplayFixture(t *testing.T, exitCode int, output ...deliveryCommandOutputFixture) standardCodeDeliveryReplayFixture {
	t.Helper()
	base := newDrydockApplicationFixture(t, "delivery replay")
	// Use an independently reopenable real SQLite store and real Git worktree.
	dbPath := filepath.Join(t.TempDir(), "delivery-replay.db")
	state, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	if err := state.SaveWorkspace(t.Context(), base.workspace); err != nil {
		t.Fatal(err)
	}
	_, run, err := NewRunService(state).Create(t.Context(), CreateRunRequest{
		Goal: "delivery replay integration", Profile: "code", Surface: "code", Phase: "plan",
		WorkspaceID: base.workspace.ID, Budget: domain.Budget{MaxTurns: 8, MaxTokens: 2000, MaxToolCalls: 32}})
	if err != nil {
		t.Fatal(err)
	}
	base.state, base.run = state, run
	base.service, err = NewDrydockService(state, base.executor)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{WorkspaceSandboxEnabled: true, OperatorApprovalEnabled: true}
	checkpoints, err := NewWorkspaceCheckpointService(state, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	base.service.WithCheckpointService(checkpoints)
	// This fixture exercises persisted command outcomes and their checkpoints;
	// the adapter identity below is test data, not an OS sandbox execution claim.
	adapter := commandruntimeadapter.SandboxedWorkspace(CommandRuntimeLocalSandboxBackend, "delivery-test-adapter", "delivery-test-generation")
	presets, err := NewStandardCodePresetService(state, base.service, CapabilityReadinessRuntime{
		RunControlEnabled: true, RunExecutionEnabled: true, ExecutionPermissionControlEnabled: true,
		StandardCodePresetEnabled: true, ExecutionPermissionCapabilities: capabilities,
		LocalSandboxInstalled: true, LocalSandboxProven: true, LocalBackendReady: true,
		CommandRuntimeAdapters: []commandruntimeadapter.Identity{adapter}})
	if err != nil {
		t.Fatal(err)
	}
	configure := ConfigureStandardCodeRequest{Version: domain.StandardCodePresetProtocolVersion,
		RunID: run.ID, BackendIntent: "auto", Action: "configure", OperationKey: "delivery-preset-0001", RequestedBy: "operator"}
	preview, err := presets.Configure(t.Context(), configure)
	if err != nil || !preview.TrustRequired {
		t.Fatalf("preset preview=%+v err=%v", preview, err)
	}
	configure.ConfirmWorkspaceTrust, configure.ExpectedTrustDigest = true, preview.TrustDigest
	configured, err := presets.Configure(t.Context(), configure)
	if err != nil || configured.Status != StandardCodeResultConfigured {
		t.Fatalf("preset=%+v err=%v", configured, err)
	}
	preset, _, err := state.GetConfiguredStandardCodePresetOperation(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	workspace, _, err := state.GetDrydockByRun(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	phase, err := NewRunService(state).ChangePhase(t.Context(), ChangeRunPhaseRequest{
		RunID: run.ID, Phase: "deliver", OperationKey: "delivery-phase-0001", RequestedBy: "operator", Reason: "verification fixture"})
	if err != nil {
		t.Fatal(err)
	}
	configured.Mode = &phase.Mode
	base.run, err = NewRunService(state).Start(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	acquired, err := state.AcquireRunExecutionLease(t.Context(), domain.AcquireRunExecutionLeaseRequest{
		RunID: run.ID, OwnerID: "delivery-replay-test", TTL: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := state.BeginSupervisorTurn(t.Context(), acquired.Lease, "verify delivery")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	snapshot := domain.StandardCodeSupervisorSnapshot{ProtocolVersion: domain.StandardCodeSupervisorProtocolVersion,
		RunID: run.ID, MissionID: run.MissionID, WorkspaceID: base.workspace.ID, RootAgentID: turn.Agent.ID,
		PresetOperationKeyDigest: preset.KeyDigest, State: domain.StandardCodeSupervisorExecute,
		ModeSnapshotID: configured.Mode.ID, ModeRevision: configured.Mode.Revision,
		ProfileSnapshotID: configured.Profile.ID, ProfileRevision: configured.Profile.Revision,
		InteractionSnapshotID: configured.Interaction.ID, InteractionRevision: configured.Interaction.Revision,
		PermissionSnapshotID: configured.Permission.ID, PermissionRevision: configured.Permission.Revision,
		BrowserCDPSnapshotID: configured.BrowserCDP.ID, BrowserCDPRevision: configured.BrowserCDP.Revision,
		WorkspaceRootFingerprint: standardcodedelivery.Hash(workspace.Path), CapabilityGeneration: standardcodedelivery.Hash("capability"),
		Turn: turn.Checkpoint.NextTurn, AttemptID: turn.Checkpoint.AttemptID, MutationEpoch: 1,
		RunTokenLimit: run.Budget.MaxTokens, RunToolCallLimit: run.Budget.MaxToolCalls,
		RunTimeoutMillis: run.Budget.TimeoutSeconds * 1000, Limits: domain.DefaultStandardCodeSupervisorLimits(),
		CreatedAt: now, UpdatedAt: now}
	machine := &standardCodeSupervisorTurn{store: state, turn: turn, snapshot: snapshot, preset: preset,
		permission: *configured.Permission}
	if err := machine.append(t.Context(), domain.StandardCodeSupervisorInitialized,
		domain.StandardCodeSupervisorRecorded, "", "", "", domain.StandardCodeToolOther,
		"", "", "", "", "", "delivery_fixture"); err != nil {
		t.Fatal(err)
	}
	writeDrydockTestFile(t, filepath.Join(workspace.Path, "tracked.txt"), "verified revision\n")
	job := seedDeliveryCommandOutcome(t, state, base.run, turn, acquired.Lease, configured, adapter, exitCode, output...)
	if len(output) != 0 {
		if _, err := state.CaptureToolOutput(t.Context(), artifact.CaptureRequest{RunID: job.RunID, SessionID: job.SessionID, WorkspaceID: job.WorkspaceID, SourceID: job.ID, ToolName: "command_runtime", Outputs: []artifact.Output{{Stream: artifact.StreamStdout, MIME: "text/plain; charset=utf-8", Content: job.Stdout}, {Stream: artifact.StreamStderr, MIME: "text/plain; charset=utf-8", Content: job.Stderr}}}); err != nil {
			t.Fatal(err)
		}
	}
	boundary := WorkspaceMutationBoundaryRequest{RunID: run.ID, Kind: workspacecheckpoint.TransactionCommandBatch,
		OperationKey: "verification-boundary", TriggerReceiptID: job.ID, AttemptID: turn.Checkpoint.AttemptID,
		CapabilityGeneration: snapshot.CapabilityGeneration, LeaseID: acquired.Lease.LeaseID, LeaseGeneration: acquired.Lease.Generation}
	if _, err := base.service.checkpoints.BeginBoundary(t.Context(), boundary); err != nil {
		t.Fatal(err)
	}
	if _, err := base.service.checkpoints.CompleteBoundary(t.Context(), boundary, nil); err != nil {
		t.Fatal(err)
	}
	projection := standardCodeCommandOutput(t, "run", []runner.CommandRuntimeJobSnapshot{
		runner.ProjectCommandRuntimeJob(job)}, nil)
	if _, _, err := machine.observeCommand(t.Context(), domain.SupervisorToolCall{Status: domain.SupervisorToolCompleted},
		standardCodeCallDescriptor{Kind: domain.StandardCodeToolCommandRun, Action: "run"},
		supervisorToolResultEnvelope{Stdout: projection}); err != nil {
		t.Fatal(err)
	}
	if err := machine.append(t.Context(), domain.StandardCodeSupervisorRoundObserved,
		domain.StandardCodeSupervisorObserved, "", "", "verification", domain.StandardCodeToolOther,
		"", "", "", "", "", "delivery_fixture_verification"); err != nil {
		t.Fatal(err)
	}
	service, err := NewStandardCodeDeliveryService(state, base.service)
	if err != nil {
		t.Fatal(err)
	}
	return standardCodeDeliveryReplayFixture{base: base, service: service, workspace: workspace,
		machine: machine, job: job, lease: acquired.Lease, capabilities: capabilities, dbPath: dbPath}
}

func seedDeliveryCommandOutcome(t *testing.T, state *store.SQLiteStore, run domain.Run,
	turn domain.SupervisorTurn, lease domain.RunExecutionLease, configured StandardCodePresetResult,
	adapter commandruntimeadapter.Identity, exitCode int, output ...deliveryCommandOutputFixture,
) runner.CommandRuntimeJob {
	t.Helper()
	now := time.Now().UTC()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	hash := standardcodedelivery.Hash
	job := runner.CommandRuntimeJob{ID: "delivery-job", OperationDigest: hash("job-operation"),
		RequestFingerprint: hash("job-intent"), InvocationID: "delivery-job-invocation",
		RunID: run.ID, MissionID: run.MissionID, SessionID: run.SessionID, WorkspaceID: turn.Mission.WorkspaceID,
		RootAgentID: turn.Agent.ID, WorkspaceRootSHA256: hash("root"),
		ModeSnapshotID: configured.Mode.ID, ModeRevision: configured.Mode.Revision,
		ProfileSnapshotID: configured.Profile.ID, ProfileRevision: configured.Profile.Revision,
		PermissionSnapshotID: configured.Permission.ID, PermissionRevision: configured.Permission.Revision,
		PermissionMode: configured.Permission.Mode, LeaseID: lease.LeaseID, LeaseGeneration: lease.Generation,
		LeaseOwnerID: lease.OwnerID, Adapter: adapter, OwnerID: "test-command-owner", OwnerGeneration: 1,
		OwnerRenewedAt: now, OwnerExpiresAt: now.Add(time.Minute), IntentJSON: `{}`,
		SpecFingerprint: hash("command-spec"), Profile: runner.CommandRuntimeProcess, ExecutablePath: executable,
		ExecutableSHA256: hash("test-executable"), EnvironmentSHA256: hash("environment"), WorkingDirectory: ".",
		StdinPolicy: runner.CommandRuntimeStdinClosed, Network: runner.CommandRuntimeNetworkDisabled,
		Credentials: runner.CommandRuntimeCredentialsNone, TimeoutMilliseconds: 1000,
		InlineLimitBytes: 4096, ArtifactLimitBytes: 4096, State: runner.CommandRuntimeJobPrepared,
		OutputFramesJSON: "[]", StdinClosed: true, Version: 1, CreatedAt: now, UpdatedAt: now}
	if len(output) != 0 {
		job.ArtifactLimitBytes = 65536
	}
	job, _, err = state.PrepareCommandRuntimeJob(t.Context(), job)
	if err != nil {
		t.Fatal(err)
	}
	job.State = runner.CommandRuntimeJobRunning
	job.StartedAt, job.JobAssignedAtCreation, job.Version = &now, true, job.Version+1
	job, err = state.UpdateCommandRuntimeJob(t.Context(), job, job.Version-1)
	if err != nil {
		t.Fatal(err)
	}
	job.State, job.ExitCode, job.TreeReaped = runner.CommandRuntimeJobCompleted, &exitCode, true
	job.CompletedAt, job.StdoutSHA256, job.StderrSHA256, job.Version = &now, hash(""), hash(""), job.Version+1
	if len(output) != 0 {
		o := output[0]
		job.Stdout = o.stdout
		job.Stderr = o.stderr
		job.TruncationReason = o.reason
		job.StdoutSHA256 = hash(job.Stdout)
		job.StderrSHA256 = hash(job.Stderr)
		job.StdoutObservedBytes = int64(len(job.Stdout) + o.rawExtraBytes)
		job.StderrObservedBytes = int64(len(job.Stderr))
	}
	job, err = state.UpdateCommandRuntimeJob(t.Context(), job, job.Version-1)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func deliveryReplayDurableState(t *testing.T, state *store.SQLiteStore, runID string) string {
	t.Helper()
	ctx := context.Background()
	events, err := state.ListRunEvents(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	checkpoints, err := state.ListWorkspaceCheckpoints(ctx, runID, 2000)
	if err != nil {
		t.Fatal(err)
	}
	cursor, _, err := state.GetWorkspaceCheckpointRunState(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	report, _, err := state.GetLatestStandardCodeDelivery(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal([]any{events, checkpoints, cursor, report})
	if err != nil {
		t.Fatal(err)
	}
	return strconv.Itoa(len(encoded)) + ":" + string(encoded)
}

// Optional output setup occurs before terminal persistence; no immutable Job is
// edited after completion. Raw observed bytes may exceed sanitized UTF-8 bytes.
type deliveryCommandOutputFixture struct {
	stdout, stderr, reason string
	rawExtraBytes          int
}
