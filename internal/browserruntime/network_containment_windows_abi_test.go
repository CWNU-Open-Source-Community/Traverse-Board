//go:build windows

package browserruntime

import (
	"testing"
	"unsafe"
)

func TestWindowsWFPStructABI(t *testing.T) {
	checks := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"FWP_BYTE_BLOB size", unsafe.Sizeof(fwpByteBlob{}), 16},
		{"FWP_VALUE0 size", unsafe.Sizeof(fwpValue0{}), 16},
		{"FWPM_SESSION0 size", unsafe.Sizeof(fwpmSession0{}), 72},
		{"FWPM_SUBLAYER0 size", unsafe.Sizeof(fwpmSubLayer0{}), 72},
		{"FWPM_FILTER_CONDITION0 size", unsafe.Sizeof(fwpmFilterCondition0{}), 40},
		{"FWPM_ACTION0 size", unsafe.Sizeof(fwpmAction0{}), 20},
		{"FWPM_ACTION0 data offset", unsafe.Offsetof(fwpmAction0{}.Data), 4},
		{"FWPM_FILTER0 size", unsafe.Sizeof(fwpmFilter0{}), 200},
		{"FWPM_FILTER0 weight offset", unsafe.Offsetof(fwpmFilter0{}.Weight), 96},
		{"FWPM_FILTER0 condition count offset", unsafe.Offsetof(fwpmFilter0{}.NumFilterConditions), 112},
		{"FWPM_FILTER0 condition pointer offset", unsafe.Offsetof(fwpmFilter0{}.FilterCondition), 120},
		{"FWPM_FILTER0 action offset", unsafe.Offsetof(fwpmFilter0{}.Action), 128},
		{"FWPM_FILTER0 context offset", unsafe.Offsetof(fwpmFilter0{}.Context), 152},
		{"FWPM_FILTER0 reserved offset", unsafe.Offsetof(fwpmFilter0{}.Reserved), 168},
		{"FWPM_FILTER0 id offset", unsafe.Offsetof(fwpmFilter0{}.FilterID), 176},
		{"FWPM_FILTER0 effective weight offset", unsafe.Offsetof(fwpmFilter0{}.EffectiveWeight), 184},
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Errorf("%s = %d, want %d", check.name, check.got, check.want)
		}
	}
}

func TestWindowsWFPIPv4AddressUsesHostOrder(t *testing.T) {
	address, err := netIPv4HostOrder("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if address != 0x7f000001 {
		t.Fatalf("IPv4 host-order value = %#x, want %#x", address, uint32(0x7f000001))
	}
}
