package webevidence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const githubConnectorVersion = "github-public.v1"

type GitHubSourceConnector struct{ client *SafeHTTPClient }

func NewGitHubSourceConnector(client *SafeHTTPClient) *GitHubSourceConnector {
	if client == nil {
		client = NewSafeHTTPClient()
	}
	return &GitHubSourceConnector{client: client}
}

func (*GitHubSourceConnector) Name() string    { return "github" }
func (*GitHubSourceConnector) Version() string { return githubConnectorVersion }
func (*GitHubSourceConnector) SearchEndpoint() string {
	return "https://api.github.com/search/issues"
}

func (*GitHubSourceConnector) MatchURL(rawURL string) bool {
	_, _, _, ok := parseGitHubIssueURL(rawURL)
	return ok
}

func (c *GitHubSourceConnector) Search(ctx context.Context, query string, limit int,
	authority NetworkAuthority,
) ([]ConnectorSearchItem, error) {
	if limit < 1 || limit > MaxSources {
		return nil, newConnectorError("invalid_request", nil)
	}
	endpoint, _ := url.Parse(c.SearchEndpoint())
	parameters := endpoint.Query()
	parameters.Set("q", query)
	parameters.Set("sort", "updated")
	parameters.Set("order", "desc")
	parameters.Set("per_page", strconv.Itoa(limit))
	endpoint.RawQuery = parameters.Encode()
	var response struct {
		Items []struct {
			ID        int64  `json:"id"`
			HTMLURL   string `json:"html_url"`
			Title     string `json:"title"`
			Body      string `json:"body"`
			UpdatedAt string `json:"updated_at"`
			User      struct {
				Login string `json:"login"`
			} `json:"user"`
		} `json:"items"`
	}
	_, err := connectorGetJSON(ctx, c.client, endpoint.String(), authority, &response)
	if err != nil {
		return nil, err
	}
	items := make([]ConnectorSearchItem, 0, min(limit, len(response.Items)))
	seen := make(map[string]struct{}, limit)
	for _, item := range response.Items {
		canonical, err := CanonicalizePublicHTTPSURL(item.HTMLURL)
		if err != nil {
			continue
		}
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		snippet := connectorHTMLText(item.Body)
		if item.User.Login != "" {
			snippet = "@" + item.User.Login + ": " + snippet
		}
		items = append(items, ConnectorSearchItem{URL: canonical,
			Title: boundedCleanText(item.Title, 1024), Snippet: boundedSnippet(snippet),
			PublishedAt: boundedCleanText(item.UpdatedAt, 128),
			ExternalID:  strconv.FormatInt(item.ID, 10)})
		if len(items) == limit {
			break
		}
	}
	return items, nil
}

