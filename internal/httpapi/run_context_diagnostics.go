package httpapi

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
)

const contextDiagnosticLimit = 40

type RunContextDiagnosticsView struct {
	Records   []RunContextDiagnosticView `json:"records"`
	Truncated bool                       `json:"truncated"`
}

// These are observations, not a mutable status or a promise that a worker is live.
// No provider response text or raw failure message enters this public projection.
type RunContextDiagnosticView struct {
	Sequence        int64     `json:"sequence"`
	OccurredAt      time.Time `json:"occurred_at"`
	Phase           string    `json:"phase"`
	AttemptID       string    `json:"attempt_id"`
	SourceSHA256    string    `json:"source_sha256"`
	ModelAttempt    int       `json:"model_attempt,omitempty"`
	SummaryID       int64     `json:"summary_id,omitempty"`
	Generated       *bool     `json:"generated,omitempty"`
	RemovedMessages int       `json:"removed_messages,omitempty"`
	FallbackCode    string    `json:"fallback_code,omitempty"`
}

type runContextDiagnosticReader interface {
	ListRunContextDiagnosticEvents(context.Context, string, int) ([]events.Event, error)
}

func readRunContextDiagnostics(ctx context.Context, reader runContextDiagnosticReader, run domain.Run) (*RunContextDiagnosticsView, error) {
	items, err := reader.ListRunContextDiagnosticEvents(ctx, run.ID, contextDiagnosticLimit+1)
	if err != nil {
		return nil, err
	}
	view := &RunContextDiagnosticsView{Records: []RunContextDiagnosticView{}, Truncated: len(items) > contextDiagnosticLimit}
	if view.Truncated {
		items = items[:contextDiagnosticLimit]
	}
	var previous int64
	for _, item := range items {
		if item.RunID != run.ID || item.MissionID != run.MissionID || item.Sequence <= 0 || item.CreatedAt.IsZero() ||
			(previous > 0 && item.Sequence >= previous) {
			return nil, apperror.New(apperror.CodeConflict, "context diagnostic receipt binding differs")
		}
		previous = item.Sequence
		var payload struct {
			Purpose          string `json:"purpose"`
			AttemptID        string `json:"attempt_id"`
			SourceSHA256     string `json:"source_sha256"`
			CompactionSource string `json:"compaction_source_sha256"`
			ModelAttempt     int    `json:"model_attempt"`
			SummaryID        int64  `json:"summary_id"`
			Generated        *bool  `json:"generated"`
			RemovedMessages  int    `json:"removed_messages"`
			FallbackReason   string `json:"generation_fallback_reason"`
			OutputRejected   bool   `json:"compaction_output_rejected"`
			UsageInvalid     bool   `json:"compaction_usage_invalid"`
		}
		if err := json.Unmarshal([]byte(item.PayloadJSON), &payload); err != nil || !domain.ValidAgentID(payload.AttemptID) {
			return nil, apperror.New(apperror.CodeConflict, "context diagnostic receipt is invalid")
		}
		record := RunContextDiagnosticView{Sequence: item.Sequence, OccurredAt: item.CreatedAt, AttemptID: payload.AttemptID}
		if item.Source == "context_manager" && item.Type == "session.context_compacted" {
			if payload.SummaryID <= 0 || payload.Generated == nil || payload.RemovedMessages < 1 {
				return nil, apperror.New(apperror.CodeConflict, "saved context diagnostic receipt is invalid")
			}
			record.Phase, record.SourceSHA256 = "summary_saved", payload.SourceSHA256
			record.SummaryID, record.Generated, record.RemovedMessages = payload.SummaryID, payload.Generated, payload.RemovedMessages
			if payload.FallbackReason != "" {
				record.FallbackCode = contextFallbackCode(payload.FallbackReason)
			}
		} else if item.Source == "model_gateway" && payload.Purpose == "context_compaction" && payload.ModelAttempt > 0 {
			record.SourceSHA256, record.ModelAttempt = payload.CompactionSource, payload.ModelAttempt
			switch item.Type {
			case events.ModelStartedEvent:
				record.Phase = "generation_started"
			case events.ModelCompletedEvent:
				record.Phase = "generation_received"
				if payload.OutputRejected || payload.UsageInvalid {
					record.Phase = "generation_rejected"
				}
			case events.ModelFailedEvent:
				record.Phase = "generation_failed"
			}
		}
		decoded, err := hex.DecodeString(record.SourceSHA256)
		if record.Phase == "" || err != nil || len(decoded) != 32 || strings.ToLower(record.SourceSHA256) != record.SourceSHA256 {
			return nil, apperror.New(apperror.CodeConflict, "context diagnostic source is invalid")
		}
		view.Records = append(view.Records, record)
	}
	return view, nil
}

func contextFallbackCode(reason string) string {
	for _, code := range []string{"generation_cost_budget", "generation_protocol_repair", "generation_provider_failure", "generation_invalid_response",
		"generation_input_data", "generation_input_window", "generation_token_budget"} {
		if strings.HasPrefix(reason, code+":") {
			return code
		}
	}
	// Unknown/older error text is intentionally not guessed or displayed raw.
	return "unclassified"
}
