package application

import (
	"context"
	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/toolgateway"
	"sort"
	"strings"
	"time"
)

const MaxHostCommandEvidenceBytes = 16 * 1024

type HostCommandHistoryStore interface {
	GetHostCommandProposalExecutionIntentByProposal(context.Context, string) (runner.HostExecutionIntent, bool, error)
	GetHostCommandProposal(context.Context, string) (runner.HostCommandProposal, error)
	ListHostCommandProposals(context.Context, string, int) ([]runner.HostCommandProposal, error)
	GetHostCommandProposalReview(context.Context, string) (runner.HostCommandReview, bool, error)
	GetHostCommandProposalResult(context.Context, string) (runner.HostCommandProposalResult, bool, error)
	GetHostCommandProposalReceipt(context.Context, string) (runner.HostExecutionReceipt, bool, error)
}
type RiskEscalationHistoryStore interface {
	ListSessionMessages(context.Context, string, bool) ([]session.Message, error)
	GetRiskEscalationProposal(context.Context, string) (runner.RiskEscalationProposal, error)
	ListRiskEscalationProposals(context.Context, string, int) ([]runner.RiskEscalationProposal, error)
	GetApprovalByProposal(context.Context, string) (approval.Record, error)
	GetSessionGrant(context.Context, string) (approval.SessionGrant, error)
	GetGrantConsumptionByProposal(context.Context, string) (approval.GrantConsumption, bool, error)
	GetRiskEscalationExecutionIntentByProposal(context.Context, string) (runner.HostExecutionIntent, bool, error)
	GetRiskEscalationResult(context.Context, string) (runner.RiskEscalationResult, bool, error)
	GetRiskEscalationReceipt(context.Context, string) (runner.HostExecutionReceipt, bool, error)
	GetRiskEscalationInvalidation(context.Context, string) (runner.RiskEscalationInvalidation, bool, error)
}

// HostCommandHistory preserves old evidence without dispatch or approval writes.
// Current commands and reviews use Command Runtime and its existing Job ledger.
type HostCommandHistory struct {
	store     HostCommandHistoryStore
	riskStore RiskEscalationHistoryStore
}

func NewHostCommandHistory(store HostCommandHistoryStore) *HostCommandHistory {
	service := &HostCommandHistory{store: store}
	service.riskStore, _ = store.(RiskEscalationHistoryStore)
	return service
}

type HostCommandProposalView struct {
	Proposal         runner.HostCommandProposal
	RiskEscalation   *runner.RiskEscalationProposal
	Approval         *approval.Record
	Grant            *approval.SessionGrant
	GrantConsumption *approval.GrantConsumption
	RiskResult       *runner.RiskEscalationResult
	Invalidation     *runner.RiskEscalationInvalidation
	Uncertain        bool
	Review           *runner.HostCommandReview
	Result           *runner.HostCommandProposalResult
	Receipt          *runner.HostExecutionReceipt
	SavedEvidence    string
}

func (v HostCommandProposalView) ID() string {
	if v.RiskEscalation != nil {
		return v.RiskEscalation.ID
	}
	return v.Proposal.ID
}

func (v HostCommandProposalView) RunID() string {
	if v.RiskEscalation != nil {
		return v.RiskEscalation.RunID
	}
	return v.Proposal.RunID
}

