package domain

import (
	"math"
	"testing"
)

func TestTaskBudgetCanonicalDefaultsAndBounds(t *testing.T) {
	ptr := func(n int64) *int64 { return &n }
	turns := 100
	defaults, err := (*TaskBudgetSettings)(nil).Normalize()
	if err != nil || defaults != DefaultBudget() {
		t.Fatalf("default=%#v err=%v", defaults, err)
	}
	explicit, err := (&TaskBudgetSettings{MaxTurns: &turns, MaxToolCalls: ptr(100)}).Normalize()
	if err != nil || explicit != defaults {
		t.Fatalf("canonical default=%#v err=%v", explicit, err)
	}
	for _, settings := range []*TaskBudgetSettings{
		{MaxToolCalls: ptr(0)}, {MaxToolCalls: ptr(MaxTaskToolCalls + 1)},
		{MaxTokens: ptr(-1)}, {MaxTokens: ptr(MaxTaskTokens + 1)},
		{TimeoutSeconds: ptr(MaxTaskTimeoutSeconds + 1)},
	} {
		if _, err := settings.Normalize(); err == nil {
			t.Fatalf("accepted unsupported limit %#v", settings)
		}
	}
	for _, cost := range []float64{math.Inf(1), math.NaN(), -1, 0.0000001, MaxTaskCostUSD + 1} {
		if _, err := (&TaskBudgetSettings{MaxCostUSD: &cost}).Normalize(); err == nil {
			t.Fatalf("accepted cost %v", cost)
		}
	}
	cost := 1.2345671
	budget, err := (&TaskBudgetSettings{MaxCostUSD: &cost}).Normalize()
	if err != nil || budget.MaxCostUSD != 1.234567 {
		t.Fatalf("micro-USD canonicalization=%#v err=%v", budget, err)
	}
}

func TestControlledCreationFingerprintCanonicalBudget(t *testing.T) {
	base := func(b Budget) string {
		return ControlledCreationFingerprint("goal", "ws", "code", "code", "plan", "disabled", nil, "code", "operator", b)
	}
	defaultFingerprint := base(DefaultBudget())
	custom := DefaultBudget()
	custom.MaxTurns = 99
	if defaultFingerprint == base(custom) {
		t.Fatal("budget change did not alter idempotency fingerprint")
	}
	custom.MaxCostUSD = 1.123456
	if base(custom) != base(custom) {
		t.Fatal("fingerprint is not deterministic")
	}
}
