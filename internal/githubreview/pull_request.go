package githubreview

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// PullRequest is public remote evidence. Its text never grants instructions or
// execution permission. Creation is deliberately limited to an existing branch
// in the configured repository; it does not fork or push anything.
type PullRequest struct {
	Repository     RepositoryIdentity `json:"repository"`
	Number         int64              `json:"number"`
	NodeID         string             `json:"node_id"`
	URL            string             `json:"url"`
	Title          TextEvidence       `json:"title"`
	State          string             `json:"state"`
	Draft          bool               `json:"draft"`
	Merged         bool               `json:"merged"`
	HeadRepository string             `json:"head_repository"`
	HeadBranch     string             `json:"head_branch"`
	HeadSHA        string             `json:"head_sha"`
	BaseBranch     string             `json:"base_branch"`
	BaseSHA        string             `json:"base_sha"`
	UpdatedAt      time.Time          `json:"updated_at"`
}

type PullRequestDraft struct {
	Repository RepositoryIdentity  `json:"repository"`
	Credential CredentialReference `json:"credential"`
	HeadBranch string              `json:"head_branch"`
	HeadSHA    string              `json:"head_sha"`
	BaseBranch string              `json:"base_branch"`
	BaseSHA    string              `json:"base_sha"`
	Title      string              `json:"title"`
	Body       string              `json:"body"`
	Marker     string              `json:"marker"`
}

func (d PullRequestDraft) Validate() error {
	if d.Repository.Validate() != nil || d.Credential.Validate() != nil || !validRef(d.HeadBranch) ||
		!validRef(d.BaseBranch) || d.HeadBranch == d.BaseBranch || !validObjectID(d.HeadSHA) ||
		!validObjectID(d.BaseSHA) || !validDigest(d.Marker) || strings.TrimSpace(d.Title) == "" ||
		len([]rune(d.Title)) > 256 || len(d.Body) > 24*1024 || !utf8.ValidString(d.Title) || !utf8.ValidString(d.Body) || strings.ContainsRune(d.Body, 0) || strings.ContainsAny(d.Title, "\r\n\x00") {
		return errors.New("draft pull request intent is invalid")
	}
	return nil
}

const pullRequestMarkerPrefix = "<!-- traverse-board-pr:"

func draftMarker(d PullRequestDraft) string { return pullRequestMarkerPrefix + d.Marker + " -->" }

func pullRequestView(repo RepositoryIdentity, raw pullResponse) (PullRequest, error) {
	if raw.Number <= 0 || !validIdentity(raw.NodeID) || !strings.EqualFold(raw.Base.Repo.FullName, repo.FullName) ||
		(raw.State != "open" && raw.State != "closed") || !validRef(raw.Head.Ref) || !validRef(raw.Base.Ref) ||
		!validObjectID(raw.Head.SHA) || !validObjectID(raw.Base.SHA) || raw.UpdatedAt.IsZero() {
		return PullRequest{}, &Error{Code: FailureMalformed, Message: "GitHub pull request receipt is inconsistent"}
	}
	return PullRequest{Repository: repo, Number: raw.Number, NodeID: raw.NodeID,
		URL:   "https://github.com/" + repo.FullName + "/pull/" + strconv.FormatInt(raw.Number, 10),
		Title: SanitizeRemoteText(raw.Title, MaxTextBytes), State: raw.State, Draft: raw.Draft, Merged: raw.Merged,
		HeadRepository: raw.Head.Repo.FullName, HeadBranch: raw.Head.Ref, HeadSHA: strings.ToLower(raw.Head.SHA),
		BaseBranch: raw.Base.Ref, BaseSHA: strings.ToLower(raw.Base.SHA), UpdatedAt: raw.UpdatedAt}, nil
}

func (c *Client) DefaultBranch(ctx context.Context, repo RepositoryIdentity, ref CredentialReference) (string, error) {
	var result struct {
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
	}
	if repo.Validate() != nil {
		return "", errors.New("GitHub repository is invalid")
	}
	_, err := c.doJSON(ctx, http.MethodGet, repositoryAPIPath(repo), nil, nil, ref, &result)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(result.FullName, repo.FullName) || !validRef(result.DefaultBranch) {
		return "", &Error{Code: FailureMalformed, Message: "GitHub default branch is invalid"}
	}
	return result.DefaultBranch, nil
}

