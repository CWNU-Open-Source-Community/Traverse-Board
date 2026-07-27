package executionauth

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
)

func TestTerminalInputLeaseIsExactScopedTimeBoundAndProcessLocal(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	broker := newTerminalInputBroker(func() time.Time { return now },
		bytes.NewReader(bytes.Repeat([]byte{0x42}, terminalLeaseTokenBytes)))
	scope := TerminalInputScope{
		WorkspaceID: "workspace-debug", RunID: "run-debug",
		TerminalSessionID:     "terminal-debug",
		InteractionSnapshotID: "interaction-debug",
		InteractionRevision:   2,
		Mode:                  domain.RunExecutionInteractionDebug,
	}
	issued, err := broker.Issue(IssueTerminalInputLeaseRequest{
		Scope: scope, RequestedBy: "desktop_operator",
		OperatorConfirmed: true, TTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if issued.Token == "" || issued.Lease.Revoked ||
		issued.Lease.ExpiresAt.Sub(issued.Lease.IssuedAt) != time.Minute {
		t.Fatalf("unexpected issued lease: %#v", issued)
	}
	authorized, err := broker.Authorize(issued.Token, scope)
	if err != nil || authorized.ID != issued.Lease.ID {
		t.Fatalf("exact scope was not authorized: lease=%#v err=%v", authorized, err)
	}
	wrong := scope
	wrong.TerminalSessionID = "terminal-other"
	if _, err := broker.Authorize(issued.Token, wrong); !errors.Is(err, ErrLeaseDenied) {
		t.Fatalf("cross-terminal token error=%v", err)
	}
	stale := scope
	stale.InteractionRevision++
	if _, err := broker.Authorize(issued.Token, stale); !errors.Is(err, ErrLeaseDenied) {
		t.Fatalf("stale interaction token error=%v", err)
	}
	now = now.Add(time.Minute)
	if _, err := broker.Authorize(issued.Token, scope); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("expired token error=%v", err)
	}

	restarted := NewTerminalInputBroker()
	if _, err := restarted.Authorize(issued.Token, scope); !errors.Is(err, ErrLeaseDenied) {
		t.Fatalf("process restart retained token: %v", err)
	}
}

func TestTerminalInputLeaseRejectsControlledModeAndSelfAuthorization(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	random := bytes.NewReader(bytes.Repeat([]byte{0x19}, terminalLeaseTokenBytes*2))
	broker := newTerminalInputBroker(func() time.Time { return now }, random)
	controlled := TerminalInputScope{
		WorkspaceID: "workspace-code", RunID: "run-code",
		TerminalSessionID:     "terminal-code",
		InteractionSnapshotID: "interaction-code",
		InteractionRevision:   2,
		Mode:                  domain.RunExecutionInteractionControlled,
	}
	if _, err := broker.Issue(IssueTerminalInputLeaseRequest{
		Scope: controlled, RequestedBy: "desktop_operator",
		OperatorConfirmed: true,
	}); !errors.Is(err, ErrLeaseBoundary) {
		t.Fatalf("controlled mode received persistent Agent input: %v", err)
	}
	debug := controlled
	debug.Mode = domain.RunExecutionInteractionDebug
	for _, request := range []IssueTerminalInputLeaseRequest{
		{Scope: debug, RequestedBy: "desktop_operator"},
		{Scope: debug, RequestedBy: "model", OperatorConfirmed: true},
	} {
		if _, err := broker.Issue(request); !errors.Is(err, ErrLeaseDenied) {
			t.Fatalf("self-authorizing request=%#v err=%v", request, err)
		}
	}
}

func TestTerminalInputLeaseRevocationFansOutByWorkspace(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	randomBytes := append(bytes.Repeat([]byte{0x73}, terminalLeaseTokenBytes),
		bytes.Repeat([]byte{0x74}, terminalLeaseTokenBytes)...)
	random := bytes.NewReader(randomBytes)
	broker := newTerminalInputBroker(func() time.Time { return now }, random)
	firstScope := TerminalInputScope{
		WorkspaceID: "workspace-one", RunID: "run-one",
		TerminalSessionID:     "terminal-one",
		InteractionSnapshotID: "interaction-one",
		InteractionRevision:   2,
		Mode:                  domain.RunExecutionInteractionDebug,
	}
	first, err := broker.Issue(IssueTerminalInputLeaseRequest{
		Scope: firstScope, RequestedBy: "desktop_operator",
		OperatorConfirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondScope := TerminalInputScope{
		WorkspaceID: "workspace-two", RunID: "run-two",
		TerminalSessionID:     "terminal-two",
		InteractionSnapshotID: "interaction-two",
		InteractionRevision:   3,
		Mode:                  domain.RunExecutionInteractionCyber,
	}
	second, err := broker.Issue(IssueTerminalInputLeaseRequest{
		Scope: secondScope, RequestedBy: "desktop_operator",
		OperatorConfirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if revoked := broker.RevokeWorkspace("workspace-one"); revoked != 1 {
		t.Fatalf("revoked=%d want=1", revoked)
	}
	if _, err := broker.Authorize(first.Token, firstScope); !errors.Is(err, ErrLeaseRevoked) {
		t.Fatalf("Workspace revoke did not revoke first token: %v", err)
	}
	if _, err := broker.Authorize(second.Token, secondScope); err != nil {
		t.Fatalf("Workspace revoke crossed scope: %v", err)
	}
	revoked, err := broker.Revoke(second.Lease.ID, "desktop_operator", true)
	if err != nil || !revoked.Revoked {
		t.Fatalf("explicit revoke lease=%#v err=%v", revoked, err)
	}
	if _, err := broker.Authorize(second.Token, secondScope); !errors.Is(err, ErrLeaseRevoked) {
		t.Fatalf("explicit revoke did not close token: %v", err)
	}
}

func TestTerminalInputLeaseRevocationReleasesActiveCapacity(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	randomBytes := make([]byte,
		(MaxActiveTerminalInputLeases+1)*terminalLeaseTokenBytes)
	for index := 0; index <= MaxActiveTerminalInputLeases; index++ {
		binary.LittleEndian.PutUint64(
			randomBytes[index*terminalLeaseTokenBytes:], uint64(index+1))
	}
	broker := newTerminalInputBroker(func() time.Time { return now },
		bytes.NewReader(randomBytes))
	for index := 0; index < MaxActiveTerminalInputLeases; index++ {
		_, err := broker.Issue(IssueTerminalInputLeaseRequest{
			Scope: TerminalInputScope{
				WorkspaceID: "workspace-capacity",
				RunID:       "run-capacity",
				TerminalSessionID: "terminal-" + string(rune('a'+index%26)) +
					"-" + time.Duration(index).String(),
				InteractionSnapshotID: "interaction-capacity",
				InteractionRevision:   2,
				Mode:                  domain.RunExecutionInteractionDebug,
			},
			RequestedBy: "desktop_operator", OperatorConfirmed: true,
		})
		if err != nil {
			t.Fatalf("issue lease %d: %v", index, err)
		}
	}
	if revoked := broker.RevokeAll(); revoked != MaxActiveTerminalInputLeases {
		t.Fatalf("revoked=%d want=%d", revoked, MaxActiveTerminalInputLeases)
	}
	if _, err := broker.Issue(IssueTerminalInputLeaseRequest{
		Scope: TerminalInputScope{
			WorkspaceID:           "workspace-capacity",
			RunID:                 "run-capacity",
			TerminalSessionID:     "terminal-after-revoke",
			InteractionSnapshotID: "interaction-capacity-next",
			InteractionRevision:   3,
			Mode:                  domain.RunExecutionInteractionDebug,
		},
		RequestedBy: "desktop_operator", OperatorConfirmed: true,
	}); err != nil {
		t.Fatalf("revoked leases retained active capacity: %v", err)
	}
	if len(broker.entries) != 1 ||
		len(broker.revoked) != MaxRevokedTerminalInputLeaseMarks {
		t.Fatalf("unexpected bounded broker sizes: active=%d revoked=%d",
			len(broker.entries), len(broker.revoked))
	}
}

func TestTerminalInputLeaseRevokesOnlyExactTerminal(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	random := bytes.NewReader(append(
		bytes.Repeat([]byte{0x75}, terminalLeaseTokenBytes),
		bytes.Repeat([]byte{0x76}, terminalLeaseTokenBytes)...))
	broker := newTerminalInputBroker(func() time.Time { return now }, random)
	firstScope := TerminalInputScope{
		WorkspaceID: "workspace-terminal", RunID: "run-terminal-one",
		TerminalSessionID:     "terminal-one",
		InteractionSnapshotID: "interaction-terminal-one",
		InteractionRevision:   2,
		Mode:                  domain.RunExecutionInteractionDebug,
	}
	first, err := broker.Issue(IssueTerminalInputLeaseRequest{
		Scope: firstScope, RequestedBy: "desktop_operator",
		OperatorConfirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondScope := firstScope
	secondScope.RunID = "run-terminal-two"
	secondScope.TerminalSessionID = "terminal-two"
	secondScope.InteractionSnapshotID = "interaction-terminal-two"
	second, err := broker.Issue(IssueTerminalInputLeaseRequest{
		Scope: secondScope, RequestedBy: "desktop_operator",
		OperatorConfirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if revoked := broker.RevokeTerminal(firstScope.TerminalSessionID); revoked != 1 {
		t.Fatalf("revoked=%d want=1", revoked)
	}
	if _, err := broker.Authorize(first.Token, firstScope); !errors.Is(err, ErrLeaseRevoked) {
		t.Fatalf("first terminal lease survived replacement: %v", err)
	}
	if _, err := broker.Authorize(second.Token, secondScope); err != nil {
		t.Fatalf("exact terminal revoke crossed scope: %v", err)
	}
}
