package llm

import "testing"

func TestSupervisorMonetaryAttemptBindsTurnAndModelCall(t *testing.T) {
	a := ModelAttempt{SupervisorAttemptID: "attempt-one", Number: 1, MaxAttempts: 3, Provider: "fixture", Model: "model"}
	if err := a.ValidateStarted(); err != nil {
		t.Fatal(err)
	}
	key := a.MonetaryAttemptNumber()
	if key < 1<<61 || key >= 1<<62 {
		t.Fatal("normal identity overlaps legacy or compaction range")
	}
	for _, change := range []func(*ModelAttempt){func(v *ModelAttempt) { v.SupervisorAttemptID = "attempt-two" }, func(v *ModelAttempt) { v.Number = 2 }} {
		b := a
		change(&b)
		if b.MonetaryAttemptNumber() == key {
			t.Fatal("distinct invocation reused reservation")
		}
	}
	if a.MonetaryAttemptNumber() != key {
		t.Fatal("replay identity changed")
	}
	a.SupervisorAttemptID = ""
	if a.MonetaryAttemptNumber() != 1 {
		t.Fatal("legacy reservation identity changed")
	}
	for _, id := range []string{" space", "line\ninput", "null\x00input"} {
		a.SupervisorAttemptID = id
		if a.ValidateStarted() == nil {
			t.Fatal("invalid identity accepted")
		}
	}
}
