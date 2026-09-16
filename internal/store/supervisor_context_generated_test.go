package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/pricing"
	"cyberagent-workbench/internal/session"
)

func compactionTestAttempt(t *testing.T, st *SQLiteStore, cp domain.SupervisorCheckpoint, sha string) llm.ModelAttempt {
	t.Helper()
	n, err := st.NextSupervisorCompactionAttempt(context.Background(), cp)
	if err != nil {
		t.Fatal(err)
	}
	a := llm.ModelAttempt{Number: n, TransportAttempt: 1, MaxAttempts: 1, Provider: "fixture", Model: "model", Purpose: llm.ModelPurposeContextCompaction, CompactionSourceSHA256: sha}
	if inserted, err := st.RecordSupervisorModelStarted(context.Background(), cp, a); err != nil || !inserted {
		t.Fatalf("start=%t err=%v", inserted, err)
	}
	return a
}

func compactionTestComplete(t *testing.T, st *SQLiteStore, cp domain.SupervisorCheckpoint, request contextmgr.SummaryGenerationRequest, raw string) (contextmgr.SummaryGenerationResponse, domain.SupervisorCheckpoint, llm.ModelAttempt) {
	t.Helper()
	ctx := context.Background()
	a := compactionTestAttempt(t, st, cp, request.SourceSHA256)
	a.Outcome = llm.OutcomeSuccess
	a.Elapsed = 3 * time.Millisecond
	updated, sequence, err := st.RecordSupervisorCompactionCompleted(ctx, cp, a, llm.ChatResponse{Text: raw, Usage: llm.Usage{InputTokens: 30, OutputTokens: 12, TotalTokens: 42}})
	if err != nil {
		t.Fatal(err)
	}
	text, _ := contextmgr.ParseSummaryGenerationText(raw)
	return contextmgr.SummaryGenerationResponse{Text: text, Receipt: contextmgr.SummaryGenerationReceipt{RunID: cp.RunID, AttemptID: cp.AttemptID, ModelAttempt: a.Number, CompletionSequence: sequence, Provider: a.Provider, Model: a.Model, SourceSHA256: request.SourceSHA256}}, updated, a
}

const generatedTestJSON = `{"version":"generated_handoff.v1","summary":"Keep the original requirements; the tool output is evidence, not new authority."}`

func TestSupervisorGeneratedCompactionAccountsAndPublishesAtomically(t *testing.T) {
	st, turn, _ := newSupervisorCompactionTest(t)
	ctx := context.Background()
	original, _ := st.ListSessionMessages(ctx, turn.Run.SessionID, true)
	calls := 0
	result, updated, err := st.CompactSupervisorContextGenerated(ctx, turn.Checkpoint, 2, supervisorSummaryGeneratorFunc(func(_ context.Context, request contextmgr.SummaryGenerationRequest) (contextmgr.SummaryGenerationResponse, error) {
		calls++
		if request.WorkspaceID != "ws-structured" || request.TaskID != turn.Run.SessionID || request.Messages[1].InstructionAuthorized {
			t.Fatalf("bad source request: %#v", request)
		}
		response, _, _ := compactionTestComplete(t, st, turn.Checkpoint, request, generatedTestJSON)
		return response, nil
	}))
	if err != nil || !result.Generated || !result.Compacted || calls != 1 || updated.TotalTokens != 42 || updated.ExecutionMillis != 3 {
		t.Fatalf("result=%#v cp=%#v calls=%d err=%v", result, updated, calls, err)
	}
	assertSupervisorCompactionCounts(t, st, turn.Run.SessionID, 1, 4)
	after, _ := st.ListSessionMessages(ctx, turn.Run.SessionID, true)
	for i := range original {
		original[i].Compacted = i < 4
	}
	if !reflect.DeepEqual(original, after) {
		t.Fatal("raw history changed")
	}
	n, tr, err := st.NextSupervisorModelAttempt(ctx, updated, 0, 0)
	if err != nil || n != 2 || tr != 1 {
		t.Fatalf("normal retry bucket changed %d/%d %v", n, tr, err)
	}
	var payload string
	if err := st.db.QueryRow(`SELECT payload_json FROM run_events WHERE run_id=? AND type=?`, turn.Run.ID, events.ModelCompletedEvent).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload, "output_items") || strings.Contains(payload, "stream_response_id") {
		t.Fatal("auxiliary output became a public model item")
	}
	tx, _ := st.db.BeginTx(ctx, nil)
	if err := requireLatestSupervisorModelCompletedTx(ctx, tx, turn.Run.ID, updated); err == nil {
		t.Fatal("auxiliary completion authorized normal root action")
	}
	tx.Rollback()
	var generated bool
	if err := st.db.QueryRow(`SELECT json_extract(payload_json,'$.generated') FROM run_events WHERE run_id=? AND type='session.context_compacted'`, turn.Run.ID).Scan(&generated); err != nil || !generated {
		t.Fatalf("atomic receipt %t %v", generated, err)
	}
}

