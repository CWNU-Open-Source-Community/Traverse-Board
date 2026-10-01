package store

import (
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/llm"
)

func newSpecialistMonetaryLedger(t *testing.T) (*SQLiteStore, domain.Run, domain.AgentAttemptRef, llm.ModelAttempt, string) {
	t.Helper()
	ctx := t.Context()
	st, run := newMonetaryTestStore(t)
	prices := monetaryTestSnapshot(t, time.Now().UTC())
	if _, _, err := st.ImportPriceSnapshot(ctx, prices); err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunService(st).Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	root, found, err := st.GetRootAgent(ctx, run.ID)
	if err != nil || !found {
		t.Fatalf("root %t %v", found, err)
	}
	child, _, err := st.AdmitSpecialist(ctx, domain.SpecialistAdmission{
		AgentID: idgen.New("agent"), SessionID: idgen.New("sess"), RunID: run.ID, ParentAgentID: root.ID,
		Title: "source bound monetary Specialist", Skills: []string{"model.chat"}, TurnLimit: 2, TokenLimit: 64,
		MaxChildren: 2, CreatedAt: time.Now().UTC(),
	}, "monetary-specialist-admit-0001")
	if err != nil {
		t.Fatal(err)
	}
	lease := acquireTestRunExecutionLease(t, ctx, st, run.ID)
	active, _, err := st.BeginSpecialistAttempt(ctx, domain.AgentAttemptStart{
		AttemptID: idgen.New("attempt"), RunID: run.ID, AgentID: child.ID, ParentAgentID: root.ID,
		Lease: lease, StartedAt: time.Now().UTC(),
	}, "monetary-specialist-start-0001")
	if err != nil {
		t.Fatal(err)
	}
	ref := attemptRef(active)
	a := llm.ModelAttempt{SpecialistAttemptID: ref.AttemptID, Number: 1, TransportAttempt: 1, MaxAttempts: 2, Provider: "mock", Model: "mock-code"}
	if _, err := st.RecordSpecialistModelStarted(ctx, ref, a); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.ReserveModelCost(ctx, domain.MonetaryReserveRequest{
		RunID: run.ID, Scope: domain.MonetaryScopeSpecialist, Provider: a.Provider, Model: a.Model,
		AttemptNumber: a.MonetaryAttemptNumber(), ReservedMicros: 1000, PriceFingerprint: prices.Fingerprint, EstimateSource: "specialist-test",
	}); err != nil {
		t.Fatal(err)
	}
	var seq int
	var name, path string
	if err := st.db.QueryRowContext(ctx, "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	return st, run, ref, a, path
}

func TestSpecialistUnknownReceiptReconcilesAfterReopenAndCancellation(t *testing.T) {
	st, run, ref, a, path := newSpecialistMonetaryLedger(t)
	ctx := t.Context()
	if _, _, err := st.ReleaseModelCost(ctx, domain.MonetaryReleaseRequest{
		RunID: run.ID, Scope: domain.MonetaryScopeSpecialist, AttemptNumber: a.MonetaryAttemptNumber(),
	}); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("sent request released before its terminal: %v", err)
	}
	a.Outcome, a.ErrorText, a.RetryPlanned = llm.OutcomeRetryable, "request failed", true
	if _, err := st.RecordSpecialistModelFailed(ctx, ref, a, nil); err != nil {
		t.Fatal(err)
	}
	// Close exactly after the durable failure and before application settlement.
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := application.NewRunService(reopened).Cancel(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.ReleaseOpenMonetaryReservations(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	money, err := reopened.GetMonetaryUsage(ctx, run.ID)
	if err != nil || money.ReservedMicros != 1000 || money.SettledMicros != 1000 || money.ReleasedMicros != 0 {
		t.Fatalf("restart or terminal cleanup made a sent unknown call free: %+v err=%v", money, err)
	}
}

func TestSpecialistNotDispatchedReceiptReleasesAndRejectsReplayDrift(t *testing.T) {
	st, run, ref, a, path := newSpecialistMonetaryLedger(t)
	ctx := t.Context()
	a.Outcome, a.ErrorText = llm.OutcomePermanent, "prepared request changed"
	if _, err := st.RecordSpecialistModelNotDispatched(ctx, ref, a); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordSpecialistModelNotDispatched(ctx, ref, a); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordSpecialistModelFailed(ctx, ref, a, &llm.Usage{}); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("not-sent proof became an ordinary response on replay: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	money, err := reopened.GetMonetaryUsage(ctx, run.ID)
	if err != nil || money.ReservedMicros != 1000 || money.SettledMicros != 0 || money.ReleasedMicros != 1000 {
		t.Fatalf("proven unsent call retained a charge after reopen: %+v %v", money, err)
	}
}

func TestSpecialistMonetaryEvidenceRejectsForeignReceipts(t *testing.T) {
	for _, variant := range []string{"source", "subject", "identity"} {
		t.Run(variant, func(t *testing.T) {
			st, run, ref, a, _ := newSpecialistMonetaryLedger(t)
			source, subject := "specialist_model_gateway", specialistModelSubject(ref.AttemptID, a.Number)
			attemptID := ref.AttemptID
			switch variant {
			case "source":
				source = "readonly_fanout"
			case "subject":
				subject = "another-child:model:1"
			case "identity":
				attemptID = "another-child"
			}
			event, err := events.New(run.ID, run.MissionID, events.ModelCompletedEvent, source, subject, map[string]any{
				"agent_attempt_id": attemptID, "model_attempt": a.Number, "monetary_attempt_number": a.MonetaryAttemptNumber(),
				"provider": a.Provider, "model": a.Model, "usage": llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
			})
			if err != nil {
				t.Fatal(err)
			}
			tx, err := st.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := insertRunEventTx(t.Context(), tx, event); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			money, err := st.GetMonetaryUsage(t.Context(), run.ID)
			if variant == "identity" {
				if apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
					t.Fatalf("conflicting source identity accepted: %+v %v", money, err)
				}
				return
			}
			if err != nil || money.SettledMicros != 0 || money.ReleasedMicros != 0 || money.ReservedMicros != 1000 {
				t.Fatalf("foreign receipt changed another child's exposure: %+v %v", money, err)
			}
		})
	}
}

func TestSpecialistMonetaryStartBindingCannotChangeOnReplay(t *testing.T) {
	st, _, ref, a, _ := newSpecialistMonetaryLedger(t)
	stripped := a
	stripped.SpecialistAttemptID = ""
	if _, err := st.RecordSpecialistModelStarted(t.Context(), ref, stripped); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("source-bound start lost its monetary identity on replay: %v", err)
	}
	stripped.Outcome, stripped.ErrorText = llm.OutcomePermanent, "failed"
	if _, err := st.RecordSpecialistModelFailed(t.Context(), ref, stripped, nil); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("terminal omitted its start's binding: %v", err)
	}
	bad := a
	bad.SpecialistAttemptID = "another-attempt"
	if _, err := st.RecordSpecialistModelStarted(t.Context(), ref, bad); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
		t.Fatalf("foreign attempt ID accepted: %v", err)
	}
}

