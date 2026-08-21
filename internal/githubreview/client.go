package githubreview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	resolver   tokenResolver
	network    NetworkScope
	httpClient *http.Client
	restBase   string
	graphqlURL string
	now        func() time.Time
	testMode   bool
}

func NewClient(resolver tokenResolver, network NetworkScope) (*Client, error) {
	if resolver == nil || network.Validate() != nil {
		return nil, errors.New("GitHub review client configuration is invalid")
	}
	client := &Client{resolver: resolver, network: network,
		restBase: "https://api.github.com", graphqlURL: "https://api.github.com/graphql",
		now: time.Now}
	client.httpClient = &http.Client{Timeout: 45 * time.Second,
		CheckRedirect: client.checkRedirect}
	return client, nil
}

// NewClientForTest redirects both REST and GraphQL to one clean loopback
// origin. Production constructors cannot be redirected.
func NewClientForTest(resolver tokenResolver, base string, httpClient *http.Client) (*Client, error) {
	network := DefaultNetworkScope()
	client, err := NewClient(resolver, network)
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" ||
		parsed.Fragment != "" || parsed.Path != "" || !loopbackHostname(parsed.Hostname()) {
		return nil, errors.New("test GitHub API endpoint must be a clean loopback HTTP origin")
	}
	client.restBase = strings.TrimSuffix(base, "/")
	client.graphqlURL = client.restBase + "/graphql"
	client.testMode = true
	if httpClient != nil {
		copy := *httpClient
		copy.CheckRedirect = client.checkRedirect
		client.httpClient = &copy
	}
	return client, nil
}

func (c *Client) checkRedirect(request *http.Request, via []*http.Request) error {
	if len(via) > 1 {
		return &Error{Code: FailureNetworkPolicy, Message: "GitHub response exceeded one redirect"}
	}
	if c.testMode && request.URL.Scheme == "http" && loopbackHostname(request.URL.Hostname()) {
		request.Header.Del("Authorization")
		return nil
	}
	if request.URL.Scheme != "https" || request.URL.User != nil ||
		!c.allowedLogHost(request.URL.Hostname()) {
		return &Error{Code: FailureNetworkPolicy,
			Message: "GitHub log redirect host is outside the reviewed network scope"}
	}
	// Signed log URLs carry their own authorization. Never forward the GitHub
	// credential across an origin boundary.
	request.Header.Del("Authorization")
	request.Header.Del("Cookie")
	return nil
}

func (c *Client) allowedLogHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, allowed := range c.network.AllowedLogHosts {
		if strings.EqualFold(host, allowed) {
			return true
		}
	}
	return false
}

type apiResponse struct {
	Header http.Header
	Status int
	Body   []byte
}

func (c *Client) doJSON(ctx context.Context, method, path string, query url.Values,
	body any, ref CredentialReference, output any,
) (apiResponse, error) {
	if c == nil || c.resolver == nil || ref.Validate() != nil ||
		(method != http.MethodGet && method != http.MethodPost && method != http.MethodPatch) ||
		!validAPIPath(path) {
		return apiResponse{}, errors.New("GitHub API request is invalid")
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil || len(encoded) > MaxTextBytes {
			return apiResponse{}, errors.New("GitHub API request body is invalid or exceeds its bound")
		}
		reader = bytes.NewReader(encoded)
	}
	target := c.restBase + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	response, err := c.do(ctx, method, target, reader, ref, MaxResponseBytes, true)
	if err != nil {
		return apiResponse{}, err
	}
	if output != nil {
		decoder := json.NewDecoder(bytes.NewReader(response.Body))
		if err := decoder.Decode(output); err != nil {
			return apiResponse{}, &Error{Code: FailureMalformed,
				Message: "GitHub API response is malformed", StatusCode: response.Status}
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return apiResponse{}, &Error{Code: FailureMalformed,
				Message: "GitHub API response has trailing data", StatusCode: response.Status}
		}
	}
	return response, nil
}

