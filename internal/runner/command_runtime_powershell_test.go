package runner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestCommandRuntimePowerShellRejectsLeadingDeclarations(t *testing.T) {
	for _, script := range []string{
		"param($Value)\nWrite-Output $Value",
		"# leading comment\r\nparam($Value)\r\nWrite-Output $Value",
		"<# outer <# nested #> block #>\n# line\n[CmdletBinding()]\nparam($Value)",
		"# comment\nusing namespace System.Text\nWrite-Output ok",
		"<# comment #>\nusing module './module.psm1'\nWrite-Output ok",
		"using assembly './library.dll'\nWrite-Output ok",
	} {
		if argv, err := commandRuntimePowerShellArguments(script); !errors.Is(err, ErrCommandRuntimeBoundary) || argv != nil {
			t.Fatalf("inline declaration was not explicitly rejected: argv=%q err=%v", argv, err)
		}
	}
	for _, script := range []string{
		"# only a comment",
		"# header\nWrite-Output 'param($x) is text'\nreturn",
		"function Show-Value { param($Value) Write-Output $Value }\nShow-Value '中文'",
		"& ./declared.ps1",
	} {
		argv, err := commandRuntimePowerShellArguments(script)
		if err != nil || argv[len(argv)-1] != script {
			t.Fatalf("ordinary multiline body changed or was rejected: argv=%q err=%v", argv, err)
		}
	}
}

func TestCommandRuntimePowerShellFingerprintCoversUTF8Bootstrap(t *testing.T) {
	script := "Write-Output '中文'\nexit 7"
	argv, err := commandRuntimePowerShellArguments(script)
	if err != nil {
		t.Fatal(err)
	}
	spec := CommandRuntimeResolvedSpec{Spec: CommandRuntimeSpec{Version: CommandRuntimeProtocolVersion, Profile: CommandRuntimePowerShell, Script: script}, CanonicalArgv: argv}
	current := CommandRuntimeSpecFingerprint(spec)
	intent := commandRuntimeIntentJSON(spec)
	spec.CanonicalArgv = []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script}
	if current == CommandRuntimeSpecFingerprint(spec) || !strings.Contains(intent, "OutputEncoding") || strings.Contains(commandRuntimeIntentJSON(spec), "OutputEncoding") {
		t.Fatal("new initialization is not bound to the exact canonical intent")
	}
}

func TestCommandRuntimePowerShellChangedCanonicalIntentDoesNotRewriteOldJob(t *testing.T) {
	store := newCommandRuntimeMemoryStore()
	starter := &commandRuntimeFakeStarter{}
	manager, err := NewCommandRuntimeManager(store, starter, "utf8-legacy-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown(context.Background()) })
	request := commandRuntimeTestRequest(manager, 2000)
	request.Spec.Spec.Profile = CommandRuntimePowerShell
	request.Spec.Spec.Script = "Write-Output 'legacy'"
	request.Spec.CanonicalArgv = []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", request.Spec.Spec.Script}
	job, _, err := manager.Start(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(starter.last().stdoutWriter, "old output ����\n")
	starter.last().finish(0)
	waitCommandRuntimeTerminal(t, manager, job.ID)
	before, err := store.GetCommandRuntimeJob(t.Context(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	request.Spec.CanonicalArgv, err = commandRuntimePowerShellArguments(request.Spec.Spec.Script)
	if err != nil {
		t.Fatal(err)
	}
	_, replayed, err := manager.Start(t.Context(), request)
	if !replayed || !errors.Is(err, ErrCommandRuntimeUncertain) || starter.starts != 1 {
		t.Fatalf("changed canonical command restarted an old operation: replayed=%t starts=%d err=%v", replayed, starter.starts, err)
	}
	after, err := store.GetCommandRuntimeJob(t.Context(), job.ID)
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	if err != nil || string(beforeJSON) != string(afterJSON) || after.Stdout != "old output ����\n" {
		t.Fatal("old command or historical output was rewritten")
	}
}
