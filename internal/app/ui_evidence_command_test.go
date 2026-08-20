package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/uievidence"
)

func TestUIEvidenceCLIListsShowsAndExclusivelyExportsVerifiedUntrustedEvidence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CYBERAGENT_HOME", home)
	if _, stderr, code := executeTestCommand(t, "workspace", "init", "ui-cli"); code != 0 || stderr != "" {
		t.Fatalf("workspace init stderr=%q code=%d", stderr, code)
	}
	created, stderr, code := executeTestCommand(t, "run", "create",
		"UI evidence CLI contract", "--workspace", "ui-cli", "--profile", "code")
	if code != 0 || stderr != "" {
		t.Fatalf("run create output=%q stderr=%q code=%d", created, stderr, code)
	}
	runID := runIDPattern.FindString(created)
	if runID == "" {
		t.Fatalf("run identity missing: %s", created)
	}
	attempt, artifact := seedCLIUIEvidence(t, home, runID)

	listed, stderr, code := executeTestCommand(t, "ui-evidence", "list",
		"--run", runID, "--status", "passed", "--limit", "10")
	if code != 0 || stderr != "" {
		t.Fatalf("list output=%q stderr=%q code=%d", listed, stderr, code)
	}
	var attempts []uievidence.Attempt
	if err := json.Unmarshal([]byte(listed), &attempts); err != nil ||
		len(attempts) != 1 || !attempts[0].Status.Passed() ||
		attempts[0].Manifest.Fingerprint != attempt.Manifest.Fingerprint {
		t.Fatalf("listed attempts=%+v err=%v", attempts, err)
	}

	shown, stderr, code := executeTestCommand(t, "ui-evidence", "show",
		attempt.Manifest.AttemptID)
	if code != 0 || stderr != "" {
		t.Fatalf("show output=%q stderr=%q code=%d", shown, stderr, code)
	}
	var bundle application.UIEvidenceBundle
	if err := json.Unmarshal([]byte(shown), &bundle); err != nil ||
		bundle.Attempt.Manifest.Source.Commit != attempt.Manifest.Source.Commit ||
		len(bundle.Steps) != 1 || len(bundle.Artifacts) != 6 {
		t.Fatalf("shown bundle=%+v err=%v", bundle, err)
	}
	for _, metadata := range bundle.Artifacts {
		if !metadata.Untrusted {
			t.Fatalf("shown bundle contains trusted evidence metadata: %+v", metadata)
		}
	}

	outputPath := filepath.Join(t.TempDir(), "ui-evidence.json")
	exported, stderr, code := executeTestCommand(t, "ui-evidence", "artifact",
		attempt.Manifest.AttemptID, artifact.Metadata.ID, "--output", outputPath)
	if code != 0 || stderr != "" || !strings.Contains(exported, "untrusted: true") ||
		!strings.Contains(exported, artifact.Metadata.SHA256) {
		t.Fatalf("artifact output=%q stderr=%q code=%d", exported, stderr, code)
	}
	raw, err := os.ReadFile(outputPath)
	if err != nil || string(raw) != string(artifact.Content) {
		t.Fatalf("exported bytes=%q err=%v", raw, err)
	}
	if _, stderr, code := executeTestCommand(t, "ui-evidence", "artifact",
		attempt.Manifest.AttemptID, artifact.Metadata.ID, "--output", outputPath); code == 0 || !strings.Contains(stderr, "create UI evidence artifact output") {
		t.Fatalf("exclusive export did not fail closed: stderr=%q code=%d", stderr, code)
	}
}

