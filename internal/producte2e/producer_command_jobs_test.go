package producte2e

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/drydock"
	"cyberagent-workbench/internal/packagede2e"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/standardcodedelivery"
)

func TestValidateCommandJobsBindsSourceIdentityToExactDrydockRoot(t *testing.T) {
	source, owned, otherOwned := t.TempDir(), t.TempDir(), t.TempDir()
	rootDigest := func(path string) string {
		t.Helper()
		digest, err := runner.CommandRuntimeWorkspaceRootSHA256(path)
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	sourceSHA, ownedSHA, otherSHA := rootDigest(source), rootDigest(owned), rootDigest(otherOwned)
	if sourceSHA == ownedSHA || ownedSHA == otherSHA {
		t.Fatal("test roots must have distinct canonical identities")
	}
	tests := []struct {
		name    string
		mutate  func(*runFacts)
		wantErr bool
	}{
		{name: "source control identity with owned physical root"},
		{name: "source physical root", wantErr: true, mutate: func(f *runFacts) {
			f.jobs[0].WorkspaceRootSHA256 = sourceSHA
		}},
		{name: "another Run owned physical root", wantErr: true, mutate: func(f *runFacts) {
			f.jobs[0].WorkspaceRootSHA256 = otherSHA
		}},
		{name: "relabelled owned workspace", wantErr: true, mutate: func(f *runFacts) {
			for i := range f.jobs {
				f.jobs[i].WorkspaceID = f.drydock.WorkspaceID
			}
		}},
		{name: "cross mission", wantErr: true, mutate: func(f *runFacts) {
			f.jobs[0].MissionID = "mission-other"
		}},
		{name: "cross Run", wantErr: true, mutate: func(f *runFacts) {
			f.jobs[0].RunID = "run-other"
		}},
		{name: "cross session", wantErr: true, mutate: func(f *runFacts) {
			f.jobs[0].SessionID = "session-other"
		}},
		{name: "missing owned directory", wantErr: true, mutate: func(f *runFacts) {
			f.drydock.Path = filepath.Join(owned, "missing")
		}},
		{name: "host receipt cannot satisfy sandbox proof", wantErr: true, mutate: func(f *runFacts) {
			f.jobs[0].Adapter = commandruntimeadapter.HostUnsandboxed("test-generation")
			f.jobs[0].PermissionMode = domain.RunExecutionPermissionFullAccess
		}},
		{name: "truncated process output", wantErr: true, mutate: func(f *runFacts) {
			f.jobs[0].TruncationReason = "artifact_limit"
		}},
		{name: "failure without passing retry", wantErr: true, mutate: func(f *runFacts) {
			f.jobs = f.jobs[:1]
		}},
		{name: "report output mismatch", wantErr: true, mutate: func(f *runFacts) {
			f.delivery.Verifications[0].StdoutSHA256 = digestBytes([]byte("different output"))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			facts, fixture := collectorCommandFacts(t, owned, ownedSHA)
			if test.mutate != nil {
				test.mutate(&facts)
			}
			failed, passed, artifacts, err := validateCommandJobs(facts, fixture, "local")
			if (err != nil) != test.wantErr {
				t.Fatalf("failed=%d passed=%d artifacts=%d err=%v; wantErr=%t",
					failed, passed, artifacts, err, test.wantErr)
			}
			if err == nil && (failed != 1 || passed != 1 || artifacts != 1) {
				t.Fatalf("unexpected evidence counts: %d/%d/%d", failed, passed, artifacts)
			}
		})
	}
}

// These are collector input fixtures, not receipts produced by a live sandbox.
// The filesystem roots and their canonical digests above are real; accepting
// these inputs only tests the collector contract, not OS execution support.
func collectorCommandFacts(t *testing.T, ownedPath, ownedSHA string) (runFacts, packagede2e.FixtureRepository) {
	t.Helper()
	fixture := packagede2e.FixtureRepository{Command: packagede2e.FixtureCommand{
		Executable: "go", Arguments: []string{"test", "./..."}}}
	intent, err := json.Marshal(commandIntent{Version: runner.CommandRuntimeProtocolVersion,
		Profile: string(runner.CommandRuntimeProcess), ExecutablePath: "go",
		Argv: fixture.Command.Arguments, WorkingDirectory: ".",
		Network: string(runner.CommandRuntimeNetworkDisabled), Credentials: string(runner.CommandRuntimeCredentialsNone)})
	if err != nil {
		t.Fatal(err)
	}
	makeJob := func(id string, start time.Time, exit int) runner.CommandRuntimeJob {
		completed := start.Add(time.Second)
		digest := digestBytes([]byte(id))
		job := runner.CommandRuntimeJob{ID: id, OperationDigest: digest, RequestFingerprint: digest,
			InvocationID: "invocation-" + id, RunID: "run-1", MissionID: "mission-1",
			SessionID: "session-1", WorkspaceID: "source-ws-1", RootAgentID: "agent-1",
			WorkspaceRootSHA256: ownedSHA, ModeSnapshotID: "mode-1", ModeRevision: 1,
			ProfileSnapshotID: "profile-1", ProfileRevision: 1, PermissionSnapshotID: "permission-1",
			PermissionRevision: 1, PermissionMode: domain.RunExecutionPermissionWorkspaceAccess,
			LeaseID: "lease-1", LeaseGeneration: 1, LeaseOwnerID: "lease-owner-1",
			Adapter: commandruntimeadapter.SandboxedWorkspace("local", "test-local", "test-generation"),
			OwnerID: "owner-1", OwnerGeneration: 1, OwnerRenewedAt: start, OwnerExpiresAt: start.Add(time.Minute),
			IntentJSON: string(intent), SpecFingerprint: digest, Profile: runner.CommandRuntimeProcess,
			ExecutablePath: "go", ExecutableSHA256: digest, EnvironmentSHA256: digest, WorkingDirectory: ".",
			StdinPolicy: runner.CommandRuntimeStdinClosed, Network: runner.CommandRuntimeNetworkDisabled,
			Credentials: runner.CommandRuntimeCredentialsNone, TimeoutMilliseconds: 1000,
			InlineLimitBytes: runner.MinCommandRuntimeInlineBytes, ArtifactLimitBytes: runner.MinCommandRuntimeInlineBytes,
			State: runner.CommandRuntimeJobCompleted, OutputFramesJSON: "[]", StdoutSHA256: digestBytes(nil),
			StderrSHA256: digestBytes(nil), ExitCode: &exit, TreeReaped: true, JobAssignedAtCreation: true,
			StdinClosed: true, Version: 1, CreatedAt: start, StartedAt: &start, CompletedAt: &completed, UpdatedAt: completed}
		if exit != 0 {
			job.State = runner.CommandRuntimeJobFailed
		}
		if err := job.Validate(); err != nil {
			t.Fatalf("invalid collector input fixture: %v", err)
		}
		return job
	}
	now := time.Date(2026, time.September, 9, 0, 0, 0, 0, time.UTC)
	failed, passed := makeJob("job-failed", now, 1), makeJob("job-passed", now.Add(time.Minute), 0)
	return runFacts{
		run:     domain.Run{ID: "run-1", MissionID: "mission-1", SessionID: "session-1"},
		mission: domain.Mission{ID: "mission-1", WorkspaceID: "source-ws-1"},
		drydock: drydock.Workspace{ID: "drydock-1", WorkspaceID: "drydock-ws-1", SourceWorkspaceID: "source-ws-1", Path: ownedPath},
		jobs:    []runner.CommandRuntimeJob{failed, passed},
		delivery: standardcodedelivery.Report{Verifications: []standardcodedelivery.Verification{{
			JobID: passed.ID, Conclusion: standardcodedelivery.StatusPassed, ExitCode: passed.ExitCode,
			State: string(passed.State), CurrentRevision: true, TreeReaped: true,
			SpecSHA256: passed.SpecFingerprint, StdoutSHA256: passed.StdoutSHA256, StderrSHA256: passed.StderrSHA256,
			Backend: "local", Artifacts: []standardcodedelivery.Artifact{{ID: "artifact-1"}},
		}}},
	}, fixture
}
