//go:build windows

package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func testLocalScratchOwner(t *testing.T) (*windowsLocalBackend, localOwnerRecord, string) {
	t.Helper()
	base := windowsTestTempDir(t)
	workspace := filepath.Join(base, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	backend, err := NewPlatformLocalBackend(WithLocalOwnerRoot(filepath.Join(base, "owners")))
	if err != nil {
		t.Fatal(err)
	}
	b := backend.(*windowsLocalBackend)
	if b.initErr != nil {
		t.Fatal(b.initErr)
	}
	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Error(err)
		}
	})
	root, err := pinLocalRoot(workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer root.close()
	snapshot, err := captureLocalSecurity(root)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := prepareLocalProfileWithInstrumentation(t.Name(), true)
	if err != nil {
		t.Fatal(err)
	}
	owner := localOwnerRecord{ProtocolVersion: localOwnerProtocolVersion, PolicyVersion: LocalBackendPolicyVersion,
		Instrumentation: true, OwnerID: localFingerprint(t.Name(), profile.name), BindingFingerprint: localFingerprint("scratch-test-binding"),
		ProfileName: profile.name, ProfileSID: profile.sid.String(), Snapshots: []localSecuritySnapshot{snapshot}, CreatedAt: time.Now().UTC()}
	scratch, err := b.prepareScratch(owner.OwnerID, localPreparedRun{drydock: root})
	if err != nil {
		t.Fatal(err)
	}
	defer scratch.close()
	owner.Scratch = &localScratchIdentity{PathSHA256: localHostPathDigest(scratch.path), RootIdentity: scratch.identity}
	owner.seal()
	if err := b.writeOwnerLocked(owner); err != nil {
		t.Fatal(err)
	}
	if err := prepareLocalScratchDirectories(scratch.path, profile.name); err != nil {
		t.Fatal(err)
	}
	scratchSnapshot, err := captureLocalSecurity(scratch)
	if err != nil {
		t.Fatal(err)
	}
	if err := materializeLocalProfile(profile); err != nil {
		t.Fatal(err)
	}
	if err := grantLocalRoot(snapshot, profile.filesystemCapabilitySID, true, true); err != nil {
		t.Fatal(err)
	}
	if err := grantLocalRoot(scratchSnapshot, profile.filesystemCapabilitySID, true, true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scratch.path, "home", "cache.bin"), []byte("runtime cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	return b, owner, scratch.path
}

func TestWindowsLocalSandboxScratchOwnerRecovery(t *testing.T) {
	for _, alreadyRemoved := range []bool{false, true} {
		name := "runtime-state-present"
		if alreadyRemoved {
			name = "removed-before-journal-commit"
		}
		t.Run(name, func(t *testing.T) {
			first, owner, scratch := testLocalScratchOwner(t)
			if alreadyRemoved {
				if err := first.removeScratch(owner); err != nil {
					t.Fatal(err)
				}
			}
			// A process crash releases the exclusive handle; persisted PIDs are not
			// consulted. This test models recovery after the Job has already ended.
			if err := windows.CloseHandle(first.lock); err != nil {
				t.Fatal(err)
			}
			first.lock, first.closed = 0, true
			backend, err := NewPlatformLocalBackend(WithLocalOwnerRoot(first.ownerRoot))
			if err != nil {
				t.Fatal(err)
			}
			second := backend.(*windowsLocalBackend)
			t.Cleanup(func() {
				if err := second.Close(); err != nil {
					t.Error(err)
				}
			})
			if second.initErr != nil {
				t.Fatal(second.initErr)
			}
			if _, err := os.Lstat(scratch); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("scratch survived recovery: %v", err)
			}
			entries, err := os.ReadDir(second.ownerRoot)
			if err != nil || len(entries) != 1 || entries[0].Name() != localOwnerLockName {
				t.Fatalf("unrecovered entries: %v err=%v", entries, err)
			}
			root, err := pinLocalRoot(owner.Snapshots[0].Path)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := captureLocalSecurity(root)
			root.close()
			if err != nil || !windowsTestSameDACL(actual.DACLSDDL, owner.Snapshots[0].DACLSDDL) || actual.LabelSDDL != owner.Snapshots[0].LabelSDDL {
				t.Fatalf("workspace ACL was not restored: %v", err)
			}
		})
	}
}

func TestWindowsLocalSandboxScratchRejectsReplacementAndLegacyDeleteAuthority(t *testing.T) {
	b, owner, scratch := testLocalScratchOwner(t)
	backup := filepath.Join(filepath.Dir(b.ownerRoot), "original-runtime")
	if err := os.Rename(scratch, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(scratch, "foreign.txt")
	if err := os.WriteFile(sentinel, []byte("do not remove"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.removeScratch(owner); !errors.Is(err, ErrLocalSandboxBoundary) {
		t.Fatalf("replacement identity accepted: %v", err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "do not remove" {
		t.Fatal("replacement content was touched")
	}
	if err := os.Remove(sentinel); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(scratch); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, scratch); err != nil {
		t.Fatal(err)
	}
	legacy := owner
	legacy.ProtocolVersion, legacy.PolicyVersion = localPreviousOwnerProtocolVersion, localPreviousPolicyVersion
	legacy.seal()
	if legacy.validate() == nil {
		t.Fatal("old owner acquired scratch deletion authority")
	}
	if err := b.cleanupOwnerLocked(owner); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsLocalSandboxUnjournaledScratchOnlyRemovesEmptyAllocation(t *testing.T) {
	base := windowsTestTempDir(t)
	backend, err := NewPlatformLocalBackend(WithLocalOwnerRoot(filepath.Join(base, "owners")))
	if err != nil {
		t.Fatal(err)
	}
	b := backend.(*windowsLocalBackend)
	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Error(err)
		}
	})
	path, err := b.scratchPath(localFingerprint(t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(path, "unowned.txt")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.recoverEmptyScratch(filepath.Base(path)); err == nil {
		t.Fatal("unjournaled content was recursively removed")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(sentinel); err != nil {
		t.Fatal(err)
	}
	if err := b.recoverEmptyScratch(filepath.Base(path)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("same-name ordinary file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.recoverEmptyScratch(filepath.Base(path)); !errors.Is(err, ErrLocalSandboxBoundary) {
		t.Fatalf("ordinary file accepted as empty allocation: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "same-name ordinary file" {
		t.Fatal("same-name file was removed")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}
