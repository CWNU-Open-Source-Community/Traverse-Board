//go:build windows

package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsLocalSandboxInstrumentationTokenPolicy(t *testing.T) {
	base, err := prepareLocalProfile("base-profile")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := prepareLocalProfileWithInstrumentation("powershell-profile", true)
	if err != nil {
		t.Fatal(err)
	}
	if LocalBackendPolicyVersion != "windows_appcontainer_policy.v3" || base.instrumentationCapabilitySID != nil || len(localProfileCapabilities(base)) != 2 {
		t.Fatal("base policy changed its capability set")
	}
	const instrumentationSID = "S-1-15-3-1024-3153509613-960666767-3724611135-2725662640-12138253-543910227-1950414635-4190290187"
	if profile.instrumentationCapabilitySID == nil || profile.instrumentationCapabilitySID.String() != instrumentationSID {
		t.Fatal("instrumentation name derived an unexpected system SID")
	}
	expected := []*windows.SID{profile.filesystemCapabilitySID, profile.registryReadCapabilitySID, profile.instrumentationCapabilitySID}
	capabilities := localProfileCapabilities(profile)
	if !localTokenHasExactCapabilities(capabilities, expected...) {
		t.Fatal("exact instrumentation capability set rejected")
	}
	network, err := windows.StringToSid("S-1-15-3-1") // internetClient
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func([]windows.SIDAndAttributes) []windows.SIDAndAttributes{
		"network_extra": func(v []windows.SIDAndAttributes) []windows.SIDAndAttributes {
			return append(v, windows.SIDAndAttributes{Sid: network, Attributes: windows.SE_GROUP_ENABLED})
		},
		"network_substitution":    func(v []windows.SIDAndAttributes) []windows.SIDAndAttributes { v[2].Sid = network; return v },
		"missing_instrumentation": func(v []windows.SIDAndAttributes) []windows.SIDAndAttributes { return v[:2] },
		"duplicate":               func(v []windows.SIDAndAttributes) []windows.SIDAndAttributes { v[2] = v[1]; return v },
		"other_filesystem": func(v []windows.SIDAndAttributes) []windows.SIDAndAttributes {
			v[0].Sid = base.filesystemCapabilitySID
			return v
		},
		"disabled": func(v []windows.SIDAndAttributes) []windows.SIDAndAttributes { v[2].Attributes = 0; return v },
		"extra_attribute": func(v []windows.SIDAndAttributes) []windows.SIDAndAttributes {
			v[2].Attributes |= windows.SE_GROUP_USE_FOR_DENY_ONLY
			return v
		},
	} {
		t.Run(name, func(t *testing.T) {
			actual := mutate(append([]windows.SIDAndAttributes(nil), capabilities...))
			if localTokenHasExactCapabilities(actual, expected...) {
				t.Fatal("unexpected capability SID or attribute accepted")
			}
		})
	}
	if _, err := deriveLocalCapabilitySID("internetClient"); err == nil {
		t.Fatal("capability-name API accepted a network grant")
	}
}

func TestWindowsLocalSandboxInstrumentationOwnerRecovery(t *testing.T) {
	t.Run("legacy_v1", func(t *testing.T) { testWindowsLocalSandboxOwnerRecovery(t, false, true) })
	t.Run("instrumented_v2", func(t *testing.T) { testWindowsLocalSandboxOwnerRecovery(t, true, false) })
}

func TestWindowsLocalSandboxInstrumentationExecutionReceipt(t *testing.T) {
	base := windowsTestTempDir(t)
	drydock := filepath.Join(base, "drydock")
	if err := os.Mkdir(drydock, 0o700); err != nil {
		t.Fatal(err)
	}
	backend, err := NewPlatformLocalBackend(WithLocalOwnerRoot(filepath.Join(base, "owners")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	systemDirectory, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	// Copy only this test executable input; walking all of System32 would
	// include unrelated restricted directories, and granting it is unnecessary.
	toolchain := filepath.Join(base, "toolchain")
	if err := os.Mkdir(toolchain, 0o700); err != nil {
		t.Fatal(err)
	}
	commandBytes, err := os.ReadFile(filepath.Join(systemDirectory, "cmd.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(toolchain, "cmd.exe"), commandBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"base", "instrumentation"} {
		t.Run(mode, func(t *testing.T) {
			request := localWindowsTestRequest(t, backend, drydock, toolchain,
				"/tools", "/tools/cmd.exe", []string{"/d", "/c", "echo UX_L_INSTRUMENTATION_TOKEN"})
			request.Instrumentation = mode != "base"
			result, err := backend.Run(t.Context(), request)
			if err != nil || result.Validate(request) != nil || result.ExitCode != 0 || result.Instrumentation != request.Instrumentation ||
				!strings.Contains(string(result.Stdout.Data), "UX_L_INSTRUMENTATION_TOKEN") {
				t.Fatalf("real instrumented process receipt invalid: %#v err=%v", result, err)
			}
			// The cmd child proves the production token/receipt path, not PowerShell
			// compatibility. Neither a changed request nor a re-sealed false claim can
			// reuse the instrumentation-enabled execution evidence.
			changed := request
			changed.Instrumentation = !request.Instrumentation
			if result.Validate(changed) == nil {
				t.Fatal("instrumented receipt validated for a base capability request")
			}
			changedResult := result
			changedResult.Instrumentation = !result.Instrumentation
			changedResult.EvidenceFingerprint = localExecutionFingerprint(changedResult)
			if changedResult.Validate(request) == nil {
				t.Fatal("re-sealed false instrumentation claim validated")
			}
		})
	}
}
