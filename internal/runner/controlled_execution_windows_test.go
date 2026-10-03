//go:build windows

package runner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"cyberagent-workbench/internal/domain"

	"golang.org/x/sys/windows"
)

func TestWindowsFixedCommandRuntimeUsesRestrictedNativeProcess(t *testing.T) {
	for _, kind := range []ControlledCommandKind{ControlledCommandGoVersion, ControlledCommandPowerShellWorkspaceList} {
		t.Run(string(kind), func(t *testing.T) {
			request := controlledCommandTestRequest(t, kind)
			if err := os.WriteFile(filepath.Join(request.WorkspaceRoot, "fixed-list-marker.txt"), []byte("marker"), 0o600); err != nil {
				t.Fatal(err)
			}
			plan, err := PlanControlledCommand(request)
			if err != nil {
				t.Fatal(err)
			}
			manager, intent, err := NewFixedCommandRuntimeManager(newCommandRuntimeMemoryStore(), "fixed-native-test", plan, request.WorkspaceRoot)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := manager.NormalizeCommandRuntimeSpec(intent, request.WorkspaceRoot)
			if err != nil {
				t.Fatal(err)
			}
			changed := intent
			changed.Environment = []CommandRuntimeEnvironment{{Name: "UNTRUSTED", Value: "1"}}
			if _, err := manager.NormalizeCommandRuntimeSpec(changed, request.WorkspaceRoot); err == nil {
				t.Fatal("fixed plan accepted external environment")
			}
			adapter, _ := manager.AdapterIdentity()
			scope := CommandRuntimeScope{AttributionSource: domain.AgentAttributionOperatorRoot, Adapter: adapter}
			ctx := withCommandRuntimeDispatchCheck(t.Context(), func(context.Context, CommandRuntimeResolvedSpec) error { return nil })
			process, err := manager.starter.Start(ctx, scope, resolved)
			if err != nil {
				t.Fatal(err)
			}
			defer process.Close()
			native := process.(*windowsCommandRuntimeProcess)
			var token windows.Token
			if err := windows.OpenProcessToken(native.process, windows.TOKEN_QUERY, &token); err != nil {
				t.Fatal(err)
			}
			defer token.Close()
			var restricted, size uint32
			if err := windows.GetTokenInformation(token, windows.TokenHasRestrictions, (*byte)(unsafe.Pointer(&restricted)), uint32(unsafe.Sizeof(restricted)), &size); err != nil || restricted != 1 {
				t.Fatal("native token is not restricted", restricted, err)
			}
			label := make([]byte, 1024)
			if err := windows.GetTokenInformation(token, windows.TokenIntegrityLevel, &label[0], uint32(len(label)), &size); err != nil {
				t.Fatal(err)
			}
			if got := (*windows.Tokenmandatorylabel)(unsafe.Pointer(&label[0])).Label.Sid.String(); got != "S-1-16-4096" {
				t.Fatal("native integrity is not Low", got)
			}
			var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
			if err := windows.QueryInformationJobObject(native.job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)), nil); err != nil {
				t.Fatal(err)
			}
			if limits.BasicLimitInformation.ActiveProcessLimit != 1 || limits.ProcessMemoryLimit != MaxControlledProcessMemoryBytes || limits.BasicLimitInformation.LimitFlags&windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE == 0 {
				t.Fatalf("fixed Job limits changed %+v", limits)
			}
			stdout := make(chan []byte, 1)
			stderr := make(chan []byte, 1)
			go func() { b, _ := io.ReadAll(process.Stdout()); stdout <- b }()
			go func() { b, _ := io.ReadAll(process.Stderr()); stderr <- b }()
			code, err := process.Wait()
			out, diagnostic := <-stdout, <-stderr
			if err != nil || code != 0 || len(diagnostic) != 0 {
				t.Fatalf("restricted process exit=%d stdout=%q stderr=%q error=%v", code, out, diagnostic, err)
			}
			want := "go version "
			if kind == ControlledCommandPowerShellWorkspaceList {
				want = "fixed-list-marker.txt"
			}
			if !bytes.Contains(out, []byte(want)) {
				t.Fatalf("fixed output missing %q: %q", want, out)
			}
			if reaped, err := waitControlledJobReaped(t.Context(), native.job, time.Second); err != nil || !reaped {
				t.Fatal("fixed process tree remained", reaped, err)
			}
		})
	}
}