func TestSupervisorGeneratedCompactionRejectsDriftButRetainsUsage(t *testing.T) {
	for _, kind := range []string{"message", "input", "queued correction", "unrelated accounting", "lease"} {
		t.Run(kind, func(t *testing.T) {
			st, turn, lease := newSupervisorCompactionTest(t)
			ctx := context.Background()
			_, returned, err := st.CompactSupervisorContextGenerated(ctx, turn.Checkpoint, 2, supervisorSummaryGeneratorFunc(func(_ context.Context, r contextmgr.SummaryGenerationRequest) (contextmgr.SummaryGenerationResponse, error) {
				response, updated, _ := compactionTestComplete(t, st, turn.Checkpoint, r, generatedTestJSON)
				switch kind {
				case "queued correction":
					if _, e := st.EnqueueOperatorSteering(ctx, domain.EnqueueOperatorSteeringRequest{RunID: turn.Run.ID, SessionID: turn.Run.SessionID, Content: "correct the goal during generation", OperationKey: "generated-queued-correction", RequestedBy: "operator"}); e != nil {
						t.Fatal(e)
					}
				case "message":
					_, e := st.SaveSessionMessage(ctx, session.NewMessage(turn.Run.SessionID, "user", "new correction during generation"))
					if e != nil {
						t.Fatal(e)
					}
				case "input":
					_, e := st.db.Exec(`UPDATE run_supervisor_checkpoints SET pending_input='new pending input' WHERE run_id=?`, turn.Run.ID)
					if e != nil {
						t.Fatal(e)
					}
				case "unrelated accounting":
					a := llm.ModelAttempt{Number: 2, TransportAttempt: 1, MaxAttempts: 1, Provider: "fixture", Model: "model"}
					if _, e := st.RecordSupervisorModelStarted(ctx, updated, a); e != nil {
						t.Fatal(e)
					}
					a.Outcome = llm.OutcomeSuccess
					if _, e := st.RecordSupervisorModelCompleted(ctx, updated, a, llm.ChatResponse{Text: "other response", Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}}); e != nil {
						t.Fatal(e)
					}
				case "lease":
					expireTestRunExecutionLease(t, ctx, st, lease)
				}
				return response, nil
			}))
			if !errors.Is(err, contextmgr.ErrSummaryGenerationAborted) || returned.RunID != "" {
				t.Fatalf("stale candidate err=%v returned=%#v", err, returned)
			}
			assertSupervisorCompactionCounts(t, st, turn.Run.SessionID, 0, 0)
			actual, _, _ := st.GetSupervisorCheckpoint(ctx, turn.Run.ID)
			if actual.TotalTokens < 42 {
				t.Fatalf("usage lost %#v", actual)
			}
		})
	}
}

