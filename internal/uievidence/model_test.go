package uievidence

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func TestStatusNotRunNeverPasses(t *testing.T) {
	for _, status := range []Status{StatusNotRun, StatusRunning, StatusFailed,
		StatusCancelled, StatusTimedOut, StatusInterrupted} {
		if status.Passed() {
			t.Fatalf("status %q unexpectedly maps to passed", status)
		}
	}
	if !StatusPassed.Passed() {
		t.Fatal("passed status did not map to passed")
	}
}

func TestSealManifestBindsSourceRecipeRuntimeAndPresentation(t *testing.T) {
	manifest := validManifest(t)
	sealed, err := SealManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if sealed.Fingerprint == "" {
		t.Fatal("manifest fingerprint is empty")
	}

	changed := sealed
	changed.Environment.Viewport.Width++
	if changed.Validate() == nil {
		t.Fatal("mutated viewport retained a valid fingerprint")
	}
	changed = sealed
	changed.Source.DirtyDigest = digest("changed")
	if changed.Validate() == nil {
		t.Fatal("mutated source retained a valid fingerprint")
	}
}

func TestManifestV1RequiresCoreCapturesAndRejectsVideo(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*CapturePolicy)
	}{
		{name: "screenshot", mutate: func(value *CapturePolicy) { value.Screenshot = false }},
		{name: "DOM", mutate: func(value *CapturePolicy) { value.DOM = false }},
		{name: "accessibility", mutate: func(value *CapturePolicy) { value.Accessibility = false }},
		{name: "console", mutate: func(value *CapturePolicy) { value.Console = false }},
		{name: "network", mutate: func(value *CapturePolicy) { value.Network = false }},
		{name: "performance", mutate: func(value *CapturePolicy) { value.Performance = false }},
		{name: "video", mutate: func(value *CapturePolicy) { value.Video = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifest := validManifest(t)
			test.mutate(&manifest.Capture)
			if _, err := SealManifest(manifest); err == nil {
				t.Fatal("incomplete UI evidence v1 capture policy was accepted")
			}
		})
	}
}

func TestManifestRejectsPseudoCommitsForGitSources(t *testing.T) {
	for _, commit := range []string{"unborn", "non-git"} {
		manifest := validManifest(t)
		manifest.Source.Commit = commit
		if _, err := SealManifest(manifest); err == nil {
			t.Fatalf("Git source accepted pseudo commit %q", commit)
		}
	}
}

func TestArtifactV1RejectsReservedVideoKind(t *testing.T) {
	_, err := SealArtifact(ArtifactMetadata{
		ID: "ui-artifact-video", AttemptID: "ui-attempt-test", RunID: "run-test",
		StepID: "capture", Kind: ArtifactVideo, MIME: "video/webm",
		Viewport:        Viewport{Width: 1280, Height: 720, DPR: 1},
		SourceCommit:    "0123456789012345678901234567890123456789",
		RetentionPolicy: ArtifactRetentionRunHistory, Untrusted: true,
		CreatedAt: time.Now().UTC(),
	}, []byte("reserved"))
	if err == nil {
		t.Fatal("ui-evidence.v1 accepted reserved video content")
	}
}

func TestViewportRejectsPixelSurfaceOutsideScreenshotLimits(t *testing.T) {
	for _, viewport := range []Viewport{
		{Width: 7680, Height: 1080, DPR: 2},
		{Width: 1920, Height: 4320, DPR: 1.25},
	} {
		if err := viewport.Validate(); err == nil {
			t.Fatalf("oversized viewport pixel surface was accepted: %+v", viewport)
		}
	}
}

func TestScreenshotDimensionsMustMatchViewportPixelSurface(t *testing.T) {
	_, err := SealArtifact(ArtifactMetadata{
		ID: "ui-artifact-screenshot", AttemptID: "ui-attempt-test", RunID: "run-test",
		StepID: "capture", Kind: ArtifactScreenshot, MIME: "image/png",
		Width: 1, Height: 1, Viewport: Viewport{Width: 1280, Height: 720, DPR: 1.5},
		SourceCommit:    "0123456789012345678901234567890123456789",
		RetentionPolicy: ArtifactRetentionRunHistory, Redacted: true, Untrusted: true,
		CreatedAt: time.Now().UTC(),
	}, []byte("not a production screenshot"))
	if err == nil {
		t.Fatal("screenshot dimensions outside the manifest pixel surface were accepted")
	}
}

