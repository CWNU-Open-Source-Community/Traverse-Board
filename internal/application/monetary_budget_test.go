package application_test

import (
	"context"
	"path/filepath"
	"strings"
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

func TestGeneratedModelCostUsesSourceIdentityAndKeepsUnknownCharge(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "generated-cost.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := t.Context()
	_, run, err := application.NewRunService(st).Create(ctx, application.CreateRunRequest{
		Goal: "generated summary monetary identity", Profile: "code", ModelRoute: "usage-test/model",
		Budget: domain.Budget{MaxTurns: 3, MaxCostUSD: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunService(st).Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	importSupervisorPriceSnapshot(t, ctx, st)
	service := application.NewMonetaryBudgetService(st)
	request := llm.ChatRequest{MaxTokens: 32, Messages: []llm.Message{{Role: "user", Content: "history"}}}
	ordinary := llm.ModelAttempt{Number: 1, Provider: "usage-test", Model: "model"}
	first := ordinary
	first.Purpose, first.CompactionSourceSHA256 = llm.ModelPurposeContextCompaction, strings.Repeat("a", 64)
	second := first
	second.CompactionSourceSHA256 = strings.Repeat("b", 64)
	for _, attempt := range []llm.ModelAttempt{ordinary, first} {
		if _, err := service.ReserveModelCall(ctx, run, domain.MonetaryScopeRoot, attempt, request); err != nil {
			t.Fatal(err)
		}
		if _, err := service.SettleModelCall(ctx, run.ID, domain.MonetaryScopeRoot, attempt, llm.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}, 0); err != nil {
			t.Fatal(err)
		}
	}
	before, err := service.Usage(ctx, run.ID)
	if err != nil || before.SettledMicros != 16 {
		t.Fatalf("ordinary and auxiliary attempt 1 collided: %+v %v", before, err)
	}
	reserved, err := service.ReserveModelCall(ctx, run, domain.MonetaryScopeRoot, second, request)
	if err != nil {
		t.Fatal(err)
	}
	ceiling := reserved.ReservedMicros - before.ReservedMicros
	after, err := service.SettleUnknownModelCall(ctx, run.ID, domain.MonetaryScopeRoot, second)
	if err != nil || ceiling <= 0 || after.SettledMicros != before.SettledMicros+ceiling {
		t.Fatalf("unknown generation response was treated as free or reused an old source: before=%+v after=%+v ceiling=%d err=%v", before, after, ceiling, err)
	}
	replay, err := service.SettleUnknownModelCall(ctx, run.ID, domain.MonetaryScopeRoot, second)
	if err != nil || replay.SettledMicros != after.SettledMicros {
		t.Fatalf("settlement replay double-charged: %+v %v", replay, err)
	}
}

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
		// Reserve the complete tool schemas as well as the reply window. This
		// settlement fixture has room for that conservative prompt allowance;
		// actual usage remains 8 micro-USD, irrespective of catalog size.
		Budget: domain.Budget{MaxTurns: 3, MaxCostUSD: 0.1},
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
	// This fixture returns three output tokens. Reserve an explicit small
	// response window so the test measures ledger settlement, independent of
	// the production tool-argument output allowance and prompt length.
	window := llm.DefaultContextWindow()
	window.DefaultOutputTokens, window.MaxOutputTokens = 256, 512
	if err := router.SetContextWindow(llm.ModelRef{Provider: provider.Name(), Model: "model"}, window); err != nil {
		t.Fatal(err)
	}
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
	// open and the remaining headroom excludes the already spent 8 micro-USD.
	if !usage.Tracked || usage.CapMicros != 100000 ||
		usage.SettledMicros != 8 ||
		usage.ReservedMicros != usage.SettledMicros+usage.ReleasedMicros ||
		usage.RemainingMicros != 99992 {
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

func TestMonetaryReservationIncludesCompleteToolSchema(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "tool-price.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, run, err := application.NewRunService(st).Create(t.Context(), application.CreateRunRequest{
		Goal: "price the complete advertised tool", Profile: "code", ModelRoute: "usage-test/model",
		Budget: domain.Budget{MaxTurns: 3, MaxCostUSD: 0.001},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunService(st).Start(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	importSupervisorPriceSnapshot(t, t.Context(), st)
	_, err = application.NewMonetaryBudgetService(st).ReserveModelCall(t.Context(), run, domain.MonetaryScopeRoot,
		llm.ModelAttempt{Number: 1, Provider: "usage-test", Model: "model"},
		llm.ChatRequest{MaxTokens: 32, Messages: []llm.Message{{Role: "user", Content: "read the file"}},
			Tools: []llm.ToolSpec{{Name: "workspace_read", Description: strings.Repeat("bounded file reader schema documentation ", 100)}}})
	if apperror.CodeOf(err) != apperror.CodeResourceExhausted {
		t.Fatalf("complete tool schema did not constrain the monetary allowance: %v", err)
	}
}
