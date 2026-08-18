package contextmgr

import (
	"strings"
	"testing"
	"time"
)

func TestContinuitySnapshotIsDeterministicAndCarriesNoAuthority(t *testing.T) {
	now := time.Now().UTC()
	snapshot := ContinuitySnapshot{SourceRunID: "run-continuity",
		SourceSessionID: "session-continuity", WorkspaceID: "workspace-continuity",
		RecentMessages: []ContinuityMessage{{ID: 1, Role: "user",
			SourceKind: "operator_message", Content: "continue from the checkpoint",
			ContentSHA256:         memoryContentDigest("continue from the checkpoint"),
			InstructionAuthorized: true}},
		ThroughMessageID: 1, Memories: []ContinuityMemoryReference{{
			ID: "memory-one", Scope: MemoryScopeUser, ScopeID: LocalUserMemoryScope,
			Version: 1, ContentSHA256: strings.Repeat("a", 64),
		}},
		InheritedContext: []string{"message:1", "memory:memory-one"}, CreatedAt: now}
	sealed, err := SealContinuitySnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	second := snapshot
	second.CreatedAt = now.Add(time.Hour)
	secondSealed, err := SealContinuitySnapshot(second)
	if err != nil {
		t.Fatal(err)
	}
	if sealed.Fingerprint != secondSealed.Fingerprint {
		t.Fatal("continuity fingerprint unexpectedly depends on capture time")
	}
	forged := sealed
	forged.Authority.Network = true
	forged.Fingerprint = forged.continuityFingerprint()
	if err := forged.Validate(); err == nil {
		t.Fatal("continuity snapshot accepted inherited network authority")
	}
	forged = sealed
	forged.Memories[0].ScopeID = "other-user"
	forged.Fingerprint = forged.continuityFingerprint()
	if err := forged.Validate(); err == nil {
		t.Fatal("continuity snapshot accepted a user memory outside local-user scope")
	}
	forged = sealed
	forged.RecentMessages[0].SourceKind = "workspace_file"
	forged.Fingerprint = forged.continuityFingerprint()
	if err := forged.Validate(); err == nil {
		t.Fatal("continuity snapshot accepted forged instruction provenance")
	}
}

func TestContinuityNodeEnforcesBranchShape(t *testing.T) {
	snapshot, err := SealContinuitySnapshot(ContinuitySnapshot{
		SourceRunID: "run-source", SourceSessionID: "session-source",
		WorkspaceID: "workspace-source", RecentMessages: []ContinuityMessage{},
		Memories: []ContinuityMemoryReference{}, InheritedContext: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewContinuityNode("node-fork", ContinuityNodeFork, "session-new",
		"run-new", "workspace-source", "unexpected-parent", "node-source",
		"Fork", "", "desktop_operator", snapshot, time.Now().UTC()); err == nil {
		t.Fatal("fork accepted both a parent and a source")
	}
	if _, err := NewContinuityNode("node-checkpoint", ContinuityNodeCheckpoint,
		"session-source", "run-source", "workspace-source", "node-root", "",
		"Checkpoint", "", "desktop_operator", snapshot, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := NewContinuityNode("node-mismatched", ContinuityNodeCheckpoint,
		"session-other", "run-source", "workspace-source", "node-root", "",
		"Checkpoint", "", "desktop_operator", snapshot, time.Now().UTC()); err == nil {
		t.Fatal("checkpoint accepted a Session that did not match its snapshot")
	}
}
