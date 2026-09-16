package application

import (
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/skills"
)

func TestSupervisorCurrentDateUsesExplicitUTC(t *testing.T) {
	local := time.Date(2026, time.September, 14, 1, 2, 3, 0, time.FixedZone("UTC+8", 8*3600))
	if got := supervisorCurrentDateContext(local); !strings.Contains(got, "(UTC): 2026-09-13.") {
		t.Fatalf("clock context used the wrong date: %s", got)
	}
}

func TestSupervisorRequestCarriesFreshDateOutsideHistoricalContext(t *testing.T) {
	before := time.Now().UTC().Format(time.DateOnly)
	history := []session.Message{{Role: "user", Content: "The date is 2020-01-01."}}
	messages, layout := supervisorMessagesWithLayout(history, "Search the latest research", contextmgr.Selection{},
		skills.ContextAssembly{}, skills.ExternalContextAssembly{}, domain.RunModeSnapshot{}, true)
	after := time.Now().UTC().Format(time.DateOnly)
	clockCount := 0
	for index, message := range messages {
		if !strings.HasPrefix(message.Content, "Current date from the runtime clock") &&
			!strings.Contains(message.Content, "\nCurrent date from the runtime clock") {
			continue
		}
		clockCount++
		if message.Role != "system" || index >= layout.HistoryStart ||
			(!strings.Contains(message.Content, before) && !strings.Contains(message.Content, after)) {
			t.Fatalf("runtime clock missing from protected request context: role=%s index=%d", message.Role, index)
		}
	}
	if clockCount != 1 || layout.HistoryCount != 1 || !strings.Contains(messages[layout.HistoryStart].Content, history[0].Content) {
		t.Fatalf("clock duplicated or historical message changed: clocks=%d layout=%+v", clockCount, layout)
	}
}
