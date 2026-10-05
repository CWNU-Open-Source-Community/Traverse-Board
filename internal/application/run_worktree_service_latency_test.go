package application

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Latency work must not turn a successful observation into cached authority.
// Reuse one caller context and prove both physical observations are fresh on
// every call, then change the owned contents and source identity between calls.
func TestDrydockExecutionObservationsRevalidateEveryCall(t *testing.T) {
	f := newDrydockApplicationFixture(t, "fresh observations")
	workspace := mustCreateDrydock(t, f)
	ctx := t.Context()
	for round := 0; round < 2; round++ {
		started := time.Now()
		current, source, observed, err := f.service.loadExactDrydock(ctx, f.run.ID, workspace.Generation, false)
		if err != nil || current.Generation != workspace.Generation || observed.Path != workspace.Path ||
			source.State.CapturedAt.Before(started) || observed.Binding.CapturedAt.Before(started) ||
			observed.Binding.Fingerprint() != workspace.ExpectedBindingFingerprint {
			t.Fatalf("round %d reused or lost physical ownership evidence: source=%+v observed=%+v err=%v", round, source, observed, err)
		}
		t.Logf("fresh source and owned-worktree observation %d: %.6fs", round, time.Since(started).Seconds())
	}
	writeDrydockTestFile(t, filepath.Join(workspace.Path, "tracked.txt"), "changed after observation\n")
	_, _, changed, err := f.service.loadExactDrydock(ctx, f.run.ID, workspace.Generation, false)
	if err != nil || changed.Binding.Fingerprint() == workspace.ExpectedBindingFingerprint {
		t.Fatalf("later owned content was hidden by a prior observation: %+v err=%v", changed, err)
	}
	runDrydockTestGit(t, f.sourceRoot, "branch", "-m", "changed-source-branch")
	if _, _, _, err := f.service.loadExactDrydock(ctx, f.run.ID, workspace.Generation, false); err == nil ||
		!strings.Contains(err.Error(), "drifted") {
		t.Fatalf("source identity change was hidden by prior observations: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, _, err := f.service.loadExactDrydock(cancelled, f.run.ID, workspace.Generation, false); err == nil {
		t.Fatal("cancelled observation returned authority")
	}
	after, found, err := f.state.GetDrydockByRun(ctx, f.run.ID)
	if err != nil || !found || after.Generation != workspace.Generation || after.State != workspace.State {
		t.Fatalf("read-only observations changed durable ownership: %+v found=%t err=%v", after, found, err)
	}
}
