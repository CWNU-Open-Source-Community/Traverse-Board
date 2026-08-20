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
)

const idempotencyMarkerPrefix = "<!-- prayu-github-review:"

func (c *Client) ExecuteWrite(ctx context.Context, spec WriteSpec,
	preview WritePreview,
) (WriteReceipt, error) {
	started := c.now().UTC()
	receipt := WriteReceipt{ProtocolVersion: ReceiptProtocolVersion,
		ID: "ghr-" + Fingerprint("receipt", preview.ID)[:32], PreviewID: preview.ID,
		Operation: preview.Operation, Status: ReceiptFailed, Identity: preview.Identity,
		TargetID: preview.TargetID, IdempotencyMarker: preview.IdempotencyMarker,
		StartedAt: started}
	if !c.network.WriteEnabled {
		err := &Error{Code: FailureNetworkPolicy,
			Message: "GitHub review write-back is disabled for this connection"}
		return completeWriteError(receipt, err, c.now().UTC()), err
	}
	if err := validateWriteBinding(spec, preview); err != nil {
		return completeWriteError(receipt, err, c.now().UTC()), err
	}
	current, err := c.readCurrentIdentity(ctx, spec.Identity.Repository,
		spec.Identity.Number, spec.Credential)
	if err != nil {
		return completeWriteError(receipt, err, c.now().UTC()), err
	}
	if err := compareWriteIdentity(preview.Identity, current); err != nil {
		return completeWriteError(receipt, err, c.now().UTC()), err
	}
	if recovered, found, reconcileErr := c.reconcileWrite(ctx, spec, preview); reconcileErr != nil {
		return completeWriteError(receipt, reconcileErr, c.now().UTC()), reconcileErr
	} else if found {
		recovered.ProtocolVersion = ReceiptProtocolVersion
		recovered.ID = receipt.ID
		recovered.PreviewID = preview.ID
		recovered.Operation = preview.Operation
		recovered.Status = ReceiptRecovered
		recovered.Identity = current
		recovered.TargetID = preview.TargetID
		recovered.IdempotencyMarker = preview.IdempotencyMarker
		recovered.Recovered = true
		recovered.StartedAt = started
		recovered.CompletedAt = c.now().UTC()
		return recovered, nil
	}

	resultID, resultURL, err := c.performWrite(ctx, spec, preview)
	receipt.CompletedAt = c.now().UTC()
	if err != nil {
		return completeWriteError(receipt, err, receipt.CompletedAt), err
	}
	receipt.Status = ReceiptSucceeded
	receipt.Identity = current
	receipt.ResultID = sanitizeIdentity(resultID, MaxIdentityRunes)
	receipt.ResultURL = safeGitHubURL(resultURL)
	return receipt, nil
}

// RecoverWrite observes an interrupted write and never issues a mutation. It
// returns a recovered receipt only when the exact idempotency marker or target
// state is already visible remotely; otherwise it fails closed so startup
// recovery cannot turn a prepared ledger row into a new side effect.
func (c *Client) RecoverWrite(ctx context.Context, spec WriteSpec,
	preview WritePreview,
) (WriteReceipt, error) {
	started := c.now().UTC()
	receipt := WriteReceipt{ProtocolVersion: ReceiptProtocolVersion,
		ID: "ghr-" + Fingerprint("receipt", preview.ID)[:32], PreviewID: preview.ID,
		Operation: preview.Operation, Status: ReceiptFailed, Identity: preview.Identity,
		TargetID: preview.TargetID, IdempotencyMarker: preview.IdempotencyMarker,
		StartedAt: started}
	if err := validateWriteBinding(spec, preview); err != nil {
		return completeWriteError(receipt, err, c.now().UTC()), err
	}
	current, err := c.readCurrentIdentity(ctx, spec.Identity.Repository,
		spec.Identity.Number, spec.Credential)
	if err != nil {
		return completeWriteError(receipt, err, c.now().UTC()), err
	}
	if err := compareWriteIdentity(preview.Identity, current); err != nil {
		return completeWriteError(receipt, err, c.now().UTC()), err
	}
	recovered, found, err := c.reconcileWrite(ctx, spec, preview)
	if err != nil {
		return completeWriteError(receipt, err, c.now().UTC()), err
	}
	if !found {
		err = &Error{Code: FailureInterrupted,
			Message: "interrupted GitHub review write has no observable remote receipt and was not replayed"}
		return completeWriteError(receipt, err, c.now().UTC()), err
	}
	recovered.ProtocolVersion = ReceiptProtocolVersion
	recovered.ID = receipt.ID
	recovered.PreviewID = preview.ID
	recovered.Operation = preview.Operation
	recovered.Status = ReceiptRecovered
	recovered.Identity = current
	recovered.TargetID = preview.TargetID
	recovered.IdempotencyMarker = preview.IdempotencyMarker
	recovered.Recovered = true
	recovered.StartedAt = started
	recovered.CompletedAt = c.now().UTC()
	return recovered, nil
}

