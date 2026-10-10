package sandbox

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func sbxNamespaceTestName(t *testing.T) string {
	t.Helper()
	var nonce [5]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	return "sbx-test-" + hex.EncodeToString(nonce[:])
}

func TestSBXNamespaceExcludesDifferentProcessesAndJournalRoots(t *testing.T) {
	name := sbxNamespaceTestName(t)
	firstJournal, secondJournal := t.TempDir(), t.TempDir()
	if firstJournal == secondJournal {
		t.Fatal("test requires different journals")
	}
	owner, err := sbxAcquireNamespaceNamedLock(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	// Environment path redirection and a different journal must not split the
	// namespace lock. The helper makes no daemon or sandbox calls.
	cmd := sbxNamespaceHelper(t, name, "contend")
	cmd.Env = append(cmd.Env, "SBX_NAMESPACE_TEST_JOURNAL="+secondJournal)
	for _, key := range []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "XDG_RUNTIME_DIR", "TMP", "TEMP"} {
		cmd.Env = sbxNamespaceSetEnv(cmd.Env, key, secondJournal)
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("different-root process acquired namespace: %v: %s", err, output)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := sbxAcquireNamespaceNamedLock(name)
	if err != nil {
		t.Fatalf("released namespace could not be acquired: %v", err)
	}
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSBXNamespaceExcludesInstancesAndClosesFromAnotherGoroutine(t *testing.T) {
	name := sbxNamespaceTestName(t)
	owner, err := sbxAcquireNamespaceNamedLock(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	if other, err := sbxAcquireNamespaceNamedLock(name); !errors.Is(err, ErrSBXOwnership) {
		_ = other.Close()
		t.Fatalf("second instance was not excluded: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- owner.Close() }()
	if err := <-done; err != nil {
		t.Fatalf("release from another goroutine failed: %v", err)
	}
	if err := owner.Close(); err != nil {
		t.Fatalf("duplicate close failed: %v", err)
	}
	next, err := sbxAcquireNamespaceNamedLock(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSBXNamespaceProcessExitReleasesLock(t *testing.T) {
	name := sbxNamespaceTestName(t)
	cmd := sbxNamespaceHelper(t, name, "exit-without-close")
	output, err := cmd.CombinedOutput()
	if err != nil || string(output) != "namespace-owned\n" {
		t.Fatalf("helper did not own namespace before exit: %v: %s", err, output)
	}
	next, err := sbxAcquireNamespaceNamedLock(name)
	if err != nil {
		t.Fatalf("process exit did not release namespace: %v", err)
	}
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSBXNamespaceRejectsInvalidNames(t *testing.T) {
	for _, name := range []string{"", "../other", "another/namespace", strings.Repeat("x", 21)} {
		if lock, err := sbxAcquireNamespaceNamedLock(name); !errors.Is(err, ErrSBXOwnership) {
			_ = lock.Close()
			t.Fatalf("invalid namespace accepted: %q: %v", name, err)
		}
	}
}

func TestSBXNamespaceBackendCannotSplitOwnershipAcrossJournals(t *testing.T) {
	first, fake, _ := sbxFixture(t)
	config := first.config
	journal, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config.JournalRoot = journal
	second, err := NewSBXBackend(config, WithSBXProcessTransport(fake), WithSBXDaemonTransport(fake))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	fake.mu.Lock()
	before := len(fake.calls)
	fake.mu.Unlock()
	if err := second.Prepare(t.Context()); !errors.Is(err, ErrSBXOwnership) {
		t.Fatalf("different-journal backend obtained namespace: %v", err)
	}
	fake.mu.Lock()
	after := len(fake.calls)
	fake.mu.Unlock()
	if after != before {
		t.Fatal("contending backend performed a daemon or helper operation")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.Prepare(t.Context()); err != nil {
		t.Fatalf("backend could not acquire namespace after owner closed: %v", err)
	}
}

func TestSBXNamespaceRecoverySkipsInactiveJournals(t *testing.T) {
	for _, phase := range []string{"empty", "unused", "removed"} {
		t.Run(phase, func(t *testing.T) {
			owner, fake, request := sbxFixture(t)
			idle := sbxNamespaceRecoveryBackend(t, owner.config, fake)
			if phase != "empty" {
				record := sbxNamespaceRecoveryRecord(request, phase)
				if err := idle.save(record); err != nil {
					t.Fatal(err)
				}
			}
			fake.mu.Lock()
			before := len(fake.calls)
			fake.mu.Unlock()
			if err := idle.RecoverStartup(t.Context()); err != nil {
				t.Fatalf("inactive journal conflicted with namespace owner: %v", err)
			}
			if idle.namespaceLock != nil {
				t.Fatal("inactive recovery acquired the namespace")
			}
			if err := idle.Close(); err != nil {
				t.Fatalf("inactive shutdown conflicted with namespace owner: %v", err)
			}
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if len(fake.calls) != before {
				t.Fatal("inactive recovery or shutdown reached daemon")
			}
		})
	}
}

func TestSBXNamespaceRecoveryValidatesMalformedJournalWithoutOwnership(t *testing.T) {
	owner, fake, _ := sbxFixture(t)
	idle := sbxNamespaceRecoveryBackend(t, owner.config, fake)
	path := filepath.Join(idle.config.JournalRoot, strings.Repeat("a", 64)+".json")
	if err := os.WriteFile(path, []byte(`{"unexpected":"field"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := idle.RecoverStartup(t.Context()); !errors.Is(err, ErrSBXOwnership) {
		t.Fatalf("malformed journal was not rejected: %v", err)
	}
	if idle.namespaceLock != nil {
		t.Fatal("malformed journal acquired the namespace")
	}
	if err := idle.Close(); !errors.Is(err, ErrSBXOwnership) {
		t.Fatalf("shutdown hid malformed journal: %v", err)
	}
}

func TestSBXNamespaceRecoveryRequiresOwnershipForPendingRecords(t *testing.T) {
	owner, fake, request := sbxFixture(t)
	idle := sbxNamespaceRecoveryBackend(t, owner.config, fake)
	record := sbxNamespaceRecoveryRecord(request, "created")
	if err := idle.save(record); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	before := len(fake.calls)
	fake.entries = []sbxInventoryEntry{{ID: record.ID, Name: record.Name,
		Workspaces: []string{record.Workspace, filepath.Join(record.Workspace, ".git") + ":ro"}}}
	fake.mu.Unlock()
	if err := idle.RecoverStartup(t.Context()); !errors.Is(err, ErrSBXOwnership) {
		t.Fatalf("pending recovery bypassed namespace ownership: %v", err)
	}
	fake.mu.Lock()
	after := len(fake.calls)
	fake.mu.Unlock()
	if after != before || idle.namespaceLock != nil {
		t.Fatal("contending pending recovery reached daemon or acquired namespace")
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := idle.RecoverStartup(t.Context()); err != nil {
		t.Fatalf("pending recovery failed after ownership became available: %v", err)
	}
	if idle.namespaceLock == nil || fake.count("rm") != 1 {
		t.Fatal("pending recovery did not retain ownership or remove its VM")
	}
	if err := idle.Close(); err != nil {
		t.Fatal(err)
	}
	if idle.namespaceLock != nil {
		t.Fatal("shutdown retained namespace ownership")
	}
}

func sbxNamespaceRecoveryBackend(t *testing.T, config SBXBackendConfig, fake *sbxFakeTransport) *SBXBackend {
	t.Helper()
	journal, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config.Enabled, config.JournalRoot = false, journal
	backend, err := NewSBXBackend(config, WithSBXProcessTransport(fake), WithSBXDaemonTransport(fake))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	return backend
}

func sbxNamespaceRecoveryRecord(request SBXRunRequest, phase string) sbxRecord {
	return sbxRecord{AppName: SBXAppName, Version: SBXPolicyVersion,
		OperationDigest: sbxDigest("namespace-recovery"), RequestFingerprint: request.RequestFingerprint,
		Name: "traverse-sbx-" + strings.Repeat("a", 32), ID: "00000000-0000-0000-0000-000000000001",
		Workspace: request.DrydockRoot, Phase: phase, Removed: phase == "removed"}
}

func sbxNamespaceHelper(t *testing.T, name, mode string) *exec.Cmd {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestSBXNamespaceProcessHelper$")
	cmd.Dir = filepath.Dir(executable)
	cmd.Env = append(os.Environ(), "SBX_NAMESPACE_LOCK_HELPER="+mode, "SBX_NAMESPACE_LOCK_NAME="+name)
	return cmd
}

func sbxNamespaceSetEnv(environment []string, key, value string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, binding := range environment {
		name, _, _ := strings.Cut(binding, "=")
		if !strings.EqualFold(name, key) {
			result = append(result, binding)
		}
	}
	return append(result, key+"="+value)
}

func TestSBXNamespaceProcessHelper(t *testing.T) {
	mode := os.Getenv("SBX_NAMESPACE_LOCK_HELPER")
	if mode == "" {
		return
	}
	lock, err := sbxAcquireNamespaceNamedLock(os.Getenv("SBX_NAMESPACE_LOCK_NAME"))
	if mode == "contend" {
		if !errors.Is(err, ErrSBXOwnership) {
			_ = lock.Close()
			t.Fatal("contending process acquired namespace")
		}
		return
	}
	if mode != "exit-without-close" || err != nil {
		_ = lock.Close()
		t.Fatalf("unexpected helper mode or acquire failure: %s: %v", mode, err)
	}
	_, _ = os.Stdout.WriteString("namespace-owned\n")
	// Deliberately bypass deferred Close to exercise OS crash/exit release.
	os.Exit(0)
}