func (c *Client) doGraphQL(ctx context.Context, query string, variables map[string]any,
	ref CredentialReference, output any,
) (apiResponse, error) {
	if strings.TrimSpace(query) == "" || len(query) > MaxTextBytes || output == nil {
		return apiResponse{}, errors.New("GitHub GraphQL request is invalid")
	}
	payload, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil || len(payload) > MaxTextBytes {
		return apiResponse{}, errors.New("GitHub GraphQL request exceeds its bound")
	}
	response, err := c.do(ctx, http.MethodPost, c.graphqlURL, bytes.NewReader(payload),
		ref, MaxResponseBytes, true)
	if err != nil {
		return apiResponse{}, err
	}
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Type string `json:"type"`
			Path []any  `json:"path"`
		} `json:"errors"`
	}
	decoder := json.NewDecoder(bytes.NewReader(response.Body))
	if err := decoder.Decode(&envelope); err != nil || len(envelope.Errors) > 0 ||
		len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return apiResponse{}, &Error{Code: FailureMalformed,
			Message:    "GitHub GraphQL response did not contain complete review data",
			StatusCode: response.Status}
	}
	if err := json.Unmarshal(envelope.Data, output); err != nil {
		return apiResponse{}, &Error{Code: FailureMalformed,
			Message: "GitHub GraphQL data is malformed", StatusCode: response.Status}
	}
	return response, nil
}

func (c *Client) doBytes(ctx context.Context, target string, ref CredentialReference,
	limit int,
) (apiResponse, error) {
	if limit <= 0 || limit > MaxUncompressedLogBytes {
		return apiResponse{}, errors.New("GitHub byte response limit is invalid")
	}
	return c.do(ctx, http.MethodGet, target, nil, ref, limit, false)
}

func (c *Client) do(ctx context.Context, method, target string, body io.Reader,
	ref CredentialReference, limit int, requireJSON bool,
) (apiResponse, error) {
	lease, err := c.resolver.resolve(ctx, ref)
	if err != nil {
		return apiResponse{}, err
	}
	request, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return apiResponse{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", RESTAPIVersion)
	request.Header.Set("User-Agent", "Prayu-GitHub-Review/"+ProtocolVersion)
	request.Header.Set("Authorization", "Bearer "+lease.value)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return apiResponse{}, &Error{Code: FailureCancelled,
				Message: "GitHub API request was cancelled"}
		}
		var typed *Error
		if errors.As(err, &typed) {
			return apiResponse{}, typed
		}
		return apiResponse{}, &Error{Code: FailureOffline,
			Message: "GitHub API is unreachable"}
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, int64(limit)+1))
	if readErr != nil {
		return apiResponse{}, &Error{Code: FailureUnavailable,
			Message: "read GitHub API response"}
	}
	if len(data) > limit {
		return apiResponse{}, &Error{Code: FailureResponseBound,
			Message:    "GitHub API response exceeded its configured byte bound",
			StatusCode: response.StatusCode}
	}
	result := apiResponse{Header: response.Header.Clone(), Status: response.StatusCode,
		Body: data}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return apiResponse{}, classifyAPIError(response.StatusCode, response.Header, data, c.now())
	}
	if requireJSON {
		contentType := strings.ToLower(response.Header.Get("Content-Type"))
		if contentType != "" && !strings.Contains(contentType, "json") {
			return apiResponse{}, &Error{Code: FailureMalformed,
				Message:    "GitHub API returned an unexpected content type",
				StatusCode: response.StatusCode}
		}
	}
	return result, nil
}

