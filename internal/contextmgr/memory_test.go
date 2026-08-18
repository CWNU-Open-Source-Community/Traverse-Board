package contextmgr

import (
	"strings"
	"testing"
	"time"
)

func TestMemoryRequiresExplicitOperatorAndSupportsLifecycle(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	request := CreateMemoryRequest{
		ID: "memory-one", Scope: MemoryScopeUser, ScopeID: LocalUserMemoryScope,
		Title: "Preferred validation", Content: "Run focused tests before the full suite.",
		SourceKind: "operator_explicit", SourceRef: "issue-106",
		References:  []string{"docs/usage.md", "README.md", "docs/usage.md"},
		RequestedBy: "desktop_operator", ExplicitOperator: true,
	}
	memory, err := PrepareMemory(request, now)
	if err != nil {
		t.Fatal(err)
	}
	if memory.Status != MemoryStatusActive || memory.Version != 1 ||
		memory.ContentSHA256 == "" || len(memory.References) != 2 ||
		memory.References[0] != "README.md" {
		t.Fatalf("unexpected memory: %#v", memory)
	}
	disabled := MemoryStatusDisabled
	newContent := "Prefer focused tests, then run all affected packages."
	retention := now.Add(24 * time.Hour)
	retentionPtr := &retention
	updated, err := UpdateMemory(memory, UpdateMemoryRequest{
		Content: &newContent, Status: &disabled, RetentionUntil: &retentionPtr,
		RequestedBy: "desktop_operator", ExpectedVersion: 1,
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != MemoryStatusDisabled || updated.Version != 2 ||
		updated.Content == memory.Content || updated.RetentionUntil == nil {
		t.Fatalf("memory lifecycle update failed: %#v", updated)
	}
}

func TestMemoryRejectsImplicitSensitiveAndAgentWrites(t *testing.T) {
	base := CreateMemoryRequest{
		ID: "memory-sensitive", Scope: MemoryScopeProject, ScopeID: "workspace-one",
		Title: "Credential", Content: "token=ghp_abcdefghijklmnopqrstuvwxyz1234567890",
		RequestedBy: "desktop_operator", ExplicitOperator: true,
	}
	if _, err := PrepareMemory(base, time.Now()); err == nil {
		t.Fatal("sensitive memory was accepted without explicit redaction")
	}
	base.RedactSensitive = true
	memory, err := PrepareMemory(base, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !memory.Redacted || strings.Contains(memory.Content, "ghp_") {
		t.Fatalf("sensitive memory was not redacted: %#v", memory)
	}
	base.ExplicitOperator = false
	if _, err := PrepareMemory(base, time.Now()); err == nil {
		t.Fatal("implicit memory write was accepted")
	}
	base.ExplicitOperator = true
	base.RequestedBy = "model"
	if _, err := PrepareMemory(base, time.Now()); err == nil {
		t.Fatal("model-authored memory was accepted")
	}
	base.RequestedBy = "desktop_operator"
	base.SourceRef = ".env"
	if _, err := PrepareMemory(base, time.Now()); err == nil {
		t.Fatal("sensitive source path was accepted")
	}
}

func TestMemoryRetentionExpiresWithoutBecomingAuthority(t *testing.T) {
	now := time.Now().UTC()
	retention := now.Add(time.Hour)
	memory, err := PrepareMemory(CreateMemoryRequest{
		ID: "memory-retention", Scope: MemoryScopeUser, ScopeID: LocalUserMemoryScope,
		Title: "Temporary preference", Content: "Use compact output.",
		RetentionUntil: &retention, RequestedBy: "cli_operator", ExplicitOperator: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if memory.Expired(now) || !memory.Expired(retention) {
		t.Fatalf("unexpected expiration behavior: %#v", memory)
	}
}
