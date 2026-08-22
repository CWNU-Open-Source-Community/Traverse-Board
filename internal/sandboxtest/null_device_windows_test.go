//go:build windows

package sandboxtest

import (
	"testing"

	"golang.org/x/sys/windows"
)

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