func TestSpecialistNotDispatchedReceiptRejectsSentAndRetryEvidence(t *testing.T) {
	for _, variant := range []string{"stream_events", "stream_bytes", "retry_planned", "retry_after", "outcome", "legacy", "foreign_attempt"} {
		t.Run(variant, func(t *testing.T) {
			st, run, ref, a, _ := newSpecialistMonetaryLedger(t)
			a.Outcome, a.ErrorText = llm.OutcomePermanent, "prepared request changed"
			switch variant {
			case "stream_events":
				a.StreamEvents = 1
			case "stream_bytes":
				a.StreamBytes = 1
			case "retry_planned":
				a.RetryPlanned = true
			case "retry_after":
				a.RetryAfter = time.Second
			case "outcome":
				a.Outcome = llm.OutcomeRetryable
			case "legacy":
				a.SpecialistAttemptID = ""
			case "foreign_attempt":
				a.SpecialistAttemptID = "another-attempt"
			}
			if _, err := st.RecordSpecialistModelNotDispatched(t.Context(), ref, a); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
				t.Fatalf("unproven not-dispatched receipt accepted: %v", err)
			}
			money, err := st.GetMonetaryUsage(t.Context(), run.ID)
			if err != nil || money.ReservedMicros != 1000 || money.SettledMicros != 0 || money.ReleasedMicros != 0 {
				t.Fatalf("rejected proof changed the sent exposure: %+v %v", money, err)
			}
		})
	}
}

func TestLegacySpecialistReservationsRemainConservativeDuringCleanup(t *testing.T) {
	st, run := newMonetaryTestStore(t)
	ctx := t.Context()
	if _, _, err := st.ReserveModelCost(ctx, domain.MonetaryReserveRequest{
		RunID: run.ID, Scope: domain.MonetaryScopeSpecialist, Provider: "mock", Model: "mock-code",
		AttemptNumber: 1, ReservedMicros: 1000, PriceFingerprint: monetaryTestSnapshot(t, time.Now().UTC()).Fingerprint, EstimateSource: "legacy-specialist-test",
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.ReleaseModelCost(ctx, domain.MonetaryReleaseRequest{
		RunID: run.ID, Scope: domain.MonetaryScopeSpecialist, AttemptNumber: 1,
	}); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("ambiguous legacy exposure became free: %v", err)
	}
	if _, err := st.ReleaseOpenMonetaryReservations(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	money, err := st.GetMonetaryUsage(ctx, run.ID)
	if err != nil || money.ReservedMicros != 1000 || money.SettledMicros != 0 || money.ReleasedMicros != 0 {
		t.Fatalf("cleanup guessed a legacy child's monetary identity: %+v %v", money, err)
	}
}