func completeWriteError(receipt WriteReceipt, err error,
	completed time.Time,
) WriteReceipt {
	receipt.Status = ReceiptFailed
	receipt.CompletedAt = completed.UTC()
	var typed *Error
	if errors.As(err, &typed) {
		receipt.ErrorCode = string(typed.Code)
		receipt.ErrorSummary = boundedPlainText(typed.Message, 500)
	} else if err != nil {
		receipt.ErrorCode = string(FailureUnavailable)
		receipt.ErrorSummary = boundedPlainText(err.Error(), 500)
	}
	return receipt
}

func validateWriteBinding(spec WriteSpec, preview WritePreview) error {
	spec.Normalize()
	if spec.Validate() != nil || preview.Validate() != nil {
		return errors.New("GitHub review write binding is invalid")
	}
	expected, err := NewWritePreview(spec, preview.CreatedAt)
	if err != nil || expected.ID != preview.ID ||
		expected.ApprovalFingerprint != preview.ApprovalFingerprint ||
		expected.IdempotencyMarker != preview.IdempotencyMarker ||
		expected.BodySHA256 != preview.BodySHA256 || expected.TargetID != preview.TargetID ||
		expected.CapabilityGeneration != preview.CapabilityGeneration {
		return &Error{Code: FailureDrift,
			Message: "GitHub review write preview no longer matches the exact request"}
	}
	return nil
}

func (c *Client) readCurrentIdentity(ctx context.Context, repository RepositoryIdentity,
	number int64, ref CredentialReference,
) (PullRequestIdentity, error) {
	path := repositoryAPIPath(repository)
	var pull pullResponse
	_, err := c.doJSON(ctx, http.MethodGet,
		path+"/pulls/"+strconv.FormatInt(number, 10), nil, nil, ref, &pull)
	if err != nil {
		return PullRequestIdentity{}, err
	}
	identity, err := pullIdentity(repository, pull)
	if err != nil {
		return PullRequestIdentity{}, &Error{Code: FailureMalformed,
			Message: "GitHub pull request metadata is inconsistent"}
	}
	var comparison compareResponse
	_, err = c.doJSON(ctx, http.MethodGet,
		path+"/compare/"+url.PathEscape(identity.BaseSHA)+"..."+url.PathEscape(identity.HeadSHA),
		nil, nil, ref, &comparison)
	if err != nil {
		return PullRequestIdentity{}, err
	}
	identity.MergeBaseSHA = strings.ToLower(strings.TrimSpace(comparison.MergeBaseCommit.SHA))
	if identity.Validate() != nil {
		return PullRequestIdentity{}, &Error{Code: FailureMalformed,
			Message: "GitHub current pull request identity is invalid"}
	}
	return identity, nil
}

func compareWriteIdentity(expected, current PullRequestIdentity) error {
	if current.Merged {
		return &Error{Code: FailureMerged, Message: "GitHub pull request was merged after preview"}
	}
	if current.State != "open" {
		return &Error{Code: FailureClosed, Message: "GitHub pull request was closed after preview"}
	}
	if expected.Repository.FullName != current.Repository.FullName ||
		expected.Number != current.Number || expected.NodeID != current.NodeID ||
		expected.BaseSHA != current.BaseSHA || expected.HeadSHA != current.HeadSHA ||
		expected.MergeBaseSHA != current.MergeBaseSHA {
		return &Error{Code: FailureDrift,
			Message: "GitHub pull request base, head, merge-base, or identity drifted after preview"}
	}
	return nil
}

func markerFor(preview WritePreview) string {
	return idempotencyMarkerPrefix + preview.IdempotencyMarker + " -->"
}

func outboundBody(spec WriteSpec, preview WritePreview) string {
	marker := markerFor(preview)
	if spec.Body == "" {
		return marker
	}
	return strings.TrimSpace(spec.Body) + "\n\n" + marker
}

func (c *Client) performWrite(ctx context.Context, spec WriteSpec,
	preview WritePreview,
) (string, string, error) {
	switch spec.Operation {
	case WriteReply:
		return c.writeThreadReply(ctx, spec, preview)
	case WriteResolve, WriteUnresolve:
		return c.writeThreadState(ctx, spec, preview)
	case WriteSubmitReview:
		return c.writeReview(ctx, spec, preview)
	case WriteRequestReviewer:
		return c.writeRequestedReviewers(ctx, spec, preview)
	default:
		return "", "", errors.New("unsupported GitHub review write operation")
	}
}

const threadReplyMutation = `mutation($thread:ID!,$body:String!,$clientMutationId:String!){
  addPullRequestReviewThreadReply(input:{pullRequestReviewThreadId:$thread,body:$body,clientMutationId:$clientMutationId}){
    comment{id databaseId url}}}`

