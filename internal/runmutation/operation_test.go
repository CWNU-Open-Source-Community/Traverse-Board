package runmutation

import (
	"strings"
	"testing"
	"time"
)

func TestOperationFingerprintIsDomainSeparatedAndStable(t *testing.T) {
	first := OperationKeyDigest("note_create", "run-1", "call-1")
	if first != OperationKeyDigest("note_create", "run-1", "call-1") || len(first) != 64 {
		t.Fatalf("operation digest is not stable: %q", first)
	}
	for _, changed := range []string{
		OperationKeyDigest("work_item_create", "run-1", "call-1"),
		OperationKeyDigest("note_create", "run-2", "call-1"),
		OperationKeyDigest("note_create", "run-1", "call-2"),
	} {
		if changed == first {
			t.Fatal("operation digest ignored a domain component")
		}
	}
}

func TestDurableOperationPilotHelpersPreserveReleasedDigests(t *testing.T) {
	if got := RunCreationOperationDigest("run-create-operation-0001"); got != "9b2ff570e61ef169aa472e8efa7681b161e11a03c72890feb35321d07f74e82d" {
		t.Fatalf("Run creation operation digest=%s", got)
	}
	if got := RunCreationRequestFingerprint("Implement the parser",
		"workspace-controlled-create", "code", "code", "plan", "http_control"); got != "dd5310aa4cfa7866278471cebd0f680e0586d01760e70932e46fb47e88fcef4e" {
		t.Fatalf("Run creation request fingerprint=%s", got)
	}
	if got := ScheduledJobOperationDigest("first", "second"); got != "f27b338c6ab1740eb1a700b3118cfe81b70496ad731cb63e653f6fd5aa746e50" {
		t.Fatalf("scheduled job operation digest=%s", got)
	}
	if got := ScheduledJobCreateRequestFingerprint("run-東京",
		`{"message":"修复","empty":""}`, "operator-é", false); got != "89c086217f03a7597266d0c7d018397fc221f2b35a9ca39821e323e60feeffb2" {
		t.Fatalf("scheduled job request fingerprint=%s", got)
	}
}

func TestOperationValidation(t *testing.T) {
	operation := Operation{
		KeyDigest: Fingerprint("key"), RequestFingerprint: Fingerprint("request"),
		InvocationID: "toolcall-1", RunID: "run-1", SessionID: "sess-1", WorkspaceID: "ws-1",
		ToolName: "note_create", TargetKind: TargetNote, TargetID: "note-1", RequestedBy: "root",
		CreatedAt: time.Now().UTC(),
	}
	if err := operation.Validate(); err != nil {
		t.Fatal(err)
	}
	operation.KeyDigest = strings.Repeat("z", 64)
	if err := operation.Validate(); err == nil {
		t.Fatal("expected malformed digest rejection")
	}
}

func TestSupervisorToolIdentityIsDeterministicAndTurnScoped(t *testing.T) {
	first := SupervisorToolOperationKey("run-1", 2, "note_create", `{"content":"x"}`)
	replayed := SupervisorToolOperationKey("run-1", 2, "note_create", `{"content":"x"}`)
	otherTurn := SupervisorToolOperationKey("run-1", 3, "note_create", `{"content":"x"}`)
	callID, err := SupervisorToolCallID(first, 1)
	if err != nil {
		t.Fatal(err)
	}
	if first != replayed || first == otherTurn || len(callID) != len("toolu_")+24 {
		t.Fatalf("unexpected supervisor tool identity: first=%s replayed=%s other=%s call=%s",
			first, replayed, otherTurn, callID)
	}
	secondRoundID, err := SupervisorToolCallID(first, 2)
	if err != nil || secondRoundID == callID {
		t.Fatalf("supervisor tool call id did not separate rounds: first=%s second=%s err=%v",
			callID, secondRoundID, err)
	}
	if _, err := SupervisorToolCallID("raw-provider-id", 1); err == nil {
		t.Fatal("non-digest supervisor tool operation key was accepted")
	}
}
