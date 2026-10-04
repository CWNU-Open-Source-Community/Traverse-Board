package application

import (
	"context"
	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/runner"
	"strings"
)

// ControlledCommandHistory reads sealed legacy records. It has no executor or
// mutation dependency; all new fixed commands use the Command Runtime Job.
type ControlledCommandHistory struct{ store ControlledCommandHistoryStore }
type ControlledCommandHistoryStore interface {
	GetControlledCommandProposal(context.Context, string) (runner.ControlledCommandProposal, error)
	ListControlledCommandProposals(context.Context, string, int) ([]runner.ControlledCommandProposal, error)
	GetControlledCommandProposalReview(context.Context, string) (runner.ControlledCommandProposalReview, bool, error)
	GetControlledCommandProposalResult(context.Context, string) (runner.ControlledCommandProposalResult, bool, error)
	GetControlledExecutionReceipt(context.Context, string) (runner.ControlledExecutionReceipt, bool, error)
}

func NewControlledCommandHistory(store ControlledCommandHistoryStore) *ControlledCommandHistory {
	return &ControlledCommandHistory{store: store}
}

type ControlledCommandProposalView struct {
	Proposal runner.ControlledCommandProposal
	Review   *runner.ControlledCommandProposalReview
	Result   *runner.ControlledCommandProposalResult
	Receipt  *runner.ControlledExecutionReceipt
}

func (s *ControlledCommandHistory) List(
	ctx context.Context,
	runID string,
	limit int,
) ([]ControlledCommandProposalView, error) {
	if s == nil || s.store == nil {
		return nil, apperror.New(apperror.CodeFailedPrecondition,
			"controlled command proposal store is required")
	}
	proposals, err := s.store.ListControlledCommandProposals(
		ctx, strings.TrimSpace(runID), limit)
	if err != nil {
		return nil, apperror.Normalize(err)
	}
	views := make([]ControlledCommandProposalView, 0, len(proposals))
	for _, proposal := range proposals {
		view, err := s.loadView(ctx, proposal)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

func (s *ControlledCommandHistory) Get(
	ctx context.Context,
	proposalID string,
) (ControlledCommandProposalView, error) {
	if s == nil || s.store == nil {
		return ControlledCommandProposalView{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"controlled command proposal store is required")
	}
	proposal, err := s.store.GetControlledCommandProposal(
		ctx, strings.TrimSpace(proposalID))
	if err != nil {
		return ControlledCommandProposalView{}, apperror.Normalize(err)
	}
	return s.loadView(ctx, proposal)
}

func (s *ControlledCommandHistory) loadView(
	ctx context.Context,
	proposal runner.ControlledCommandProposal,
) (ControlledCommandProposalView, error) {
	view := ControlledCommandProposalView{Proposal: proposal}
	review, found, err := s.store.GetControlledCommandProposalReview(
		ctx, proposal.ID)
	if err != nil {
		return ControlledCommandProposalView{}, apperror.Normalize(err)
	}
	if found {
		view.Review = &review
	}
	result, found, err := s.store.GetControlledCommandProposalResult(
		ctx, proposal.ID)
	if err != nil {
		return ControlledCommandProposalView{}, apperror.Normalize(err)
	}
	if !found {
		return view, nil
	}
	view.Result = &result
	receipt, found, err := s.store.GetControlledExecutionReceipt(
		ctx, result.RequestID)
	if err != nil {
		return ControlledCommandProposalView{}, apperror.Normalize(err)
	}
	if !found {
		return ControlledCommandProposalView{}, apperror.New(
			apperror.CodeInternal,
			"controlled command proposal result has no execution receipt")
	}
	view.Receipt = &receipt
	return view, nil
}
