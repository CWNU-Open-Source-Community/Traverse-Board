package standardcodedelivery

import (
	"strings"
	"testing"
	"time"
)

func TestEvaluateExplicitDeliveryOutcomes(t *testing.T) {
	success := Verification{Conclusion: StatusPassed, ReasonCode: ReasonPassed}
	tests := []struct {
		name string
		in   Evaluation
		want Status
	}{
		{name: "passed", in: Evaluation{Verifications: []Verification{success}}, want: StatusPassed},
		{name: "failed", in: Evaluation{Verifications: []Verification{{Conclusion: StatusFailed, ReasonCode: ReasonVerificationFailed}}}, want: StatusFailed},
		{name: "truncated", in: Evaluation{Verifications: []Verification{{Conclusion: StatusPartial, ReasonCode: ReasonOutputTruncated}}}, want: StatusPartial},
		{name: "cancelled", in: Evaluation{Verifications: []Verification{{Conclusion: StatusBlocked, ReasonCode: ReasonCommandCancelled}}}, want: StatusBlocked},
		{name: "timeout", in: Evaluation{Verifications: []Verification{{Conclusion: StatusBlocked, ReasonCode: ReasonCommandTimedOut}}}, want: StatusBlocked},
		{name: "approval denied", in: Evaluation{Declaration: DeclarationApprovalDenied}, want: StatusBlocked},
		{name: "no tests", in: Evaluation{Declaration: DeclarationNoApplicableTests}, want: StatusNotRun},
		{name: "user skipped", in: Evaluation{Declaration: DeclarationUserSkipped}, want: StatusNotRun},
		{name: "budget", in: Evaluation{Declaration: DeclarationBudgetExhausted}, want: StatusBlocked},
		{name: "dependency", in: Evaluation{Declaration: DeclarationMissingDependency}, want: StatusBlocked},
		{name: "stale wins", in: Evaluation{RevisionStale: true, Verifications: []Verification{success}}, want: StatusStale},
		{name: "uncovered", in: Evaluation{Verifications: []Verification{success}, UncoveredCount: 1}, want: StatusPartial},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, _ := Evaluate(test.in)
			if got != test.want {
				t.Fatalf("status = %q, want %q", got, test.want)
			}
		})
	}
}

func TestObservationMakesPassedReceiptStaleWithoutRewritingReceipt(t *testing.T) {
	report := validDeliveryReport(t)
	current := report.WithObservation(report.FinalCheckpoint.RevisionSHA256, "", testTime())
	if current.Status != StatusPassed || !current.Verified || current.ReceiptStatus != StatusPassed {
		t.Fatalf("unexpected current projection: %+v", current)
	}
	if err := current.Validate(); err != nil {
		t.Fatalf("current observation invalidated immutable receipt: %v", err)
	}
	stale := report.WithObservation(Hash("revision-two"), "", testTime())
	if stale.Status != StatusStale || stale.Verified ||
		stale.ReceiptStatus != StatusPassed ||
		stale.Observation.ReasonCode != ReasonWorkspaceModifiedAfterVerification {
		t.Fatalf("unexpected stale projection: %+v", stale)
	}
	if err := stale.Validate(); err != nil {
		t.Fatalf("stale observation invalidated immutable receipt: %v", err)
	}
}

func TestPassedReportRequiresAlignedTerminalEvidenceAndHashes(t *testing.T) {
	report := validDeliveryReport(t)
	if err := report.Validate(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*Report)
	}{
		{name: "nonterminal", mutate: func(value *Report) {
			value.Verifications[0].State = "running"
			value.Verifications[0].CompletedAt = nil
		}},
		{name: "tree not reaped", mutate: func(value *Report) {
			value.Verifications[0].TreeReaped = false
		}},
		{name: "nonzero exit", mutate: func(value *Report) {
			exit := 1
			value.Verifications[0].ExitCode = &exit
		}},
		{name: "stale command revision", mutate: func(value *Report) {
			value.Verifications[0].CurrentRevision = false
		}},
		{name: "diff digest drift", mutate: func(value *Report) {
			value.Diff.SHA256 = Hash("different Diff")
		}},
		{name: "checkpoint reason digest", mutate: func(value *Report) {
			value.FinalCheckpoint.IncompleteReasonSHA256 = []string{"not-a-digest"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := report
			candidate.Verifications = append([]Verification(nil), report.Verifications...)
			test.mutate(&candidate)
			candidate.ReceiptSHA256 = ""
			candidate.EventSequence = 0
			if _, err := candidate.Seal(42); err == nil {
				t.Fatal("inconsistent passed delivery was sealed")
			}
		})
	}
}

func TestPublicDeliveryRejectsSecretAndAbsoluteHostPath(t *testing.T) {
	for _, summary := range []string{
		"token=very-secret-delivery-value",
		`inspect C:\\Users\\alice\\private.txt`,
		"inspect /home/alice/private.txt",
	} {
		t.Run(summary, func(t *testing.T) {
			report := validDeliveryReport(t)
			report.UncoveredItems = []UncoveredItem{{Summary: summary,
				SummarySHA256: Hash(summary)}}
			report.ReceiptStatus = StatusPartial
			report.Status = StatusPartial
			report.Verified = false
			report.Reasons = []Reason{ReasonFact(ReasonUncoveredItems,
				report.FinalCheckpoint.RevisionSHA256, report.Diff.SHA256,
				report.RequestFingerprint), report.Reasons[0]}
			report.ReceiptSHA256 = ""
			report.EventSequence = 0
			if _, err := report.Seal(42); err == nil {
				t.Fatal("private delivery text was sealed")
			}
		})
	}
}

