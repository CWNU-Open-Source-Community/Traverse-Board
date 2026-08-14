package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/sandbox"
)

func storeTestDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func newDockerContainerIOStoreAuthority(t *testing.T, ctx context.Context,
	st *SQLiteStore, runID, root, prefix string,
) (sandbox.DockerContainerPlan, sandbox.DockerContainerSpec, sandbox.DockerObservation) {
	t.Helper()
	_, manifest, observation := createDockerContainerPlanStoreAuthority(t, ctx, st,
		runID, root, prefix)
	plan, operation := newDockerContainerPlanStoreRecord(t, ctx, observation, manifest,
		prefix+"-plan")
	if _, _, err := st.CreateDockerContainerPlan(ctx, plan, operation); err != nil {
		t.Fatal(err)
	}
	spec, err := sandbox.CompileDockerContainerSpec(ctx, observation, manifest)
	if err != nil {
		t.Fatal(err)
	}
	return plan, spec, observation
}

func TestDockerContainerIOStoreLedger(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "docker-container-io.db")
	st, run, root := openSandboxManifestStoreAt(t, ctx, path)
	t.Cleanup(func() { _ = st.Close() })
	plan, spec, observation := newDockerContainerIOStoreAuthority(t, ctx, st, run.ID, root,
		"docker-container-io")

	t.Run("input projection", func(t *testing.T) {
		entries := []sandbox.DockerInputProjectionEntry{{
			Ordinal: 1, Path: "data/input.json", SHA256: storeTestDigest("input"),
			SizeBytes: 4, MediaType: "application/json",
		}}
		projection, err := sandbox.NewDockerInputProjection(idgen.New("sandbox-docker-input"),
			idgen.New("sandbox-docker-attempt"), 1, plan.ID, observation.ID, run.ID,
			observation.MissionID, observation.WorkspaceID, observation.InputArtifactDigest,
			spec.SpecFingerprint, spec.AuthorityFingerprint,
			sandbox.DockerInputArtifactMountTarget, entries, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		inserted, err := st.InsertDockerInputProjection(ctx, projection)
		if err != nil || !inserted {
			t.Fatalf("projection insert = %t, %v", inserted, err)
		}
		replayed, err := st.InsertDockerInputProjection(ctx, projection)
		if err != nil || replayed {
			t.Fatalf("projection replay = %t, %v", replayed, err)
		}
	})

	t.Run("log capture receipt", func(t *testing.T) {
		capturePlan, err := sandbox.NewDockerLogCapturePlan(idgen.New("sandbox-docker-attempt"), 1,
			run.ID, storeTestDigest("container"), 1024, 64, 60)
		if err != nil {
			t.Fatal(err)
		}
		records := []sandbox.DockerLogStreamRecord{
			{Stream: "stdout", ByteCount: 6, LineCount: 1, ContentDigest: storeTestDigest("hello")},
			{Stream: "stderr", ContentDigest: storeTestDigest("")},
		}
		receipt, err := sandbox.NewDockerLogCaptureReceipt(idgen.New("sandbox-docker-log"),
			capturePlan.AttemptID, 1, capturePlan.RunID, capturePlan.ContainerIDFingerprint,
			capturePlan, records, sandbox.DockerLogCaptureStatusCompleted, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		inserted, err := st.InsertDockerLogCaptureReceipt(ctx, receipt)
		if err != nil || !inserted {
			t.Fatalf("log receipt insert = %t, %v", inserted, err)
		}
		replayed, err := st.InsertDockerLogCaptureReceipt(ctx, receipt)
		if err != nil || replayed {
			t.Fatalf("log receipt replay = %t, %v", replayed, err)
		}
	})

	t.Run("staging and atomic commit", func(t *testing.T) {
		exportPlan, err := sandbox.NewDockerOutputExportPlan(idgen.New("sandbox-docker-attempt"), 1,
			run.ID, storeTestDigest("container"), "/run/cyberagent/outputs",
			sandbox.MaxDockerOutputFiles, sandbox.MaxDockerOutputFileBytes,
			sandbox.MaxDockerOutputTotalBytes)
		if err != nil {
			t.Fatal(err)
		}
		entry := sandbox.DockerStagedOutputEntry{Path: "report/result.json",
			SHA256: storeTestDigest("result"), SizeBytes: 6, MediaType: "application/json"}
		staging, err := sandbox.NewDockerOutputStagingReceipt(idgen.New("sandbox-docker-staging"),
			exportPlan.AttemptID, 1, exportPlan.RunID, exportPlan.ContainerIDFingerprint,
			exportPlan, []sandbox.DockerStagedOutputEntry{entry},
			sandbox.DockerOutputStagingStatusCompleted, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		inserted, err := st.InsertDockerOutputStagingReceipt(ctx, staging)
		if err != nil || !inserted {
			t.Fatalf("staging insert = %t, %v", inserted, err)
		}
		accepted := []sandbox.DockerOutputCommitEntry{{
			Path: entry.Path, SHA256: entry.SHA256, SizeBytes: entry.SizeBytes,
			MediaType: entry.MediaType,
		}}
		request, err := sandbox.NewDockerOutputCommitRequest(staging.AttemptID, 1, staging.RunID,
			observation.WorkspaceID, staging.ID, storeTestDigest("operation"), accepted)
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := sandbox.NewDockerOutputCommitReceipt(idgen.New("sandbox-docker-commit"),
			request.AttemptID, request.Generation, request.RunID, request.WorkspaceID,
			request, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		committed, err := st.CommitDockerOutputs(ctx, request, receipt)
		if err != nil || !committed {
			t.Fatalf("commit = %t, %v", committed, err)
		}
		replayed, err := st.CommitDockerOutputs(ctx, request, receipt)
		if err != nil || replayed {
			t.Fatalf("commit replay = %t, %v", replayed, err)
		}
	})
}

func removeSchemaV98ForTestStatements() []string {
	return []string{
		`DROP TABLE sandbox_docker_output_commit_entries`,
		`DROP TABLE sandbox_docker_output_commit_receipts`,
		`DROP TABLE sandbox_docker_output_staging_receipts`,
		`DROP TABLE sandbox_docker_log_capture_receipts`,
		`DROP TABLE sandbox_docker_input_projections`,
		`DELETE FROM schema_migrations WHERE version = 98`,
	}
}
