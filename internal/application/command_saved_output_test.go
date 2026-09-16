package application

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"cyberagent-workbench/internal/artifact"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/standardcodedelivery"
	"cyberagent-workbench/internal/store"
)

type savedOutputFaultStore struct {
	*store.SQLiteStore
	blobReads int
	mutate    func(*artifact.Blob) error
}

func (s *savedOutputFaultStore) GetRunArtifact(ctx context.Context, id string) (artifact.Blob, error) {
	s.blobReads++
	blob, err := s.SQLiteStore.GetRunArtifact(ctx, id)
	if err == nil && s.mutate != nil {
		err = s.mutate(&blob)
	}
	return blob, err
}

// Real SQLite/Git receipts and command outcomes exercise both decision points.
// This fixture seeds the Job outcome; the separate actual N PS7 journey proves
// OS execution. Observed raw bytes intentionally exceed normalized LF text.
func TestStandardCodeSavedInlineOutputRequiresCompleteBlobsAndPreservesOldReceipt(t *testing.T) {
	stdout := "BEGIN\n" + strings.Repeat("中文离线检查明细，保留所有用户文件。\n", 300) + "END\n"
	f := newStandardCodeDeliveryReplayFixture(t, 0, deliveryCommandOutputFixture{
		stdout: stdout, stderr: "diagnostic\n", reason: "inline_window", rawExtraBytes: 302})
	if f.job.StdoutObservedBytes <= int64(len(stdout)) || f.job.TruncationReason != "inline_window" {
		t.Fatal("fixture must retain raw-byte/normalized-text difference and immutable inline reason")
	}
	if f.machine.snapshot.State != domain.StandardCodeSupervisorDeliver ||
		f.machine.snapshot.VerifiedMutationEpoch != f.machine.snapshot.MutationEpoch {
		t.Fatalf("complete saved output did not verify the observed mutation: %+v", f.machine.snapshot)
	}
	if commandSavedOutputTruncated(t.Context(), f.base.state, f.job) {
		t.Fatal("complete saved stdout/stderr were mistaken for a truncated ring preview")
	}
	faults := &savedOutputFaultStore{SQLiteStore: f.base.state}
	for _, test := range []struct {
		name   string
		mutate func(*artifact.Blob) error
	}{
		{"missing", func(*artifact.Blob) error { return errors.New("fixture artifact unavailable") }},
		{"bad content hash", func(blob *artifact.Blob) error { blob.Content += "changed"; return nil }},
		{"wrong Run", func(blob *artifact.Blob) error { blob.RunID = "other-run"; return nil }},
		{"wrong Job", func(blob *artifact.Blob) error { blob.SourceID = "other-job"; return nil }},
		{"wrong stream", func(blob *artifact.Blob) error { blob.Stream = artifact.StreamStderr; return nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			faults.mutate = test.mutate
			if !commandSavedOutputTruncated(t.Context(), faults, f.job) {
				t.Fatal("unproven output became complete")
			}
		})
	}
	for _, reason := range []string{"artifact_limit", "future_unknown_reason"} {
		changed := f.job
		changed.TruncationReason = reason
		if !commandSavedOutputTruncated(t.Context(), f.base.state, changed) {
			t.Fatalf("%s became complete", reason)
		}
	}
	// Full proof is required in the Supervisor, too; a scoped projection alone
	// cannot upgrade an unavailable stored artifact or an incomplete adapter.
	faults.mutate = func(*artifact.Blob) error { return errors.New("fixture unavailable") }
	blocked := *f.machine
	blocked.store = faults
	blocked.snapshot.VerifiedMutationEpoch = 0
	blocked.snapshot.State = domain.StandardCodeSupervisorExecute
	projection := standardCodeCommandOutput(t, "run", []runner.CommandRuntimeJobSnapshot{runner.ProjectCommandRuntimeJob(f.job)}, nil)
	if _, _, err := blocked.observeCommand(t.Context(), domain.SupervisorToolCall{Status: domain.SupervisorToolCompleted},
		standardCodeCallDescriptor{Kind: domain.StandardCodeToolCommandRun, Action: "run"},
		supervisorToolResultEnvelope{Stdout: projection}); err != nil {
		t.Fatal(err)
	}
	if blocked.snapshot.VerifiedMutationEpoch != 0 || blocked.snapshot.State != domain.StandardCodeSupervisorDiagnose {
		t.Fatal("Supervisor accepted missing complete-output proof")
	}
	// Record a genuinely incomplete observation, then make the previously
	// unavailable bodies readable. The sealed partial receipt must stay partial.
	degraded := *f.service
	degraded.store = faults
	request := StandardCodeDeliveryRecordRequest{RunID: f.base.run.ID, OperationKey: "saved-output-unavailable", RequestedBy: "api_operator"}
	first, err := degraded.Record(t.Context(), request)
	if err != nil || first.Report.Status != standardcodedelivery.StatusPartial || !first.Report.Verifications[0].OutputTruncated {
		t.Fatalf("missing proof: status=%s err=%v", first.Report.Status, err)
	}
	reads := faults.blobReads
	current, found, err := degraded.Current(t.Context(), request.RunID)
	if err != nil || !found || current.ReceiptSHA256 != first.Report.ReceiptSHA256 ||
		current.Status != standardcodedelivery.StatusPartial || faults.blobReads != reads {
		t.Fatal("Current re-evaluated saved output instead of preserving sealed receipt")
	}
	secondRequest := request
	secondRequest.OperationKey = "saved-output-confirmed"
	second, err := f.service.Record(t.Context(), secondRequest)
	if err != nil || second.Report.Status != standardcodedelivery.StatusPassed ||
		!second.Report.Verified || second.Report.Verifications[0].OutputTruncated {
		t.Fatalf("complete proof: status=%s err=%v", second.Report.Status, err)
	}
	replay, err := f.service.Record(t.Context(), request)
	if err != nil || !replay.Replayed || replay.Report.Status != standardcodedelivery.StatusPartial ||
		replay.Report.ReceiptSHA256 != first.Report.ReceiptSHA256 ||
		!reflect.DeepEqual(replay.Report.Verifications, first.Report.Verifications) {
		t.Fatalf("old partial receipt was upgraded on replay: %+v, %v", replay.Report, err)
	}
	storedJob, err := f.base.state.GetCommandRuntimeJob(t.Context(), f.job.ID)
	if err != nil || !reflect.DeepEqual(storedJob, f.job) {
		t.Fatal("output decision rewrote immutable Job")
	}
}