func TestPublicDeliveryAcceptsRedactionMarkersWithContentDigest(t *testing.T) {
	report := validDeliveryReport(t)
	summary := "inspect [REDACTED:host-path] with token=[REDACTED:secret]"
	report.UncoveredItems = []UncoveredItem{{Summary: summary, SummarySHA256: Hash(summary)}}
	report.ReceiptStatus = StatusPartial
	report.Status = StatusPartial
	report.Verified = false
	verification := report.Verifications[0]
	report.Reasons = []Reason{ReasonFact(ReasonUncoveredItems,
		report.FinalCheckpoint.RevisionSHA256, report.Diff.SHA256,
		report.RequestFingerprint), ReasonFact(verification.ReasonCode,
		verification.JobID, verification.SpecSHA256, verification.RevisionSHA256)}
	report.ReceiptSHA256 = ""
	report.EventSequence = 0
	if _, err := report.Seal(42); err != nil {
		t.Fatalf("safe redacted summary was rejected: %v", err)
	}
}

func validDeliveryReport(t *testing.T) Report {
	t.Helper()
	now := testTime()
	exit := 0
	revision := RevisionSHA256(Hash("manifest"), Hash("index"), Hash("root"),
		Hash("root-path"), strings.Repeat("1", 40), "codex/delivery")
	verification := Verification{JobID: "verification-job", Conclusion: StatusPassed,
		ReasonCode: ReasonPassed, State: "completed", ExitCode: &exit,
		SpecSHA256: Hash("spec"), ExecutableSHA256: Hash("executable"),
		EnvironmentSHA256: Hash("environment"), PermissionRevision: 3,
		Backend: "local", BackendGenerationSHA256: Hash("backend"),
		CheckpointID: "verification-checkpoint", RevisionSHA256: revision,
		CurrentRevision: true, StdoutSHA256: Hash(""), StderrSHA256: Hash(""),
		TreeReaped: true, StartedAt: &now, CompletedAt: &now, Artifacts: []Artifact{}}
	report := Report{ID: "standard-code-delivery-test",
		ProtocolVersion: ProtocolVersion, OperationKeySHA256: Hash("operation"),
		RequestFingerprint: Hash("request"), Status: StatusPassed,
		ReceiptStatus: StatusPassed, Verified: true,
		Binding: Binding{RunID: "run-test", MissionID: "mission-test",
			SessionID: "session-test", SourceWorkspaceID: "workspace-source",
			DrydockWorkspaceID: "workspace-drydock", DrydockID: "drydock-test",
			DrydockGeneration: 2, PresetOperationSHA256: Hash("preset"),
			PermissionSnapshotID: "permission-test", PermissionRevision: 3,
			Backend: "local", BackendGenerationSHA256: Hash("backend"),
			CapabilityGenerationSHA256: Hash("capability"), SupervisorMutationEpoch: 4},
		BaseCommit: strings.Repeat("0", 40), HeadCommit: strings.Repeat("1", 40),
		Diff: Diff{SHA256: Hash("patch"), Files: []ChangedFile{}},
		FinalCheckpoint: Checkpoint{ID: "final-checkpoint", ManifestSHA256: Hash("manifest"),
			IndexSHA256: Hash("index"), RootFingerprint: Hash("root"),
			RootPathSHA256: Hash("root-path"), HeadCommit: strings.Repeat("1", 40),
			BranchSHA256: Hash("codex/delivery"), RevisionSHA256: revision,
			RecoveryLevel: "complete", IncompleteReasonSHA256: []string{}, CreatedAt: now},
		Verifications: []Verification{verification}, UncoveredItems: []UncoveredItem{},
		Links: Links{Self: "/api/v1/runs/run-test/standard-code-delivery",
			Checkpoint:         "/api/v1/runs/run-test/workspace-checkpoints?checkpoint_id=final-checkpoint",
			CheckpointTimeline: "/api/v1/runs/run-test/workspace-checkpoints",
			Undo:               "/api/v1/runs/run-test/workspace-checkpoints/undo",
			Rewind:             "/api/v1/runs/run-test/workspace-checkpoints/rewind",
			Fork:               "/api/v1/runs/run-test/workspace-checkpoints/fork"},
		Safeguards: Safeguards{}, CreatedAt: now}
	report.Reasons = []Reason{ReasonFact(ReasonPassed, revision, report.Diff.SHA256,
		report.RequestFingerprint)}
	sealed, err := report.Seal(42)
	if err != nil {
		t.Fatalf("seal valid delivery fixture: %v", err)
	}
	return sealed
}

func testTime() (value time.Time) {
	return time.Unix(1_700_000_000, 0).UTC()
}
