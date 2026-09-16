package application

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
)

func attachmentZipFixture(t *testing.T) []byte {
	t.Helper()
	var raw bytes.Buffer
	w := zip.NewWriter(&raw)
	f, err := w.Create("note.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("ORIGINAL_ZIP_TOOL_READ 中文原件\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}

func TestOriginalAttachmentMaterializationPreservesBytesAndRejectsCacheChanges(t *testing.T) {
	if runtime.GOOS == "windows" && attachmentPathsOverlap(`C:\private\attachment-inputs`, `D:\source`) {
		t.Fatal("separate home and source volumes overlap")
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "attachments.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	source := t.TempDir()
	if err := st.SaveWorkspace(t.Context(), store.WorkspaceRecord{ID: "workspace-inputs", Name: "inputs", RootPath: source}); err != nil {
		t.Fatal(err)
	}
	raw := attachmentZipFixture(t)
	file, err := st.SaveWorkspaceFileAttachment(t.Context(), "workspace-inputs", "original-input-zip-key", "application/zip", "CON.zip", raw)
	if err != nil {
		t.Fatal(err)
	}
	set := domain.FileAttachmentInputSet{Version: "file_attachment_inputs.v1", ThreadID: "thread-input", RunID: "run-input", SessionID: "session-input", WorkspaceID: file.WorkspaceID, Files: []domain.FileAttachmentInput{domain.NewFileAttachmentInput(file)}}
	first, err := materializeOriginalAttachmentInputs(t.Context(), st, set, source)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(first.Root, filepath.FromSlash(set.Files[0].RelativePath))
	info, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	second, err := materializeOriginalAttachmentInputs(t.Context(), st, set, source)
	if err != nil || first != second {
		t.Fatalf("cache reuse changed: %#v %v", second, err)
	}
	info2, _ := os.Stat(name)
	if !info.ModTime().Equal(info2.ModTime()) {
		t.Fatal("same manifest rewrote original bytes")
	}
	got, err := os.ReadFile(name)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatal("raw ZIP changed", err)
	}
	if entries, err := os.ReadDir(source); err != nil || len(entries) != 0 {
		t.Fatal("materialization wrote source workspace", err)
	}
	forged := set
	forged.Files = append([]domain.FileAttachmentInput(nil), set.Files...)
	forged.Files[0].SHA256 = strings.Repeat("0", 64)
	if _, err := materializeOriginalAttachmentInputs(t.Context(), st, forged, source); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatal("changed identity admitted", err)
	}
	if _, err := materializeOriginalAttachmentInputs(t.Context(), st, set, filepath.Dir(st.FileAttachmentInputDirectory())); err == nil {
		t.Fatal("private input copied into source ancestor")
	}
	if err := os.Chmod(name, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, append([]byte("BAD"), raw[3:]...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := materializeOriginalAttachmentInputs(t.Context(), st, set, source); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatal("tampered original cache admitted", err)
	}
	_, stillOriginal, err := st.GetWorkspaceFileAttachment(t.Context(), file.WorkspaceID, file.ID)
	if err != nil || !bytes.Equal(stillOriginal, raw) {
		t.Fatal("cache corruption changed immutable blob")
	}
}

func TestCommandRuntimeReadsActualSentZipAndExcludesQueuedUploads(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("actual PowerShell 7 Windows command runtime")
	}
	if !strings.EqualFold(filepath.Base(os.Getenv("CYBERAGENT_POWERSHELL_PATH")), "pwsh.exe") {
		t.Skip("configure CYBERAGENT_POWERSHELL_PATH for the actual PowerShell 7 archive regression")
	}
	ctx := t.Context()
	st, run, root, lease, capabilities := newCommandRuntimeTestRuntime(t, ctx)
	raw := attachmentZipFixture(t)
	file, err := st.SaveWorkspaceFileAttachment(ctx, "workspace-command-runtime-app", "actual-zip-input-upload", "application/zip", "original.zip", raw)
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.SaveWorkspaceFileAttachment(ctx, file.WorkspaceID, "queued-zip-input-upload", "text/plain", "not-sent.txt", []byte("QUEUED_MUST_NOT_BE_EXPOSED"))
	if err != nil {
		t.Fatal(err)
	}
	queue := func(f domain.WorkspaceFileAttachment, key string) string {
		r := domain.EnqueueOperatorSteeringRequest{RunID: run.ID, SessionID: run.SessionID, OperationKey: key, RequestedBy: "test_operator", Attachments: []domain.FileAttachmentReference{{ID: f.ID, WorkspaceID: f.WorkspaceID, SHA256: f.SHA256, ByteSize: f.ByteSize}}}
		result, err := st.EnqueueOperatorSteering(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		return result.Message.ID
	}
	messageID := queue(file, "actual-zip-input-message")
	queue(other, "queued-zip-input-message")
	turn, err := st.BeginSupervisorSteeringTurnForMessage(ctx, lease, messageID)
	if err != nil {
		t.Fatal(err)
	}
	set, err := st.ListSupervisorFileAttachmentInputs(ctx, turn.Checkpoint)
	if err != nil || len(set.Files) != 1 || set.Files[0] != domain.NewFileAttachmentInput(file) {
		t.Fatalf("exact sent inventory %#v %v", set, err)
	}
	stale := turn.Checkpoint
	stale.LeaseGeneration++
	if _, err := st.ListSupervisorFileAttachmentInputs(ctx, stale); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatal("old lease accepted", err)
	}
	manager, err := runner.NewPlatformCommandRuntimeManager(st, idgen.New("attachment-native-owner"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = manager.Shutdown(shutdown)
	}()
	service, err := NewCommandRuntimeService(st, manager, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	script := "$p=Join-Path $env:TRAVERSE_ATTACHMENTS_DIR '" + set.Files[0].RelativePath + "'; (Get-FileHash -LiteralPath $p -Algorithm SHA256).Hash; Expand-Archive -LiteralPath $p -DestinationPath '" + strings.ReplaceAll(destination, "'", "''") + "'; Get-Content -Raw -Encoding utf8 -LiteralPath '" + strings.ReplaceAll(filepath.Join(destination, "note.txt"), "'", "''") + "'"
	maxBytes := 4096
	input := toolgateway.CommandRuntimeInput{Version: toolgateway.CommandRuntimeToolProtocolVersion, Action: toolgateway.CommandRuntimeActionRun, FailurePolicy: toolgateway.CommandRuntimeFailFast, MaxBytes: &maxBytes, Commands: []runner.CommandRuntimeSpec{{Version: runner.CommandRuntimeProtocolVersion, Profile: runner.CommandRuntimePowerShell, Script: script, WorkingDirectory: ".", Environment: []runner.CommandRuntimeEnvironment{}, StdinPolicy: runner.CommandRuntimeStdinClosed, CloseInitialStdin: true, TimeoutMilliseconds: 10000, Output: runner.CommandRuntimeOutputPolicy{InlineBytes: 4096, ArtifactBytes: 4096}, Network: runner.CommandRuntimeNetworkDisabled, Credentials: runner.CommandRuntimeCredentialsNone, Purpose: "read and extract the exact sent ZIP in an isolated test directory"}}}
	scope := commandRuntimeTestScope(t, ctx, st, service, run, root, lease, "original-zip-command")
	result, err := service.ExecuteCommandRuntime(ctx, scope, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Jobs) != 1 || result.Jobs[0].State != runner.CommandRuntimeJobCompleted || len(result.Artifacts) != 1 || !strings.Contains(result.Artifacts[0].Stdout, "ORIGINAL_ZIP_TOOL_READ 中文原件") || !strings.Contains(strings.ToLower(result.Artifacts[0].Stdout), file.SHA256) {
		t.Fatalf("actual ZIP read failed %#v", result)
	}
	replay, err := service.ExecuteCommandRuntime(ctx, scope, input)
	if err != nil || !replay.Replayed || replay.Jobs[0].ID != result.Jobs[0].ID {
		t.Fatalf("original command repeated %#v %v", replay, err)
	}
	job, err := st.GetCommandRuntimeJob(ctx, result.Jobs[0].ID)
	_, manifestSHA := set.Manifest()
	if err != nil || !strings.Contains(job.IntentJSON, manifestSHA) || !job.TreeReaped {
		t.Fatal("raw input absent from durable execution identity", err)
	}
	workspace, err := st.GetWorkspaceByID(ctx, file.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(workspace.RootPath); err != nil || len(entries) != 0 {
		t.Fatal("ZIP extraction changed project", err)
	}
	cache := filepath.Join(st.FileAttachmentInputDirectory(), run.ID, manifestSHA)
	if _, err := os.Stat(filepath.Join(cache, other.ID)); !os.IsNotExist(err) {
		t.Fatal("later queued file exposed in input root", err)
	}
}
