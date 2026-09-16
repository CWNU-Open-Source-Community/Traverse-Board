package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
)

func TestSupervisorThreadContinueRepairReopensOriginalInputAndRejectsOldLease(t *testing.T) {
	for _, test := range []struct {
		name    string
		round   int
		padding bool
	}{
		{"after tools", 1, false}, {"normalized whitespace", 1, true}, {"text tool tail before any tools", 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			dbPath := filepath.Join(t.TempDir(), "thread-continue.db")
			st, err := Open(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			_, run, err := application.NewRunService(st).Create(ctx, application.CreateRunRequest{Goal: "record two notes", Profile: "review", Surface: "code", Phase: "deliver", Interactive: true, Budget: domain.Budget{MaxTurns: 8}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = application.NewRunService(st).Start(ctx, run.ID); err != nil {
				t.Fatal(err)
			}
			queued, err := st.EnqueueOperatorSteering(ctx, domain.EnqueueOperatorSteeringRequest{RunID: run.ID, SessionID: run.SessionID, Content: "original operator input", OperationKey: "thread-continue-original", RequestedBy: "operator"})
			if err != nil {
				t.Fatal(err)
			}
			lease := acquireTestRunExecutionLease(t, ctx, st, run.ID)
			turn, err := st.BeginSupervisorSteeringTurn(ctx, lease)
			if err != nil {
				t.Fatal(err)
			}
			cp := turn.Checkpoint
			if test.round > 0 {
				a := startToolRepairModel(t, st, turn.Checkpoint, 0, 0)
				a.Outcome = llm.OutcomeSuccess
				cp, err = st.RecordSupervisorModelCompleted(ctx, turn.Checkpoint, a, llm.ChatResponse{Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}, ToolCalls: []llm.ToolCall{toolRepairNote(t, turn.Checkpoint)}})
				if err != nil {
					t.Fatal(err)
				}
				rounds, err := st.ListSupervisorToolRounds(ctx, cp)
				if err != nil || len(rounds) != 1 {
					t.Fatal(err)
				}
				call := rounds[0].Calls[0]
				if _, err = st.RecordSupervisorToolExecutionStarted(ctx, cp, call.CallID); err != nil {
					t.Fatal(err)
				}
				if _, _, err = st.RecordSupervisorToolResult(ctx, cp, domain.SupervisorToolResult{CallID: call.CallID, Status: domain.SupervisorToolCompleted, ResultJSON: `{"status":"completed"}`, CompletedAt: time.Now().UTC()}); err != nil {
					t.Fatal(err)
				}
			}
			a := startToolRepairModel(t, st, cp, 0, test.round)
			reason, _ := domain.NewSupervisorThreadContinueRepairReason(1)
			response := llm.ChatResponse{Text: `{"version":"root_lifecycle.v1","action":"continue","message":"next step remains"}`, Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}}

			if test.padding {
				response.Text = `{"version":" root_lifecycle.v1 ","action":"continue","message":"` + strings.Repeat(" ", 17*1024) + `next step remains ","summary":" ","reason":" "}`
			}
			if test.round == 0 {
				reason, _ = domain.NewSupervisorTextToolRepairReason(0)
				response.Text += ` <｜｜DSML｜｜ calls><｜｜DSML｜｜ invoke name="note_create">unexecuted text</｜｜DSML｜｜ invoke></｜｜DSML｜｜ calls>`
			}
			wrong := response
			wrong.Text = `{"version":"root_lifecycle.v1","action":"wait","message":"question","reason":"external input"}`
			if _, err = st.RecordSupervisorProtocolFailure(ctx, cp, a, wrong, reason, true); err == nil {
				t.Fatal("wait response acquired continuation tool correction")
			}
			cp, err = st.RecordSupervisorProtocolFailure(ctx, cp, a, response, reason, true)
			if err != nil || cp.RepairPhase != domain.ProtocolRepairPending {
				t.Fatalf("pending=%+v err=%v", cp, err)
			}
			oldCP := cp
			if err = st.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			expireTestRunExecutionLease(t, ctx, reopened, lease)
			replacement, err := reopened.AcquireRunExecutionLease(ctx, domain.AcquireRunExecutionLeaseRequest{RunID: run.ID, OwnerID: "reopen-continue", TTL: time.Minute})
			if err != nil {
				t.Fatal(err)
			}
			recovered, err := reopened.BeginSupervisorSteeringTurnForMessage(ctx, replacement.Lease, queued.Message.ID)
			if err != nil || !recovered.Recovered || recovered.Checkpoint.RepairReason != reason || recovered.Checkpoint.PendingInput != queued.Message.Content {
				t.Fatalf("recovery=%+v err=%v", recovered, err)
			}
			a = startToolRepairModel(t, reopened, recovered.Checkpoint, 1, test.round)
			a.Outcome = llm.OutcomeSuccess
			finish := llm.ChatResponse{Text: `{"version":"root_lifecycle.v1","action":"finish","message":"current reply done","summary":"current reply done"}`, Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}}
			if _, err = reopened.RecordSupervisorModelCompleted(ctx, oldCP, a, finish); err == nil {
				t.Fatal("old lease accepted correction")
			}
			cp, err = reopened.RecordSupervisorModelCompleted(ctx, recovered.Checkpoint, a, finish)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, err = reopened.CompleteSupervisorTurn(ctx, cp, finish, domain.RootAction{Version: domain.RootLifecycleVersion, Kind: domain.RootActionContinue, Message: "not an explicit ending"}, policy.Decision{Allowed: true}, 0); err == nil {
				t.Fatal("store silently consumed pending continue correction")
			}
			current, settled, _, err := reopened.CompleteSupervisorTurn(ctx, cp, finish, domain.RootAction{Version: domain.RootLifecycleVersion, Kind: domain.RootActionFinish, Message: "current reply done", Summary: "current reply done"}, policy.Decision{Allowed: true}, 0)
			if err != nil || current.Status != domain.RunRunning || settled.RepairPhase != domain.ProtocolRepairNone {
				t.Fatalf("settled=%+v err=%v", settled, err)
			}
			list, err := reopened.ListRunEvents(ctx, run.ID)
			if err != nil || countRunEventType(list, events.ProtocolRepairStartedEvent) != 1 || countRunEventType(list, events.SupervisorToolBatchEvent) != test.round {
				t.Fatal("reopening repeated work or repair start")
			}

		})
	}
}