func TestSupervisorCompactionDoesNotConsumePreparedSkillRequirement(t *testing.T) {
	st, turn, _, _ := createRootSkillContextTurn(t, filepath.Join(t.TempDir(), "compaction-skills.db"))
	defer st.Close()
	ctx := context.Background()
	reader, finish, e := st.beginThreadRequestObservation(ctx)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := readSupervisorCompactionSnapshot(ctx, reader, turn.Checkpoint)
	finish()
	if e != nil {
		t.Fatal(e)
	}
	a := compactionTestAttempt(t, st, turn.Checkpoint, supervisorCompactionSourceHash(snapshot))
	assertTableCount(t, st, "root_skill_context_commits", 0)
	a.Outcome = llm.OutcomePermanent
	a.ErrorText = "fixture denied"
	a.Elapsed = time.Millisecond
	updated, e := st.RecordSupervisorModelFailed(ctx, turn.Checkpoint, a)
	if e != nil {
		t.Fatal(e)
	}
	n, tr, e := st.NextSupervisorModelAttempt(ctx, updated, 0, 0)
	if e != nil || n != 2 || tr != 1 {
		t.Fatalf("retry counter %d/%d %v", n, tr, e)
	}
	if _, e := st.RecordSupervisorModelStarted(ctx, updated, llm.ModelAttempt{Number: n, TransportAttempt: tr, MaxAttempts: 3, Provider: "fixture", Model: "model"}); apperror.CodeOf(e) != apperror.CodeFailedPrecondition {
		t.Fatalf("normal Skill requirement bypassed %v", e)
	}
	assertTableCount(t, st, "root_skill_context_commits", 0)
}

