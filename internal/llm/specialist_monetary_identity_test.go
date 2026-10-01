package llm

import "testing"

func TestSpecialistMonetaryIdentityBindsAttemptAndSequence(t *testing.T) {
	first := ModelAttempt{SpecialistAttemptID: "attempt-child-a-turn-1", Number: 1}
	seen := map[int64]bool{}
	for _, attempt := range []ModelAttempt{first,
		{SpecialistAttemptID: "attempt-child-b-turn-1", Number: 1},
		{SpecialistAttemptID: "attempt-child-a-turn-2", Number: 1},
		{SpecialistAttemptID: first.SpecialistAttemptID, Number: 2},
	} {
		key := attempt.MonetaryAttemptNumber()
		if key < 1<<61 || seen[key] || attempt.Number < 1 {
			t.Fatalf("different child/turn/request reused a monetary key: %+v key=%d", attempt, key)
		}
		seen[key] = true
	}
	replay := first
	replay.TransportAttempt, replay.ProtocolRepair = 2, 1
	if first.MonetaryAttemptNumber() != replay.MonetaryAttemptNumber() {
		t.Fatal("transport/repair metadata changed the same model request's identity")
	}
	for _, invalid := range []ModelAttempt{
		{SpecialistAttemptID: " bad-id", Number: 1},
		{SpecialistAttemptID: "attempt\nprivate", Number: 1},
		{SpecialistAttemptID: "attempt-1", Number: 0},
		{SpecialistAttemptID: "attempt-1", Number: 1, SupervisorAttemptID: "attempt-root"},
		{SpecialistAttemptID: "attempt-1", Number: 1, Purpose: ModelPurposeContextCompaction},
	} {
		if invalid.MonetaryAttemptNumber() != 0 {
			t.Fatalf("invalid mixed identity produced a key: %+v", invalid)
		}
	}
}
