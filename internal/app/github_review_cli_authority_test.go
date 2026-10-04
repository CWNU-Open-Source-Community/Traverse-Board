package app

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/credential"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/githubreview"
	"cyberagent-workbench/internal/store"
)

// A write-disabled connection reaches the business gate without performing any
// credential lookup or network request. A missing runtime authority would stop
// Ask/Auto earlier, which is the CLI composition regression exercised here.
func TestGitHubReviewCLIUsesCurrentAuthorityBeforeConnectionWriteGate(t *testing.T) {
	for _, mode := range []string{"ask", "auto", "full"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			state, err := store.Open(filepath.Join(home, "review.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = state.Close() })
			workspace := store.WorkspaceRecord{ID: "workspace-review-cli", Name: "review-cli", RootPath: t.TempDir(), CreatedAt: time.Now().UTC()}
			if err := state.SaveWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			runs := application.NewRunService(state)
			_, run, err := runs.Create(t.Context(), application.CreateRunRequest{Goal: "CLI review authority", Profile: "code", Surface: "code", Phase: "deliver", WorkspaceID: workspace.ID, Budget: domain.Budget{MaxTurns: 3}})
			if err != nil {
				t.Fatal(err)
			}
			if mode != "ask" {
				seed := cliExecutionPermissionCapabilities(true, true)
				if _, err := application.NewRunExecutionPermissionService(state, seed).Change(t.Context(), application.ChangeRunExecutionPermissionRequest{RunID: run.ID, Mode: mode, OperationKey: "review-cli-permission", RequestedBy: "test_operator", ConfirmFull: mode == "full"}); err != nil {
					t.Fatal(err)
				}
				seed.RuntimeAuthority.RevokeRun(run.ID)
			}
			if _, err := runs.Start(t.Context(), run.ID); err != nil {
				t.Fatal(err)
			}
			app := &App{home: home, store: state, credentials: credential.NewMemoryStore(), out: io.Discard, errOut: io.Discard}
			service, err := app.newGitHubReviewService("", cliExecutionPermissionCapabilities(true, true))
			if err != nil {
				t.Fatal(err)
			}
			repo, err := githubreview.ParseRepository("fixture/review")
			if err != nil {
				t.Fatal(err)
			}
			ref := githubreview.CredentialReference{Name: "unused-memory-reference", Kind: githubreview.AuthFineGrainedPAT}
			configured, err := service.Configure(t.Context(), application.GitHubReviewConfigureRequest{ProtocolVersion: application.GitHubReviewAPIProtocolVersion, Repository: repo, Credential: ref, Enabled: true, WriteEnabled: false, RequestedBy: "test_operator"})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			snapshot := githubreview.Snapshot{ProtocolVersion: githubreview.SnapshotProtocolVersion,
				Identity:   githubreview.PullRequestIdentity{Repository: repo, Number: 1, NodeID: "pull-node", State: "open", BaseRef: "main", BaseSHA: strings.Repeat("1", 40), HeadRef: "review", HeadSHA: strings.Repeat("2", 40), MergeBaseSHA: strings.Repeat("1", 40), UpdatedAt: now},
				Capability: githubreview.CapabilitySnapshot{ProtocolVersion: githubreview.CapabilityProtocolVersion, Generation: githubreview.Fingerprint("cli-fixture"), APIHost: "api.github.com", APIVersion: githubreview.RESTAPIVersion, AccountLogin: "reviewer", Repository: repo, Credential: ref, Read: true, Reply: true, CapturedAt: now},
				State:      githubreview.EvidenceVerified, FetchedAt: now}
			snapshot.Finalize()
			if _, _, err := state.SaveGitHubReviewSnapshot(t.Context(), configured.Connection.ID, snapshot); err != nil {
				t.Fatal(err)
			}
			args := []string{"write", string(githubreview.WriteReply), "--run", run.ID,
				"--connection", configured.Connection.ID, "--snapshot", snapshot.ID, "--operation-key", "cli-review-write",
				"--target", "thread-node", "--body", "Fixture reply", "--confirm",
				"--enable-github-review", "--enable-permission-control", "--enable-danger-full-access"}
			assertGate := func(args []string, gate string) {
				t.Helper()
				err := app.githubReviewCommand(t.Context(), args)
				if err == nil || apperror.CodeOf(apperror.Normalize(err)) != apperror.CodePolicyDenied || !strings.Contains(err.Error(), gate) {
					t.Fatalf("expected %q: %v", gate, err)
				}
			}
			if mode == "full" {
				assertGate(args, "active approval mode and runtime authority")
				assertGate(append(append([]string{}, args...), "--confirm-full"), "write-back is disabled")
				assertGate(args, "active approval mode and runtime authority")
			} else {
				assertGate(args, "write-back is disabled")
			}
			approvals, err := state.ListApprovals(t.Context(), approval.ListFilter{RunID: run.ID, Limit: 100})
			if err != nil || len(approvals) != 0 {
				t.Fatalf("closed connection created approvals: %v %#v", err, approvals)
			}
		})
	}
}
