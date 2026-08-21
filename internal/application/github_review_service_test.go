package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/credential"
	"cyberagent-workbench/internal/githubreview"
)

type fakeGitHubReviewRemote struct {
	qualification githubreview.Qualification
	snapshot      githubreview.Snapshot
	executeCalls  int
	recoverCalls  int
}

func (f *fakeGitHubReviewRemote) Qualify(context.Context,
	githubreview.RepositoryIdentity, int64, githubreview.CredentialReference,
) (githubreview.Qualification, error) {
	return f.qualification, nil
}

func (f *fakeGitHubReviewRemote) ReadSnapshot(context.Context,
	githubreview.SnapshotRequest,
) (githubreview.Snapshot, error) {
	return f.snapshot, nil
}

func (f *fakeGitHubReviewRemote) ExecuteWrite(_ context.Context,
	_ githubreview.WriteSpec, preview githubreview.WritePreview,
) (githubreview.WriteReceipt, error) {
	f.executeCalls++
	now := time.Now().UTC()
	return githubreview.WriteReceipt{ProtocolVersion: githubreview.ReceiptProtocolVersion,
		ID: "receipt-success", PreviewID: preview.ID, Operation: preview.Operation,
		Status: githubreview.ReceiptSucceeded, Identity: preview.Identity,
		TargetID: preview.TargetID, ResultID: "comment-result",
		IdempotencyMarker: preview.IdempotencyMarker, StartedAt: now,
		CompletedAt: now.Add(time.Millisecond)}, nil
}

func (f *fakeGitHubReviewRemote) RecoverWrite(_ context.Context,
	_ githubreview.WriteSpec, preview githubreview.WritePreview,
) (githubreview.WriteReceipt, error) {
	f.recoverCalls++
	now := time.Now().UTC()
	return githubreview.WriteReceipt{ProtocolVersion: githubreview.ReceiptProtocolVersion,
		ID: "receipt-recovered", PreviewID: preview.ID, Operation: preview.Operation,
		Status: githubreview.ReceiptRecovered, Identity: preview.Identity,
		TargetID: preview.TargetID, ResultID: "comment-result",
		IdempotencyMarker: preview.IdempotencyMarker, Recovered: true,
		StartedAt: now, CompletedAt: now.Add(time.Millisecond)}, nil
}

