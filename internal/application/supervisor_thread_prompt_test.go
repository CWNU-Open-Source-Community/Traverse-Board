package application

import (
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/skills"
)

func TestSupervisorThreadPromptUsesExactEndTurnBinding(t *testing.T) {
	now := time.Now().UTC()
	thread := domain.Thread{ID: "thread-prompt", ProtocolVersion: domain.ThreadProtocolVersion,
		WorkspaceID: "workspace", MissionID: "mission", Title: "Thread prompt", Status: domain.ThreadActive,
		ActiveRunID: "run", LastRunID: "run", Version: 1, CreatedAt: now, UpdatedAt: now}
	turn := domain.SupervisorTurn{Run: domain.Run{ID: "run", MissionID: "mission", SessionID: "session",
		Status: domain.RunRunning, Config: domain.RunConfig{Interactive: true}},
		Mission: domain.Mission{ID: "mission", WorkspaceID: "workspace"},
		Mode:    domain.RunModeSnapshot{Surface: "code", Phase: domain.ExecutionPhaseDeliver}, OperatorSteering: true,
		Checkpoint: domain.SupervisorCheckpoint{RunID: "run", Phase: domain.SupervisorTurnStarted,
			AttemptID: "attempt", LeaseID: "lease", LeaseGeneration: 1}}
	items := []domain.WorkItem{{ID: "unfinished-work", Title: "Still pending", Status: domain.WorkItemPending, Version: 1}}
	for _, test := range []struct {
		name   string
		change func(*domain.SupervisorTurn, *domain.Thread)
		want   bool
	}{
		{"operator", func(*domain.SupervisorTurn, *domain.Thread) {}, true},
		{"approval continuation", func(v *domain.SupervisorTurn, _ *domain.Thread) {
			v.OperatorSteering = false
			v.ApprovalContinuation = true
		}, true},
		{"autonomous", func(v *domain.SupervisorTurn, _ *domain.Thread) { v.OperatorSteering = false }, false},
		{"Plan", func(v *domain.SupervisorTurn, _ *domain.Thread) { v.Mode.Phase = domain.ExecutionPhasePlan }, false},
		{"historical Run", func(_ *domain.SupervisorTurn, v *domain.Thread) { v.ActiveRunID = "successor" }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			current, bound := turn, thread
			test.change(&current, &bound)
			endTurn, err := supervisorThreadEndTurn(t.Context(), supervisorThreadLookupFixture{thread: bound}, current)
			if err != nil || endTurn != test.want {
				t.Fatalf("binding=%t err=%v", endTurn, err)
			}
			memory, err := supervisorMemoryContextWithinBudget(maxSupervisorMemoryTokens, endTurn, contextmgr.Summary{}, false, items, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			messages, _ := supervisorMessagesWithLayout(nil, "Report actual progress", memory, skills.ContextAssembly{}, skills.ExternalContextAssembly{}, current.Mode, endTurn)
			var text strings.Builder
			for _, message := range messages {
				text.WriteString(message.Content)
			}
			for _, legacy := range []string{"finish only when the mission is complete", "do not use finish while any listed item remains active"} {
				if strings.Contains(text.String(), legacy) == test.want {
					t.Errorf("wrong scoped lifecycle guidance %q", legacy)
				}
			}
			if strings.Contains(text.String(), "finish ends only the current reply") != test.want {
				t.Fatal("reply guidance did not match verified Thread binding")
			}
			for _, guidance := range []string{
				"tool-free action=continue after tool results is not a finished reply",
				"Outside an explicitly announced Harness scheduling boundary",
				"first create the exact reviewable proposal",
				"Questions, progress reports, and planning-only requests may be answered",
			} {
				if strings.Contains(text.String(), guidance) != test.want {
					t.Errorf("turn completion guidance %q did not match verified Thread binding", guidance)
				}
			}
			if !strings.Contains(text.String(), `"id":"unfinished-work"`) || !strings.Contains(text.String(), `"status":"pending"`) {
				t.Fatal("workboard evidence omitted or marked complete")
			}
			if current.Mode.Phase == domain.ExecutionPhasePlan && !strings.Contains(text.String(), "Never return finish.") {
				t.Fatal("Plan gate lost")
			}
		})
	}
}
