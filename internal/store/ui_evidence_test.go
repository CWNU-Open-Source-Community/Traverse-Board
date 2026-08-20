package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/uievidence"
	"cyberagent-workbench/internal/workspacecheckpoint"
)

func TestUIEvidenceStorePersistsImmutableAttemptStepsAndArtifacts(t *testing.T) {
	state, runRecord, mission, _ := newWorkspaceCheckpointStoreFixture(t)
	defer state.Close()
	ctx := context.Background()
	now := time.Date(2026, 8, 19, 6, 0, 0, 0, time.UTC)
	manifest := storeUIEvidenceManifest(t, runRecord.ID, mission.ID,
		runRecord.SessionID, mission.WorkspaceID, "ui-attempt-store", now)
	attempt, err := uievidence.NewAttempt(manifest, "store-operation", now)
	if err != nil {
		t.Fatal(err)
	}
	manifestPayload := map[string]any{}
	manifestJSON, err := json.Marshal(attempt.Manifest)
	if err != nil || json.Unmarshal(manifestJSON, &manifestPayload) != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	delete(manifestPayload["capture"].(map[string]any), "network")
	manifestJSON, err = json.Marshal(manifestPayload)
	if err != nil {
		t.Fatal(err)
	}
	attemptJSON, err := json.Marshal(attempt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.db.ExecContext(ctx, `INSERT INTO ui_evidence_attempts (
		id, protocol_version, operation_digest, request_fingerprint, run_id,
		mission_id, session_id, workspace_id, manifest_fingerprint, source_commit,
		dirty_digest, status, failure_stage, artifact_count, artifact_bytes, version,
		manifest_json, attempt_json, created_at, started_at, completed_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		attempt.Manifest.AttemptID, attempt.ProtocolVersion, attempt.OperationDigest,
		attempt.RequestFingerprint, attempt.Manifest.RunID, attempt.Manifest.MissionID,
		attempt.Manifest.SessionID, attempt.Manifest.WorkspaceID,
		attempt.Manifest.Fingerprint, attempt.Manifest.Source.Commit,
		attempt.Manifest.Source.DirtyDigest, attempt.Status, attempt.FailureStage,
		attempt.ArtifactCount, attempt.ArtifactBytes, attempt.Version,
		string(manifestJSON), string(attemptJSON), ts(attempt.CreatedAt), nil, nil,
		ts(attempt.UpdatedAt)); err == nil {
		t.Fatal("manifest with a missing required JSON field was accepted")
	}
	created, replayed, err := state.CreateUIEvidenceAttempt(ctx, attempt)
	if err != nil || replayed || created.Status != uievidence.StatusNotRun {
		t.Fatalf("created=%+v replayed=%t err=%v", created, replayed, err)
	}
	replayedAttempt, replayed, err := state.CreateUIEvidenceAttempt(ctx, attempt)
	if err != nil || !replayed || replayedAttempt.Manifest.AttemptID != manifest.AttemptID {
		t.Fatalf("replay=%+v replayed=%t err=%v", replayedAttempt, replayed, err)
	}
	started, err := uievidence.StartAttempt(created, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	started, err = state.UpdateUIEvidenceAttempt(ctx, started, created.Version)
	if err != nil {
		t.Fatal(err)
	}

	stepStarted := now.Add(2 * time.Second)
	wrongReceipt, err := uievidence.SealStepReceipt(uievidence.StepReceipt{
		AttemptID: manifest.AttemptID, StepID: "wrong-step", Sequence: 1,
		Kind: uievidence.StepNavigate, Status: uievidence.StatusPassed,
		FailureStage: uievidence.FailureNone, StartedAt: stepStarted,
		CompletedAt: stepStarted.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.AddUIEvidenceStep(ctx, wrongReceipt); err == nil {
		t.Fatal("step outside the immutable manifest was accepted")
	}
	receipt, err := uievidence.SealStepReceipt(uievidence.StepReceipt{
		AttemptID: manifest.AttemptID, StepID: "navigate", Sequence: 1,
		Kind: uievidence.StepNavigate, Status: uievidence.StatusPassed,
		FailureStage: uievidence.FailureNone, StartedAt: stepStarted,
		CompletedAt: stepStarted.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.AddUIEvidenceStep(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	staleArtifact := storeUIEvidenceArtifact(t, manifest, runRecord.ID, receipt.StepID,
		"ui-artifact-stale-time", uievidence.ArtifactDOM, manifest.Environment.Viewport, now)
	if err := state.AddUIEvidenceArtifact(ctx, staleArtifact); err == nil {
		t.Fatal("artifact captured before the running attempt was accepted")
	}
	mismatchedViewport := manifest.Environment.Viewport
	mismatchedViewport.Width++
	mismatched := storeUIEvidenceArtifact(t, manifest, runRecord.ID, receipt.StepID,
		"ui-artifact-bad-viewport", uievidence.ArtifactDOM, mismatchedViewport,
		stepStarted.Add(2*time.Second))
	if err := state.AddUIEvidenceArtifact(ctx, mismatched); err == nil {
		t.Fatal("artifact from a viewport outside the immutable manifest was accepted")
	}
	artifact := storeUIEvidenceArtifact(t, manifest, runRecord.ID, receipt.StepID,
		"ui-artifact-dom", uievidence.ArtifactDOM, manifest.Environment.Viewport,
		stepStarted.Add(2*time.Second))
	if err := state.AddUIEvidenceArtifact(ctx, artifact); err != nil {
		t.Fatal(err)
	}
	loadedArtifact, err := state.GetUIEvidenceArtifact(ctx,
		manifest.AttemptID, artifact.Metadata.ID)
	if err != nil || string(loadedArtifact.Content) != string(artifact.Content) {
		t.Fatalf("artifact=%+v err=%v", loadedArtifact.Metadata, err)
	}

	count, bytes, err := state.UIEvidenceArtifactTotals(ctx, manifest.AttemptID)
	if err != nil || count != 1 || bytes != int64(len(artifact.Content)) {
		t.Fatalf("artifact totals=%d/%d err=%v", count, bytes, err)
	}
	cleanup := uievidence.CleanupReceipt{BrowserTreeReaped: true,
		ApplicationTreeReaped: true, ProfileRemoved: true, NetworkReleased: true,
		PortReleased: true}
	premature, err := uievidence.CompleteAttempt(started, uievidence.StatusPassed,
		uievidence.FailureNone, "", "", uievidence.DiagnosticsSummary{}, cleanup,
		count, bytes, now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.UpdateUIEvidenceAttempt(ctx, premature, started.Version); err == nil {
		t.Fatal("passing attempt without all six core artifacts was accepted")
	}
	for index, kind := range []uievidence.ArtifactKind{
		uievidence.ArtifactScreenshot,
		uievidence.ArtifactAccessibility,
		uievidence.ArtifactConsole,
		uievidence.ArtifactNetwork,
		uievidence.ArtifactPerformance,
	} {
		value := storeUIEvidenceArtifact(t, manifest, runRecord.ID, receipt.StepID,
			"ui-artifact-"+string(kind), kind, manifest.Environment.Viewport,
			stepStarted.Add(time.Duration(index+3)*time.Second))
		if err := state.AddUIEvidenceArtifact(ctx, value); err != nil {
			t.Fatalf("add %s artifact: %v", kind, err)
		}
	}
	count, bytes, err = state.UIEvidenceArtifactTotals(ctx, manifest.AttemptID)
	if err != nil || count != 6 || bytes < 6 {
		t.Fatalf("complete artifact totals=%d/%d err=%v", count, bytes, err)
	}
	fabricated, err := uievidence.CompleteAttempt(started, uievidence.StatusPassed,
		uievidence.FailureNone, "", "", uievidence.DiagnosticsSummary{}, cleanup,
		count+1, bytes+1, now.Add(9*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.UpdateUIEvidenceAttempt(ctx, fabricated, started.Version); err == nil {
		t.Fatal("passing attempt with fabricated artifact totals was accepted")
	}
	completed, err := uievidence.CompleteAttempt(started, uievidence.StatusPassed,
		uievidence.FailureNone, "", "", uievidence.DiagnosticsSummary{}, cleanup,
		count, bytes, now.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	completed, err = state.UpdateUIEvidenceAttempt(ctx, completed, started.Version)
	if err != nil || !completed.Status.Passed() {
		t.Fatalf("completed=%+v err=%v", completed, err)
	}
	if _, err := state.db.ExecContext(ctx, `UPDATE ui_evidence_attempts
		SET status = 'failed', version = version + 1 WHERE id = ?`,
		manifest.AttemptID); err == nil {
		t.Fatal("terminal UI evidence accepted mutation")
	}
	late := storeUIEvidenceArtifact(t, manifest, runRecord.ID, receipt.StepID,
		"ui-artifact-late-dom", uievidence.ArtifactDOM, manifest.Environment.Viewport,
		now.Add(11*time.Second))
	if err := state.AddUIEvidenceArtifact(ctx, late); err == nil {
		t.Fatal("terminal UI evidence accepted a late artifact")
	}
}

func TestUIEvidenceStartupReconciliationNeverTurnsNotRunGreen(t *testing.T) {
	state, runRecord, mission, _ := newWorkspaceCheckpointStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 19, 7, 0, 0, 0, time.UTC)
	manifest := storeUIEvidenceManifest(t, runRecord.ID, mission.ID,
		runRecord.SessionID, mission.WorkspaceID, "ui-attempt-reconcile", now)
	attempt, err := uievidence.NewAttempt(manifest, "reconcile-operation", now)
	if err != nil {
		t.Fatal(err)
	}
	created, _, err := state.CreateUIEvidenceAttempt(t.Context(), attempt)
	if err != nil {
		t.Fatal(err)
	}
	started, err := uievidence.StartAttempt(created, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.UpdateUIEvidenceAttempt(t.Context(), started, created.Version); err != nil {
		t.Fatal(err)
	}
	reconciled, err := state.ReconcileUIEvidenceAttempts(t.Context(), now.Add(time.Minute))
	if err != nil || len(reconciled) != 1 ||
		reconciled[0].Status != uievidence.StatusInterrupted ||
		reconciled[0].Status.Passed() ||
		reconciled[0].FailureStage != uievidence.FailureCleanup {
		t.Fatalf("reconciled=%+v err=%v", reconciled, err)
	}
}

func TestSchemaV119UpgradeAddsUIEvidenceWithoutRewritingV118State(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "ui-evidence-v118.db")
	state, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	workspaceRoot := newWorkspaceCheckpointGitRepository(t)
	workspace := WorkspaceRecord{ID: "workspace-migration-119", Name: "migration-119",
		RootPath: workspaceRoot}
	if err := state.SaveWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	mission, runRecord, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "preserve schema v118 state across v119",
			Profile: "code", WorkspaceID: workspace.ID,
			Budget: domain.Budget{MaxTurns: 2, MaxTokens: 500, MaxToolCalls: 4}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)
	checkpoint := captureStoreCheckpoint(t, runRecord, mission, workspaceRoot,
		"checkpoint-before-v119", "receipt-before-v119",
		workspacecheckpoint.PhaseStandalone, now)
	if _, _, err := state.CreateWorkspaceCheckpoint(ctx, checkpoint); err != nil {
		t.Fatal(err)
	}
	for _, statement := range removeSchemaV119ForTestStatements() {
		if _, err := state.db.ExecContext(ctx, statement); err != nil {
			_ = state.Close()
			t.Fatalf("downgrade v119 with %q: %v", statement, err)
		}
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	loadedRun, err := upgraded.GetRun(ctx, runRecord.ID)
	if err != nil || loadedRun.ID != runRecord.ID {
		t.Fatalf("Run after v119 migration=%+v err=%v", loadedRun, err)
	}
	loadedCheckpoint, err := upgraded.GetWorkspaceCheckpoint(ctx,
		checkpoint.Checkpoint.ID)
	if err != nil || loadedCheckpoint.ManifestSHA256 != checkpoint.Checkpoint.ManifestSHA256 {
		t.Fatalf("v118 checkpoint after v119 migration=%+v err=%v", loadedCheckpoint, err)
	}
	if version, err := upgraded.SchemaVersion(ctx); err != nil ||
		version != LatestSchemaVersion {
		t.Fatalf("schema version=%d err=%v", version, err)
	}
	for _, table := range []string{"ui_evidence_attempts", "ui_evidence_steps",
		"ui_evidence_artifacts"} {
		var count int
		if err := upgraded.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master
			WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("table %s count=%d err=%v", table, count, err)
		}
	}
	attempts, err := upgraded.ListUIEvidenceAttempts(ctx,
		uievidence.ListFilter{RunID: runRecord.ID, Limit: 10})
	if err != nil || len(attempts) != 0 {
		t.Fatalf("v119 fabricated UI evidence: %+v err=%v", attempts, err)
	}
	for name, fragments := range map[string][]string{
		"trg_ui_evidence_attempt_transition":      {"not_run", "running", "interrupted"},
		"trg_ui_evidence_attempt_run_binding":     {"runs run", "run.session_id", "mission.workspace_id"},
		"trg_ui_evidence_attempt_artifact_totals": {"COUNT(*)", "SUM(size_bytes)"},
		"trg_ui_evidence_attempt_pass_complete":   {"status = 'passed'", "COUNT(DISTINCT kind)", "performance"},
		"trg_ui_evidence_attempt_terminal_time":   {"julianday(completed_at)", "julianday(NEW.completed_at)"},
		"trg_ui_evidence_step_insert_running":     {"NEW.sequence - 1", ".id", ".kind"},
		"trg_ui_evidence_artifact_insert_running": {"NEW.kind != 'video'", "viewport.dpr", "source_commit", "ABS(NEW.width"},
		"trg_ui_evidence_artifact_attempt_quota":  {"134217728", "SUM(size_bytes)"},
		"trg_ui_evidence_artifact_store_quota":    {"2147483648", "SUM(size_bytes)"},
	} {
		var triggerSQL string
		if err := upgraded.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master
			WHERE type = 'trigger' AND name = ?`, name).Scan(&triggerSQL); err != nil {
			t.Fatal(err)
		}
		for _, fragment := range fragments {
			if !strings.Contains(triggerSQL, fragment) {
				t.Fatalf("trigger %s does not contain %q: %s", name, fragment, triggerSQL)
			}
		}
	}
}

