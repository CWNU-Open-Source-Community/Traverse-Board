//go:build windows

package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
)

func TestCommandRuntimeAttachmentHostJobReceivesFixedInputWithoutChangingPermission(t *testing.T) {
	root, inputs := t.TempDir(), filepath.Clean(t.TempDir())
	inputPath := filepath.Join(inputs, "original.txt")
	const contents = "native full-access sent input 中文"
	if err := os.WriteFile(inputPath, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	spec := commandRuntimeTestPowerShellSpec()
	spec.Script = `Get-Content -LiteralPath "$env:TRAVERSE_ATTACHMENTS_DIR/original.txt" -Raw -Encoding utf8`
	spec.TimeoutMilliseconds = 10000
	resolved, err := NormalizeCommandRuntimeSpec(spec, root)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := BindCommandRuntimeAttachmentInput(resolved, CommandRuntimeAttachmentInput{Root: inputs, ManifestSHA256: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	store := newCommandRuntimeMemoryStore()
	manager, err := NewCommandRuntimeManager(store, newPlatformCommandRuntimeStarter(), "attachment-host-owner")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := manager.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	request := commandRuntimeTestRequest(manager, 10000)
	request.Spec = bound
	request.Scope.WorkspaceRootSHA256 = bound.WorkspaceRootSHA256
	job, replayed, err := manager.Start(t.Context(), request)
	if err != nil || replayed {
		t.Fatalf("host start failed: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var page CommandRuntimeOutputPage
	for !job.State.Terminal() && time.Now().Before(deadline) {
		job, page, err = manager.Wait(t.Context(), job.ID, 100*time.Millisecond, 0, MaxCommandRuntimeOutputRead)
		if err != nil {
			t.Fatal(err)
		}
	}
	var output strings.Builder
	for _, frame := range page.Frames {
		output.WriteString(frame.Text)
	}
	if job.State != CommandRuntimeJobCompleted || job.ExitCode == nil || *job.ExitCode != 0 || !strings.Contains(output.String(), contents) {
		t.Fatalf("host file input failed: job=%+v output=%s", job, output.String())
	}
	record, err := store.GetCommandRuntimeJob(t.Context(), job.ID)
	if err != nil || record.PermissionMode != domain.RunExecutionPermissionFullAccess || !record.Adapter.SameBackend(request.Scope.Adapter) || !strings.Contains(record.IntentJSON, "attachment_manifest_sha256") {
		t.Fatal("attachment injection changed the existing host permission/adapter or lost its manifest")
	}
	if raw, err := os.ReadFile(inputPath); err != nil || string(raw) != contents {
		t.Fatal("host read changed source bytes")
	}
}

func TestCommandRuntimeAttachmentDifferentWindowsVolumeIsNotWorkspaceOverlap(t *testing.T) {
	base := attachmentTestSpec(t)
	inputs := filepath.Clean(t.TempDir())
	// The binding checks namespace overlap before launch separately checks the
	// real cwd. This requires no writes to a second user drive.
	otherVolume := "Z:"
	if strings.EqualFold(filepath.VolumeName(inputs), otherVolume) {
		otherVolume = "Y:"
	}
	base.WorkspaceRoot = otherVolume + `\project`
	if _, err := BindCommandRuntimeAttachmentInput(base, CommandRuntimeAttachmentInput{Root: inputs, ManifestSHA256: strings.Repeat("a", 64)}); err != nil {
		t.Fatalf("disjoint Windows volume rejected as an overlapping workspace: %v", err)
	}
}
