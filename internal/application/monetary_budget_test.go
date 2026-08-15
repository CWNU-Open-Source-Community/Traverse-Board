package application_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/pricing"
	"cyberagent-workbench/internal/store"
)

func importSupervisorPriceSnapshot(t *testing.T, ctx context.Context,
	st *store.SQLiteStore,
) pricing.Snapshot {
	t.Helper()
	now := time.Now().UTC()
	snapshot := pricing.Snapshot{
		ProtocolVersion: pricing.ProtocolVersion, ID: "supervisor-test-price-table",
		Source: pricing.SourceOperatorImport, Currency: pricing.CurrencyUSD,
		ImportedBy: "application_test", ImportedAt: now,
		ValidFrom: now.Add(-time.Minute), ValidUntil: now.Add(time.Hour),
		Entries: []pricing.Entry{{
			Provider: "usage-test", Model: "model",
			InputPerMillionMicros: 1000000, OutputPerMillionMicros: 2000000,
		}},
	}
	snapshot.Fingerprint = pricing.Fingerprint(snapshot)
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("price snapshot fixture is invalid: %v", err)
	}
	stored, replayed, err := st.ImportPriceSnapshot(ctx, snapshot)
	if err != nil || replayed || stored.ID != snapshot.ID {
		t.Fatalf("price snapshot import failed: stored=%#v replayed=%t err=%v",
			stored, replayed, err)
	}
	return stored
}

func TestRunSupervisorReservesAndSettlesMonetaryBudget(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "cyberagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	service := application.NewRunService(st)
	_, run, err := service.Create(ctx, application.CreateRunRequest{
		Goal: "monetary budget", Profile: "code", ModelRoute: "usage-test/model",
		Budget: domain.Budget{MaxTurns: 3, MaxCostUSD: 0.01},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	importSupervisorPriceSnapshot(t, ctx, st)
	provider := &fixedUsageProvider{}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	supervisor := application.NewRunSupervisor(st, router,
		policy.NewDefaultChecker()).WithMonetaryBudget(
		application.NewMonetaryBudgetService(st))
	result, err := supervisor.Step(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Checkpoint.InputTokens != 2 || result.Checkpoint.OutputTokens != 3 {
		t.Fatalf("unexpected model usage: %#v", result.Checkpoint)
	}
	usage, err := st.GetMonetaryUsage(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 2 input tokens at 1.00 USD/M and 3 output tokens at 2.00 USD/M settle to
	// 8 micro-USD. The unused reserve portion is released, so no exposure stays
	// open and the remaining headroom returns to the full cap.
	if !usage.Tracked || usage.CapMicros != 10000 ||
		usage.SettledMicros != 8 ||
		usage.ReservedMicros != usage.SettledMicros+usage.ReleasedMicros ||
		usage.RemainingMicros != 10000 {
		t.Fatalf("monetary ledger did not settle the root call: %#v", usage)
	}
}

func TestRunSupervisorMonetaryBudgetFailsClosedWithoutPriceEntry(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "cyberagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	service := application.NewRunService(st)
	_, run, err := service.Create(ctx, application.CreateRunRequest{
		Goal: "monetary fail closed", Profile: "code", ModelRoute: "usage-test/model",
		Budget: domain.Budget{MaxTurns: 3, MaxCostUSD: 0.01},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	provider := &fixedUsageProvider{}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	supervisor := application.NewRunSupervisor(st, router,
		policy.NewDefaultChecker()).WithMonetaryBudget(
		application.NewMonetaryBudgetService(st))
	_, err = supervisor.Step(ctx, run.ID)
	if apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("budgeted run without a price snapshot did not fail closed: code=%s err=%v",
			apperror.CodeOf(err), err)
	}
}