func TestWindowsControlledExecutionOptIn(t *testing.T) {
	if os.Getenv("CYBERAGENT_TEST_WINDOWS_CONTROLLED_EXECUTION") != "1" {
		t.Skip("set CYBERAGENT_TEST_WINDOWS_CONTROLLED_EXECUTION=1 for the process smoke")
	}
	request := controlledExecutionTestRequest(t, ControlledCommandGoVersion)
	token, tokenErr := newLowIntegrityRestrictedToken()
	if tokenErr != nil {
		t.Fatalf("restricted token setup: %v", tokenErr)
	}
	_ = token.Close()
	executor, err := NewPlatformControlledExecutor()
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || !bytes.HasPrefix(result.Stdout.Data, []byte("go version ")) ||
		len(result.Stderr.Data) != 0 || !result.TreeReaped ||
		!result.RestrictedToken || !result.LowIntegrityToken ||
		!result.JobAssignedAtCreation {
		t.Fatalf("unexpected Windows execution result: %+v", result)
	}

	const expressionPath = "(Write-Output PRAYU_PATH_INJECTION)"
	planRequest := controlledCommandTestRequest(t,
		ControlledCommandPowerShellWorkspaceList)
	planRequest.RelativePath = expressionPath
	if err := os.Mkdir(filepath.Join(planRequest.WorkspaceRoot, expressionPath),
		0o755); err != nil {
		t.Fatal(err)
	}
	const marker = "literal-path-marker.txt"
	if err := os.WriteFile(filepath.Join(planRequest.WorkspaceRoot,
		expressionPath, marker), []byte("marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanControlledCommand(planRequest)
	if err != nil {
		t.Fatal(err)
	}
	pathResult, err := executor.Execute(context.Background(),
		ControlledExecutionRequest{
			Plan: plan, WorkspaceRoot: planRequest.WorkspaceRoot,
			Interaction:    planRequest.Interaction,
			CurrentProfile: planRequest.CurrentProfile,
			CurrentSurface: planRequest.CurrentSurface,
			RequestedBy:    "test_operator", OperatorConfirmed: true,
		})
	if err != nil {
		t.Fatal(err)
	}
	if pathResult.ExitCode != 0 ||
		!bytes.Contains(pathResult.Stdout.Data, []byte(marker)) {
		t.Fatalf("PowerShell path was evaluated instead of decoded: %+v",
			pathResult)
	}
}

func TestControlledExecutableCandidatesIgnoreEnvironmentRedirects(t *testing.T) {
	redirect := t.TempDir()
	t.Setenv("SystemRoot", redirect)
	t.Setenv("ProgramFiles", redirect)
	t.Setenv("LocalAppData", redirect)

	systemRoot, err := controlledWindowsDirectory()
	if err != nil {
		t.Fatal(err)
	}
	powerShell := controlledExecutableCandidates("windows-powershell")
	if len(powerShell) != 1 ||
		!strings.EqualFold(filepath.Dir(filepath.Dir(filepath.Dir(
			filepath.Dir(powerShell[0])))), systemRoot) {
		t.Fatalf("unexpected PowerShell candidate: %v", powerShell)
	}
	redirectPrefix := strings.ToLower(filepath.Clean(redirect)) +
		string(filepath.Separator)
	for _, executableID := range []string{"go", "git"} {
		for _, candidate := range controlledExecutableCandidates(executableID) {
			if strings.HasPrefix(strings.ToLower(candidate), redirectPrefix) {
				t.Fatalf("%s candidate trusted redirected environment: %s",
					executableID, candidate)
			}
		}
	}
}

func TestControlledOutputReaderOwnsAndClosesTransferredHandle(t *testing.T) {
	pipe, err := newControlledPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.close()
	readHandle := pipe.read
	pipe.read = 0
	var written uint32
	if err := windows.WriteFile(pipe.write, []byte("owned"), &written, nil); err != nil {
		t.Fatal(err)
	}
	if written != uint32(len("owned")) {
		t.Fatalf("written bytes=%d", written)
	}
	if err := windows.CloseHandle(pipe.write); err != nil {
		t.Fatal(err)
	}
	pipe.write = 0

	resultChannel := make(chan controlledOutputResult, 1)
	errorChannel := make(chan error, 1)
	readControlledOutput(readHandle, resultChannel, errorChannel)
	result := <-resultChannel
	if result.err != nil || string(result.output.Data) != "owned" {
		t.Fatalf("output=%+v error=%v", result.output, result.err)
	}
	if err := windows.CloseHandle(readHandle); !errors.Is(err,
		windows.ERROR_INVALID_HANDLE) {
		t.Fatalf("transferred read handle remained open: %v", err)
	}
}

func TestControlledWorkspaceRejectsEscapingRelativeDirectory(t *testing.T) {
	request := controlledExecutionTestRequest(t,
		ControlledCommandPowerShellWorkspaceList)
	outside := t.TempDir()
	link := filepath.Join(request.WorkspaceRoot, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("directory symlink is unavailable: %v", err)
	}
	request.Plan.RelativePath = "escape"
	request.Plan.Argv[7] = encodeControlledRelativePath("escape")
	request.Plan.Fingerprint = controlledCommandPlanFingerprint(request.Plan)
	spec := ControlledStartSpec{
		RequestID: ControlledExecutionRequestID(request.Plan),
		PlanID:    request.Plan.ID, PlanFingerprint: request.Plan.Fingerprint,
		ExecutableID:  request.Plan.ExecutableID,
		Argv:          append([]string(nil), request.Plan.Argv...),
		WorkspaceRoot: request.WorkspaceRoot,
		Timeout:       DefaultControlledCommandTimeout,
	}
	if _, err := openControlledWorkspace(spec); err == nil {
		t.Fatal("escaping relative directory was accepted")
	}
}