func TestAttemptRequiresCleanDiagnosticsAndCleanupToPass(t *testing.T) {
	manifest, err := SealManifest(validManifest(t))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	started := now.Add(time.Second)
	completed := started.Add(time.Second)
	attempt := Attempt{ProtocolVersion: AttemptProtocolVersion, Manifest: manifest,
		OperationDigest: digest("operation"), RequestFingerprint: manifest.Fingerprint,
		Status: StatusPassed, FailureStage: FailureNone,
		Cleanup: CleanupReceipt{BrowserTreeReaped: true, ApplicationTreeReaped: true,
			ProfileRemoved: true, NetworkReleased: true, PortReleased: true},
		ArtifactCount: 1, ArtifactBytes: 1,
		Version: 3, CreatedAt: now, StartedAt: &started, CompletedAt: &completed,
		UpdatedAt: completed}
	if err := attempt.Validate(); err != nil {
		t.Fatal(err)
	}
	wrongVersion := attempt
	wrongVersion.Version = 2
	if wrongVersion.Validate() == nil {
		t.Fatal("terminal UI evidence accepted a non-terminal lifecycle version")
	}
	for _, test := range []struct {
		name        string
		diagnostics DiagnosticsSummary
	}{
		{name: "console error", diagnostics: DiagnosticsSummary{ConsoleErrors: 1}},
		{name: "page error", diagnostics: DiagnosticsSummary{PageErrors: 1}},
		{name: "failed request", diagnostics: DiagnosticsSummary{FailedRequests: 1}},
		{name: "HTTP failure", diagnostics: DiagnosticsSummary{HTTPFailures: 1}},
		{name: "blocked request", diagnostics: DiagnosticsSummary{BlockedRequests: 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := attempt
			changed.Diagnostics = test.diagnostics
			if changed.Validate() == nil {
				t.Fatal("unclean diagnostics unexpectedly produced passing evidence")
			}
		})
	}
	withoutArtifacts := attempt
	withoutArtifacts.ArtifactCount = 0
	withoutArtifacts.ArtifactBytes = 0
	if withoutArtifacts.Validate() == nil {
		t.Fatal("passing evidence without an artifact was accepted")
	}
}

func TestNotRunAttemptRejectsExecutionResidue(t *testing.T) {
	manifest, err := SealManifest(validManifest(t))
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := NewAttempt(manifest, "operation", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	wrongVersion := attempt
	wrongVersion.Version = 2
	if wrongVersion.Validate() == nil {
		t.Fatal("not-run UI evidence accepted an executed lifecycle version")
	}
	for _, mutate := range []func(*Attempt){
		func(value *Attempt) { value.Diagnostics.ConsoleWarnings = 1 },
		func(value *Attempt) { value.Cleanup.ProfileRemoved = true },
		func(value *Attempt) { value.ArtifactCount, value.ArtifactBytes = 1, 1 },
	} {
		changed := attempt
		mutate(&changed)
		if changed.Validate() == nil {
			t.Fatal("not-run evidence accepted execution residue")
		}
	}
}

func TestInputDigestRejectsSecretLikeValues(t *testing.T) {
	if _, err := InputSHA256("token=abcdefghijklmnopqrstuvwxyz1234567890"); err == nil {
		t.Fatal("secret-like input was accepted")
	}
	want := digest("fixture value")
	if got, err := InputSHA256("fixture value"); err != nil || got != want {
		t.Fatalf("input digest=%q err=%v", got, err)
	}
}

func validManifest(t *testing.T) Manifest {
	t.Helper()
	recipe := CommandRecipe{ProtocolVersion: "command-runtime.v2", Profile: "process",
		ExecutableName: "fixture.exe", ExecutablePathSHA256: digest("path"),
		ExecutableSHA256: digest("exe"), CanonicalArgv: []string{"--serve"},
		WorkingDirectory: ".", EnvironmentNames: []string{},
		EnvironmentSHA256: digest("env"), TimeoutMilliseconds: 30000,
		Network: "disabled", Credentials: "none", Purpose: "serve fixture"}
	recipe.Fingerprint = fingerprint(recipe)
	return Manifest{AttemptID: "ui_attempt_test", RunID: "run_test",
		MissionID: "mission_test", SessionID: "session_test", WorkspaceID: "workspace_test",
		Source: SourceBinding{RepositoryKind: "git", Commit: "0123456789012345678901234567890123456789",
			Branch: "main", DirtyDigest: digest("dirty"), RootFingerprint: digest("root"),
			IndexSHA256: digest("index"), ManifestSHA256: digest("manifest")},
		Start: recipe,
		Readiness: Readiness{URL: "http://127.0.0.1:4173/health", Method: "GET",
			ExpectedStatus: []int{200}, TimeoutMilliseconds: 30000, IntervalMilliseconds: 100},
		Browser: BrowserIdentity{Product: "Chromium", Version: "1.2.3",
			ExecutableSHA256: digest("browser"), DriverProtocol: DriverProtocolVersion,
			Headless: true, TemporaryProfile: true},
		URL: "http://127.0.0.1:4173/health", Route: "/health",
		Environment: Environment{Viewport: Viewport{Width: 1280, Height: 720, DPR: 1},
			Locale: "en-US", Theme: ThemeLight, ReducedMotion: true},
		Fixture: Fixture{Name: "regression", Seed: "42", PageState: "ready",
			DataSHA256: digest("fixture"), Deterministic: true, Synthetic: true},
		Steps: []Step{{ID: "navigate", Kind: StepNavigate},
			{ID: "assert", Kind: StepAssertPresent, Selector: "main"}},
		Capture: CapturePolicy{Screenshot: true, DOM: true, Accessibility: true,
			Console: true, Network: true, Performance: true, MaskSelectors: []string{}},
		FailurePolicy: FailurePolicy{FailOnConsoleError: true, FailOnPageError: true,
			FailOnRequestError: true, FailOnHTTPStatus: true},
		CreatedAt: time.Now().UTC()}
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
