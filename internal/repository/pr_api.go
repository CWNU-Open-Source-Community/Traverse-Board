package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
)

const MaxPRAPIResponseBytes = 64 * 1024

// PRReceipt is the redacted GitHub API evidence. The token, request bodies,
// and raw responses never appear here or in any log.
type PRReceipt struct {
	URL    string
	Number int64
	State  string
	Title  string
}

// PRClient creates and updates Pull Requests through the GitHub REST API.
// Only github.com is accepted; unknown hosts are rejected before any
// request is made.
type PRClient struct {
	client  *http.Client
	apiBase string // test override
}

func NewPRClient() *PRClient {
	return &PRClient{client: &http.Client{Timeout: 30 * time.Second}}
}

func (c *PRClient) repoPath(ctx context.Context, rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil {
		return "", errors.New("PR operations require a clean HTTPS repository URL")
	}
	if parsed.Hostname() != "github.com" {
		return "", errors.New("PR operations are only available for github.com repositories")
	}
	path := strings.TrimSuffix(strings.TrimPrefix(parsed.Path, "/"), ".git")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", errors.New("repository identity cannot be derived from the remote URL")
	}
	return parts[0] + "/" + parts[1], nil
}

// CreatePR opens one Pull Request for head→base. The token is a resolved
// credential reference; it is used only in the Authorization header.
func (c *PRClient) CreatePR(ctx context.Context, repositoryURL, headBranch, baseBranch,
	title, body, token string,
) (PRReceipt, error) {
	repo, err := c.repoPath(ctx, repositoryURL)
	if err != nil {
		return PRReceipt{}, err
	}
	payload := map[string]string{"title": title, "head": headBranch, "base": baseBranch, "body": body}
	raw, err := json.Marshal(payload)
	if err != nil {
		return PRReceipt{}, err
	}
	base := c.apiBase
	if base == "" {
		base = "https://api.github.com"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/repos/"+repo+"/pulls", bytes.NewReader(raw))
	if err != nil {
		return PRReceipt{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	return c.do(ctx, request, "create PR")
}

// UpdatePR updates the title and body of one existing Pull Request.
func (c *PRClient) UpdatePR(ctx context.Context, repositoryURL string, number int64,
	title, body, token string,
) (PRReceipt, error) {
	repo, err := c.repoPath(ctx, repositoryURL)
	if err != nil {
		return PRReceipt{}, err
	}
	payload := map[string]string{"title": title, "body": body}
	raw, err := json.Marshal(payload)
	if err != nil {
		return PRReceipt{}, err
	}
	base := c.apiBase
	if base == "" {
		base = "https://api.github.com"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPatch,
		base+"/repos/"+repo+"/pulls/"+strconv.FormatInt(number, 10), bytes.NewReader(raw))
	if err != nil {
		return PRReceipt{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	return c.do(ctx, request, "update PR")
}

func (c *PRClient) do(ctx context.Context, request *http.Request, label string) (PRReceipt, error) {
	response, err := c.client.Do(request)
	if err != nil {
		return PRReceipt{}, apperror.New(apperror.CodeUnavailable,
			label+" failed: the GitHub API is unreachable")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, MaxPRAPIResponseBytes+1))
	if err != nil {
		return PRReceipt{}, apperror.Normalize(err)
	}
	if len(body) > MaxPRAPIResponseBytes {
		return PRReceipt{}, apperror.New(apperror.CodeResourceExhausted,
			label+" response exceeded its bound")
	}
	var view struct {
		HTMLURL string `json:"html_url"`
		Number  int64  `json:"number"`
		State   string `json:"state"`
		Title   string `json:"title"`
		Message string `json:"message"`
	}
	switch response.StatusCode {
	case http.StatusCreated, http.StatusOK:
		if err := json.Unmarshal(body, &view); err != nil {
			return PRReceipt{}, apperror.New(apperror.CodeInternal, label+" response is malformed")
		}
		return PRReceipt{URL: view.HTMLURL, Number: view.Number, State: view.State, Title: view.Title}, nil
	case http.StatusUnauthorized:
		return PRReceipt{}, apperror.New(apperror.CodeUnavailable,
			label+" failed: authentication was rejected by GitHub")
	case http.StatusForbidden:
		_ = json.Unmarshal(body, &view)
		if strings.Contains(strings.ToLower(view.Message), "rate limit") {
			return PRReceipt{}, apperror.New(apperror.CodeResourceExhausted,
				label+" failed: GitHub rate limit reached; retry later")
		}
		return PRReceipt{}, apperror.New(apperror.CodePolicyDenied,
			label+" failed: permission denied by the repository")
	case http.StatusUnprocessableEntity:
		_ = json.Unmarshal(body, &view)
		return PRReceipt{}, apperror.New(apperror.CodeFailedPrecondition,
			label+" failed: "+boundedPRMessage(view.Message))
	case http.StatusNotFound:
		return PRReceipt{}, apperror.New(apperror.CodeNotFound,
			label+" failed: repository or PR not found")
	default:
		return PRReceipt{}, apperror.New(apperror.CodeUnavailable,
			fmt.Sprintf("%s failed: GitHub returned HTTP %d", label, response.StatusCode))
	}
}

func boundedPRMessage(value string) string {
	if len(value) > 300 {
		value = value[:300]
	}
	return strings.TrimSpace(value)
}
