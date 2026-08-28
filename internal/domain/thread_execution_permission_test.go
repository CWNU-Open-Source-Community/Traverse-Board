package domain

import (
	"testing"
	"time"
)

func TestThreadExecutionPermissionIsConservativeAndNonAuthorizingByDefault(t *testing.T) {
	at := time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)
	threadRecord := Thread{ID: "thread-permission-domain", MissionID: "mission-domain",
		ProtocolVersion: ThreadProtocolVersion, Title: "permission domain",
		Status: ThreadActive, ActiveRunID: "run-domain", LastRunID: "run-domain",
		Version: 1, CreatedAt: at, UpdatedAt: at}
	initial, err := NewInitialThreadExecutionPermissionSnapshot(
		"thread-permission-snapshot", threadRecord, "operator", at)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Mode != RunExecutionPermissionConservative || initial.Revision != 1 ||
		initial.OperatorConfirmed || initial.ProcessEnabled ||
		initial.ExecutionAuthorized || initial.CapabilityGrant {
		t.Fatalf("initial Thread permission widened authority: %+v", initial)
	}
	next, err := initial.Next("thread-permission-snapshot-2",
		RunExecutionPermissionWorkspaceAccess, true, "operator",
		"select bounded Workspace Access", at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if next.Mode != RunExecutionPermissionWorkspaceAccess || next.Revision != 2 ||
		!next.OperatorConfirmed || next.ProcessEnabled || next.ExecutionAuthorized ||
		next.CapabilityGrant {
		t.Fatalf("next Thread permission widened authority: %+v", next)
	}
	next.ExecutionAuthorized = true
	if err := next.Validate(); err == nil {
		t.Fatal("Thread permission accepted durable runtime authority")
	}
}

func TestThreadExecutionPermissionRequiresModeExactConfirmation(t *testing.T) {
	at := time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)
	threadRecord := Thread{ID: "thread-permission-confirm", MissionID: "mission-confirm",
		ProtocolVersion: ThreadProtocolVersion, Title: "permission confirmation",
		Status: ThreadActive, ActiveRunID: "run-confirm", LastRunID: "run-confirm",
		Version: 1, CreatedAt: at, UpdatedAt: at}
	initial, err := NewInitialThreadExecutionPermissionSnapshot(
		"thread-permission-confirm-1", threadRecord, "operator", at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := initial.Next("thread-permission-confirm-2",
		RunExecutionPermissionFullAccess, false, "operator",
		"missing danger confirmation", at.Add(time.Second)); err == nil {
		t.Fatal("full access Thread preference accepted missing confirmation")
	}
}
