package application

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/pricing"
)

// MonetaryBudgetStore is the durable monetary boundary. Only the Go control
// plane computes prices; a Provider response, README, Skill, or repository
// file can never touch this interface.
type MonetaryBudgetStore interface {
	ActivePriceSnapshot(context.Context) (pricing.Snapshot, bool, error)
	ReserveModelCost(context.Context, domain.MonetaryReserveRequest) (domain.MonetaryUsage, bool, error)
	SettleModelCost(context.Context, domain.MonetarySettleRequest) (domain.MonetaryUsage, bool, error)
	ReleaseModelCost(context.Context, domain.MonetaryReleaseRequest) (domain.MonetaryUsage, bool, error)
	GetMonetaryUsage(context.Context, string) (domain.MonetaryUsage, error)
	ReleaseOpenMonetaryReservations(context.Context, string) (int, error)
}

// MonetaryBudgetService composes the operator price snapshot with the
// durable run aggregate. Reserve computes the upper-bound cost before a
// model call; settle closes it with actual usage; release returns the
// unused portion. Runs without MaxCostUSD stay untracked and skip every
// gate.
type MonetaryBudgetService struct {
	store MonetaryBudgetStore
	now   func() time.Time
}

func NewMonetaryBudgetService(store MonetaryBudgetStore) *MonetaryBudgetService {
	return &MonetaryBudgetService{store: store, now: func() time.Time { return time.Now().UTC() }}
}

// ReserveModelCall reserves the upper-bound cost for one model attempt.
// For tracked runs the active price snapshot must carry an exact
// provider/model entry; unknown or expired prices fail closed.
func (s *MonetaryBudgetService) ReserveModelCall(ctx context.Context, run domain.Run,
	scope string, attempt llm.ModelAttempt, request llm.ChatRequest,
) (domain.MonetaryUsage, error) {
	if s == nil || s.store == nil {
		return domain.MonetaryUsage{}, nil
	}
	if run.Budget.MaxCostUSD <= 0 {
		return domain.MonetaryUsage{}, nil
	}
	snapshot, found, err := s.store.ActivePriceSnapshot(ctx)
	if err != nil {
		return domain.MonetaryUsage{}, apperror.Normalize(err)
	}
	if !found || !snapshot.Active(s.now()) {
		return domain.MonetaryUsage{}, apperror.New(apperror.CodeFailedPrecondition,
			"no active price snapshot is configured for the run monetary budget")
	}
	entry, ok := snapshot.Lookup(attempt.Provider, attempt.Model)
	if !ok {
		return domain.MonetaryUsage{}, apperror.New(apperror.CodeFailedPrecondition,
			"the model has no price entry in the active price snapshot")
	}
	inputBytes := estimateModelRequestBytes(request)
	maxTokens := request.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	reserve := entry.EstimateCost(inputBytes, int64(maxTokens), 0, int64(len(request.Tools)))
	if reserve <= 0 {
		return domain.MonetaryUsage{}, apperror.New(apperror.CodeFailedPrecondition,
			"the model price entry yields no positive cost bound")
	}
	usage, _, err := s.store.ReserveModelCost(ctx, domain.MonetaryReserveRequest{
		RunID: run.ID, Scope: scope, Provider: attempt.Provider, Model: attempt.Model,
		AttemptNumber: int64(attempt.Number), ReservedMicros: reserve,
		PriceFingerprint: snapshot.Fingerprint,
		EstimateSource:   pricing.ProtocolVersion + "/" + snapshot.ID,
	})
	if err != nil {
		return domain.MonetaryUsage{}, apperror.Normalize(err)
	}
	return usage, nil
}

// SettleModelCall closes one reservation with the actual usage cost. When
// the active snapshot no longer carries the model's entry, the settlement
// conservatively charges the full reservation instead of under-counting.
func (s *MonetaryBudgetService) SettleModelCall(ctx context.Context, runID, scope string,
	attempt llm.ModelAttempt, usage llm.Usage, toolCallCount int,
) (domain.MonetaryUsage, error) {
	if s == nil || s.store == nil {
		return domain.MonetaryUsage{}, nil
	}
	actual := int64(math.MaxInt64)
	if snapshot, found, err := s.store.ActivePriceSnapshot(ctx); err == nil && found &&
		snapshot.Active(s.now()) {
		if entry, ok := snapshot.Lookup(attempt.Provider, attempt.Model); ok {
			actual = entry.EstimateCost(int64(usage.InputTokens), int64(usage.OutputTokens), 0,
				int64(toolCallCount))
		}
	}
	return s.settle(ctx, domain.MonetarySettleRequest{
		RunID: runID, Scope: scope, AttemptNumber: int64(attempt.Number), ActualMicros: actual,
	})
}

func (s *MonetaryBudgetService) settle(ctx context.Context,
	request domain.MonetarySettleRequest,
) (domain.MonetaryUsage, error) {
	usage, _, err := s.store.SettleModelCost(ctx, request)
	if err != nil {
		return domain.MonetaryUsage{}, apperror.Normalize(err)
	}
	return usage, nil
}

// ReleaseModelCall releases an open reservation without settling (failed
// attempts and terminal runs).
func (s *MonetaryBudgetService) ReleaseModelCall(ctx context.Context, runID, scope string,
	attempt llm.ModelAttempt,
) (domain.MonetaryUsage, error) {
	if s == nil || s.store == nil {
		return domain.MonetaryUsage{}, nil
	}
	usage, _, err := s.store.ReleaseModelCost(ctx, domain.MonetaryReleaseRequest{
		RunID: runID, Scope: scope, AttemptNumber: int64(attempt.Number),
	})
	if err != nil {
		return domain.MonetaryUsage{}, apperror.Normalize(err)
	}
	return usage, nil
}

// Usage projects the run aggregate (with reconciliation of terminal
// attempts).
func (s *MonetaryBudgetService) Usage(ctx context.Context, runID string) (domain.MonetaryUsage, error) {
	if s == nil || s.store == nil {
		return domain.MonetaryUsage{}, nil
	}
	usage, err := s.store.GetMonetaryUsage(ctx, runID)
	if err != nil {
		return domain.MonetaryUsage{}, apperror.Normalize(err)
	}
	return usage, nil
}

// ReleaseOpenReservations releases every open reservation for a terminal
// run.
func (s *MonetaryBudgetService) ReleaseOpenReservations(ctx context.Context, runID string) error {
	if s == nil || s.store == nil {
		return nil
	}
	_, err := s.store.ReleaseOpenMonetaryReservations(ctx, runID)
	return apperror.Normalize(err)
}

// estimateModelRequestBytes is the conservative input upper bound: every
// BPE token occupies at least one byte, so the serialized request size can
// never under-count the input tokens.
func estimateModelRequestBytes(request llm.ChatRequest) int64 {
	raw, err := json.Marshal(request.Messages)
	if err == nil {
		return int64(len(raw)) + int64(len(request.Tools))*128
	}
	total := int64(0)
	for _, message := range request.Messages {
		total += int64(len(message.Content))
		for _, call := range message.ToolCalls {
			total += int64(len(call.Name) + len(call.Arguments))
		}
		for _, result := range message.ToolResults {
			total += int64(len(result.Content))
		}
	}
	return total + int64(len(request.Tools))*128
}

