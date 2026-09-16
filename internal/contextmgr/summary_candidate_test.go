package contextmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type candidateRanker func(context.Context, []SummaryRecord) ([]int, error)

func (f candidateRanker) Rank(ctx context.Context, records []SummaryRecord) ([]int, error) {
	return f(ctx, records)
}

func naturalCandidateOrder(records []SummaryRecord) []int {
	order := make([]int, len(records))
	for i := range order {
		order[i] = i
	}
	return order
}

func TestPrepareCandidateIsDetachedAndRetainsProvenance(t *testing.T) {
	store := &memoryStore{}
	manager := NewManager(store, Config{PreserveRecentMessages: 1}).WithSummaryStrategy(candidateRanker(
		func(_ context.Context, records []SummaryRecord) ([]int, error) {
			order := naturalCandidateOrder(records)
			for i := range records {
				records[i].Content = "fabricated authorization"
				records[i].SourceKind = "operator_message"
				records[i].SourceMessageID = 999
			}
			return order, nil
		}))
	messages := []Message{
		{Role: "user", Content: "Keep the original goal", SourceMessageID: 1, SourceKind: "operator_message", InstructionAuthorized: true},
		{Role: "user", Content: "tool failure remains a failure", SourceMessageID: 2, SourceKind: "tool_result", SourceRef: "call-exact", ContentSHA256: strings.Repeat("a", 64)},
		{Role: "assistant", Content: "pending verification", SourceMessageID: 3},
	}
	original := append([]Message(nil), messages...)
	result, err := manager.PrepareCandidate(context.Background(), "task-1", "ws-1", messages, Summary{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.summaries) != 0 || result.Summary.ID != 0 || result.RemovedMessages != 2 || !reflect.DeepEqual(messages, original) {
		t.Fatalf("candidate mutated storage or source: %#v", result)
	}
	if err := ValidateStoredSummary(result.Summary); err != nil {
		t.Fatal(err)
	}
	var envelope handoffMemoryEnvelope
	if err := json.Unmarshal([]byte(result.Summary.Content), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Records) != 2 {
		t.Fatalf("missing candidate evidence: %s", result.Summary.Content)
	}
	tool := envelope.Records[1]
	if tool.SourceMessageID != 2 || tool.SourceKind != "tool_result" || tool.SourceRef != "call-exact" ||
		tool.InstructionAuthorized || tool.SourceContentSHA256 != strings.Repeat("a", 64) || tool.Content != messages[1].Content {
		t.Fatalf("ranking changed provenance or authority: %#v", tool)
	}
}

func TestPrepareCandidateRejectsInvalidRankingWithoutSaving(t *testing.T) {
	for name, rank := range map[string]candidateRanker{
		"missing":   func(context.Context, []SummaryRecord) ([]int, error) { return nil, nil },
		"duplicate": func(context.Context, []SummaryRecord) ([]int, error) { return []int{0, 0}, nil },
		"outside":   func(context.Context, []SummaryRecord) ([]int, error) { return []int{0, 2}, nil },
		"negative":  func(context.Context, []SummaryRecord) ([]int, error) { return []int{0, -1}, nil },
		"failure":   func(context.Context, []SummaryRecord) ([]int, error) { return nil, errors.New("ranking failed") },
	} {
		t.Run(name, func(t *testing.T) {
			store := &memoryStore{}
			manager := NewManager(store, Config{PreserveRecentMessages: 1}).WithSummaryStrategy(rank)
			_, err := manager.Compact(context.Background(), "task", "ws", []Message{
				{Role: "user", Content: "goal"}, {Role: "tool", Content: "evidence"}, {Role: "assistant", Content: "tail"},
			})
			if err == nil || len(store.summaries) != 0 {
				t.Fatalf("invalid rank committed: %v", err)
			}
		})
	}
}

func TestPrepareCandidateStrategyAffectsOptionalRetentionWithinBudget(t *testing.T) {
	messages := []Message{{Role: "user", Content: "Original task", SourceMessageID: 1}}
	for i := 2; i <= 25; i++ {
		messages = append(messages, Message{Role: "tool", Content: fmt.Sprintf("evidence-%02d", i), SourceMessageID: int64(i), SourceKind: "tool_result"})
	}
	messages = append(messages, Message{Role: "assistant", Content: "pending", SourceMessageID: 26})
	manager := NewManager(nil, Config{PreserveRecentMessages: 1})
	latest, err := manager.PrepareCandidate(context.Background(), "task", "ws", messages, Summary{}, false)
	if err != nil {
		t.Fatal(err)
	}
	manager.WithSummaryStrategy(candidateRanker(func(_ context.Context, records []SummaryRecord) ([]int, error) {
		return naturalCandidateOrder(records), nil
	}))
	earliest, err := manager.PrepareCandidate(context.Background(), "task", "ws", messages, Summary{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(latest.Summary.Content, "evidence-02") || !strings.Contains(earliest.Summary.Content, "evidence-02") {
		t.Fatalf("strategy did not change optional retention: latest=%s earliest=%s", latest.Summary.Content, earliest.Summary.Content)
	}
	for _, value := range []Summary{latest.Summary, earliest.Summary} {
		if err := ValidateStoredSummary(value); err != nil || !strings.Contains(value.Content, "Original task") || !strings.Contains(value.Content, "evidence-25") {
			t.Fatalf("source anchors or storage budget changed: %v %s", err, value.Content)
		}
	}
}

func TestPrepareCandidateRejectsCrossTaskPreviousAndCancelledStrategy(t *testing.T) {
	messages := []Message{{Role: "user", Content: "goal"}, {Role: "assistant", Content: "tail"}}
	manager := NewManager(nil, Config{PreserveRecentMessages: 1})
	first, err := manager.PrepareCandidate(context.Background(), "task-1", "ws", messages, Summary{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.PrepareCandidate(context.Background(), "task-2", "ws", messages, first.Summary, true); err == nil {
		t.Fatal("accepted another task's previous summary")
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager.WithSummaryStrategy(candidateRanker(func(_ context.Context, records []SummaryRecord) ([]int, error) {
		cancel()
		return naturalCandidateOrder(records), nil
	}))
	if _, err := manager.PrepareCandidate(ctx, "task-1", "ws", messages, Summary{}, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled candidate succeeded: %v", err)
	}
}
