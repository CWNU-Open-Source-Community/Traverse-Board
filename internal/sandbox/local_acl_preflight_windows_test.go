//go:build windows

package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsLocalSecurityUnchangedRestoreNeedsNoWriteDAC(t *testing.T) {
	root := localTestReadOnlyDACLRoot(t)
	snapshot, err := captureLocalSecurity(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := restoreLocalSecurity(snapshot); err != nil {
		t.Fatalf("unchanged pinned security required an unnecessary ACL write: %v", err)
	}
	after, err := captureLocalSecurity(root)
	if err != nil || after != snapshot {
		t.Fatalf("no-op recovery changed security: after=%+v err=%v", after, err)
	}
}

func TestWindowsLocalToolchainPreflightRejectsBeforeOwnerOrACLMutation(t *testing.T) {
	toolchain := localTestReadOnlyDACLRoot(t)
	// This file is only a path fixture. Preflight must reject before any
	// process launch; neither this file nor an installed runtime is executed.
	if err := os.WriteFile(filepath.Join(toolchain.path, "not-started.exe"), []byte("not executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := windowsTestCanonicalRoot(t, windowsTestTempDir(t))
	pinned, err := pinLocalRoot(workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.close()
	original, err := captureLocalSecurity(pinned)
	if err != nil {
		t.Fatal(err)
	}
	ownerRoot := filepath.Join(windowsTestTempDir(t), "owners")
	backend, err := NewPlatformLocalBackend(WithLocalOwnerRoot(ownerRoot))
	if err != nil {
		t.Fatal(err)
	}
	request := localWindowsTestRequest(t, backend, workspace, toolchain.path,
		"/toolchain", "/toolchain/not-started.exe", []string{})
	result, runErr := backend.Run(context.Background(), request)
	closeErr := backend.Close()
	if !errors.Is(runErr, windows.ERROR_ACCESS_DENIED) || !strings.Contains(runErr.Error(), "preflight") ||
		!strings.Contains(runErr.Error(), "toolchain") || !strings.Contains(runErr.Error(), "WRITE_DAC") ||
		!strings.Contains(runErr.Error(), strconv.Quote(toolchain.path)) || closeErr != nil || !result.StartedAt.IsZero() {
		t.Fatalf("unmodifiable toolchain lacks a clean preflight rejection: run=%v close=%v result=%+v", runErr, closeErr, result)
	}
	after, err := captureLocalSecurity(pinned)
	if err != nil || after != original {
		t.Fatalf("toolchain preflight changed Workspace ACL: err=%v", err)
	}
	entries, err := os.ReadDir(ownerRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != localOwnerLockName {
			t.Fatalf("failed preflight retained a new owner or scratch: %s", entry.Name())
		}
	}
}

func TestWindowsLocalSecurityChangedFieldsStillRestore(t *testing.T) {
	for _, field := range []string{"dacl", "label", "protection"} {
		t.Run(field, func(t *testing.T) {
			root, err := pinLocalRoot(windowsTestCanonicalRoot(t, windowsTestTempDir(t)))
			if err != nil {
				t.Fatal(err)
			}
			defer root.close()
			before, err := captureLocalSecurity(root)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := restoreLocalSecurity(before); err != nil {
					t.Errorf("restore fixture security: %v", err)
				}
			}()
			switch field {
			case "dacl":
				sid, err := windows.CreateWellKnownSid(windows.WinBuiltinAnyPackageSid)
				if err != nil {
					t.Fatal(err)
				}
				if err := grantLocalRoot(before, sid, false, false); err != nil {
					t.Fatal(err)
				}
			case "label":
				sd, err := windows.SecurityDescriptorFromString("S:(ML;OICI;NW;;;LW)")
				if err != nil {
					t.Fatal(err)
				}
				label, _, err := sd.SACL()
				if err != nil {
					t.Fatal(err)
				}
				if err := windows.SetNamedSecurityInfo(root.path, windows.SE_FILE_OBJECT,
					windows.LABEL_SECURITY_INFORMATION, nil, nil, nil, label); err != nil {
					t.Fatal(err)
				}
			case "protection":
				sd, err := windows.SecurityDescriptorFromString(before.DACLSDDL)
				if err != nil {
					t.Fatal(err)
				}
				acl, _, err := sd.DACL()
				if err != nil {
					t.Fatal(err)
				}
				info := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
				if before.DACLProtected {
					info = windows.DACL_SECURITY_INFORMATION | windows.UNPROTECTED_DACL_SECURITY_INFORMATION
				}
				if err := windows.SetNamedSecurityInfo(root.path, windows.SE_FILE_OBJECT, info, nil, nil, acl, nil); err != nil {
					t.Fatal(err)
				}
			}
			changed, err := captureLocalSecurity(root)
			if err != nil || changed == before {
				t.Fatalf("fixture did not change %s: %v", field, err)
			}
			if err := restoreLocalSecurity(before); err != nil {
				t.Fatal(err)
			}
			after, err := captureLocalSecurity(root)
			if err != nil || after != before {
				t.Fatalf("changed %s was skipped instead of restored: before=%+v after=%+v err=%v", field, before, after, err)
			}
		})
	}
}

// Deny newly opened WRITE_DAC access on a temporary directory, while retaining
// a pre-authorized handle solely to restore this fixture's original DACL.
func localTestReadOnlyDACLRoot(t *testing.T) localPinnedRoot {
	t.Helper()
	root, err := pinLocalRoot(windowsTestCanonicalRoot(t, windowsTestTempDir(t)))
	if err != nil {
		t.Fatal(err)
	}
	before, err := captureLocalSecurity(root)
	if err != nil {
		root.close()
		t.Fatal(err)
	}
	pointer, err := windows.UTF16PtrFromString(root.path)
	if err != nil {
		root.close()
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(pointer, windows.WRITE_DAC|windows.READ_CONTROL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		root.close()
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString(before.DACLSDDL)
	if err != nil {
		t.Fatal(err)
	}
	original, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		info := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.UNPROTECTED_DACL_SECURITY_INFORMATION)
		if before.DACLProtected {
			info = windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION
		}
		if err := windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, info, nil, nil, original, nil); err != nil {
			t.Errorf("restore held-handle fixture DACL: %v", err)
		}
		_ = windows.CloseHandle(handle)
		root.close()
	})
	sid, err := windows.StringToSid("S-1-3-4") // OWNER RIGHTS, not a privilege grant.
	if err != nil {
		t.Fatal(err)
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{AccessPermissions: windows.WRITE_DAC,
		AccessMode: windows.DENY_ACCESS, Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID,
			TrusteeType: windows.TRUSTEE_IS_USER, TrusteeValue: windows.TrusteeValueFromSID(sid)}}}, original)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	probe, err := windows.CreateFile(pointer, windows.WRITE_DAC,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if probe != windows.InvalidHandle {
		_ = windows.CloseHandle(probe)
	}
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("fixture does not actually deny WRITE_DAC: %v", err)
	}
	return root
}