func (c *Client) writeThreadReply(ctx context.Context, spec WriteSpec,
	preview WritePreview,
) (string, string, error) {
	var data struct {
		AddPullRequestReviewThreadReply struct {
			Comment *struct {
				ID         string `json:"id"`
				DatabaseID int64  `json:"databaseId"`
				URL        string `json:"url"`
			} `json:"comment"`
		} `json:"addPullRequestReviewThreadReply"`
	}
	_, err := c.doGraphQL(ctx, threadReplyMutation, map[string]any{
		"thread": spec.TargetID, "body": outboundBody(spec, preview),
		"clientMutationId": preview.IdempotencyMarker}, spec.Credential, &data)
	if err != nil {
		return "", "", err
	}
	if data.AddPullRequestReviewThreadReply.Comment == nil {
		return "", "", &Error{Code: FailureMalformed,
			Message: "GitHub thread reply mutation returned no comment"}
	}
	return data.AddPullRequestReviewThreadReply.Comment.ID,
		data.AddPullRequestReviewThreadReply.Comment.URL, nil
}

const resolveThreadMutation = `mutation($thread:ID!,$clientMutationId:String!){
  resolveReviewThread(input:{threadId:$thread,clientMutationId:$clientMutationId}){
    thread{id isResolved}}}`

const unresolveThreadMutation = `mutation($thread:ID!,$clientMutationId:String!){
  unresolveReviewThread(input:{threadId:$thread,clientMutationId:$clientMutationId}){
    thread{id isResolved}}}`

func (c *Client) writeThreadState(ctx context.Context, spec WriteSpec,
	preview WritePreview,
) (string, string, error) {
	query := resolveThreadMutation
	field := "resolveReviewThread"
	expected := true
	if spec.Operation == WriteUnresolve {
		query = unresolveThreadMutation
		field = "unresolveReviewThread"
		expected = false
	}
	var envelope map[string]jsonRaw
	_, err := c.doGraphQL(ctx, query, map[string]any{"thread": spec.TargetID,
		"clientMutationId": preview.IdempotencyMarker}, spec.Credential, &envelope)
	if err != nil {
		return "", "", err
	}
	var data struct {
		Thread *struct {
			ID         string `json:"id"`
			IsResolved bool   `json:"isResolved"`
		} `json:"thread"`
	}
	raw, exists := envelope[field]
	if !exists || raw.decode(&data) != nil || data.Thread == nil || data.Thread.IsResolved != expected {
		return "", "", &Error{Code: FailureMalformed,
			Message: "GitHub thread state mutation returned inconsistent state"}
	}
	return data.Thread.ID, "", nil
}

func (c *Client) writeReview(ctx context.Context, spec WriteSpec,
	preview WritePreview,
) (string, string, error) {
	var response struct {
		ID      int64  `json:"id"`
		NodeID  string `json:"node_id"`
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
	}
	_, err := c.doJSON(ctx, http.MethodPost,
		repositoryAPIPath(spec.Identity.Repository)+"/pulls/"+
			strconv.FormatInt(spec.Identity.Number, 10)+"/reviews", nil,
		map[string]any{"event": spec.ReviewEvent, "body": outboundBody(spec, preview),
			"commit_id": spec.Identity.HeadSHA}, spec.Credential, &response)
	if err != nil {
		return "", "", err
	}
	if response.ID <= 0 && response.NodeID == "" {
		return "", "", &Error{Code: FailureMalformed,
			Message: "GitHub review mutation returned no identity"}
	}
	identity := response.NodeID
	if identity == "" {
		identity = strconv.FormatInt(response.ID, 10)
	}
	return identity, response.HTMLURL, nil
}

func (c *Client) writeRequestedReviewers(ctx context.Context, spec WriteSpec,
	preview WritePreview,
) (string, string, error) {
	var response struct {
		RequestedReviewers []struct {
			Login string `json:"login"`
		} `json:"requested_reviewers"`
	}
	_, err := c.doJSON(ctx, http.MethodPost,
		repositoryAPIPath(spec.Identity.Repository)+"/pulls/"+
			strconv.FormatInt(spec.Identity.Number, 10)+"/requested_reviewers", nil,
		map[string]any{"reviewers": spec.Reviewers}, spec.Credential, &response)
	if err != nil {
		return "", "", err
	}
	return preview.IdempotencyMarker, "", nil
}

const threadReconcileQuery = `query($id:ID!,$cursor:String){node(id:$id){... on PullRequestReviewThread{
  id isResolved comments(first:100,after:$cursor){nodes{id body url}
    pageInfo{hasNextPage endCursor}}}}}`