func (s *HostCommandHistory) List(ctx context.Context, runID string,
	limit int,
) ([]HostCommandProposalView, error) {
	if s == nil || s.store == nil {
		return nil, apperror.New(apperror.CodeFailedPrecondition,
			"host command proposal store is required")
	}
	proposals, err := s.store.ListHostCommandProposals(ctx, strings.TrimSpace(runID), limit)
	if err != nil {
		return nil, apperror.Normalize(err)
	}
	escalations := []runner.RiskEscalationProposal{}
	if s.riskStore != nil {
		escalations, err = s.riskStore.ListRiskEscalationProposals(ctx,
			strings.TrimSpace(runID), limit)
		if err != nil {
			return nil, apperror.Normalize(err)
		}
	}
	views := make([]HostCommandProposalView, 0, len(proposals)+len(escalations))
	for _, proposal := range proposals {
		view, err := s.loadView(ctx, proposal)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	for _, proposal := range escalations {
		view, err := s.loadRiskEscalationView(ctx, proposal)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool {
		created := func(view HostCommandProposalView) time.Time {
			if view.RiskEscalation != nil {
				return view.RiskEscalation.CreatedAt
			}
			return view.Proposal.CreatedAt
		}
		return created(views[i]).After(created(views[j]))
	})
	if len(views) > limit {
		views = views[:limit]
	}
	return views, nil
}

func (s *HostCommandHistory) Get(ctx context.Context,
	proposalID string,
) (HostCommandProposalView, error) {
	if s == nil || s.store == nil {
		return HostCommandProposalView{}, apperror.New(
			apperror.CodeFailedPrecondition, "host command proposal store is required")
	}
	proposalID = strings.TrimSpace(proposalID)
	if strings.HasPrefix(proposalID, "risk-escalation-") {
		if s.riskStore == nil {
			return HostCommandProposalView{}, apperror.New(
				apperror.CodeNotFound, "risk escalation proposal was not found")
		}
		proposal, err := s.riskStore.GetRiskEscalationProposal(ctx, proposalID)
		if err != nil {
			return HostCommandProposalView{}, apperror.Normalize(err)
		}
		return s.loadRiskEscalationView(ctx, proposal)
	}
	proposal, err := s.store.GetHostCommandProposal(ctx, proposalID)
	if err != nil {
		return HostCommandProposalView{}, apperror.Normalize(err)
	}
	view, err := s.loadView(ctx, proposal)
	if err != nil {
		return HostCommandProposalView{}, err
	}
	if reader, ok := s.store.(interface {
		GetHostCommandProposalEvidence(context.Context, string) (session.Message, bool, error)
	}); ok && view.Result != nil {
		message, found, readErr := reader.GetHostCommandProposalEvidence(ctx, proposal.ID)
		if readErr != nil {
			return HostCommandProposalView{}, apperror.Normalize(readErr)
		}
		if !found {
			return HostCommandProposalView{}, apperror.New(apperror.CodeConflict, "Host command result has no saved output evidence")
		}
		view.SavedEvidence = truncateUTF8Bytes(redact.String(sanitizeControlledCommandEvidence([]byte(message.Content))), MaxHostCommandEvidenceBytes)
	}
	return view, nil
}

func (s *HostCommandHistory) loadView(ctx context.Context,
	proposal runner.HostCommandProposal,
) (HostCommandProposalView, error) {
	view := HostCommandProposalView{Proposal: proposal}
	review, found, err := s.store.GetHostCommandProposalReview(ctx, proposal.ID)
	if err != nil {
		return HostCommandProposalView{}, apperror.Normalize(err)
	}
	if found {
		view.Review = &review
	}
	result, found, err := s.store.GetHostCommandProposalResult(ctx, proposal.ID)
	if err != nil {
		return HostCommandProposalView{}, apperror.Normalize(err)
	}
	if !found {
		_, prepared, readErr := s.store.GetHostCommandProposalExecutionIntentByProposal(ctx, proposal.ID)
		if readErr != nil {
			return HostCommandProposalView{}, apperror.Normalize(readErr)
		}
		view.Uncertain = prepared
		return view, nil
	}
	view.Result = &result
	receipt, found, err := s.store.GetHostCommandProposalReceipt(ctx, result.RequestID)
	if err != nil {
		return HostCommandProposalView{}, apperror.Normalize(err)
	}
	if !found {
		return HostCommandProposalView{}, apperror.New(
			apperror.CodeInternal, "host command proposal result has no execution receipt")
	}
	view.Receipt = &receipt
	return view, nil
}

func (s *HostCommandHistory) loadRiskEscalationView(ctx context.Context,
	proposal runner.RiskEscalationProposal,
) (HostCommandProposalView, error) {
	view := HostCommandProposalView{RiskEscalation: &proposal}
	record, err := s.riskStore.GetApprovalByProposal(ctx, proposal.ID)
	if err != nil {
		return HostCommandProposalView{}, apperror.Normalize(err)
	}
	view.Approval = &record
	if record.GrantID != "" {
		grant, loadErr := s.riskStore.GetSessionGrant(ctx, record.GrantID)
		if loadErr != nil {
			return HostCommandProposalView{}, apperror.Normalize(loadErr)
		}
		view.Grant = &grant
		if consumption, found, loadErr := s.riskStore.GetGrantConsumptionByProposal(
			ctx, proposal.ID); loadErr != nil {
			return HostCommandProposalView{}, apperror.Normalize(loadErr)
		} else if found {
			view.GrantConsumption = &consumption
		}
	}
	if invalidation, found, loadErr := s.riskStore.GetRiskEscalationInvalidation(
		ctx, proposal.ID); loadErr != nil {
		return HostCommandProposalView{}, apperror.Normalize(loadErr)
	} else if found {
		view.Invalidation = &invalidation
		view.Uncertain = invalidation.ReasonCode == "execution_uncertain"
	}
	result, found, err := s.riskStore.GetRiskEscalationResult(ctx, proposal.ID)
	if err != nil {
		return HostCommandProposalView{}, apperror.Normalize(err)
	}
	if !found {
		if _, intentFound, loadErr := s.riskStore.GetRiskEscalationExecutionIntentByProposal(
			ctx, proposal.ID); loadErr != nil {
			return HostCommandProposalView{}, apperror.Normalize(loadErr)
		} else if intentFound {
			view.Uncertain = true
		}
		return view, nil
	}
	view.RiskResult = &result
	receipt, found, err := s.riskStore.GetRiskEscalationReceipt(ctx, result.RequestID)
	if err != nil {
		return HostCommandProposalView{}, apperror.Normalize(err)
	}
	if !found {
		return HostCommandProposalView{}, apperror.New(apperror.CodeInternal,
			"risk escalation result has no execution receipt")
	}
	view.Receipt = &receipt
	return view, nil
}

func (s *HostCommandHistory) riskEscalationResult(ctx context.Context,
	proposal runner.RiskEscalationProposal, replayed bool,
) (toolgateway.HostCommandProposalResult, error) {
	record, err := s.riskStore.GetApprovalByProposal(ctx, proposal.ID)
	if err != nil {
		return toolgateway.HostCommandProposalResult{}, apperror.Normalize(err)
	}
	base := toolgateway.HostCommandProposalResult{ProposalID: proposal.ID,
		SpecFingerprint: proposal.Spec.Fingerprint, ApprovalID: record.ID,
		GrantID: record.GrantID, Replayed: replayed}
	if invalidation, found, loadErr := s.riskStore.GetRiskEscalationInvalidation(
		ctx, proposal.ID); loadErr != nil {
		return toolgateway.HostCommandProposalResult{}, apperror.Normalize(loadErr)
	} else if found {
		base.State = toolgateway.HostCommandProposalFailed
		base.ErrorCode = invalidation.ReasonCode
		base.Message = invalidation.Detail
		base.Uncertain = invalidation.ReasonCode == "execution_uncertain"
		return base, nil
	}
	switch record.Status {
	case approval.StatusPending:
		base.State = toolgateway.HostCommandProposalWaiting
		return base, nil
	case approval.StatusDenied:
		base.State = toolgateway.HostCommandProposalDenied
		base.Message = record.DecisionReason
		if base.Message == "" {
			base.Message = "operator denied the exact risk escalation"
		}
		return base, nil
	case approval.StatusApproved:
	default:
		return toolgateway.HostCommandProposalResult{}, apperror.New(
			apperror.CodeInternal, "risk escalation approval state is invalid")
	}
	result, found, err := s.riskStore.GetRiskEscalationResult(ctx, proposal.ID)
	if err != nil {
		return toolgateway.HostCommandProposalResult{}, apperror.Normalize(err)
	}
	if found {
		base.State = toolgateway.HostCommandProposalCompleted
		if result.Status == "failed" {
			base.State = toolgateway.HostCommandProposalFailed
			base.ErrorCode = result.ErrorCode
			base.Message = "approved risk escalation command failed"
		}
		messages, loadErr := s.riskStore.ListSessionMessages(ctx, proposal.SessionID, true)
		if loadErr != nil {
			return toolgateway.HostCommandProposalResult{}, apperror.Normalize(loadErr)
		}
		for _, message := range messages {
			if message.Provenance.SourceKind == result.SourceKind &&
				message.Provenance.SourceRef == result.SourceRef &&
				message.Provenance.ContentSHA256 == result.ContentSHA256 {
				base.Evidence = message.Content
				break
			}
		}
		return base, nil
	}
	if _, found, err := s.riskStore.GetRiskEscalationExecutionIntentByProposal(
		ctx, proposal.ID); err != nil {
		return toolgateway.HostCommandProposalResult{}, apperror.Normalize(err)
	} else if found {
		base.State = toolgateway.HostCommandProposalFailed
		base.ErrorCode = "execution_uncertain"
		base.Message = "a durable write-ahead intent exists without a result; automatic retry is permanently disabled"
		base.Uncertain = true
		return base, nil
	}
	base.State = toolgateway.HostCommandProposalWaiting
	return base, nil
}
