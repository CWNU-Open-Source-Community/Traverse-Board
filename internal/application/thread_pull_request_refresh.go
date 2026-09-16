package application

import (
	"context"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/githubreview"
)

// Refresh does actual network reads. Snapshot failures remain failures; a local
// diff that cannot be bound does not erase useful remote CI/comment evidence.
func (s *ThreadPullRequestService) Refresh(ctx context.Context, threadID string, request ThreadPullRequestRefreshRequest) (ThreadPullRequestRefreshResult, error) {
	if request.Version != ThreadPullRequestVersion || request.PullRequest <= 0 {
		return ThreadPullRequestRefreshResult{}, apperror.New(apperror.CodeInvalidArgument, "an existing pull request number is required")
	}
	connection, _, _, err := s.client(ctx, request.ConnectionID)
	if err != nil {
		return ThreadPullRequestRefreshResult{}, err
	}
	bound, err := s.git.CaptureThreadGitContext(ctx, threadID)
	if err != nil {
		return ThreadPullRequestRefreshResult{}, err
	}
	if err = requireThreadPRRepository(bound, connection.Repository); err != nil {
		return ThreadPullRequestRefreshResult{}, err
	}
	fetched, err := s.review.Fetch(ctx, GitHubReviewFetchRequest{ProtocolVersion: GitHubReviewAPIProtocolVersion, ConnectionID: connection.ID, PullRequest: request.PullRequest})
	if err != nil {
		return ThreadPullRequestRefreshResult{}, err
	}
	snapshot := fetched.Snapshot
	value := ThreadPullRequestRefreshResult{Version: ThreadPullRequestVersion, ThreadID: threadID, RunID: bound.RunID, Snapshot: snapshot, LocalHeadSHA: bound.HeadSHA, HeadMatchesLocal: bound.HeadSHA == snapshot.Identity.HeadSHA, Stale: snapshot.State == githubreview.EvidenceStale, Omissions: append([]string{}, snapshot.Omissions...)}
	fresh, err := s.git.CaptureThreadGitContext(ctx, threadID)
	if err != nil || fresh.RunID != bound.RunID || fresh.WorkspaceID != bound.WorkspaceID || fresh.StatusFingerprint != bound.StatusFingerprint {
		value.Stale = true
		value.Omissions = append(value.Omissions, "task Git state changed while refreshing; local evidence was not rebound")
		return value, nil
	}
	if !value.HeadMatchesLocal || value.Stale {
		value.Omissions = append(value.Omissions, "local diff evidence requires the exact current PR head and a non-stale remote snapshot")
		return value, nil
	}
	diff, err := s.review.executor.CaptureReviewDiffEvidence(ctx, bound.RootPath, snapshot.Identity.BaseSHA, snapshot.Identity.HeadSHA)
	if err != nil {
		value.Omissions = append(value.Omissions, "local PR base/head diff is unavailable; remote evidence remains readable")
		return value, nil
	}
	graph, err := githubreview.BuildEvidenceGraph(snapshot, diff, nil, s.now())
	if err != nil {
		value.Omissions = append(value.Omissions, "remote snapshot could not be bound to the local diff")
		return value, nil
	}
	id := githubreview.Fingerprint("github-review-evidence-record", bound.RunID, bound.WorkspaceID, graph.Fingerprint)
	record, _, err := s.review.store.SaveGitHubReviewEvidence(ctx, githubreview.EvidenceRecord{ID: "ghg-" + id[:32], RunID: bound.RunID, WorkspaceID: bound.WorkspaceID, Graph: graph})
	if err != nil {
		return value, apperror.Normalize(err)
	}
	value.Evidence = &record
	return value, nil
}
