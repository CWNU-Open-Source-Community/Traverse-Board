package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
)

type supervisorThreadLookupFixture struct {
	thread domain.Thread
	err    error
}

func (s supervisorThreadLookupFixture) GetThreadByRun(context.Context, string) (domain.Thread, error) {
	return s.thread, s.err
}

func TestSupervisorThreadFinishRequiresExactInteractiveBinding(t *testing.T) {
	now := time.Now().UTC()
	thread := domain.Thread{ID: "thread-end-turn", ProtocolVersion: domain.ThreadProtocolVersion,
		WorkspaceID: "workspace", MissionID: "mission", Title: "End turn", Status: domain.ThreadActive,
		ActiveRunID: "run", LastRunID: "run", Version: 1, CreatedAt: now, UpdatedAt: now}
	turn := domain.SupervisorTurn{Run: domain.Run{ID: "run", MissionID: "mission", SessionID: "session",
		Status: domain.RunRunning, Config: domain.RunConfig{Interactive: true}},
		Mission: domain.Mission{ID: "mission", WorkspaceID: "workspace"},
		Mode:    domain.RunModeSnapshot{Phase: domain.ExecutionPhaseDeliver}, OperatorSteering: true,
		Checkpoint: domain.SupervisorCheckpoint{RunID: "run", Phase: domain.SupervisorTurnStarted,
			AttemptID: "attempt", LeaseID: "lease", LeaseGeneration: 1}}
	finish := domain.RootAction{Kind: domain.RootActionFinish, Message: "Response done", Summary: "Response done"}
	items := []domain.WorkItem{{Status: domain.WorkItemPending}}
	for _, test := range []struct {
		name   string
		change func(*domain.SupervisorTurn, *domain.Thread)
		want   bool
	}{
		{"current operator turn", func(*domain.SupervisorTurn, *domain.Thread) {}, true},
		{"approval continuation", func(t *domain.SupervisorTurn, _ *domain.Thread) {
			t.OperatorSteering = false
			t.ApprovalContinuation = true
		}, true},
		{"autonomous run", func(t *domain.SupervisorTurn, _ *domain.Thread) { t.OperatorSteering = false }, false},
		{"noninteractive", func(t *domain.SupervisorTurn, _ *domain.Thread) { t.Run.Config.Interactive = false }, false},
		{"Plan finish forbidden", func(t *domain.SupervisorTurn, _ *domain.Thread) { t.Mode.Phase = domain.ExecutionPhasePlan }, false},
		{"other active Run", func(_ *domain.SupervisorTurn, th *domain.Thread) { th.ActiveRunID = "another-run" }, false},
		{"other mission", func(_ *domain.SupervisorTurn, th *domain.Thread) { th.MissionID = "another-mission" }, false},
		{"other workspace", func(_ *domain.SupervisorTurn, th *domain.Thread) { th.WorkspaceID = "another-workspace" }, false},
		{"archived Thread", func(_ *domain.SupervisorTurn, th *domain.Thread) { th.Status = domain.ThreadArchived }, false},
		{"unfenced attempt", func(t *domain.SupervisorTurn, _ *domain.Thread) { t.Checkpoint.LeaseID = "" }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			current, bound := turn, thread
			test.change(&current, &bound)
			endTurn, err := supervisorThreadEndTurn(t.Context(), supervisorThreadLookupFixture{thread: bound}, current)
			if err != nil || endTurn != test.want {
				t.Fatalf("endTurn=%t want=%t err=%v", endTurn, test.want, err)
			}
			action := supervisorValidationAction(finish, endTurn)
			err = validateRootActionAgainstWorkBoard(action, items, current.Mode.Phase)
			if (err == nil) != test.want {
				t.Fatalf("workboard gate changed: %+v %v", action, err)
			}
			if items[0].Status != domain.WorkItemPending || finish.Kind != domain.RootActionFinish {
				t.Fatal("validation mutated original records")
			}
		})
	}
	if value, err := supervisorThreadEndTurn(t.Context(), struct{}{}, turn); err != nil || value {
		t.Fatal("unsupported Store bypassed completion checks")
	}
	if value, err := supervisorThreadEndTurn(t.Context(), supervisorThreadLookupFixture{err: apperror.New(apperror.CodeNotFound, "missing")}, turn); err != nil || value {
		t.Fatal("missing Thread bypassed completion checks")
	}
	if value, err := supervisorThreadEndTurn(t.Context(), supervisorThreadLookupFixture{err: errors.New("lookup failed")}, turn); err == nil || value {
		t.Fatal("failed lookup was treated as an end-turn binding")
	}
}
