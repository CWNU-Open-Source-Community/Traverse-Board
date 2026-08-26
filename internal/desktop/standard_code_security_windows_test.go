//go:build windows

package desktop

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestConfigureStandardCodeSecuritySubprocessHidesWindowsConsole(t *testing.T) {
	command := exec.Command("cmd.exe", "/c", "exit", "0")
	configureStandardCodeSecuritySubprocess(command)
	if command.SysProcAttr == nil || !command.SysProcAttr.HideWindow ||
		command.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("security subprocess can create a visible console: %+v", command.SysProcAttr)
	}
}

func TestStandardCodeSecurityRecoveryTerminationPolicy(t *testing.T) {
	forced := map[string]bool{
		"recovery_force_termination":               true,
		"recovery_reboot_equivalent":               true,
		"recovery_lease_expiry":                    true,
		"recovery_dirty_untracked_concurrent_edit": true,
	}
	for caseID := range standardCodeRecoveryCases {
		if got := standardCodeSecurityRecoveryForceTermination(caseID); got != forced[caseID] {
			t.Fatalf("force termination for %s = %t, want %t", caseID, got, forced[caseID])
		}
	}
}

func TestStandardCodeSecurityRecoveryDirectoryNamesAreBoundAndDistinct(t *testing.T) {
	seen := make(map[string]string)
	for caseID := range standardCodeRecoveryCases {
		for _, backend := range []string{"local", "docker"} {
			name := standardCodeSecurityRecoveryDirectoryName(caseID, backend)
			if len(name) != len("recovery-")+24 || filepath.Base(name) != name {
				t.Fatalf("recovery directory for %s/%s is not bounded: %q",
					caseID, backend, name)
			}
			if previous := seen[name]; previous != "" {
				t.Fatalf("recovery directory collision: %s and %s", previous,
					caseID+"/"+backend)
			}
			seen[name] = caseID + "/" + backend
			if repeated := standardCodeSecurityRecoveryDirectoryName(caseID, backend); repeated != name {
				t.Fatalf("recovery directory for %s/%s drifted", caseID, backend)
			}
		}
	}
}

func TestSetStandardCodeSecurityEnvironmentRestoresHostState(t *testing.T) {
	const name = "TRAVERSE_BOARD_ISSUE181_ENVIRONMENT_TEST"
	t.Cleanup(func() { _ = os.Unsetenv(name) })
	if err := os.Setenv(name, "original"); err != nil {
		t.Fatal(err)
	}
	restore, err := setStandardCodeSecurityEnvironment(name, "synthetic")
	if err != nil || os.Getenv(name) != "synthetic" {
		t.Fatalf("set synthetic environment: value=%q err=%v", os.Getenv(name), err)
	}
	if err := restore(); err != nil || os.Getenv(name) != "original" {
		t.Fatalf("restore original environment: value=%q err=%v", os.Getenv(name), err)
	}
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	restore, err = setStandardCodeSecurityEnvironment(name, "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	if _, found := os.LookupEnv(name); found {
		t.Fatal("previously absent host environment was not removed")
	}
}

func TestCreateStandardCodeSecurityNamedPipeIsExclusive(t *testing.T) {
	name, handle, err := createStandardCodeSecurityNamedPipe(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	if name == "" || handle == 0 {
		t.Fatalf("named pipe boundary is incomplete: name=%q handle=%v", name, handle)
	}
	if _, duplicate, duplicateErr := createStandardCodeSecurityNamedPipe(t.Name()); duplicateErr == nil {
		_ = windows.CloseHandle(duplicate)
		t.Fatal("duplicate harness-owned named pipe unexpectedly opened")
	}
	if err := windows.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	_, reopened, err := createStandardCodeSecurityNamedPipe(t.Name())
	if err != nil {
		t.Fatalf("named pipe boundary was not released: %v", err)
	}
	if err := windows.CloseHandle(reopened); err != nil {
		t.Fatal(err)
	}
}

func TestSeedStandardCodeSecurityConcurrentEdit(t *testing.T) {
	root := t.TempDir()
	drydockRoot := filepath.Join(root, "drydock")
	if err := os.Mkdir(drydockRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("fixed fixture\n")
	if err := os.WriteFile(filepath.Join(drydockRoot, "README.md"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	state := standardCodeSecurityRecoveryState{DrydockRelativePath: "drydock"}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, recoveryWorkerStateName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := seedStandardCodeSecurityConcurrentEdit(root); err != nil {
		t.Fatal(err)
	}
	tracked, err := os.ReadFile(filepath.Join(drydockRoot, "README.md"))
	if err != nil || !bytes.HasPrefix(tracked, original) ||
		!bytes.Contains(tracked, []byte("issue181-concurrent-edit\r\n")) {
		t.Fatalf("tracked concurrent edit was not preserved: %q err=%v", tracked, err)
	}
	untracked, err := os.ReadFile(filepath.Join(drydockRoot, "issue181-concurrent.bin"))
	if err != nil || !bytes.Equal(untracked, []byte{0x00, 0x18, 0x01, 0xff}) {
		t.Fatalf("untracked concurrent edit was not preserved: %v err=%v", untracked, err)
	}
}
