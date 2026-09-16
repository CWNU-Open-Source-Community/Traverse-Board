//go:build windows

package sandbox

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/runner"
)

func TestWindowsLocalSandboxAttachmentZipUsesReadOnlyInputAndPrivateScratch(t *testing.T) {
	// Keep Windows' nested AppContainer profile paths independent of the long
	// test name, following the existing real process-tree regression fixture.
	base, err := os.MkdirTemp("", "local-att-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(base); err != nil {
			t.Error(err)
		}
	})
	base = windowsTestCanonicalRoot(t, base)
	drydock := filepath.Join(base, "drydock")
	inputRoot := filepath.Join(base, "sent-inputs")
	for _, root := range []string{drydock, inputRoot} {
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
	}
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	entry, err := writer.Create("data/原文.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("exact sent zip content 中文")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	inputPath := filepath.Join(inputRoot, "original.zip")
	if err := os.WriteFile(inputPath, archive.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	before := sha256.Sum256(archive.Bytes())
	backend, err := NewPlatformLocalBackend(WithLocalOwnerRoot(filepath.Join(base, "owners")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	toolchain := windowsTestCanonicalRoot(t, filepath.Dir(executable))
	request := localWindowsTestRequest(t, backend, drydock, toolchain, "/tools", "/tools/"+filepath.Base(executable),
		[]string{"-test.run=^TestWindowsLocalSandboxAttachmentChild$", "--", "/attachments/original.zip"})
	request.Manifest.Environment = []EnvironmentBinding{{Name: "TRAVERSE_BOARD_LOCAL_CHILD", Source: EnvironmentLiteral, Value: "attachment"}}
	pathSHA, _ := LocalHostPathDigest(inputRoot)
	request.ToolchainInputs = append(request.ToolchainInputs, LocalToolchainInput{ID: "sent-attachment-manifest", Root: inputRoot, VirtualRoot: LocalAttachmentVirtualRoot, RootSHA256: pathSHA, DataOnly: true})
	result, err := backend.Run(t.Context(), request)
	if err != nil || result.ExitCode != 0 || !result.ACLsRestored || !result.TreeReaped || !strings.Contains(string(result.Stdout.Data), "zip original read-only; scratch extracted exact bytes") {
		t.Fatalf("real restricted ZIP command failed: err=%v result=%+v stdout=%s stderr=%s", err, result, result.Stdout.Data, result.Stderr.Data)
	}
	after, err := os.ReadFile(inputPath)
	if err != nil || sha256.Sum256(after) != before {
		t.Fatal("original ZIP changed")
	}
	if entries, err := os.ReadDir(drydock); err != nil || len(entries) != 0 {
		t.Fatal("attachment/extraction polluted project")
	}
	// The same literal file path cannot be read when the binding is absent.
	request.ToolchainInputs = request.ToolchainInputs[:1]
	request.Manifest.Command.Arguments = []string{"-test.run=^TestWindowsLocalSandboxAttachmentChild$", "--", inputPath}
	request.Manifest.Environment[0].Value = "attachment-unbound"
	result, err = backend.Run(t.Context(), request)
	if err != nil || result.ExitCode != 0 || !strings.Contains(string(result.Stdout.Data), "unbound input denied") {
		t.Fatalf("unbound input was readable: %v stdout=%s stderr=%s", err, result.Stdout.Data, result.Stderr.Data)
	}
}

func TestWindowsLocalSandboxAttachmentChild(t *testing.T) {
	mode := os.Getenv("TRAVERSE_BOARD_LOCAL_CHILD")
	if mode != "attachment" && mode != "attachment-unbound" {
		t.Skip("AppContainer child only")
	}
	if len(os.Args) < 3 {
		t.Fatal("missing bound input argument")
	}
	input := os.Args[len(os.Args)-1]
	if mode == "attachment-unbound" {
		if _, err := os.ReadFile(input); err == nil {
			t.Fatal("missing binding still allowed file read")
		}
		if os.Getenv(LocalAttachmentEnvironment) != "" {
			t.Fatal("missing binding received an attachment path")
		}
		fmt.Println("unbound input denied")
		return
	}
	inputRoot := os.Getenv(LocalAttachmentEnvironment)
	if inputRoot == "" || filepath.Join(inputRoot, "original.zip") != input {
		t.Fatal("virtual path/env mapping mismatch")
	}
	for _, root := range filepath.SplitList(os.Getenv("PATH")) {
		if strings.EqualFold(root, inputRoot) {
			t.Fatal("attachment directory entered executable PATH")
		}
	}
	archive, err := zip.OpenReader(input)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if len(archive.File) != 1 {
		t.Fatal("unexpected ZIP fixture")
	}
	reader, err := archive.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(data) != "exact sent zip content 中文" {
		t.Fatal("original zipped bytes lost")
	}
	output := filepath.Join(os.TempDir(), "extracted.txt")
	if err := os.WriteFile(output, data, 0600); err != nil {
		t.Fatal(err)
	}
	if saved, err := os.ReadFile(output); err != nil || !bytes.Equal(saved, data) {
		t.Fatal("private scratch extraction failed")
	}
	if err := os.WriteFile(input, []byte("tampered"), 0600); err == nil {
		t.Fatal("original input was writable")
	}
	if err := os.Remove(input); err == nil {
		t.Fatal("original input was deletable")
	}
	if err := os.WriteFile(filepath.Join(inputRoot, "injected.txt"), data, 0600); err == nil {
		t.Fatal("input directory was writable")
	}
	fmt.Println("zip original read-only; scratch extracted exact bytes")
}

func TestWindowsLocalSandboxAttachmentRejectsExecutableAndEnvironmentOverrides(t *testing.T) {
	base := windowsTestTempDir(t)
	drydock, tools, inputs := filepath.Join(base, "drydock"), filepath.Join(base, "tools"), filepath.Join(base, "inputs")
	for _, root := range []string{drydock, tools, inputs} {
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
	}
	backend := &attachmentGenerationBackend{}
	request := localWindowsTestRequest(t, backend, drydock, tools, "/tools", "/tools/program.exe", nil)
	digest, _ := LocalHostPathDigest(inputs)
	request.ToolchainInputs = append(request.ToolchainInputs, LocalToolchainInput{ID: "inputs", Root: inputs, VirtualRoot: LocalAttachmentVirtualRoot, RootSHA256: digest, DataOnly: true})
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	bound := localExecutionBindingFingerprint(request)
	changed := request
	changed.ToolchainInputs = append([]LocalToolchainInput(nil), request.ToolchainInputs...)
	changed.ToolchainInputs[1].DataOnly = false
	if localExecutionBindingFingerprint(changed) == bound {
		t.Fatal("data-only binding absent from fingerprint")
	}
	for _, command := range []string{"/attachments/program.exe", "/attachments/sub/program.exe"} {
		changed := request
		changed.Manifest.Command.Executable = command
		if changed.Validate() == nil {
			t.Fatal("data root could supply executable")
		}
	}
	for _, name := range []string{LocalAttachmentEnvironment, strings.ToLower(LocalAttachmentEnvironment)} {
		changed := request
		changed.Manifest.Environment = []EnvironmentBinding{{Name: name, Source: EnvironmentLiteral, Value: inputs}}
		if changed.Validate() == nil {
			t.Fatal("caller could override fixed attachment environment")
		}
	}
}

type attachmentGenerationBackend struct{ LocalBackend }

func (*attachmentGenerationBackend) Generation() string { return strings.Repeat("a", 64) }

func TestWindowsLocalSandboxAttachmentPowerShellZipUsesFixedDirectory(t *testing.T) {
	base, err := os.MkdirTemp("", "local-att-ps-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(base); err != nil {
			t.Error(err)
		}
	})
	base = windowsTestCanonicalRoot(t, base)
	drydock, inputs := filepath.Join(base, "drydock"), filepath.Join(base, "inputs")
	for _, root := range []string{drydock, inputs} {
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
	}
	var raw bytes.Buffer
	zw := zip.NewWriter(&raw)
	entry, err := zw.Create("answer.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, err = entry.Write([]byte("PowerShell sandbox original ZIP 中文"))
	if err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inputs, "original.zip"), raw.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	script := `Expand-Archive -LiteralPath "$env:TRAVERSE_ATTACHMENTS_DIR/original.zip" -DestinationPath "$env:TEMP/e"; Get-Content -LiteralPath "$env:TEMP/e/answer.txt" -Raw -Encoding utf8`
	resolved, err := runner.NormalizeLocalSandboxCommandRuntimeSpec(runner.CommandRuntimeSpec{
		Version: runner.CommandRuntimeProtocolVersion, Profile: runner.CommandRuntimePowerShell,
		Script: script, WorkingDirectory: ".", Environment: []runner.CommandRuntimeEnvironment{},
		StdinPolicy: runner.CommandRuntimeStdinClosed, CloseInitialStdin: true, TimeoutMilliseconds: 20000,
		Output:  runner.CommandRuntimeOutputPolicy{InlineBytes: 65536, ArtifactBytes: 65536},
		Network: runner.CommandRuntimeNetworkDisabled, Credentials: runner.CommandRuntimeCredentialsNone, Purpose: "read original sent ZIP in existing sandbox",
	}, drydock)
	if err != nil {
		t.Fatalf("configured PowerShell 7 required for real attachment shell check: %v", err)
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
	toolchain := filepath.Dir(resolved.ExecutablePath)
	request := localWindowsTestRequest(t, backend, drydock, toolchain, "/tools", "/tools/"+filepath.Base(resolved.ExecutablePath), resolved.CanonicalArgv)
	request.Instrumentation = true
	request.Manifest.Resources.MemoryBytes = 1024 * 1024 * 1024
	digest, _ := LocalHostPathDigest(inputs)
	request.ToolchainInputs = append(request.ToolchainInputs, LocalToolchainInput{ID: "sent-inputs", Root: inputs, VirtualRoot: LocalAttachmentVirtualRoot, RootSHA256: digest, DataOnly: true})
	result, err := backend.Run(t.Context(), request)
	if err != nil || result.ExitCode != 0 || !strings.Contains(string(result.Stdout.Data), "PowerShell sandbox original ZIP 中文") {
		t.Fatalf("real PowerShell input path failed: %v exit=%d stdout=%s stderr=%s", err, result.ExitCode, result.Stdout.Data, result.Stderr.Data)
	}
	if actual, err := os.ReadFile(filepath.Join(inputs, "original.zip")); err != nil || !bytes.Equal(actual, raw.Bytes()) {
		t.Fatal("shell changed original ZIP")
	}
	if entries, err := os.ReadDir(drydock); err != nil || len(entries) != 0 {
		t.Fatal("shell extraction wrote project")
	}
}