func TestSupervisorCompactionMonetaryReconciliationUsesExactSourceCostKey(t *testing.T) {
	for _, kind := range []string{"known", "unknown failure", "zero usage", "total only", "invalid usage", "late known after cancel", "stopped unknown"} {
		t.Run(kind, func(t *testing.T) {
			st, run := newMonetaryTestStore(t)
			ctx := context.Background()
			prices := monetaryTestSnapshot(t, time.Now().UTC())
			prices.Entries[0].Provider = "fixture"
			prices.Entries[0].Model = "model"
			prices.Fingerprint = pricing.Fingerprint(prices)
			if _, _, e := st.ImportPriceSnapshot(ctx, prices); e != nil {
				t.Fatal(e)
			}
			if _, e := application.NewRunService(st).Start(ctx, run.ID); e != nil {
				t.Fatal(e)
			}
			turn, e := st.BeginSupervisorTurn(ctx, acquireTestRunExecutionLease(t, ctx, st, run.ID), "current input")
			if e != nil {
				t.Fatal(e)
			}
			for i := range 6 {
				if _, e := st.SaveSessionMessage(ctx, session.NewMessage(run.SessionID, "user", strings.Repeat("historical source ", i+1))); e != nil {
					t.Fatal(e)
				}
			}
			result, _, err := st.CompactSupervisorContextGenerated(ctx, turn.Checkpoint, 2, supervisorSummaryGeneratorFunc(func(_ context.Context, r contextmgr.SummaryGenerationRequest) (contextmgr.SummaryGenerationResponse, error) {
				n, e := st.NextSupervisorCompactionAttempt(ctx, turn.Checkpoint)
				if e != nil {
					t.Fatal(e)
				}
				a := llm.ModelAttempt{Number: n, TransportAttempt: 1, MaxAttempts: 1, Provider: "fixture", Model: "model", Purpose: llm.ModelPurposeContextCompaction, CompactionSourceSHA256: r.SourceSHA256}
				for _, key := range []int64{int64(a.Number), a.MonetaryAttemptNumber()} {
					if _, _, e := st.ReserveModelCost(ctx, domain.MonetaryReserveRequest{RunID: run.ID, Scope: domain.MonetaryScopeRoot, Provider: a.Provider, Model: a.Model, AttemptNumber: key, ReservedMicros: 500000, PriceFingerprint: prices.Fingerprint, EstimateSource: "generated-test"}); e != nil {
						t.Fatal(e)
					}
				}
				if _, e := st.RecordSupervisorModelStarted(ctx, turn.Checkpoint, a); e != nil {
					t.Fatal(e)
				}
				a.Elapsed = time.Millisecond
				if kind == "late known after cancel" || kind == "stopped unknown" {
					if _, e := application.NewRunService(st).Cancel(ctx, run.ID); e != nil {
						t.Fatal(e)
					}
					var status string
					if e := st.db.QueryRow(`SELECT status FROM run_monetary_reservations WHERE run_id=? AND attempt_number=?`, run.ID, a.MonetaryAttemptNumber()).Scan(&status); e != nil || status != "reserved" {
						t.Fatalf("started unknown released: %s %v", status, e)
					}
					if kind == "stopped unknown" {
						return contextmgr.SummaryGenerationResponse{}, contextmgr.ErrSummaryGenerationAborted
					}
				}
				if kind == "unknown failure" {
					a.Outcome = llm.OutcomeRetryable
					a.ErrorText = "response lost"
					if _, e := st.RecordSupervisorModelFailed(ctx, turn.Checkpoint, a); e != nil {
						t.Fatal(e)
					}
					return contextmgr.SummaryGenerationResponse{}, errors.New("response lost")
				}
				a.Outcome = llm.OutcomeSuccess
				usage := llm.Usage{InputTokens: 100, OutputTokens: 50, TotalTokens: 150}
				if kind == "zero usage" {
					usage = llm.Usage{}
				}
				if kind == "total only" {
					usage = llm.Usage{TotalTokens: 124}
				}
				if kind == "invalid usage" {
					usage = llm.Usage{InputTokens: -2, OutputTokens: 3, TotalTokens: 1}
				}
				_, seq, e := st.RecordSupervisorCompactionCompleted(ctx, turn.Checkpoint, a, llm.ChatResponse{Text: generatedTestJSON, Usage: usage})
				if kind == "invalid usage" {
					return contextmgr.SummaryGenerationResponse{}, e
				}
				if e != nil {
					t.Fatal(e)
				}
				text, _ := contextmgr.ParseSummaryGenerationText(generatedTestJSON)
				return contextmgr.SummaryGenerationResponse{Text: text, Receipt: contextmgr.SummaryGenerationReceipt{RunID: run.ID, AttemptID: turn.Checkpoint.AttemptID, ModelAttempt: n, CompletionSequence: seq, Provider: a.Provider, Model: a.Model, SourceSHA256: r.SourceSHA256}}, nil
			}))
			if kind == "invalid usage" || kind == "late known after cancel" || kind == "stopped unknown" {
				if !errors.Is(err, contextmgr.ErrSummaryGenerationAborted) {
					t.Fatalf("invalid usage %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if kind == "unknown failure" && result.GenerationFallbackReason == "" {
				t.Fatal("fallback not disclosed")
			}
			usage, e := st.GetMonetaryUsage(ctx, run.ID)
			if e != nil {
				t.Fatal(e)
			}
			wantSettled, wantReleased := int64(500000), int64(0)
			if kind == "known" {
				wantSettled, wantReleased = 200, 499800
			}
			if kind == "late known after cancel" {
				wantSettled, wantReleased = 200, 999800
			}
			if kind == "stopped unknown" {
				wantSettled, wantReleased = 0, 500000
			}
			if usage.SettledMicros != wantSettled || usage.ReleasedMicros != wantReleased {
				t.Fatalf("source-key reconciliation %#v want=%d/%d", usage, wantSettled, wantReleased)
			}
			var normalStatus string
			st.db.QueryRow(`SELECT status FROM run_monetary_reservations WHERE run_id=? AND attempt_number=1`, run.ID).Scan(&normalStatus)
			wantNormal := "reserved"
			if kind == "late known after cancel" || kind == "stopped unknown" {
				wantNormal = "released"
			}
			if normalStatus != wantNormal {
				t.Fatal("compaction terminal settled legacy normal reservation")
			}
			if kind == "total only" {
				cp, _, e := st.GetSupervisorCheckpoint(ctx, run.ID)
				if e != nil || cp.TotalTokens != 124 {
					t.Fatalf("known total lost %#v %v", cp, e)
				}
			}
			if kind == "late known after cancel" {
				current, _, e := st.GetSupervisorCheckpoint(ctx, run.ID)
				if e != nil || current.TotalTokens != 150 || current.PendingInput != turn.Checkpoint.PendingInput || current.Phase != turn.Checkpoint.Phase {
					t.Fatalf("late usage overwrote current state: %#v %v", current, e)
				}
				stopped, e := st.GetRun(ctx, run.ID)
				if e != nil || stopped.Status != domain.RunCancelled {
					t.Fatalf("late completion revived Run %#v %v", stopped, e)
				}
				assertSupervisorCompactionCounts(t, st, run.SessionID, 0, 0)
			}
		})
	}
}

func TestSupervisorGeneratedCompactionFailureAndPublicationRetryDoNotReissue(t *testing.T) {
	st, turn, _ := newSupervisorCompactionTest(t)
	ctx := context.Background()
	calls := 0
	if _, err := st.db.Exec(`CREATE TRIGGER reject_generated_marks BEFORE UPDATE OF compacted ON session_messages WHEN NEW.compacted=1 BEGIN SELECT RAISE(ABORT,'test publication rejection'); END`); err != nil {
		t.Fatal(err)
	}
	g := supervisorSummaryGeneratorFunc(func(_ context.Context, r contextmgr.SummaryGenerationRequest) (contextmgr.SummaryGenerationResponse, error) {
		calls++
		response, _, _ := compactionTestComplete(t, st, turn.Checkpoint, r, generatedTestJSON)
		return response, nil
	})
	if _, _, err := st.CompactSupervisorContextGenerated(ctx, turn.Checkpoint, 2, g); err == nil {
		t.Fatal("publication failure ignored")
	}
	assertSupervisorCompactionCounts(t, st, turn.Run.SessionID, 0, 0)
	var dbPath string
	st.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&dbPath)
	st.db.Exec(`DROP TRIGGER reject_generated_marks`)
	st.Close()
	reopened, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	cp, _, _ := reopened.GetSupervisorCheckpoint(ctx, turn.Run.ID)
	result, updated, err := reopened.CompactSupervisorContextGenerated(ctx, cp, 2, g)
	if err != nil || result.Generated || result.GenerationFallbackReason == "" || calls != 1 || updated.TotalTokens != 42 {
		t.Fatalf("retry=%#v calls=%d cp=%#v err=%v", result, calls, updated, err)
	}
	assertSupervisorCompactionCounts(t, reopened, turn.Run.SessionID, 1, 4)
}

func TestSupervisorGeneratedCompactionUnknownStartSurvivesRestartAndTakeover(t *testing.T) {
	st, turn, lease := newSupervisorCompactionTest(t)
	ctx := context.Background()
	calls := 0
	g := supervisorSummaryGeneratorFunc(func(_ context.Context, r contextmgr.SummaryGenerationRequest) (contextmgr.SummaryGenerationResponse, error) {
		calls++
		compactionTestAttempt(t, st, turn.Checkpoint, r.SourceSHA256)
		return contextmgr.SummaryGenerationResponse{}, errors.New("transport disappeared without a terminal receipt")
	})
	if _, _, err := st.CompactSupervisorContextGenerated(ctx, turn.Checkpoint, 2, g); !errors.Is(err, contextmgr.ErrSummaryGenerationAborted) {
		t.Fatal(err)
	}
	var dbPath string
	st.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&dbPath)
	st.Close()
	reopened, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	expireTestRunExecutionLease(t, ctx, reopened, lease)
	next, err := reopened.AcquireRunExecutionLease(ctx, domain.AcquireRunExecutionLeaseRequest{RunID: turn.Run.ID, OwnerID: "restarted-owner", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := reopened.BeginSupervisorTurn(ctx, next.Lease, turn.Checkpoint.PendingInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := reopened.CompactSupervisorContextGenerated(ctx, recovered.Checkpoint, 2, g); !errors.Is(err, contextmgr.ErrSummaryGenerationAborted) || calls != 1 {
		t.Fatalf("unknown repeated %d %v", calls, err)
	}
	assertSupervisorCompactionCounts(t, reopened, turn.Run.SessionID, 0, 0)
}

func TestSupervisorCompactionPurposeCannotMasqueradeOrPublishTools(t *testing.T) {
	st, turn, _ := newSupervisorCompactionTest(t)
	ctx := context.Background()
	reader, finish, err := st.beginThreadRequestObservation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := readSupervisorCompactionSnapshot(ctx, reader, turn.Checkpoint)
	finish()
	if err != nil {
		t.Fatal(err)
	}
	a := compactionTestAttempt(t, st, turn.Checkpoint, supervisorCompactionSourceHash(snapshot))
	wrong := a
	wrong.Purpose = ""
	wrong.CompactionSourceSHA256 = ""
	if _, err := st.RecordSupervisorModelStarted(ctx, turn.Checkpoint, wrong); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("start masquerade %v", err)
	}
	wrong.Outcome = llm.OutcomeSuccess
	if _, err := st.RecordSupervisorModelCompleted(ctx, turn.Checkpoint, wrong, llm.ChatResponse{Text: "fake"}); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("completion masquerade %v", err)
	}
	if _, err := st.RecordSupervisorModelDelta(ctx, turn.Checkpoint, a, llm.ModelDelta{}); err == nil {
		t.Fatal("compaction delta allowed")
	}
	if _, err := st.RecordSupervisorModelPublicCommentary(ctx, turn.Checkpoint, a, domain.ModelPublicCommentary{}); err == nil {
		t.Fatal("compaction commentary allowed")
	}
	if _, err := st.RecordSupervisorProtocolFailure(ctx, turn.Checkpoint, a, llm.ChatResponse{}, "bad", true); err == nil {
		t.Fatal("compaction entered root repair")
	}
	a.Outcome = llm.OutcomeSuccess
	response := llm.ChatResponse{Text: generatedTestJSON, ToolCalls: []llm.ToolCall{{ID: "unwanted-tool", Name: "workspace_read", Arguments: json.RawMessage(`{"path":"secret.txt"}`)}}, Usage: llm.Usage{InputTokens: 4, OutputTokens: 2, TotalTokens: 6}}
	updated, seq, err := st.RecordSupervisorCompactionCompleted(ctx, turn.Checkpoint, a, response)
	if err != nil || seq == 0 || updated.TotalTokens != 6 {
		t.Fatalf("unexpected tool usage lost %v", err)
	}
	var tools int
	st.db.QueryRow(`SELECT COUNT(*) FROM run_supervisor_tool_calls WHERE run_id=?`, turn.Run.ID).Scan(&tools)
	if tools != 0 {
		t.Fatal("unwanted compaction tools dispatched")
	}
	response.Text = "different"
	if _, _, err := st.RecordSupervisorCompactionCompleted(ctx, updated, a, response); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("changed receipt replay %v", err)
	}
}

