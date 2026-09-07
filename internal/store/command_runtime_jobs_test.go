package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/artifact"
	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
)

func TestCommandRuntimeJobLedgerFencesScopeAndPreservesTerminalAudit(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "command-runtime-jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	workspaceRoot := t.TempDir()
	workspace := WorkspaceRecord{ID: "workspace-command-runtime", Name: "command-runtime",
		RootPath: workspaceRoot}
	if err := st.SaveWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	runs := application.NewRunService(st)
	mission, runRecord, err := runs.Create(ctx, application.CreateRunRequest{
		Goal: "persist one fenced managed command", Profile: "code", WorkspaceID: workspace.ID,
		Budget: domain.Budget{MaxTurns: 4, MaxTokens: 1000, MaxToolCalls: 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	profileResult, err := application.NewRunExecutionProfileService(st).Change(ctx,
		application.ChangeRunExecutionProfileRequest{RunID: runRecord.ID, Profile: "local",
			OperationKey: "command-runtime-profile-0001", RequestedBy: "test_operator",
			Reason: "run an owned local command"})
	if err != nil {
		t.Fatal(err)
	}
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true}
	permissionResult, err := application.NewRunExecutionPermissionService(st, capabilities).Change(ctx,
		application.ChangeRunExecutionPermissionRequest{RunID: runRecord.ID,
			Mode:         string(domain.RunExecutionPermissionFullAccess),
			OperationKey: "command-runtime-permission-0001", RequestedBy: "test_operator",
			Reason: "enable the managed command runtime", ConfirmDangerFullAccess: true})
	if err != nil {
		t.Fatal(err)
	}
	startedRun, err := runs.Start(ctx, runRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	lease := acquireTestRunExecutionLease(t, ctx, st, startedRun.ID)
	mode, err := st.GetRunMode(ctx, startedRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	root, found, err := st.GetRootAgent(ctx, startedRun.ID)
	if err != nil || !found {
		t.Fatalf("root agent found=%t err=%v", found, err)
	}
	specialistStarted := time.Now().UTC()
	child, replayed, err := st.AdmitSpecialist(ctx, domain.SpecialistAdmission{
		AgentID:   "agent-command-runtime-specialist",
		SessionID: "session-command-runtime-specialist", RunID: startedRun.ID,
		ParentAgentID: root.ID, Title: "command actor conflict",
		Skills: []string{"model.chat"}, TurnLimit: 1, TokenLimit: 64,
		MaxChildren: 2, CreatedAt: specialistStarted},
		"command-runtime-agent-admission-0001")
	if err != nil || replayed {
		t.Fatalf("admit conflicting actor replayed=%t err=%v", replayed, err)
	}
	childAttempt, replayed, err := st.BeginSpecialistAttempt(ctx,
		domain.AgentAttemptStart{AttemptID: "attempt-command-runtime-specialist",
			RunID: startedRun.ID, AgentID: child.ID, ParentAgentID: root.ID,
			Lease: lease, StartedAt: specialistStarted}, "command-runtime-agent-attempt-0001")
	if err != nil || replayed {
		t.Fatalf("begin conflicting actor attempt replayed=%t err=%v", replayed, err)
	}
	if _, err := st.BeginSupervisorTurn(ctx, lease, "bind Command Runtime actor"); err != nil {
		t.Fatal(err)
	}
	root, found, err = st.GetRootAgent(ctx, startedRun.ID)
	if err != nil || !found {
		t.Fatalf("active root agent found=%t err=%v", found, err)
	}
	if root.ActiveAttemptID == "" {
		t.Fatal("started root Agent has no active attempt for command attribution")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := runner.NormalizeCommandRuntimeSpec(runner.CommandRuntimeSpec{
		Version: runner.CommandRuntimeProtocolVersion, Profile: runner.CommandRuntimeProcess,
		Executable: executable, Arguments: []string{"--help"}, WorkingDirectory: ".",
		Environment: []runner.CommandRuntimeEnvironment{},
		StdinPolicy: runner.CommandRuntimeStdinClosed, CloseInitialStdin: true,
		TimeoutMilliseconds: 1000,
		Output: runner.CommandRuntimeOutputPolicy{InlineBytes: runner.MinCommandRuntimeInlineBytes,
			ArtifactBytes: runner.MinCommandRuntimeInlineBytes},
		Network:     runner.CommandRuntimeNetworkDisabled,
		Credentials: runner.CommandRuntimeCredentialsNone, Purpose: "test persistence",
	}, workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	job := runner.CommandRuntimeJob{
		ID: "command-job-ledger-1", OperationDigest: testCommandRuntimeDigest("operation-1"),
		RequestFingerprint: testCommandRuntimeDigest("request-1"),
		InvocationID:       "invocation-1", RunID: startedRun.ID, MissionID: mission.ID,
		SessionID: startedRun.SessionID, WorkspaceID: workspace.ID, RootAgentID: root.ID,
		WorkspaceRootSHA256: resolved.WorkspaceRootSHA256,
		ModeSnapshotID:      mode.ID, ModeRevision: mode.Revision,
		ProfileSnapshotID:    profileResult.Profile.ID,
		ProfileRevision:      profileResult.Profile.Revision,
		PermissionSnapshotID: permissionResult.Permission.ID,
		PermissionRevision:   permissionResult.Permission.Revision,
		PermissionMode:       permissionResult.Permission.Mode,
		LeaseID:              lease.LeaseID, LeaseGeneration: lease.Generation, LeaseOwnerID: lease.OwnerID,
		Adapter: commandruntimeadapter.HostUnsandboxed(strings.Repeat("d", 64)),
		OwnerID: "command-runtime-owner-1", OwnerGeneration: 1,
		OwnerRenewedAt: now, OwnerExpiresAt: now.Add(time.Minute), IntentJSON: `{}`,
		SpecFingerprint: runner.CommandRuntimeSpecFingerprint(resolved),
		Profile:         resolved.Spec.Profile, ExecutablePath: resolved.ExecutablePath,
		ExecutableSHA256:  resolved.ExecutableSHA256,
		EnvironmentSHA256: resolved.EnvironmentSHA256,
		WorkingDirectory:  resolved.Spec.WorkingDirectory,
		StdinPolicy:       resolved.Spec.StdinPolicy, Network: resolved.Spec.Network,
		Credentials:         resolved.Spec.Credentials,
		TimeoutMilliseconds: resolved.Spec.TimeoutMilliseconds,
		InlineLimitBytes:    resolved.Spec.Output.InlineBytes,
		ArtifactLimitBytes:  resolved.Spec.Output.ArtifactBytes,
		State:               runner.CommandRuntimeJobPrepared, OutputFramesJSON: "[]",
		StdinClosed: true, Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	rootAttribution := domain.AgentAttribution{AgentID: root.ID,
		AgentAttemptID: root.ActiveAttemptID, Source: domain.AgentAttributionRecorded}
	prepared, replayed, err := st.PrepareCommandRuntimeJobForAgent(ctx, job,
		rootAttribution)
	if err != nil || replayed || prepared.ID != job.ID {
		t.Fatalf("prepare=%#v replayed=%t err=%v", prepared, replayed, err)
	}
	threadRecord, err := st.GetThreadByRun(ctx, job.RunID)
	if err != nil {
		t.Fatal(err)
	}
	storedAttribution, err := st.GetThreadCommandRuntimeJobAgentAttribution(ctx,
		threadRecord.ID, job.ID)
	if err != nil || storedAttribution != rootAttribution {
		t.Fatalf("stored Command Agent attribution=%#v err=%v",
			storedAttribution, err)
	}
	replay, replayed, err := st.PrepareCommandRuntimeJobForAgent(ctx, job,
		rootAttribution)
	if err != nil || !replayed || replay.ID != job.ID {
		t.Fatalf("replay=%#v replayed=%t err=%v", replay, replayed, err)
	}
	if _, _, err := st.PrepareCommandRuntimeJobForAgent(ctx, job,
		(domain.AgentAttribution{AgentID: child.ID, AgentAttemptID: childAttempt.ID,
			Source: domain.AgentAttributionRecorded})); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("conflicting Command Agent replay error=%v", err)
	}
	reused := job
	reused.RequestFingerprint = testCommandRuntimeDigest("different-request")
	if _, _, err := st.PrepareCommandRuntimeJobForAgent(ctx, reused,
		rootAttribution); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("operation-key reuse error=%v", err)
	}
	stale := job
	stale.ID = "command-job-ledger-stale"
	stale.OperationDigest = testCommandRuntimeDigest("operation-stale")
	stale.RequestFingerprint = testCommandRuntimeDigest("request-stale")
	stale.LeaseGeneration++
	if _, _, err := st.PrepareCommandRuntimeJobForAgent(ctx, stale,
		rootAttribution); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("stale generation error=%v", err)
	}
	startedAt := now.Add(time.Millisecond)
	running := prepared
	running.State = runner.CommandRuntimeJobRunning
	running.PID = 4242
	running.ProcessGroup = 4242
	running.JobAssignedAtCreation = true
	running.StartedAt = &startedAt
	running.Version++
	running.UpdatedAt = startedAt
	running, err = st.UpdateCommandRuntimeJob(ctx, running, prepared.Version)
	if err != nil {
		t.Fatal(err)
	}
	oversizedUTF8 := strings.Repeat("界", running.ArtifactLimitBytes/2)
	if _, err := st.db.ExecContext(ctx, `UPDATE command_runtime_jobs
		SET stdout = ?, stdout_observed_bytes = ?, version = version + 1
		WHERE id = ?`, oversizedUTF8, len([]byte(oversizedUTF8)), running.ID); err == nil {
		t.Fatal("SQLite accepted multibyte output beyond the byte artifact limit")
	}
	renewedAt := startedAt.Add(time.Millisecond)
	renewed := running
	renewed.OwnerRenewedAt = renewedAt
	renewed.OwnerExpiresAt = renewedAt.Add(time.Minute)
	renewed.Version++
	renewed.UpdatedAt = renewedAt
	running, err = st.UpdateCommandRuntimeJob(ctx, renewed, running.Version)
	if err != nil || !running.OwnerRenewedAt.Equal(renewedAt) {
		t.Fatalf("running owner heartbeat=%#v err=%v", running, err)
	}
	if active, err := st.CommandRuntimeJobOwnershipActive(ctx, running); err != nil || !active {
		t.Fatalf("active command runtime ownership=%t err=%v", active, err)
	}
	expireTestRunExecutionLease(t, ctx, st, lease)
	if active, err := st.CommandRuntimeJobOwnershipActive(ctx, running); err != nil || !active {
		t.Fatalf("owner heartbeat did not survive turn lease release: active=%t err=%v",
			active, err)
	}
	completedAt := startedAt.Add(time.Millisecond)
	exitCode := 0
	terminal := running
	terminal.State = runner.CommandRuntimeJobCompleted
	terminal.Stdout = "ok\n"
	terminal.StdoutObservedBytes = 3
	terminal.OutputCursor = 3
	terminal.ExitCode = &exitCode
	terminal.TreeReaped = true
	terminal.StdoutSHA256 = testCommandRuntimeDigest("stdout")
	terminal.StderrSHA256 = testCommandRuntimeDigest("stderr")
	terminal.CompletedAt = &completedAt
	terminal.Version++
	terminal.UpdatedAt = completedAt
	terminal, err = st.UpdateCommandRuntimeJob(ctx, terminal, running.Version)
	if err != nil || terminal.State != runner.CommandRuntimeJobCompleted {
		t.Fatalf("terminal=%#v err=%v", terminal, err)
	}
	if active, err := st.CommandRuntimeJobOwnershipActive(ctx, terminal); err != nil || active {
		t.Fatalf("terminal command runtime ownership=%t err=%v", active, err)
	}
	jobs, err := st.ListCommandRuntimeJobs(ctx,
		runner.CommandRuntimeListFilter{RunID: startedRun.ID, Limit: 10})
	if err != nil || len(jobs) != 1 || jobs[0].Stdout != "ok\n" || !jobs[0].TreeReaped {
		t.Fatalf("jobs=%#v err=%v", jobs, err)
	}
	operatorJob := job
	operatorJob.ID = "command-job-ledger-operator"
	operatorJob.OperationDigest = testCommandRuntimeDigest("operation-operator")
	operatorJob.RequestFingerprint = testCommandRuntimeDigest("request-operator")
	operatorLease := acquireTestRunExecutionLease(t, ctx, st, startedRun.ID)
	operatorJob.LeaseID = operatorLease.LeaseID
	operatorJob.LeaseGeneration = operatorLease.Generation
	operatorJob.LeaseOwnerID = operatorLease.OwnerID
	operatorAttribution := domain.AgentAttribution{AgentID: root.ID,
		Source: domain.AgentAttributionOperatorRoot}
	if preparedOperator, replayed, err := st.PrepareCommandRuntimeJobForAgent(ctx,
		operatorJob, operatorAttribution); err != nil || replayed ||
		preparedOperator.ID != operatorJob.ID {
		t.Fatalf("prepare operator-root=%#v replayed=%t err=%v",
			preparedOperator, replayed, err)
	}
	storedOperator, err := st.GetThreadCommandRuntimeJobAgentAttribution(ctx,
		threadRecord.ID, operatorJob.ID)
	if err != nil || storedOperator != operatorAttribution ||
		storedOperator.AgentAttemptID != "" {
		t.Fatalf("stored operator-root attribution=%#v err=%v", storedOperator, err)
	}
	if replayedOperator, replayed, err := st.PrepareCommandRuntimeJobForAgent(ctx,
		operatorJob, operatorAttribution); err != nil || !replayed ||
		replayedOperator.ID != operatorJob.ID {
		t.Fatalf("replay operator-root=%#v replayed=%t err=%v",
			replayedOperator, replayed, err)
	}
	if _, _, err := st.PrepareCommandRuntimeJobForAgent(ctx, operatorJob,
		rootAttribution); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("operator-root replay accepted Agent-attempt substitution: %v", err)
	}
	supervisorJob := operatorJob
	supervisorJob.ID = "command-job-ledger-supervisor-root"
	supervisorJob.OperationDigest = testCommandRuntimeDigest("operation-supervisor-root")
	supervisorJob.RequestFingerprint = testCommandRuntimeDigest("request-supervisor-root")
	if preparedSupervisor, replayed, err := st.PrepareCommandRuntimeJob(ctx,
		supervisorJob); err != nil || replayed || preparedSupervisor.ID != supervisorJob.ID {
		t.Fatalf("prepare inferred supervisor-root=%#v replayed=%t err=%v",
			preparedSupervisor, replayed, err)
	}
	storedSupervisor, err := st.GetThreadCommandRuntimeJobAgentAttribution(ctx,
		threadRecord.ID, supervisorJob.ID)
	if err != nil || storedSupervisor != (domain.AgentAttribution{
		AgentID: root.ID, AgentAttemptID: root.ActiveAttemptID,
		Source: domain.AgentAttributionSupervisorRoot}) {
		t.Fatalf("stored inferred supervisor-root attribution=%#v err=%v",
			storedSupervisor, err)
	}
	artifacts, err := st.CaptureToolOutput(ctx, artifact.CaptureRequest{
		RunID: terminal.RunID, SessionID: terminal.SessionID,
		WorkspaceID: terminal.WorkspaceID, SourceID: terminal.ID,
		ToolName: "command_runtime", Outputs: []artifact.Output{{
			Stream: artifact.StreamStdout, MIME: "text/plain; charset=utf-8",
			Content: terminal.Stdout,
		}},
	})
	if err != nil || len(artifacts) != 1 || artifacts[0].SHA256 != artifact.Hash(terminal.Stdout) {
		t.Fatalf("command runtime output artifact=%#v err=%v", artifacts, err)
	}
	if _, err := st.db.ExecContext(ctx,
		`DELETE FROM command_runtime_jobs WHERE id = ?`, terminal.ID); err == nil {
		t.Fatal("terminal command runtime audit row was deleted")
	}
	mutated := terminal
	mutated.OwnerID = strings.Repeat("x", 8)
	mutated.Version++
	mutated.UpdatedAt = completedAt.Add(time.Millisecond)
	if _, err := st.UpdateCommandRuntimeJob(ctx, mutated, terminal.Version); err == nil {
		t.Fatal("immutable command runtime ownership was changed")
	}
}

func testCommandRuntimeDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