func (c *GitHubSourceConnector) Read(ctx context.Context, rawURL string, maxItems int,
	authority NetworkAuthority,
) (ConnectorDocument, error) {
	owner, repository, number, ok := parseGitHubIssueURL(rawURL)
	if !ok {
		return ConnectorDocument{}, newConnectorError("unsupported_url", nil)
	}
	if maxItems <= 0 {
		maxItems = DefaultConnectorItemLimit
	}
	if maxItems > MaxConnectorItemLimit {
		return ConnectorDocument{}, newConnectorError("invalid_request", nil)
	}
	canonical, _ := CanonicalizePublicHTTPSURL(rawURL)
	apiBase := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d",
		url.PathEscape(owner), url.PathEscape(repository), number)
	var issue struct {
		ID          int64           `json:"id"`
		HTMLURL     string          `json:"html_url"`
		Title       string          `json:"title"`
		Body        string          `json:"body"`
		CreatedAt   string          `json:"created_at"`
		Comments    int             `json:"comments"`
		PullRequest json.RawMessage `json:"pull_request"`
		User        struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	issueDocument, err := connectorGetJSON(ctx, c.client, apiBase, authority, &issue)
	if err != nil {
		return ConnectorDocument{}, err
	}
	if observed, canonicalErr := CanonicalizePublicHTTPSURL(issue.HTMLURL); canonicalErr != nil || observed != canonical {
		return ConnectorDocument{}, newConnectorError("invalid_response",
			errors.New("GitHub response URL does not match the requested thread"))
	}
	requestDocuments := []HTTPDocument{issueDocument}
	requestEndpoints := []string{issueDocument.RequestedURL}
	commentsIncluded := 0
	var continuationFailure *ConnectorFailure
	var comments []struct {
		ID        int64  `json:"id"`
		HTMLURL   string `json:"html_url"`
		Body      string `json:"body"`
		CreatedAt string `json:"created_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if issue.Comments > 0 && maxItems > 0 {
		commentsEndpoint, _ := url.Parse(apiBase + "/comments")
		parameters := commentsEndpoint.Query()
		parameters.Set("per_page", strconv.Itoa(min(maxItems, MaxConnectorItemLimit)))
		parameters.Set("page", "1")
		commentsEndpoint.RawQuery = parameters.Encode()
		commentsDocument, commentsErr := connectorGetJSON(ctx, c.client,
			commentsEndpoint.String(), authority, &comments)
		requestEndpoints = append(requestEndpoints, commentsEndpoint.String())
		if commentsErr != nil {
			continuationFailure = connectorFailure(c.Name(), commentsErr)
			comments = nil
		} else {
			requestDocuments = append(requestDocuments, commentsDocument)
			commentsIncluded = len(comments)
		}
	}
	var builder strings.Builder
	markdownSection(&builder, "# "+boundedConnectorText(issue.Title, 4096),
		fmt.Sprintf("URL: %s\nAuthor: @%s\nCreated: %s",
			canonical, boundedConnectorText(issue.User.Login, 256),
			boundedConnectorText(issue.CreatedAt, 128)))
	markdownSection(&builder, "## Body", boundedConnectorText(issue.Body, MaxBodyBytes))
	if len(comments) > 0 {
		var commentsBody strings.Builder
		for index, comment := range comments {
			if index > 0 {
				commentsBody.WriteString("\n\n")
			}
			fmt.Fprintf(&commentsBody, "### Comment %d by @%s at %s\n\n%s",
				comment.ID, boundedConnectorText(comment.User.Login, 256),
				boundedConnectorText(comment.CreatedAt, 128),
				boundedConnectorText(comment.Body, MaxBodyBytes))
		}
		markdownSection(&builder,
			fmt.Sprintf("## Comments (%d of %d)", commentsIncluded, issue.Comments),
			commentsBody.String())
	}
	truncated := issue.Comments > commentsIncluded || continuationFailure != nil
	truncationReason := ""
	if truncated {
		truncationReason = "comment_limit"
	}
	if continuationFailure != nil {
		truncationReason = "comment_read_failed"
	}
	contentKind := "github_issue_thread"
	coverage := "body_and_issue_comments"
	if len(issue.PullRequest) > 0 && string(issue.PullRequest) != "null" {
		contentKind = "github_pull_request_thread"
		coverage = "body_and_issue_comments_without_review_comments"
	}
	return ConnectorDocument{CanonicalURL: canonical, RequestEndpoints: requestEndpoints,
		HTTPStatus:  issueDocument.StatusCode,
		RawDigest:   combineConnectorRawDigest(requestDocuments...),
		Title:       boundedCleanText(issue.Title, 1024),
		Byline:      boundedCleanText(issue.User.Login, 512),
		PublishedAt: boundedCleanText(issue.CreatedAt, 128),
		MIME:        "text/markdown", Charset: "utf-8", Body: builder.String(),
		ContentKind: contentKind, Coverage: coverage,
		ItemsIncluded: commentsIncluded + 1, ItemsAvailable: max(issue.Comments, commentsIncluded) + 1,
		Truncated: truncated, TruncationReason: truncationReason,
		ContinuationFailure: continuationFailure}, nil
}

func parseGitHubIssueURL(rawURL string) (string, string, int, bool) {
	canonical, err := CanonicalizePublicHTTPSURL(rawURL)
	if err != nil {
		return "", "", 0, false
	}
	parsed, _ := url.Parse(canonical)
	if parsed.Hostname() != "github.com" {
		return "", "", 0, false
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 4 || (parts[2] != "issues" && parts[2] != "pull") {
		return "", "", 0, false
	}
	number, err := strconv.Atoi(parts[3])
	if err != nil || number < 1 || parts[0] == "" || parts[1] == "" {
		return "", "", 0, false
	}
	return parts[0], parts[1], number, true
}