func classifyAPIError(status int, header http.Header, data []byte, now time.Time) error {
	message := "GitHub API rejected the request"
	var view struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(data, &view)
	view.Message = strings.ToLower(SanitizeRemoteText(view.Message, 500).Text)
	retryAt := parseRetryAt(header, now)
	sso := strings.TrimSpace(header.Get("X-GitHub-SSO")) != "" || strings.Contains(view.Message, "sso")
	rate := status == http.StatusTooManyRequests || header.Get("X-RateLimit-Remaining") == "0" ||
		strings.Contains(view.Message, "rate limit") || strings.Contains(view.Message, "secondary rate")
	switch {
	case status == http.StatusUnauthorized:
		return &Error{Code: FailureAuthentication, Message: message,
			RetryAt: retryAt, StatusCode: status}
	case sso:
		return &Error{Code: FailureSSO,
			Message: "GitHub organization SSO authorization is required",
			RetryAt: retryAt, StatusCode: status}
	case rate:
		return &Error{Code: FailureRateLimit,
			Message: "GitHub API rate limit was reached", RetryAt: retryAt, StatusCode: status}
	case status == http.StatusForbidden:
		return &Error{Code: FailurePermission,
			Message: "GitHub credential lacks permission for this operation", StatusCode: status}
	case status == http.StatusNotFound:
		return &Error{Code: FailureNotFound,
			Message: "GitHub repository, pull request, or review object was not found", StatusCode: status}
	case status == http.StatusConflict:
		return &Error{Code: FailureConflict, Message: message, StatusCode: status}
	case status == http.StatusGone:
		return &Error{Code: FailureUnavailable,
			Message: "configured GitHub API version or object is no longer available", StatusCode: status}
	case status == http.StatusUnprocessableEntity:
		return &Error{Code: FailureDrift,
			Message: "GitHub rejected stale or invalid review state", StatusCode: status}
	default:
		return &Error{Code: FailureUnavailable,
			Message: fmt.Sprintf("GitHub API returned HTTP %d", status),
			RetryAt: retryAt, StatusCode: status}
	}
}

func parseRetryAt(header http.Header, now time.Time) time.Time {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if duration := parseSecondsHeader(value); duration > 0 {
		return now.UTC().Add(duration)
	}
	if parsed, err := http.ParseTime(value); err == nil {
		return parsed.UTC()
	}
	if unixValue, err := strconv.ParseInt(strings.TrimSpace(header.Get("X-RateLimit-Reset")), 10, 64); err == nil && unixValue > 0 {
		return time.Unix(unixValue, 0).UTC()
	}
	return time.Time{}
}

func rateLimitFromHeaders(header http.Header) RateLimit {
	parse := func(name string) int {
		value, _ := strconv.Atoi(strings.TrimSpace(header.Get(name)))
		return value
	}
	result := RateLimit{Limit: parse("X-RateLimit-Limit"),
		Remaining: parse("X-RateLimit-Remaining"), Used: parse("X-RateLimit-Used"),
		Resource: sanitizeIdentity(header.Get("X-RateLimit-Resource"), 64)}
	if value, err := strconv.ParseInt(strings.TrimSpace(header.Get("X-RateLimit-Reset")), 10, 64); err == nil && value > 0 {
		result.ResetAt = time.Unix(value, 0).UTC()
	}
	return result
}

func validAPIPath(value string) bool {
	return strings.HasPrefix(value, "/") && !strings.Contains(value, "\\") &&
		!strings.Contains(value, "/../") && !strings.Contains(value, "/./") &&
		!strings.HasSuffix(value, "/..") && !strings.HasSuffix(value, "/.") &&
		!strings.ContainsAny(value, "?#")
}

func repositoryAPIPath(repo RepositoryIdentity) string {
	return "/repos/" + url.PathEscape(repo.Owner) + "/" + url.PathEscape(repo.Name)
}

func parseLinkNext(header http.Header) bool {
	for _, value := range header.Values("Link") {
		for _, part := range strings.Split(value, ",") {
			if strings.Contains(part, `rel="next"`) {
				return true
			}
		}
	}
	return false
}

func isLoopbackOrigin(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil {
		return false
	}
	ip := net.ParseIP(parsed.Hostname())
	return strings.EqualFold(parsed.Hostname(), "localhost") || ip != nil && ip.IsLoopback()
}

type userResponse struct {
	Login  string `json:"login"`
	ID     int64  `json:"id"`
	NodeID string `json:"node_id"`
}