func TestGitHubReviewServiceEvidenceApprovalAndReceipt(t *testing.T) {
	fixture := newGitAdvancedApplicationFixture(t)
	base := strings.TrimSpace(runFixtureGit(t, "-C", fixture.root, "rev-parse", "HEAD"))
	path := filepath.Join(fixture.root, "base.txt")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(raw), "two\n", "TWO\n", 1)),
		0o600); err != nil {
		t.Fatal(err)
	}
	runFixtureGit(t, "-C", fixture.root, "add", "base.txt")
	runFixtureGit(t, "-C", fixture.root, "commit", "--quiet", "-m", "review head")
	head := strings.TrimSpace(runFixtureGit(t, "-C", fixture.root, "rev-parse", "HEAD"))
	repositoryID, err := githubreview.ParseRepository("owner/repository")
	if err != nil {
		t.Fatal(err)
	}
	credentialRef := githubreview.CredentialReference{Name: "github-review-test",
		Kind: githubreview.AuthFineGrainedPAT}
	capability := githubreview.CapabilitySnapshot{
		ProtocolVersion: githubreview.CapabilityProtocolVersion,
		Generation:      githubreview.Fingerprint("fixture-capability"),
		APIHost:         "api.github.com", APIVersion: githubreview.RESTAPIVersion,
		AccountLogin: "reviewer", Repository: repositoryID, Credential: credentialRef,
		Permissions: map[string]string{"metadata": "read", "pull_requests": "write"},
		Read:        true, Reply: true, Resolve: true, Review: true, RequestReviewer: true,
		CapturedAt: time.Now().UTC(),
	}
	identity := githubreview.PullRequestIdentity{Repository: repositoryID, Number: 9,
		NodeID: "pull-node", State: "open", BaseRef: "main", BaseSHA: base,
		HeadRef: "review-head", HeadSHA: head, MergeBaseSHA: base,
		UpdatedAt: time.Now().UTC()}
	text := githubreview.SanitizeRemoteText("review body", 1024)
	snapshot := githubreview.Snapshot{ProtocolVersion: githubreview.SnapshotProtocolVersion,
		Identity: identity, Capability: capability, Title: text, Body: text,
		Author: "author", RequestedReviewers: []string{}, Files: []githubreview.ChangedFile{},
		Reviews: []githubreview.Review{}, Threads: []githubreview.ReviewThread{{
			ID: "thread-node", Path: "base.txt", Side: "RIGHT", Line: 2,
			Comments: []githubreview.Comment{{NodeID: "comment-node", Author: "reviewer",
				Body: text, Position: githubreview.Position{Path: "base.txt", Side: "RIGHT",
					Line: 2, CommitSHA: head}, CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC()}},
		}}, LooseComments: []githubreview.Comment{}, CheckSuites: []githubreview.CheckSuite{},
		CheckRuns: []githubreview.CheckRun{}, Jobs: []githubreview.WorkflowJob{},
		Artifacts: []githubreview.ArtifactMetadata{}, Pagination: []githubreview.PageEvidence{},
		State: githubreview.EvidenceVerified, Omissions: []string{}, FetchedAt: time.Now().UTC()}
	snapshot.Finalize()
	remote := &fakeGitHubReviewRemote{snapshot: snapshot,
		qualification: githubreview.Qualification{ProtocolVersion: githubreview.ProtocolVersion,
			Eligible: true, HostReachable: true, CredentialConfigured: true,
			Authenticated: true, SSOAuthorized: true, RepositoryAccessible: true,
			PullRequestAccessible: true, NetworkAllowed: true, Capability: capability,
			Diagnostics: []githubreview.Diagnostic{}, CheckedAt: time.Now().UTC()}}
	credentials := credential.NewMemoryStore()
	if err := credentials.Put(t.Context(), credentialRef.Name, "test-secret-token"); err != nil {
		t.Fatal(err)
	}
	service, err := NewGitHubReviewService(fixture.state, credentials, fixture.executor,
		fixture.capabilities)
	if err != nil {
		t.Fatal(err)
	}
	service.clientFactory = func(*githubreview.AuthManager,
		githubreview.Connection) (githubReviewRemote, error) {
		return remote, nil
	}
	configured, err := service.Configure(t.Context(), GitHubReviewConfigureRequest{
		ProtocolVersion: GitHubReviewAPIProtocolVersion, Repository: repositoryID,
		Credential: credentialRef, AllowedLogHosts: []string{}, WriteEnabled: true, Enabled: true,
		RequestedBy: "test_operator"})
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := service.Configure(t.Context(), GitHubReviewConfigureRequest{
		ProtocolVersion: GitHubReviewAPIProtocolVersion, Repository: repositoryID,
		Credential: credentialRef, AllowedLogHosts: []string{}, WriteEnabled: true, Enabled: true,
		RequestedBy: "test_operator"})
	if err != nil || !repeated.Replayed || repeated.Connection.Generation != 1 {
		t.Fatalf("idempotent configure: %v %#v", err, repeated)
	}
	fetched, err := service.Fetch(t.Context(), GitHubReviewFetchRequest{
		ProtocolVersion: GitHubReviewAPIProtocolVersion,
		ConnectionID:    configured.Connection.ID, PullRequest: identity.Number})
	if err != nil || fetched.Snapshot.ID != snapshot.ID {
		t.Fatalf("fetch: %v %#v", err, fetched)
	}
	evidence, err := service.BuildEvidence(t.Context(), GitHubReviewEvidenceRequest{
		ProtocolVersion: GitHubReviewAPIProtocolVersion, RunID: fixture.run.ID,
		SnapshotID: snapshot.ID})
	if err != nil || evidence.Evidence.Graph.State != githubreview.EvidenceVerified ||
		len(evidence.Evidence.Graph.Mappings) != 1 ||
		evidence.Evidence.Graph.Mappings[0].HunkID == "" {
		t.Fatalf("evidence: %v %#v", err, evidence)
	}
	spec := githubreview.WriteSpec{ProtocolVersion: githubreview.WriteProtocolVersion,
		Operation: githubreview.WriteReply, Identity: identity, Credential: credentialRef,
		CapabilityGeneration: capability.Generation, TargetID: "thread-node",
		Body: "Applied the requested change.", Reviewers: []string{},
		LocalChangeSummary: "one reviewed hunk", ValidationSummary: "focused test passed"}
	reviewRequest := GitHubReviewWriteReviewRequest{
		ProtocolVersion: GitHubReviewAPIProtocolVersion, RunID: fixture.run.ID,
		ConnectionID: configured.Connection.ID, SnapshotID: snapshot.ID,
		OperationKey: "reply-once", RequestedBy: "test_operator", Spec: spec}
	reviewed, err := service.ReviewWrite(t.Context(), reviewRequest)
	if err != nil || reviewed.Approval.Status != approval.StatusPending {
		t.Fatalf("review: %v %#v", err, reviewed)
	}
	decision, err := fixture.state.DecideApproval(t.Context(), approval.DecisionRequest{
		ProposalID: reviewed.Operation.ID, IdempotencyKey: "approve-reply-once",
		Action: approval.ActionApprove, ReviewedBy: "test_operator"})
	if err != nil {
		t.Fatal(err)
	}
	executed, err := service.ExecuteWrite(t.Context(), GitHubReviewWriteExecuteRequest{
		ProtocolVersion: GitHubReviewAPIProtocolVersion, RunID: fixture.run.ID,
		OperationID: reviewed.Operation.ID, ApprovalID: decision.Approval.ID,
		RequestedBy: "test_operator"})
	if err != nil || executed.Receipt.Status != githubreview.ReceiptSucceeded ||
		remote.executeCalls != 1 {
		t.Fatalf("execute: %v %#v calls=%d", err, executed, remote.executeCalls)
	}
	replayed, err := service.ExecuteWrite(t.Context(), GitHubReviewWriteExecuteRequest{
		ProtocolVersion: GitHubReviewAPIProtocolVersion, RunID: fixture.run.ID,
		OperationID: reviewed.Operation.ID, ApprovalID: decision.Approval.ID,
		RequestedBy: "test_operator"})
	if err != nil || !replayed.Replayed || remote.executeCalls != 1 {
		t.Fatalf("replay: %v %#v calls=%d", err, replayed, remote.executeCalls)
	}
	reviewedAgain, err := service.ReviewWrite(t.Context(), reviewRequest)
	if err != nil || !reviewedAgain.Replayed ||
		reviewedAgain.Operation.ID != reviewed.Operation.ID ||
		reviewedAgain.Approval.ID != decision.Approval.ID ||
		reviewedAgain.Approval.Status != approval.StatusApproved {
		t.Fatalf("review replay after completion: %v %#v", err, reviewedAgain)
	}
	decisionAgain, err := fixture.state.DecideApproval(t.Context(), approval.DecisionRequest{
		ProposalID: reviewedAgain.Operation.ID, IdempotencyKey: "approve-reply-once",
		Action: approval.ActionApprove, ReviewedBy: "test_operator"})
	if err != nil || !decisionAgain.Replayed || decisionAgain.Approval.ID != decision.Approval.ID {
		t.Fatalf("approval replay after completion: %v %#v", err, decisionAgain)
	}
	executedAgain, err := service.ExecuteWrite(t.Context(), GitHubReviewWriteExecuteRequest{
		ProtocolVersion: GitHubReviewAPIProtocolVersion, RunID: fixture.run.ID,
		OperationID: reviewedAgain.Operation.ID, ApprovalID: decisionAgain.Approval.ID,
		RequestedBy: "test_operator"})
	if err != nil || !executedAgain.Replayed || remote.executeCalls != 1 {
		t.Fatalf("full write replay after completion: %v %#v calls=%d", err, executedAgain,
			remote.executeCalls)
	}
	recoverySpec := spec
	recoverySpec.Body = "Recover this exact reply after restart."
	recoveryReview, err := service.ReviewWrite(t.Context(), GitHubReviewWriteReviewRequest{
		ProtocolVersion: GitHubReviewAPIProtocolVersion, RunID: fixture.run.ID,
		ConnectionID: configured.Connection.ID, SnapshotID: snapshot.ID,
		OperationKey: "reply-recovery", RequestedBy: "test_operator", Spec: recoverySpec})
	if err != nil {
		t.Fatal(err)
	}
	recoveryDecision, err := fixture.state.DecideApproval(t.Context(), approval.DecisionRequest{
		ProposalID: recoveryReview.Operation.ID, IdempotencyKey: "approve-reply-recovery",
		Action: approval.ActionApprove, ReviewedBy: "test_operator"})
	if err != nil {
		t.Fatal(err)
	}
	if _, replayed, err := fixture.state.StartGitHubReviewWrite(t.Context(),
		recoveryReview.Operation.ID, recoveryDecision.Approval.ID,
		recoveryReview.Operation.ApprovalFingerprint, time.Now().UTC()); err != nil || replayed {
		t.Fatalf("simulate uncertain write start: %v replayed=%t", err, replayed)
	}
	restarted, err := NewGitHubReviewService(fixture.state, credentials, fixture.executor,
		fixture.capabilities)
	if err != nil {
		t.Fatal(err)
	}
	restarted.clientFactory = service.clientFactory
	reconciled, err := restarted.ReconcileStartup(t.Context(), 100)
	if err != nil || reconciled.Examined != 1 || reconciled.Recovered != 1 ||
		remote.recoverCalls != 1 || len(reconciled.OperationIDs) != 1 ||
		reconciled.OperationIDs[0] != recoveryReview.Operation.ID {
		t.Fatalf("restart recovery: %v %#v calls=%d", err, reconciled, remote.recoverCalls)
	}
	reconciledAgain, err := restarted.ReconcileStartup(t.Context(), 100)
	if err != nil || reconciledAgain.Examined != 0 || remote.recoverCalls != 1 {
		t.Fatalf("restart recovery replay: %v %#v calls=%d", err, reconciledAgain,
			remote.recoverCalls)
	}
	projection, err := service.Projection(t.Context(), fixture.run.ID,
		configured.Connection.ID, identity.Number, 20)
	if err != nil || len(projection.Snapshots) != 1 || len(projection.Evidence) != 1 ||
		len(projection.Writes) != 2 {
		t.Fatalf("projection: %v %#v", err, projection)
	}
	encoded, _ := json.Marshal(projection)
	if strings.Contains(string(encoded), "test-secret-token") {
		t.Fatal("projection exposed the credential value")
	}
	readOnly, err := service.Configure(t.Context(), GitHubReviewConfigureRequest{
		ProtocolVersion: GitHubReviewAPIProtocolVersion, Repository: repositoryID,
		Credential: credentialRef, AllowedLogHosts: []string{}, WriteEnabled: false, Enabled: true,
		ExpectedGeneration: configured.Connection.Generation, RequestedBy: "test_operator"})
	if err != nil || readOnly.Connection.Network.WriteEnabled ||
		readOnly.Connection.Generation != configured.Connection.Generation+1 {
		t.Fatalf("disable connection write-back: %v %#v", err, readOnly)
	}
	_, err = service.ReviewWrite(t.Context(), GitHubReviewWriteReviewRequest{
		ProtocolVersion: GitHubReviewAPIProtocolVersion, RunID: fixture.run.ID,
		ConnectionID: readOnly.Connection.ID, SnapshotID: snapshot.ID,
		OperationKey: "reply-while-read-only", RequestedBy: "test_operator", Spec: spec})
	if apperror.CodeOf(err) != apperror.CodePolicyDenied {
		t.Fatalf("read-only connection admitted a GitHub write: %v", err)
	}
}
