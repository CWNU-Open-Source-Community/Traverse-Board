package contextmgr

import (
	"context"
	"errors"
	"sort"
)

// SummaryRecord is a detached, provenance-labeled excerpt available to a
// ranking strategy. Changing this value cannot change the stored source or
// grant authority. The strategy returns indexes, never replacement text.
type SummaryRecord struct {
	Ordinal         int
	Category        string
	SourceMessageID int64
	SourceKind      string
	SourceRef       string
	ContentSHA256   string
	Content         string
}

// SummaryStrategy ranks all candidate records in descending preference. Go
// retains the source anchors and applies the envelope/record budgets. This is
// an extractive policy seam, not a generative-summary or memory-service API.
type SummaryStrategy interface {
	Rank(context.Context, []SummaryRecord) ([]int, error)
}

func (m *Manager) WithSummaryStrategy(strategy SummaryStrategy) *Manager {
	if m != nil {
		m.strategy = strategy
	}
	return m
}

func rankHandoffRecords(ctx context.Context, records []handoffMemoryRecord,
	strategy SummaryStrategy,
) ([]handoffMemoryRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strategy == nil {
		candidates := append([]handoffMemoryRecord(nil), records...)
		sort.SliceStable(candidates, func(left, right int) bool {
			lp, rp := handoffRetentionPriority(candidates[left]), handoffRetentionPriority(candidates[right])
			if lp != rp {
				return lp > rp
			}
			return candidates[left].Ordinal > candidates[right].Ordinal
		})
		return candidates, nil
	}
	input := make([]SummaryRecord, len(records))
	for i, record := range records {
		input[i] = SummaryRecord{Ordinal: record.Ordinal, Category: record.Category,
			SourceMessageID: record.SourceMessageID, SourceKind: record.SourceKind,
			SourceRef: record.SourceRef, ContentSHA256: record.ContentSHA256, Content: record.Content}
	}
	order, err := strategy.Rank(ctx, input)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(order) != len(records) {
		return nil, errors.New("summary strategy must rank every source record exactly once")
	}
	seen := make([]bool, len(records))
	candidates := make([]handoffMemoryRecord, 0, len(records))
	for _, index := range order {
		if index < 0 || index >= len(records) || seen[index] {
			return nil, errors.New("summary strategy returned a duplicate or invalid source index")
		}
		seen[index] = true
		candidates = append(candidates, records[index])
	}
	return candidates, nil
}