type repositoryResponse struct {
	ID       int64  `json:"id"`
	NodeID   string `json:"node_id"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Private  bool   `json:"private"`
	Owner    struct {
		Login string `json:"login"`
	} `json:"owner"`
	Permissions struct {
		Admin    bool `json:"admin"`
		Maintain bool `json:"maintain"`
		Push     bool `json:"push"`
		Triage   bool `json:"triage"`
		Pull     bool `json:"pull"`
	} `json:"permissions"`
}

func (c *Client) Qualify(ctx context.Context, repository RepositoryIdentity, prNumber int64,
	ref CredentialReference,
) (Qualification, error) {
	now := c.now().UTC()
	result := Qualification{ProtocolVersion: ProtocolVersion, NetworkAllowed: true,
		SSOAuthorized: true, Diagnostics: []Diagnostic{}, CheckedAt: now}
	if repository.Validate() != nil || ref.Validate() != nil || prNumber < 0 {
		return Qualification{}, errors.New("GitHub qualification request is invalid")
	}
	configured, err := c.resolver.configured(ctx, ref)
	if err != nil {
		return Qualification{}, err
	}
	result.CredentialConfigured = configured
	if !configured {
		result.Diagnostics = append(result.Diagnostics, Diagnostic{Code: "credential_missing",
			Level: DiagnosticError, Message: "GitHub credential is not configured",
			Remediation: "Complete GitHub App device authorization or add one reviewed fine-grained token reference."})
		return result, nil
	}
	var user userResponse
	userHTTP, err := c.doJSON(ctx, http.MethodGet, "/user", nil, nil, ref, &user)
	if err != nil {
		appendQualificationError(&result, err)
		return result, nil
	}
	result.HostReachable = true
	result.Authenticated = validSlug(user.Login, 100)
	result.RateLimit = rateLimitFromHeaders(userHTTP.Header)
	if !result.Authenticated {
		result.Diagnostics = append(result.Diagnostics, Diagnostic{Code: "account_invalid",
			Level: DiagnosticError, Message: "GitHub account response did not contain a valid login"})
		return result, nil
	}
	var repoResponse repositoryResponse
	repoHTTP, err := c.doJSON(ctx, http.MethodGet, repositoryAPIPath(repository), nil, nil, ref, &repoResponse)
	if err != nil {
		appendQualificationError(&result, err)
		return result, nil
	}
	result.RepositoryAccessible = strings.EqualFold(repoResponse.FullName, repository.FullName)
	repository.NodeID = sanitizeIdentity(repoResponse.NodeID, MaxIdentityRunes)
	repository.Private = repoResponse.Private
	// Repository collaborator permissions do not prove the effective scopes of
	// an OAuth or fine-grained PAT. Only advertise the PR read path that this
	// qualification actually exercised; write-back remains closed unless a
	// GitHub App installation exposes its exact permission map below.
	permissions := map[string]string{"metadata": "read", "pull_requests": "read"}
	userCanWrite := repoResponse.Permissions.Admin || repoResponse.Permissions.Maintain || repoResponse.Permissions.Push
	canWrite := false
	installationID, installationPermissions, installationDiagnostics := c.findInstallation(ctx,
		repository, ref)
	result.Diagnostics = append(result.Diagnostics, installationDiagnostics...)
	readAllowed := result.RepositoryAccessible
	logsAllowed := len(c.network.AllowedLogHosts) > 0
	pushAllowed := false
	if ref.Kind == AuthGitHubAppDevice {
		permissions = installationPermissions
		readAllowed = result.RepositoryAccessible && installationID > 0 &&
			permissionAtLeast(permissions["contents"], "read") &&
			permissionAtLeast(permissions["pull_requests"], "read")
		canWrite = c.network.WriteEnabled && userCanWrite &&
			permissionAtLeast(permissions["pull_requests"], "write")
		pushAllowed = c.network.WriteEnabled && userCanWrite &&
			permissionAtLeast(permissions["contents"], "write")
		logsAllowed = logsAllowed && permissionAtLeast(permissions["actions"], "read")
		if installationID > 0 && !readAllowed {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{
				Code: "installation_read_permission_missing", Level: DiagnosticError,
				Message:     "GitHub App installation lacks required contents or pull request read permission",
				Remediation: "Grant Contents: read and Pull requests: read to the GitHub App installation."})
		}
	}
	capability := CapabilitySnapshot{ProtocolVersion: CapabilityProtocolVersion,
		APIHost: "api.github.com", APIVersion: RESTAPIVersion,
		AccountLogin: user.Login, InstallationID: installationID,
		Repository: repository, Credential: ref, Permissions: permissions,
		Read: readAllowed, Reply: canWrite, Resolve: canWrite,
		Review: canWrite, RequestReviewer: canWrite, Push: pushAllowed,
		Logs: logsAllowed, CapturedAt: now}
	capability.Generation = capabilityFingerprint(capability)
	result.Capability = capability
	result.RateLimit = rateLimitFromHeaders(repoHTTP.Header)
	if prNumber > 0 {
		var pull struct {
			Number int64 `json:"number"`
		}
		_, err = c.doJSON(ctx, http.MethodGet,
			repositoryAPIPath(repository)+"/pulls/"+strconv.FormatInt(prNumber, 10),
			nil, nil, ref, &pull)
		if err != nil {
			appendQualificationError(&result, err)
			return result, nil
		}
		result.PullRequestAccessible = pull.Number == prNumber
	} else {
		result.PullRequestAccessible = true
	}
	if !capability.Logs {
		result.Diagnostics = append(result.Diagnostics, Diagnostic{Code: "ci_log_hosts_not_allowed",
			Level:       DiagnosticWarning,
			Message:     "CI metadata is readable, but signed Actions log download hosts are not in the reviewed network scope",
			Remediation: "Add only the exact observed signed-log hosts to the connection allowlist."})
	}
	if !c.network.WriteEnabled {
		result.Diagnostics = append(result.Diagnostics, Diagnostic{Code: "write_network_disabled",
			Level:       DiagnosticWarning,
			Message:     "GitHub review write-back is disabled for this connection",
			Remediation: "Reconfigure the connection with explicit write-back enabled only if remote mutations are required."})
	} else if ref.Kind != AuthGitHubAppDevice {
		result.Diagnostics = append(result.Diagnostics, Diagnostic{Code: "write_scope_unverifiable",
			Level:       DiagnosticWarning,
			Message:     "OAuth and fine-grained token write scopes cannot be proven by repository collaborator permissions",
			Remediation: "Use the recommended GitHub App installation path for approval-gated write-back."})
	} else if !canWrite {
		result.Diagnostics = append(result.Diagnostics, Diagnostic{Code: "write_permission_missing",
			Level:       DiagnosticWarning,
			Message:     "GitHub repository is read-only for this credential",
			Remediation: "Install the GitHub App with pull request write permission only if write-back is required."})
	}
	result.Eligible = result.HostReachable && result.Authenticated && result.SSOAuthorized &&
		result.RepositoryAccessible && result.PullRequestAccessible && capability.Read &&
		(ref.Kind != AuthGitHubAppDevice || installationID > 0)
	return result, nil
}

func capabilityFingerprint(capability CapabilitySnapshot) string {
	keys := make([]string, 0, len(capability.Permissions))
	for key := range capability.Permissions {
		keys = append(keys, key)
	}
	sortStrings(keys)
	parts := []string{"capability", capability.APIHost, capability.APIVersion,
		capability.AccountLogin, fmt.Sprint(capability.InstallationID),
		capability.Repository.FullName, capability.Credential.Name,
		string(capability.Credential.Kind), fmt.Sprint(capability.Read),
		fmt.Sprint(capability.Reply), fmt.Sprint(capability.Resolve),
		fmt.Sprint(capability.Review), fmt.Sprint(capability.RequestReviewer),
		fmt.Sprint(capability.Push), fmt.Sprint(capability.Logs)}
	for _, key := range keys {
		parts = append(parts, key+"="+capability.Permissions[key])
	}
	return Fingerprint(parts...)
}

func appendQualificationError(result *Qualification, err error) {
	code := "unavailable"
	message := "GitHub qualification could not complete"
	remediation := "Verify the network scope, credential, repository identity, and GitHub status."
	var typed *Error
	if errors.As(err, &typed) {
		code = string(typed.Code)
		message = typed.Message
		if typed.Code == FailureSSO {
			result.SSOAuthorized = false
			remediation = "Authorize the GitHub App or token for the organization SSO policy."
		}
		if typed.Code == FailureNetworkPolicy {
			result.NetworkAllowed = false
		}
		if typed.Code == FailureOffline {
			result.HostReachable = false
		}
	}
	result.Diagnostics = append(result.Diagnostics, Diagnostic{Code: code,
		Level: DiagnosticError, Message: boundedPlainText(message, 500), Remediation: remediation})
}

type installationsResponse struct {
	TotalCount    int                    `json:"total_count"`
	Installations []installationResponse `json:"installations"`
}

type installationResponse struct {
	ID      int64 `json:"id"`
	Account struct {
		Login string `json:"login"`
	} `json:"account"`
	Permissions map[string]string `json:"permissions"`
}

type installationRepositoriesResponse struct {
	TotalCount   int `json:"total_count"`
	Repositories []struct {
		NodeID   string `json:"node_id"`
		FullName string `json:"full_name"`
	} `json:"repositories"`
}

func (c *Client) findInstallation(ctx context.Context, repository RepositoryIdentity,
	ref CredentialReference,
) (int64, map[string]string, []Diagnostic) {
	if ref.Kind != AuthGitHubAppDevice {
		return 0, nil, nil
	}
	installations := make([]installationResponse, 0)
	for page := 1; page <= MaxPages; page++ {
		var response installationsResponse
		httpResult, err := c.doJSON(ctx, http.MethodGet, "/user/installations",
			url.Values{"per_page": []string{"100"}, "page": []string{strconv.Itoa(page)}},
			nil, ref, &response)
		if err != nil {
			return 0, nil, []Diagnostic{{Code: "installation_unavailable", Level: DiagnosticWarning,
				Message:     "GitHub App installation identity could not be confirmed",
				Remediation: "Confirm the App is installed for the target repository."}}
		}
		installations = append(installations, response.Installations...)
		if !parseLinkNext(httpResult.Header) {
			break
		}
		if page == MaxPages {
			return 0, nil, []Diagnostic{{Code: "installation_pagination_incomplete",
				Level:       DiagnosticError,
				Message:     "GitHub App installation pagination exceeded its configured bound",
				Remediation: "Use an account with a bounded installation set or narrow the App account."}}
		}
	}
	for _, installation := range installations {
		if strings.EqualFold(installation.Account.Login, repository.Owner) && installation.ID > 0 {
			matched, complete, err := c.installationContainsRepository(ctx, installation.ID,
				repository, ref)
			if err != nil {
				return 0, nil, []Diagnostic{{Code: "installation_repository_unavailable",
					Level:       DiagnosticWarning,
					Message:     "GitHub App installation repository membership could not be confirmed",
					Remediation: "Confirm the App installation can list the selected repositories."}}
			}
			if !complete {
				return 0, nil, []Diagnostic{{Code: "installation_repository_pagination_incomplete",
					Level:       DiagnosticError,
					Message:     "GitHub App repository pagination exceeded its configured bound",
					Remediation: "Narrow the App installation repository selection."}}
			}
			if matched {
				return installation.ID, normalizeInstallationPermissions(installation.Permissions), nil
			}
		}
	}
	return 0, nil, []Diagnostic{{Code: "installation_missing", Level: DiagnosticError,
		Message:     "No GitHub App installation matched the repository owner",
		Remediation: "Install the GitHub App on the target account and select the repository."}}
}

func (c *Client) installationContainsRepository(ctx context.Context, installationID int64,
	repository RepositoryIdentity, ref CredentialReference,
) (bool, bool, error) {
	path := "/user/installations/" + strconv.FormatInt(installationID, 10) + "/repositories"
	for page := 1; page <= MaxPages; page++ {
		var response installationRepositoriesResponse
		httpResult, err := c.doJSON(ctx, http.MethodGet, path,
			url.Values{"per_page": []string{"100"}, "page": []string{strconv.Itoa(page)}},
			nil, ref, &response)
		if err != nil {
			return false, false, err
		}
		for _, candidate := range response.Repositories {
			if strings.EqualFold(strings.TrimSpace(candidate.FullName), repository.FullName) {
				return true, true, nil
			}
		}
		if !parseLinkNext(httpResult.Header) {
			return false, true, nil
		}
	}
	return false, false, nil
}

func normalizeInstallationPermissions(input map[string]string) map[string]string {
	result := make(map[string]string)
	for name, value := range input {
		name = strings.ToLower(strings.TrimSpace(name))
		value = strings.ToLower(strings.TrimSpace(value))
		if validPermission(name) && (value == "read" || value == "write" || value == "admin") {
			result[name] = value
		}
	}
	return result
}

func permissionAtLeast(actual, required string) bool {
	rank := map[string]int{"read": 1, "write": 2, "admin": 3}
	return rank[strings.ToLower(strings.TrimSpace(actual))] >= rank[required]
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