func (c *Client) BranchSHA(ctx context.Context, repo RepositoryIdentity, branch string, ref CredentialReference) (string, error) {
	if repo.Validate() != nil || !validRef(branch) {
		return "", errors.New("GitHub branch request is invalid")
	}
	var result struct {
		Name   string `json:"name"`
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	_, err := c.doJSON(ctx, http.MethodGet, repositoryAPIPath(repo)+"/branches/"+url.PathEscape(branch), nil, nil, ref, &result)
	if err != nil {
		return "", err
	}
	if result.Name != branch || !validObjectID(result.Commit.SHA) {
		return "", &Error{Code: FailureMalformed, Message: "GitHub branch identity is invalid"}
	}
	return strings.ToLower(result.Commit.SHA), nil
}

func (c *Client) ListPullRequests(ctx context.Context, repo RepositoryIdentity, head, base string, ref CredentialReference) ([]PullRequest, error) {
	raw, err := c.listBranchPulls(ctx, repo, head, base, "open", ref)
	if err != nil {
		return nil, err
	}
	result := make([]PullRequest, 0, len(raw))
	for _, item := range raw {
		view, err := pullRequestView(repo, item)
		if err != nil {
			return nil, err
		}
		result = append(result, view)
	}
	return result, nil
}

func (c *Client) listBranchPulls(ctx context.Context, repo RepositoryIdentity, head, base, state string, ref CredentialReference) ([]pullResponse, error) {
	if repo.Validate() != nil || !validRef(head) || !validRef(base) || (state != "open" && state != "all") {
		return nil, errors.New("GitHub pull request discovery is invalid")
	}
	raw, page, err := fetchArrayPages[pullResponse](c, ctx, repositoryAPIPath(repo)+"/pulls", url.Values{
		"head": {repo.Owner + ":" + head}, "base": {base}, "state": {state}}, ref, 100, "branch_pull_requests")
	if err != nil {
		return nil, err
	}
	if !page.Complete {
		return nil, &Error{Code: FailureResponseBound, Message: "Pull request discovery is incomplete; no creation or recovery decision was made"}
	}
	for _, p := range raw {
		if p.Head.Ref != head || p.Base.Ref != base || !strings.EqualFold(p.Head.Repo.FullName, repo.FullName) || !strings.EqualFold(p.Base.Repo.FullName, repo.FullName) {
			return nil, &Error{Code: FailureMalformed, Message: "GitHub pull request discovery returned a different branch or repository"}
		}
	}
	return raw, nil
}

func (c *Client) GetPullRequest(ctx context.Context, repo RepositoryIdentity, number int64, ref CredentialReference) (PullRequest, error) {
	if repo.Validate() != nil || number <= 0 {
		return PullRequest{}, errors.New("GitHub pull request request is invalid")
	}
	var raw pullResponse
	_, err := c.doJSON(ctx, http.MethodGet, repositoryAPIPath(repo)+"/pulls/"+strconv.FormatInt(number, 10), nil, nil, ref, &raw)
	if err != nil {
		return PullRequest{}, err
	}
	if raw.Number != number {
		return PullRequest{}, &Error{Code: FailureMalformed, Message: "GitHub returned a different pull request"}
	}
	return pullRequestView(repo, raw)
}

// ObserveDraft only reads. A missing marker is unknown, never permission to
// retry a POST. A later push/close may change head/state without undoing creation.
func (c *Client) ObserveDraft(ctx context.Context, d PullRequestDraft) (PullRequest, bool, error) {
	if err := d.Validate(); err != nil {
		return PullRequest{}, false, err
	}
	raw, err := c.listBranchPulls(ctx, d.Repository, d.HeadBranch, d.BaseBranch, "all", d.Credential)
	if err != nil {
		return PullRequest{}, false, err
	}
	var found *pullResponse
	for i := range raw {
		if strings.Contains(raw[i].Body, draftMarker(d)) {
			if found != nil {
				return PullRequest{}, false, &Error{Code: FailureConflict, Message: "Multiple pull requests contain the original operation marker"}
			}
			found = &raw[i]
		}
	}
	if found == nil {
		return PullRequest{}, false, nil
	}
	view, err := pullRequestView(d.Repository, *found)
	return view, err == nil, err
}

// CreateDraft is invoked only after the application durably claims its exact
// reviewed operation. It never retries, creates forks, pushes or publishes ready.
func (c *Client) CreateDraft(ctx context.Context, d PullRequestDraft) (PullRequest, error) {
	if err := d.Validate(); err != nil {
		return PullRequest{}, err
	}
	if !c.network.WriteEnabled {
		return PullRequest{}, &Error{Code: FailureNetworkPolicy, Message: "GitHub write-back is disabled"}
	}
	for branch, expected := range map[string]string{d.HeadBranch: d.HeadSHA, d.BaseBranch: d.BaseSHA} {
		actual, err := c.BranchSHA(ctx, d.Repository, branch, d.Credential)
		if err != nil {
			return PullRequest{}, err
		}
		if actual != expected {
			return PullRequest{}, &Error{Code: FailureDrift, Message: "GitHub branch changed after the reviewed draft preview"}
		}
	}
	prs, err := c.ListPullRequests(ctx, d.Repository, d.HeadBranch, d.BaseBranch, d.Credential)
	if err != nil {
		return PullRequest{}, err
	}
	if len(prs) > 0 {
		return PullRequest{}, &Error{Code: FailureConflict, Message: "An open pull request already exists for this branch; inspect it instead of creating another"}
	}
	var raw pullResponse
	_, err = c.doJSON(ctx, http.MethodPost, repositoryAPIPath(d.Repository)+"/pulls", nil, map[string]any{
		"head": d.HeadBranch, "base": d.BaseBranch, "title": d.Title, "body": d.Body + "\n\n" + draftMarker(d), "draft": true}, d.Credential, &raw)
	if err != nil {
		return PullRequest{}, err
	}
	view, err := pullRequestView(d.Repository, raw)
	if err != nil {
		return PullRequest{}, err
	}
	if !view.Draft || view.HeadBranch != d.HeadBranch || view.BaseBranch != d.BaseBranch || !strings.EqualFold(view.HeadRepository, d.Repository.FullName) {
		return PullRequest{}, &Error{Code: FailureMalformed, Message: fmt.Sprintf("Created pull request response did not match the draft intent (number %d); observe the original operation", view.Number)}
	}
	// The returned SHA is retained even if a concurrent push won the HTTP race.
	// The application reports that PR as created but stale, never as uncreated.
	return view, nil
}
