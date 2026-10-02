// Package providerhistory carries private, read-only Supervisor model history
// between the Store and application without adding state to public domain DTOs.
package providerhistory

import (
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
)

// Assistant is a model-history projection. Its tools are recorded historical
// results, never executable work or authority. JSON and formatting expose none
// of its fields, including the internal attempt identity.
type Assistant struct {
	Replay      *llm.ProviderReplay          `json:"-"`
	Rounds      []domain.SupervisorToolRound `json:"-"`
	RoundReplay map[int]*llm.ProviderReplay  `json:"-"`
	AttemptID   string                       `json:"-"`
}

func (Assistant) String() string   { return "<private assistant history>" }
func (Assistant) GoString() string { return "<private assistant history>" }