// removeSchemaV119ForTestStatements restores a v118 database. Older migration
// tests call this through removeSchemaV118ForTestStatements so the downgrade
// chain always removes the newest schema first.
func removeSchemaV119ForTestStatements() []string {
	return append(removeSchemaV120ForTestStatements(), []string{
		`DROP TABLE ui_evidence_artifacts`,
		`DROP TABLE ui_evidence_steps`,
		`DROP TABLE ui_evidence_attempts`,
		`DELETE FROM schema_migrations WHERE version = 119`,
	}...)
}

func storeUIEvidenceManifest(t *testing.T, runID, missionID, sessionID,
	workspaceID, attemptID string, now time.Time,
) uievidence.Manifest {
	t.Helper()
	recipe := uievidence.CommandRecipe{ProtocolVersion: "command-runtime.v2",
		Profile: "process", ExecutableName: "fixture-server.exe",
		ExecutablePathSHA256: storeUIEvidenceDigest("path"),
		ExecutableSHA256:     storeUIEvidenceDigest("executable"),
		CanonicalArgv:        []string{"--port", "4173"}, WorkingDirectory: ".",
		EnvironmentNames: []string{}, EnvironmentSHA256: storeUIEvidenceDigest("environment"),
		TimeoutMilliseconds: 30000, Network: "disabled", Credentials: "none",
		Purpose: "serve deterministic UI fixture"}
	var err error
	recipe, err = uievidence.SealCommandRecipe(recipe)
	if err != nil {
		t.Fatal(err)
	}
	manifest := uievidence.Manifest{AttemptID: attemptID, RunID: runID,
		MissionID: missionID, SessionID: sessionID, WorkspaceID: workspaceID,
		Source: uievidence.SourceBinding{RepositoryKind: "git",
			Commit: "0123456789012345678901234567890123456789", Branch: "main",
			DirtyDigest:     storeUIEvidenceDigest("dirty"),
			RootFingerprint: storeUIEvidenceDigest("root"),
			IndexSHA256:     storeUIEvidenceDigest("index"),
			ManifestSHA256:  storeUIEvidenceDigest("manifest")},
		Start: recipe,
		Readiness: uievidence.Readiness{URL: "http://127.0.0.1:4173/health",
			Method: "GET", ExpectedStatus: []int{200}, TimeoutMilliseconds: 30000,
			IntervalMilliseconds: 100},
		Browser: uievidence.BrowserIdentity{Product: "Chromium", Version: "1.2.3",
			ExecutableSHA256: storeUIEvidenceDigest("browser"),
			DriverProtocol:   uievidence.DriverProtocolVersion, Headless: true,
			TemporaryProfile: true}, URL: "http://127.0.0.1:4173/health", Route: "/health",
		Environment: uievidence.Environment{Viewport: uievidence.Viewport{
			Width: 1280, Height: 720, DPR: 1}, Locale: "en-US",
			Theme: uievidence.ThemeLight, ReducedMotion: true},
		Fixture: uievidence.Fixture{Name: "store fixture", Seed: "42",
			PageState: "ready", DataSHA256: storeUIEvidenceDigest("fixture"),
			Deterministic: true, Synthetic: true},
		Steps: []uievidence.Step{{ID: "navigate", Kind: uievidence.StepNavigate}},
		Capture: uievidence.CapturePolicy{Screenshot: true, DOM: true, Accessibility: true,
			Console: true, Network: true, Performance: true, MaskSelectors: []string{}},
		FailurePolicy: uievidence.FailurePolicy{FailOnConsoleError: true,
			FailOnPageError: true, FailOnRequestError: true, FailOnHTTPStatus: true},
		CreatedAt: now}
	sealed, err := uievidence.SealManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func storeUIEvidenceArtifact(t *testing.T, manifest uievidence.Manifest,
	runID, stepID, artifactID string, kind uievidence.ArtifactKind,
	viewport uievidence.Viewport, createdAt time.Time,
) uievidence.Artifact {
	t.Helper()
	mime := "application/json"
	width, height := 0, 0
	if kind == uievidence.ArtifactScreenshot {
		mime = "image/png"
		width = int(math.Round(float64(viewport.Width) * viewport.DPR))
		height = int(math.Round(float64(viewport.Height) * viewport.DPR))
	}
	content := []byte("bounded " + string(kind) + " evidence")
	artifact, err := uievidence.SealArtifact(uievidence.ArtifactMetadata{
		ID: artifactID, AttemptID: manifest.AttemptID, RunID: runID,
		StepID: stepID, Kind: kind, MIME: mime, Width: width, Height: height,
		Viewport: viewport, SourceCommit: manifest.Source.Commit,
		RetentionPolicy: uievidence.ArtifactRetentionRunHistory, Redacted: true,
		Untrusted: true, CreatedAt: createdAt,
	}, content)
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func storeUIEvidenceDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
