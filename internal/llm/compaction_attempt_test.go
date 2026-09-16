package llm

import (
	"strings"
	"testing"
)

func TestCompactionAttemptPurposeAndMonetaryIdentity(t *testing.T) {
	a := ModelAttempt{Number: 3, TransportAttempt: 1, MaxAttempts: 1, Provider: "fixture", Model: "model", Purpose: ModelPurposeContextCompaction, CompactionSourceSHA256: strings.Repeat("ab", 32)}
	if err := a.ValidateStarted(); err != nil {
		t.Fatal(err)
	}
	key := a.MonetaryAttemptNumber()
	if key < 1<<62 {
		t.Fatal("compaction reused normal attempt cost range")
	}
	b := a
	b.Number = 1
	if b.MonetaryAttemptNumber() != key {
		t.Fatal("restart changed same-source cost identity")
	}
	b.CompactionSourceSHA256 = strings.Repeat("cd", 32)
	if b.MonetaryAttemptNumber() == key {
		t.Fatal("distinct source cost identity collided")
	}
	for name, change := range map[string]func(*ModelAttempt){
		"normal source": func(v *ModelAttempt) { v.Purpose = "" },
		"unknown":       func(v *ModelAttempt) { v.Purpose = "other" },
		"invalid sha":   func(v *ModelAttempt) { v.CompactionSourceSHA256 = "guess" },
		"retry":         func(v *ModelAttempt) { v.MaxAttempts = 2 },
		"repair":        func(v *ModelAttempt) { v.ProtocolRepair = 1 },
		"tool":          func(v *ModelAttempt) { v.ToolRound = 1 },
		"stream":        func(v *ModelAttempt) { v.StreamEvents = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			v := a
			change(&v)
			if v.ValidateStarted() == nil {
				t.Fatal("invalid purpose accepted")
			}
		})
	}
	if (ModelAttempt{Number: 7}).MonetaryAttemptNumber() != 7 {
		t.Fatal("normal cost key changed")
	}
}