func seedCLIUIEvidence(t *testing.T, home, runID string) (
	uievidence.Attempt, uievidence.Artifact,
) {
	t.Helper()
	ctx := context.Background()
	state, err := store.Open(filepath.Join(home, "cyberagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	runRecord, err := state.GetRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	mission, err := state.GetMission(ctx, runRecord.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 20, 2, 0, 0, 0, time.UTC)
	recipe, err := uievidence.SealCommandRecipe(uievidence.CommandRecipe{
		ProtocolVersion: "command-runtime.v2", Profile: "process",
		ExecutableName: "fixture-server", ExecutablePathSHA256: cliUIDigest("path"),
		ExecutableSHA256: cliUIDigest("executable"), CanonicalArgv: []string{"--port", "4173"},
		WorkingDirectory: ".", EnvironmentNames: []string{},
		EnvironmentSHA256: cliUIDigest("environment"), TimeoutMilliseconds: 30000,
		Network: "disabled", Credentials: "none", Purpose: "serve synthetic UI fixture"})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := uievidence.SealManifest(uievidence.Manifest{
		AttemptID: "ui-attempt-cli", RunID: runRecord.ID, MissionID: mission.ID,
		SessionID: runRecord.SessionID, WorkspaceID: mission.WorkspaceID,
		Source: uievidence.SourceBinding{RepositoryKind: "git",
			Commit: strings.Repeat("1", 40), Branch: "main", DirtyDigest: cliUIDigest("dirty"),
			RootFingerprint: cliUIDigest("root"), IndexSHA256: cliUIDigest("index"),
			ManifestSHA256: cliUIDigest("manifest")},
		Start: recipe, Readiness: uievidence.Readiness{URL: "http://127.0.0.1:4173/",
			Method: "GET", ExpectedStatus: []int{200}, TimeoutMilliseconds: 30000,
			IntervalMilliseconds: 100},
		Browser: uievidence.BrowserIdentity{Product: "edge", Version: "151.0.1",
			ExecutableSHA256: cliUIDigest("browser"),
			DriverProtocol:   uievidence.DriverProtocolVersion, Headless: true,
			TemporaryProfile: true},
		URL: "http://127.0.0.1:4173/", Route: "/",
		Environment: uievidence.Environment{Viewport: uievidence.Viewport{
			Width: 1280, Height: 720, DPR: 1}, Locale: "en-US",
			Theme: uievidence.ThemeLight, ReducedMotion: true},
		Fixture: uievidence.Fixture{Name: "CLI fixture", Seed: "42",
			PageState: "ready", DataSHA256: cliUIDigest("fixture"),
			Deterministic: true, Synthetic: true},
		Steps: []uievidence.Step{{ID: "navigate", Kind: uievidence.StepNavigate}},
		Capture: uievidence.CapturePolicy{Screenshot: true, DOM: true, Accessibility: true,
			Console: true, Network: true, Performance: true, MaskSelectors: []string{}},
		FailurePolicy: uievidence.FailurePolicy{FailOnConsoleError: true,
			FailOnPageError: true, FailOnRequestError: true, FailOnHTTPStatus: true},
		CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := uievidence.NewAttempt(manifest, "cli-ui-evidence-operation", now)
	if err != nil {
		t.Fatal(err)
	}
	attempt, _, err = state.CreateUIEvidenceAttempt(ctx, attempt)
	if err != nil {
		t.Fatal(err)
	}
	started, err := uievidence.StartAttempt(attempt, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	started, err = state.UpdateUIEvidenceAttempt(ctx, started, attempt.Version)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := uievidence.SealStepReceipt(uievidence.StepReceipt{
		AttemptID: manifest.AttemptID, StepID: "navigate", Sequence: 1,
		Kind: uievidence.StepNavigate, Status: uievidence.StatusPassed,
		FailureStage: uievidence.FailureNone, StartedAt: now.Add(2 * time.Second),
		CompletedAt: now.Add(3 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.AddUIEvidenceStep(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	artifact, err := uievidence.SealArtifact(uievidence.ArtifactMetadata{
		ID: "ui-artifact-cli", AttemptID: manifest.AttemptID, RunID: runRecord.ID,
		StepID: receipt.StepID, Kind: uievidence.ArtifactDOM,
		MIME: "application/json", Viewport: manifest.Environment.Viewport,
		SourceCommit:    manifest.Source.Commit,
		RetentionPolicy: uievidence.ArtifactRetentionRunHistory,
		Redacted:        true, Untrusted: true,
		CreatedAt: now.Add(4 * time.Second)}, []byte(`{"state":"verified"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := state.AddUIEvidenceArtifact(ctx, artifact); err != nil {
		t.Fatal(err)
	}
	for index, kind := range []uievidence.ArtifactKind{
		uievidence.ArtifactScreenshot,
		uievidence.ArtifactAccessibility,
		uievidence.ArtifactConsole,
		uievidence.ArtifactNetwork,
		uievidence.ArtifactPerformance,
	} {
		mime := "application/json"
		width, height := 0, 0
		if kind == uievidence.ArtifactScreenshot {
			mime = "image/png"
			width = manifest.Environment.Viewport.Width
			height = manifest.Environment.Viewport.Height
		}
		value, sealErr := uievidence.SealArtifact(uievidence.ArtifactMetadata{
			ID: "ui-artifact-cli-" + string(kind), AttemptID: manifest.AttemptID,
			RunID: runRecord.ID, StepID: receipt.StepID, Kind: kind, MIME: mime,
			Width: width, Height: height, Viewport: manifest.Environment.Viewport,
			SourceCommit:    manifest.Source.Commit,
			RetentionPolicy: uievidence.ArtifactRetentionRunHistory,
			Redacted:        true, Untrusted: true,
			CreatedAt: now.Add(time.Duration(index+5) * time.Second),
		}, []byte("bounded "+string(kind)+" evidence"))
		if sealErr != nil {
			t.Fatal(sealErr)
		}
		if err := state.AddUIEvidenceArtifact(ctx, value); err != nil {
			t.Fatal(err)
		}
	}
	count, bytes, err := state.UIEvidenceArtifactTotals(ctx, manifest.AttemptID)
	if err != nil || count != 6 {
		t.Fatalf("artifact totals=%d/%d err=%v", count, bytes, err)
	}
	completed, err := uievidence.CompleteAttempt(started, uievidence.StatusPassed,
		uievidence.FailureNone, "", "", uievidence.DiagnosticsSummary{},
		uievidence.CleanupReceipt{BrowserTreeReaped: true, ApplicationTreeReaped: true,
			ProfileRemoved: true, NetworkReleased: true, PortReleased: true},
		count, bytes, now.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	completed, err = state.UpdateUIEvidenceAttempt(ctx, completed, started.Version)
	if err != nil {
		t.Fatal(err)
	}
	return completed, artifact
}

func cliUIDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
