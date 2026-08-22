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