func TestSupervisorGeneratedCompactionInvalidSchemaFallsBackWithKnownUsage(t *testing.T) {
	st, turn, _ := newSupervisorCompactionTest(t)
	ctx := context.Background()
	result, updated, err := st.CompactSupervisorContextGenerated(ctx, turn.Checkpoint, 2, supervisorSummaryGeneratorFunc(func(_ context.Context, r contextmgr.SummaryGenerationRequest) (contextmgr.SummaryGenerationResponse, error) {
		_, _, _ = compactionTestComplete(t, st, turn.Checkpoint, r, `{"version":"generated_handoff.v1","summary":"wrong","extra":"invalid schema"}`)
		return contextmgr.SummaryGenerationResponse{}, errors.New("generation_invalid_response: extra keys")
	}))
	if err != nil || result.Generated || result.GenerationFallbackReason == "" || updated.TotalTokens != 42 || updated.RepairPhase != domain.ProtocolRepairNone {
		t.Fatalf("fallback %#v %#v %v", result, updated, err)
	}
}

func TestSupervisorGeneratedCompactionStructuredResponseRedactsLeaves(t *testing.T) {
	st, turn, _ := newSupervisorCompactionTest(t)
	ctx := context.Background()
	raw := `{"version":"generated_handoff.v1","summary":"api_key=fixture_private_key\nKeep the later correction and exact file."}`
	result, _, err := st.CompactSupervisorContextGenerated(ctx, turn.Checkpoint, 2, supervisorSummaryGeneratorFunc(func(_ context.Context, r contextmgr.SummaryGenerationRequest) (contextmgr.SummaryGenerationResponse, error) {
		response, _, _ := compactionTestComplete(t, st, turn.Checkpoint, r, raw)
		return response, nil
	}))
	if err != nil || !result.Generated || !strings.Contains(result.Summary.Content, "Keep the later correction") || strings.Contains(result.Summary.Content, "fixture_private_key") {
		t.Fatalf("redaction damaged candidate %#v %v", result, err)
	}
}
