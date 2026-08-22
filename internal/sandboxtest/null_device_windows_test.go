//go:build windows

package sandboxtest

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPrepareSystemDrivePathRestoresRootWithoutPropagating(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	pointer, err := windows.UTF16PtrFromString(root)
	if err != nil {
		t.Fatal(err)
	}
	busyHandle, err := windows.CreateFile(pointer, windows.GENERIC_READ,
		windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(busyHandle)
	directHandle, directErr := windows.CreateFile(pointer, windows.MAXIMUM_ALLOWED,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if directErr == nil {
		_ = windows.CloseHandle(directHandle)
		t.Fatal("MAXIMUM_ALLOWED unexpectedly bypassed the simulated sharing conflict")
	}
	if !errors.Is(directErr, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("simulate busy root: %v", directErr)
	}
	security := func(pathValue string) string {
		t.Helper()
		descriptor, err := windows.GetNamedSecurityInfo(pathValue, windows.SE_FILE_OBJECT,
			windows.DACL_SECURITY_INFORMATION)
		if err != nil || descriptor == nil {
			t.Fatalf("read DACL for %s: %v", filepath.Base(pathValue), err)
		}
		return descriptor.String()
	}
	rootBefore, childBefore := security(root), security(child)
	restore, changed, err := prepareSystemDrivePath(root)
	if err != nil {
		t.Fatal(err)
	}
	restored := false
	t.Cleanup(func() {
		if !restored {
			_ = restore()
		}
	})
	if !changed {
		t.Skip("temporary root already has both AppContainer metadata ACEs")
	}
	descriptor, err := windows.GetNamedSecurityInfo(root, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	allPackagesSID, restrictedPackagesSID, err := applicationPackageSIDs()
	if err != nil {
		t.Fatal(err)
	}
	if !systemDriveDACLReady(dacl, allPackagesSID, restrictedPackagesSID) {
		t.Fatal("temporary root did not receive both metadata ACEs")
	}
	if got := security(child); got != childBefore {
		t.Fatal("root-only metadata ACEs propagated to the child")
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	restored = true
	if got := security(root); got != rootBefore {
		t.Fatal("temporary root DACL was not restored exactly")
	}
	if got := security(child); got != childBefore {
		t.Fatal("child DACL changed during root restoration")
	}
}

func TestDACLGrantsRestrictedPackagesRequiresCompleteNullDeviceAccess(t *testing.T) {
	sid, err := windows.StringToSid("S-1-15-2-2")
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name string
		sddl string
		want bool
	}{
		{name: "partial", sddl: "D:(A;;GR;;;S-1-15-2-2)", want: false},
		{name: "split complete", sddl: "D:(A;;GR;;;S-1-15-2-2)(A;;GWGX;;;S-1-15-2-2)", want: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			descriptor, err := windows.SecurityDescriptorFromString(testCase.sddl)
			if err != nil {
				t.Fatal(err)
			}
			dacl, _, err := descriptor.DACL()
			if err != nil {
				t.Fatal(err)
			}
			if got := daclGrantsRestrictedPackages(dacl, sid); got != testCase.want {
				t.Fatalf("complete access=%t, want %t", got, testCase.want)
			}
		})
	}
}

func TestNullDeviceSecurityRequiresBothPackageSIDsAndLowLabel(t *testing.T) {
	allPackagesSID, restrictedPackagesSID, err := applicationPackageSIDs()
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name      string
		daclSDDL  string
		labelSDDL string
		want      bool
	}{
		{name: "restricted packages only",
			daclSDDL:  "D:(A;;GRGWGX;;;S-1-15-2-2)",
			labelSDDL: "S:(ML;;NW;;;LW)", want: false},
		{name: "missing low label",
			daclSDDL: "D:(A;;GRGWGX;;;AC)(A;;GRGWGX;;;S-1-15-2-2)", want: false},
		{name: "complete",
			daclSDDL:  "D:(A;;GRGWGX;;;AC)(A;;GRGWGX;;;S-1-15-2-2)",
			labelSDDL: "S:(ML;;NW;;;LW)", want: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			descriptor, err := windows.SecurityDescriptorFromString(testCase.daclSDDL)
			if err != nil {
				t.Fatal(err)
			}
			dacl, _, err := descriptor.DACL()
			if err != nil {
				t.Fatal(err)
			}
			var label *windows.ACL
			if testCase.labelSDDL != "" {
				labelDescriptor, err := windows.SecurityDescriptorFromString(testCase.labelSDDL)
				if err != nil {
					t.Fatal(err)
				}
				label, _, err = labelDescriptor.SACL()
				if err != nil {
					t.Fatal(err)
				}
			}
			security := nullDeviceSecurity{dacl: dacl, label: label}
			if got := nullDeviceSecurityReady(security, allPackagesSID,
				restrictedPackagesSID); got != testCase.want {
				t.Fatalf("security ready=%t, want %t", got, testCase.want)
			}
		})
	}
}

func TestSystemDriveDACLRequiresBothPackageMetadataACEs(t *testing.T) {
	allPackagesSID, restrictedPackagesSID, err := applicationPackageSIDs()
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name string
		sddl string
		want bool
	}{
		{name: "all packages only", sddl: "D:(A;;0x120088;;;AC)", want: false},
		{name: "both", sddl: "D:(A;;0x120088;;;AC)(A;;0x120088;;;S-1-15-2-2)",
			want: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			descriptor, err := windows.SecurityDescriptorFromString(testCase.sddl)
			if err != nil {
				t.Fatal(err)
			}
			dacl, _, err := descriptor.DACL()
			if err != nil {
				t.Fatal(err)
			}
			if got := systemDriveDACLReady(dacl, allPackagesSID,
				restrictedPackagesSID); got != testCase.want {
				t.Fatalf("system-drive DACL ready=%t, want %t", got, testCase.want)
			}
		})
	}
}
