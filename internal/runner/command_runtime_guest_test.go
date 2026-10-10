package runner

import (
	"runtime"
	"testing"
)

func TestPOSIXGuestIntentKeepsNativeHostLaunchBoundary(t *testing.T) {
	spec := CommandRuntimeSpec{Version: CommandRuntimeProtocolVersion, Profile: CommandRuntimeProcess,
		Executable: "/usr/bin/python3", Arguments: []string{"--version"}, WorkingDirectory: ".",
		Environment: []CommandRuntimeEnvironment{}, StdinPolicy: CommandRuntimeStdinClosed, CloseInitialStdin: true,
		Network: CommandRuntimeNetworkDisabled, Credentials: CommandRuntimeCredentialsNone,
		TimeoutMilliseconds: 1000, Output: CommandRuntimeOutputPolicy{InlineBytes: MinCommandRuntimeInlineBytes, ArtifactBytes: MinCommandRuntimeInlineBytes},
		Purpose: "inspect guest toolchain"}
	if _, err := NormalizeCommandRuntimeIntent(spec); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if _, err := NormalizeCommandRuntimeSpec(spec, t.TempDir()); err == nil {
			t.Fatal("guest path acquired a Windows host launch binding")
		}
	}
	for _, executable := range []string{"python3", "/bin/sh", "/bin/env", "/usr/bin/sudo"} {
		spec.Executable = executable
		if _, err := NormalizeCommandRuntimeIntent(spec); err == nil {
			t.Fatalf("unsupported process intent accepted: %s", executable)
		}
	}
}
