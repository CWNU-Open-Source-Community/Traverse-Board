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
	prepared, replayed, err := st.PrepareCommandRuntimeJob(ctx, job)
	if err != nil || replayed || prepared.ID != job.ID {
		t.Fatalf("prepare=%#v replayed=%t err=%v", prepared, replayed, err)
	}
	replay, replayed, err := st.PrepareCommandRuntimeJob(ctx, job)
	if err != nil || !replayed || replay.ID != job.ID {
		t.Fatalf("replay=%#v replayed=%t err=%v", replay, replayed, err)
	}
	reused := job
	reused.RequestFingerprint = testCommandRuntimeDigest("different-request")
	if _, _, err := st.PrepareCommandRuntimeJob(ctx, reused); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("operation-key reuse error=%v", err)
	}
	stale := job
	stale.ID = "command-job-ledger-stale"
	stale.OperationDigest = testCommandRuntimeDigest("operation-stale")
	stale.RequestFingerprint = testCommandRuntimeDigest("request-stale")
	stale.LeaseGeneration++
	if _, _, err := st.PrepareCommandRuntimeJob(ctx, stale); apperror.CodeOf(err) != apperror.CodeConflict {
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
