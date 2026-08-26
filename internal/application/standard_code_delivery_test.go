package application

import (
	"strings"
	"testing"

	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/standardcodedelivery"
)

func TestCommandVerificationConclusionProjectsExplicitRuntimeOutcomes(t *testing.T) {
	exitZero := 0
	base := runner.CommandRuntimeJob{State: runner.CommandRuntimeJobCompleted,
		ExitCode: &exitZero, TreeReaped: true, PermissionSnapshotID: "permission",
		PermissionRevision: 5,
		Adapter:            commandruntimeadapter.SandboxedWorkspace("local", "local-adapter", "generation")}
	supervisor := domain.StandardCodeSupervisorSnapshot{PermissionSnapshotID: "permission",
		PermissionRevision: 5}
	backendGeneration := commandRuntimeBackendGeneration(base)
	tests := []struct {
		name              string
		mutate            func(*runner.CommandRuntimeJob)
		current           bool
		artifactsComplete bool
		backend           string
		generation        string
		wantStatus        standardcodedelivery.Status
		wantReason        string
	}{
		{name: "passed", current: true, artifactsComplete: true, backend: "local",
			generation: backendGeneration, wantStatus: standardcodedelivery.StatusPassed,
			wantReason: standardcodedelivery.ReasonPassed},
		{name: "failed", current: true, artifactsComplete: true, backend: "local",
			generation: backendGeneration, mutate: func(job *runner.CommandRuntimeJob) {
				exit := 2
				job.ExitCode = &exit
			}, wantStatus: standardcodedelivery.StatusFailed,
			wantReason: standardcodedelivery.ReasonVerificationFailed},
		{name: "truncated", current: true, artifactsComplete: true, backend: "local",
			generation: backendGeneration, mutate: func(job *runner.CommandRuntimeJob) {
				job.TruncationReason = "inline_limit"
			}, wantStatus: standardcodedelivery.StatusPartial,
			wantReason: standardcodedelivery.ReasonOutputTruncated},
		{name: "cancelled", current: true, artifactsComplete: true, backend: "local",
			generation: backendGeneration, mutate: func(job *runner.CommandRuntimeJob) {
				job.State = runner.CommandRuntimeJobCancelled
			}, wantStatus: standardcodedelivery.StatusBlocked,
			wantReason: standardcodedelivery.ReasonCommandCancelled},
		{name: "timed out", current: true, artifactsComplete: true, backend: "local",
			generation: backendGeneration, mutate: func(job *runner.CommandRuntimeJob) {
				job.State = runner.CommandRuntimeJobTimedOut
			}, wantStatus: standardcodedelivery.StatusBlocked,
			wantReason: standardcodedelivery.ReasonCommandTimedOut},
		{name: "not terminal", current: true, artifactsComplete: true, backend: "local",
			generation: backendGeneration, mutate: func(job *runner.CommandRuntimeJob) {
				job.State = runner.CommandRuntimeJobRunning
			}, wantStatus: standardcodedelivery.StatusBlocked,
			wantReason: standardcodedelivery.ReasonCommandNotTerminal},
		{name: "artifact missing", current: true, artifactsComplete: false, backend: "local",
			generation: backendGeneration, wantStatus: standardcodedelivery.StatusPartial,
			wantReason: standardcodedelivery.ReasonArtifactMissing},
		{name: "workspace stale", current: false, artifactsComplete: true, backend: "local",
			generation: backendGeneration, wantStatus: standardcodedelivery.StatusStale,
			wantReason: standardcodedelivery.ReasonWorkspaceModifiedAfterVerification},
		{name: "permission drift", current: true, artifactsComplete: true, backend: "local",
			generation: backendGeneration, mutate: func(job *runner.CommandRuntimeJob) {
				job.PermissionRevision++
			}, wantStatus: standardcodedelivery.StatusStale,
			wantReason: standardcodedelivery.ReasonPermissionDrift},
		{name: "backend drift", current: true, artifactsComplete: true, backend: "docker",
			generation: backendGeneration, wantStatus: standardcodedelivery.StatusStale,
			wantReason: standardcodedelivery.ReasonBackendDrift},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			job := base
			if test.mutate != nil {
				test.mutate(&job)
			}
			status, reason := commandVerificationConclusion(job, test.current,
				test.artifactsComplete, supervisor, test.backend, test.generation)
			if status != test.wantStatus || reason != test.wantReason {
				t.Fatalf("got (%s, %s), want (%s, %s)", status, reason,
					test.wantStatus, test.wantReason)
			}
		})
	}
}

func TestProjectStandardCodeUncoveredItemsRemovesPrivateMaterial(t *testing.T) {
	items := projectStandardCodeUncoveredItems([]string{
		"inspect C:\\Users\\alice\\private.txt\x00 token=very-secret-delivery-value",
		"inspect /home/alice/private.txt",
	})
	if len(items) != 2 {
		t.Fatalf("items=%#v", items)
	}
	for _, item := range items {
		if strings.Contains(item.Summary, "very-secret") ||
			strings.Contains(item.Summary, `C:\Users`) ||
			strings.Contains(item.Summary, "/home/alice") ||
			strings.ContainsRune(item.Summary, 0) ||
			item.SummarySHA256 != standardcodedelivery.Hash(item.Summary) {
			t.Fatalf("unsafe uncovered projection: %#v", item)
		}
	}
}
