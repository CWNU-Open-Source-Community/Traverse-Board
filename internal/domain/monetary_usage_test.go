package domain

import (
	"math"
	"testing"
)

func TestMonetaryUsageCountsSpentAndOpenPreservesHistoricalOverspend(t *testing.T) {
	u := MonetaryUsage{RunID: "run", Currency: "USD", CapMicros: 100, ReservedMicros: 90, SettledMicros: 40, ReleasedMicros: 10, RemainingMicros: 20, Tracked: true}
	if err := u.Validate(); err != nil {
		t.Fatal(err)
	}
	u.RemainingMicros = 60
	if u.Validate() == nil {
		t.Fatal("spent money became available again")
	}
	u.ReservedMicros = 200
	u.SettledMicros = 150
	u.ReleasedMicros = 50
	u.RemainingMicros = 0
	if err := u.Validate(); err != nil {
		t.Fatalf("historical over-cap facts became unreadable: %v", err)
	}
	u.ReservedMicros = math.MaxInt64
	u.SettledMicros = math.MaxInt64
	u.ReleasedMicros = 1
	if u.Validate() == nil {
		t.Fatal("aggregate addition overflow bypassed validation")
	}
}