func (c *Client) reconcileWrite(ctx context.Context, spec WriteSpec,
	preview WritePreview,
) (WriteReceipt, bool, error) {
	marker := markerFor(preview)
	switch spec.Operation {
	case WriteReply, WriteResolve, WriteUnresolve:
		return c.reconcileThreadWrite(ctx, spec, marker)
	case WriteSubmitReview:
		reviews, page, err := fetchArrayPages[reviewResponse](c, ctx,
			repositoryAPIPath(spec.Identity.Repository)+"/pulls/"+
				strconv.FormatInt(spec.Identity.Number, 10)+"/reviews", nil,
			spec.Credential, MaxReviews, "review_reconcile")
		if err != nil {
			return WriteReceipt{}, false, err
		}
		for _, review := range reviews {
			if strings.Contains(review.Body, marker) {
				return WriteReceipt{ResultID: firstNonEmpty(review.NodeID,
					strconv.FormatInt(review.ID, 10))}, true, nil
			}
		}
		if !page.Complete {
			return WriteReceipt{}, false, &Error{Code: FailureResponseBound,
				Message: "GitHub review reconciliation exceeded the pagination bound"}
		}
	case WriteRequestReviewer:
		requested, err := c.readRequestedReviewers(ctx,
			repositoryAPIPath(spec.Identity.Repository), SnapshotRequest{
				Repository: spec.Identity.Repository, Number: spec.Identity.Number,
				Credential: spec.Credential})
		if err != nil {
			return WriteReceipt{}, false, err
		}
		set := make(map[string]struct{}, len(requested))
		for _, login := range requested {
			set[login] = struct{}{}
		}
		complete := true
		for _, login := range spec.Reviewers {
			if _, exists := set[login]; !exists {
				complete = false
				break
			}
		}
		if complete {
			return WriteReceipt{ResultID: preview.IdempotencyMarker}, true, nil
		}
	}
	return WriteReceipt{}, false, nil
}

func (c *Client) reconcileThreadWrite(ctx context.Context, spec WriteSpec,
	marker string,
) (WriteReceipt, bool, error) {
	var cursor any
	for page := 0; page < MaxPages; page++ {
		var data struct {
			Node *struct {
				ID         string `json:"id"`
				IsResolved bool   `json:"isResolved"`
				Comments   struct {
					Nodes []struct {
						ID   string `json:"id"`
						Body string `json:"body"`
						URL  string `json:"url"`
					} `json:"nodes"`
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
				} `json:"comments"`
			} `json:"node"`
		}
		_, err := c.doGraphQL(ctx, threadReconcileQuery,
			map[string]any{"id": spec.TargetID, "cursor": cursor}, spec.Credential, &data)
		if err != nil {
			return WriteReceipt{}, false, err
		}
		if data.Node == nil {
			return WriteReceipt{}, false, &Error{Code: FailureNotFound,
				Message: "GitHub review thread no longer exists"}
		}
		if spec.Operation == WriteResolve && data.Node.IsResolved ||
			spec.Operation == WriteUnresolve && !data.Node.IsResolved {
			return WriteReceipt{ResultID: data.Node.ID}, true, nil
		}
		if spec.Operation != WriteReply {
			return WriteReceipt{}, false, nil
		}
		for _, comment := range data.Node.Comments.Nodes {
			if strings.Contains(comment.Body, marker) {
				return WriteReceipt{ResultID: comment.ID,
					ResultURL: safeGitHubURL(comment.URL)}, true, nil
			}
		}
		if !data.Node.Comments.PageInfo.HasNextPage {
			return WriteReceipt{}, false, nil
		}
		if data.Node.Comments.PageInfo.EndCursor == "" {
			return WriteReceipt{}, false, &Error{Code: FailureMalformed,
				Message: "GitHub review thread pagination omitted its cursor"}
		}
		cursor = data.Node.Comments.PageInfo.EndCursor
	}
	return WriteReceipt{}, false, &Error{Code: FailureResponseBound,
		Message: "GitHub review thread reconciliation exceeded the pagination bound"}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func failedReceipt(preview WritePreview, code FailureCode, message string,
	started, completed time.Time,
) WriteReceipt {
	return WriteReceipt{ProtocolVersion: ReceiptProtocolVersion,
		ID: "ghr-" + Fingerprint("receipt", preview.ID)[:32], PreviewID: preview.ID,
		Operation: preview.Operation, Status: ReceiptFailed, Identity: preview.Identity,
		TargetID: preview.TargetID, IdempotencyMarker: preview.IdempotencyMarker,
		ErrorCode: string(code), ErrorSummary: boundedPlainText(message, 500),
		StartedAt: started.UTC(), CompletedAt: completed.UTC()}
}

func writeReceiptSummary(receipt WriteReceipt) string {
	return fmt.Sprintf("%s %s for %s#%d", receipt.Status, receipt.Operation,
		receipt.Identity.Repository.FullName, receipt.Identity.Number)
}
