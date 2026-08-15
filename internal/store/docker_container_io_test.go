package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/events"
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
		stored, found, err := st.GetDockerLogCaptureReceiptByAttempt(ctx,
			receipt.AttemptID)
		if err != nil || !found || stored.Validate() != nil || stored.ID != receipt.ID ||
			stored.ReceiptFingerprint != receipt.ReceiptFingerprint ||
			len(stored.Streams) != 2 || stored.Streams[0] != receipt.Streams[0] ||
			stored.Streams[1] != receipt.Streams[1] {
			t.Fatalf("stored log receipt = %#v found=%t err=%v", stored, found, err)
		}
		candidate, err := sandbox.NewDockerLogCaptureReceipt(
			idgen.New("sandbox-docker-log"), capturePlan.AttemptID, 1,
			capturePlan.RunID, capturePlan.ContainerIDFingerprint, capturePlan,
			records, sandbox.DockerLogCaptureStatusCompleted,
			time.Now().UTC().Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if inserted, err := st.InsertDockerLogCaptureReceipt(ctx, candidate); err != nil || inserted {
			t.Fatalf("same-attempt log candidate = %t, %v", inserted, err)
		}
		stored, found, err = st.GetDockerLogCaptureReceiptByAttempt(ctx, receipt.AttemptID)
		if err != nil || !found || stored.ID != receipt.ID || stored.ID == candidate.ID {
			t.Fatalf("log replay returned non-durable candidate: %#v err=%v", stored, err)
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
		storedStaging, found, err := st.GetDockerOutputStagingReceiptByAttempt(ctx,
			staging.AttemptID)
		if err != nil || !found || storedStaging.Validate() != nil ||
			storedStaging.ID != staging.ID || len(storedStaging.Entries) != 1 ||
			storedStaging.Entries[0] != staging.Entries[0] {
			t.Fatalf("stored staging receipt = %#v found=%t err=%v",
				storedStaging, found, err)
		}
		stagingCandidate, err := sandbox.NewDockerOutputStagingReceipt(
			idgen.New("sandbox-docker-staging"), exportPlan.AttemptID, 1,
			exportPlan.RunID, exportPlan.ContainerIDFingerprint, exportPlan,
			[]sandbox.DockerStagedOutputEntry{entry},
			sandbox.DockerOutputStagingStatusCompleted,
			time.Now().UTC().Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if inserted, err := st.InsertDockerOutputStagingReceipt(ctx,
			stagingCandidate); err != nil || inserted {
			t.Fatalf("same-attempt staging candidate = %t, %v", inserted, err)
		}
		storedStaging, found, err = st.GetDockerOutputStagingReceiptByAttempt(ctx,
			staging.AttemptID)
		if err != nil || !found || storedStaging.ID != staging.ID ||
			storedStaging.ID == stagingCandidate.ID {
			t.Fatalf("staging replay returned non-durable candidate: %#v err=%v",
				storedStaging, err)
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
		storedCommit, found, err := st.GetDockerOutputCommitReceiptByAttempt(ctx,
			receipt.AttemptID)
		if err != nil || !found || storedCommit.Validate() != nil ||
			storedCommit.ID != receipt.ID || len(storedCommit.Entries) != 1 ||
			storedCommit.Entries[0] != receipt.Entries[0] {
			t.Fatalf("stored commit receipt = %#v found=%t err=%v",
				storedCommit, found, err)
		}
		commitCandidate, err := sandbox.NewDockerOutputCommitReceipt(
			idgen.New("sandbox-docker-commit"), request.AttemptID,
			request.Generation, request.RunID, request.WorkspaceID, request,
			time.Now().UTC().Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if committed, err := st.CommitDockerOutputs(ctx, request,
			commitCandidate); err != nil || committed {
			t.Fatalf("same-attempt commit candidate = %t, %v", committed, err)
		}
		storedCommit, found, err = st.GetDockerOutputCommitReceiptByAttempt(ctx,
			receipt.AttemptID)
		if err != nil || !found || storedCommit.ID != receipt.ID ||
			storedCommit.ID == commitCandidate.ID {
			t.Fatalf("commit replay returned non-durable candidate: %#v err=%v",
				storedCommit, err)
		}
	})

	t.Run("missing receipts", func(t *testing.T) {
		attemptID := idgen.New("sandbox-docker-attempt")
		if _, found, err := st.GetDockerLogCaptureReceiptByAttempt(ctx,
			attemptID); err != nil || found {
			t.Fatalf("missing log receipt found=%t err=%v", found, err)
		}
		if _, found, err := st.GetDockerOutputStagingReceiptByAttempt(ctx,
			attemptID); err != nil || found {
			t.Fatalf("missing staging receipt found=%t err=%v", found, err)
		}
		if _, found, err := st.GetDockerOutputCommitReceiptByAttempt(ctx,
			attemptID); err != nil || found {
			t.Fatalf("missing commit receipt found=%t err=%v", found, err)
		}
	})

	t.Run("receipt events are idempotent", func(t *testing.T) {
		timeline, err := st.ListRunEvents(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, eventType := range []string{
			events.SandboxDockerLogCaptureCompletedEvent,
			events.SandboxDockerOutputStagingCompletedEvent,
			events.SandboxDockerOutputCommitCompletedEvent,
		} {
			if count := countRunEventType(timeline, eventType); count != 1 {
				t.Fatalf("%s event count = %d", eventType, count)
			}
		}
	})
}

func TestDockerOutputStagingGetterFailsClosedWithoutDurableEntries(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "docker-container-io-missing-entries.db")
	st, run, _ := openSandboxManifestStoreAt(t, ctx, path)
	t.Cleanup(func() { _ = st.Close() })
	exportPlan, err := sandbox.NewDockerOutputExportPlan(
		idgen.New("sandbox-docker-attempt"), 1, run.ID,
		storeTestDigest("container"), "/run/cyberagent/outputs",
		sandbox.MaxDockerOutputFiles, sandbox.MaxDockerOutputFileBytes,
		sandbox.MaxDockerOutputTotalBytes)
	if err != nil {
		t.Fatal(err)
	}
	entry := sandbox.DockerStagedOutputEntry{Path: "report/result.json",
		SHA256: storeTestDigest("result"), SizeBytes: 6, MediaType: "application/json"}
	receipt, err := sandbox.NewDockerOutputStagingReceipt(
		idgen.New("sandbox-docker-staging"), exportPlan.AttemptID, 1,
		exportPlan.RunID, exportPlan.ContainerIDFingerprint, exportPlan,
		[]sandbox.DockerStagedOutputEntry{entry},
		sandbox.DockerOutputStagingStatusCompleted, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, insertDockerOutputStagingReceiptSQL,
		receipt.ID, receipt.AttemptID, receipt.Generation, receipt.RunID,
		receipt.ContainerIDFingerprint, receipt.ProtocolVersion, receipt.Status,
		receipt.FileCount, receipt.TotalBytes, receipt.RedactedCount,
		receipt.EntryDigestSet, receipt.ExportFingerprint, receipt.ReceiptFingerprint,
		ts(receipt.CreatedAt)); err != nil {
		t.Fatal(err)
	}
	if _, found, err := st.GetDockerOutputStagingReceiptByAttempt(ctx,
		receipt.AttemptID); found || apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("missing durable staging entries found=%t err=%v", found, err)
	}

	emptyPlan, err := sandbox.NewDockerOutputExportPlan(
		idgen.New("sandbox-docker-attempt"), 1, run.ID,
		storeTestDigest("empty-container"), "/run/cyberagent/outputs",
		sandbox.MaxDockerOutputFiles, sandbox.MaxDockerOutputFileBytes,
		sandbox.MaxDockerOutputTotalBytes)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := sandbox.NewDockerOutputStagingReceipt(
		idgen.New("sandbox-docker-staging"), emptyPlan.AttemptID, 1,
		emptyPlan.RunID, emptyPlan.ContainerIDFingerprint, emptyPlan, nil,
		sandbox.DockerOutputStagingStatusCompleted, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if inserted, err := st.InsertDockerOutputStagingReceipt(ctx, empty); err != nil || !inserted {
		t.Fatalf("empty staging insert=%t err=%v", inserted, err)
	}
	stored, found, err := st.GetDockerOutputStagingReceiptByAttempt(ctx, empty.AttemptID)
	if err != nil || !found || stored.Validate() != nil || stored.ID != empty.ID ||
		len(stored.Entries) != 0 {
		t.Fatalf("empty staging replay=%#v found=%t err=%v", stored, found, err)
	}
}

func TestDockerContainerIOStoreConcurrentAttemptUniqueness(t *testing.T) {
	const workers = 8
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "docker-container-io-concurrent.db")
	st, run, root := openSandboxManifestStoreAt(t, ctx, path)
	t.Cleanup(func() { _ = st.Close() })
	_, _, observation := newDockerContainerIOStoreAuthority(t, ctx, st, run.ID, root,
		"docker-container-io-concurrent")

	logPlan, err := sandbox.NewDockerLogCapturePlan(idgen.New("sandbox-docker-attempt"),
		1, run.ID, storeTestDigest("log-container"), 1024, 64, 60)
	if err != nil {
		t.Fatal(err)
	}
	streams := []sandbox.DockerLogStreamRecord{
		{Stream: "stdout", ByteCount: 3, LineCount: 1,
			ContentDigest: storeTestDigest("log")},
		{Stream: "stderr", ContentDigest: storeTestDigest("")},
	}
	logCandidates := make([]sandbox.DockerLogCaptureReceipt, workers)
	for index := range workers {
		logCandidates[index], err = sandbox.NewDockerLogCaptureReceipt(
			idgen.New("sandbox-docker-log"), logPlan.AttemptID, 1, logPlan.RunID,
			logPlan.ContainerIDFingerprint, logPlan, streams,
			sandbox.DockerLogCaptureStatusCompleted,
			time.Now().UTC().Add(time.Duration(index)*time.Nanosecond))
		if err != nil {
			t.Fatal(err)
		}
	}
	logResults := make(chan bool, workers)
	logErrors := make(chan error, workers)
	var group sync.WaitGroup
	for index := range workers {
		group.Add(1)
		go func(receipt sandbox.DockerLogCaptureReceipt) {
			defer group.Done()
			inserted, err := st.InsertDockerLogCaptureReceipt(ctx, receipt)
			logResults <- inserted
			logErrors <- err
		}(logCandidates[index])
	}
	group.Wait()
	close(logResults)
	close(logErrors)
	logInserts := 0
	for inserted := range logResults {
		if inserted {
			logInserts++
		}
	}
	for err := range logErrors {
		if err != nil {
			t.Fatal(err)
		}
	}
	storedLog, found, err := st.GetDockerLogCaptureReceiptByAttempt(ctx, logPlan.AttemptID)
	if err != nil || !found || logInserts != 1 || storedLog.Validate() != nil {
		t.Fatalf("concurrent logs inserts=%d stored=%#v found=%t err=%v",
			logInserts, storedLog, found, err)
	}

	exportPlan, err := sandbox.NewDockerOutputExportPlan(
		idgen.New("sandbox-docker-attempt"), 1, run.ID,
		storeTestDigest("staging-container"), "/run/cyberagent/outputs",
		sandbox.MaxDockerOutputFiles, sandbox.MaxDockerOutputFileBytes,
		sandbox.MaxDockerOutputTotalBytes)
	if err != nil {
		t.Fatal(err)
	}
	stagedEntry := sandbox.DockerStagedOutputEntry{Path: "report/result.json",
		SHA256: storeTestDigest("result"), SizeBytes: 6, MediaType: "application/json"}
	stagingCandidates := make([]sandbox.DockerOutputStagingReceipt, workers)
	for index := range workers {
		stagingCandidates[index], err = sandbox.NewDockerOutputStagingReceipt(
			idgen.New("sandbox-docker-staging"), exportPlan.AttemptID, 1,
			exportPlan.RunID, exportPlan.ContainerIDFingerprint, exportPlan,
			[]sandbox.DockerStagedOutputEntry{stagedEntry},
			sandbox.DockerOutputStagingStatusCompleted,
			time.Now().UTC().Add(time.Duration(index)*time.Nanosecond))
		if err != nil {
			t.Fatal(err)
		}
	}
	stagingResults := make(chan bool, workers)
	stagingErrors := make(chan error, workers)
	for index := range workers {
		group.Add(1)
		go func(receipt sandbox.DockerOutputStagingReceipt) {
			defer group.Done()
			inserted, err := st.InsertDockerOutputStagingReceipt(ctx, receipt)
			stagingResults <- inserted
			stagingErrors <- err
		}(stagingCandidates[index])
	}
	group.Wait()
	close(stagingResults)
	close(stagingErrors)
	stagingInserts := 0
	for inserted := range stagingResults {
		if inserted {
			stagingInserts++
		}
	}
	for err := range stagingErrors {
		if err != nil {
			t.Fatal(err)
		}
	}
	storedStaging, found, err := st.GetDockerOutputStagingReceiptByAttempt(ctx,
		exportPlan.AttemptID)
	if err != nil || !found || stagingInserts != 1 || storedStaging.Validate() != nil ||
		len(storedStaging.Entries) != 1 {
		t.Fatalf("concurrent staging inserts=%d stored=%#v found=%t err=%v",
			stagingInserts, storedStaging, found, err)
	}

	accepted := []sandbox.DockerOutputCommitEntry{{Path: stagedEntry.Path,
		SHA256: stagedEntry.SHA256, SizeBytes: stagedEntry.SizeBytes,
		MediaType: stagedEntry.MediaType}}
	request, err := sandbox.NewDockerOutputCommitRequest(exportPlan.AttemptID, 1,
		run.ID, observation.WorkspaceID, storedStaging.ID,
		storeTestDigest("concurrent-operation"), accepted)
	if err != nil {
		t.Fatal(err)
	}
	commitCandidates := make([]sandbox.DockerOutputCommitReceipt, workers)
	for index := range workers {
		commitCandidates[index], err = sandbox.NewDockerOutputCommitReceipt(
			idgen.New("sandbox-docker-commit"), request.AttemptID, 1,
			request.RunID, request.WorkspaceID, request,
			time.Now().UTC().Add(time.Duration(index)*time.Nanosecond))
		if err != nil {
			t.Fatal(err)
		}
	}
	commitResults := make(chan bool, workers)
	commitErrors := make(chan error, workers)
	for index := range workers {
		group.Add(1)
		go func(receipt sandbox.DockerOutputCommitReceipt) {
			defer group.Done()
			committed, err := st.CommitDockerOutputs(ctx, request, receipt)
			commitResults <- committed
			commitErrors <- err
		}(commitCandidates[index])
	}
	group.Wait()
	close(commitResults)
	close(commitErrors)
	commitInserts := 0
	for committed := range commitResults {
		if committed {
			commitInserts++
		}
	}
	for err := range commitErrors {
		if err != nil {
			t.Fatal(err)
		}
	}
	storedCommit, found, err := st.GetDockerOutputCommitReceiptByAttempt(ctx,
		request.AttemptID)
	if err != nil || !found || commitInserts != 1 || storedCommit.Validate() != nil ||
		len(storedCommit.Entries) != 1 {
		t.Fatalf("concurrent commits inserts=%d stored=%#v found=%t err=%v",
			commitInserts, storedCommit, found, err)
	}
}

func TestDockerContainerIOReceiptEventsAreAtomicAndMetadataOnly(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "docker-container-io-events.db")
	st, run, root := openSandboxManifestStoreAt(t, ctx, path)
	t.Cleanup(func() { _ = st.Close() })
	_, _, observation := newDockerContainerIOStoreAuthority(t, ctx, st, run.ID, root,
		"docker-container-io-events")
	attemptID := idgen.New("sandbox-docker-attempt")
	containerFingerprint := storeTestDigest("private-container-id")

	logPlan, err := sandbox.NewDockerLogCapturePlan(attemptID, 1, run.ID,
		containerFingerprint, 1024, 64, 60)
	if err != nil {
		t.Fatal(err)
	}
	streams := []sandbox.DockerLogStreamRecord{
		{Stream: "stdout", ByteCount: 1024, LineCount: 1, TruncatedBytes: true,
			RedactedSegments: 1, ContentDigest: storeTestDigest("raw-log-secret")},
		{Stream: "stderr", ContentDigest: storeTestDigest("")},
	}
	logReceipt, err := sandbox.NewDockerLogCaptureReceipt(
		idgen.New("sandbox-docker-log"), attemptID, 1, run.ID,
		containerFingerprint, logPlan, streams,
		sandbox.DockerLogCaptureStatusTruncatedBytes, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	exportPlan, err := sandbox.NewDockerOutputExportPlan(attemptID, 1, run.ID,
		containerFingerprint, "/run/cyberagent/outputs", sandbox.MaxDockerOutputFiles,
		sandbox.MaxDockerOutputFileBytes, sandbox.MaxDockerOutputTotalBytes)
	if err != nil {
		t.Fatal(err)
	}
	stagedEntry := sandbox.DockerStagedOutputEntry{Path: "private/result.json",
		SHA256: storeTestDigest("private-output"), SizeBytes: 14,
		MediaType: "application/json", Redacted: true}
	stagingReceipt, err := sandbox.NewDockerOutputStagingReceipt(
		idgen.New("sandbox-docker-staging"), attemptID, 1, run.ID,
		containerFingerprint, exportPlan, []sandbox.DockerStagedOutputEntry{stagedEntry},
		sandbox.DockerOutputStagingStatusCompleted, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	accepted := []sandbox.DockerOutputCommitEntry{{Path: stagedEntry.Path,
		SHA256: stagedEntry.SHA256, SizeBytes: stagedEntry.SizeBytes,
		MediaType: stagedEntry.MediaType}}
	commitRequest, err := sandbox.NewDockerOutputCommitRequest(attemptID, 1, run.ID,
		observation.WorkspaceID, stagingReceipt.ID,
		storeTestDigest("private-operation-key"), accepted)
	if err != nil {
		t.Fatal(err)
	}
	commitReceipt, err := sandbox.NewDockerOutputCommitReceipt(
		idgen.New("sandbox-docker-commit"), attemptID, 1, run.ID,
		observation.WorkspaceID, commitRequest, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := st.db.ExecContext(ctx, `CREATE TRIGGER fail_docker_container_io_event
		BEFORE INSERT ON run_events
		WHEN NEW.type IN ('sandbox.docker_log_capture_completed',
			'sandbox.docker_output_staging_completed',
			'sandbox.docker_output_commit_completed')
		BEGIN SELECT RAISE(ABORT, 'forced Docker I/O event failure'); END`); err != nil {
		t.Fatal(err)
	}
	if inserted, err := st.InsertDockerLogCaptureReceipt(ctx, logReceipt); err == nil || inserted {
		t.Fatalf("log receipt survived event failure: inserted=%t err=%v", inserted, err)
	}
	if inserted, err := st.InsertDockerOutputStagingReceipt(ctx,
		stagingReceipt); err == nil || inserted {
		t.Fatalf("staging receipt survived event failure: inserted=%t err=%v", inserted, err)
	}
	if committed, err := st.CommitDockerOutputs(ctx, commitRequest,
		commitReceipt); err == nil || committed {
		t.Fatalf("commit receipt survived event failure: committed=%t err=%v", committed, err)
	}
	for name, query := range map[string]string{
		"log receipt":     `SELECT COUNT(*) FROM sandbox_docker_log_capture_receipts WHERE id = ?`,
		"staging receipt": `SELECT COUNT(*) FROM sandbox_docker_output_staging_receipts WHERE id = ?`,
		"staging entry":   `SELECT COUNT(*) FROM sandbox_docker_output_staging_entries WHERE receipt_id = ?`,
		"commit receipt":  `SELECT COUNT(*) FROM sandbox_docker_output_commit_receipts WHERE id = ?`,
		"commit entry":    `SELECT COUNT(*) FROM sandbox_docker_output_commit_entries WHERE receipt_id = ?`,
	} {
		id := stagingReceipt.ID
		if strings.HasPrefix(name, "log") {
			id = logReceipt.ID
		} else if strings.HasPrefix(name, "commit") {
			id = commitReceipt.ID
		}
		var count int
		if err := st.db.QueryRowContext(ctx, query, id).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s rollback count=%d err=%v", name, count, err)
		}
	}
	if _, err := st.db.ExecContext(ctx, `DROP TRIGGER fail_docker_container_io_event`); err != nil {
		t.Fatal(err)
	}

	if inserted, err := st.InsertDockerLogCaptureReceipt(ctx, logReceipt); err != nil || !inserted {
		t.Fatalf("log receipt after rollback inserted=%t err=%v", inserted, err)
	}
	if inserted, err := st.InsertDockerOutputStagingReceipt(ctx,
		stagingReceipt); err != nil || !inserted {
		t.Fatalf("staging receipt after rollback inserted=%t err=%v", inserted, err)
	}
	if committed, err := st.CommitDockerOutputs(ctx, commitRequest,
		commitReceipt); err != nil || !committed {
		t.Fatalf("commit receipt after rollback committed=%t err=%v", committed, err)
	}

	for _, assertion := range []struct {
		eventType string
		subjectID string
		keys      []string
	}{
		{events.SandboxDockerLogCaptureCompletedEvent, logReceipt.ID,
			[]string{"status", "stream_count", "total_bytes", "total_lines", "truncated",
				"utf8_violation_count", "redacted_segment_count"}},
		{events.SandboxDockerOutputStagingCompletedEvent, stagingReceipt.ID,
			[]string{"status", "file_count", "total_bytes", "redacted_count", "truncated"}},
		{events.SandboxDockerOutputCommitCompletedEvent, commitReceipt.ID,
			[]string{"status", "committed_count", "total_bytes"}},
	} {
		event := requireDockerContainerIOEvent(t, st, run.ID, assertion.eventType,
			assertion.subjectID)
		var payload map[string]any
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload) != len(assertion.keys) {
			t.Fatalf("%s payload keys=%v", assertion.eventType, payload)
		}
		for _, key := range assertion.keys {
			if _, found := payload[key]; !found {
				t.Fatalf("%s metadata key %q is missing: %v", assertion.eventType,
					key, payload)
			}
		}
		for _, forbidden := range []string{
			attemptID, containerFingerprint, streams[0].ContentDigest,
			logPlan.CaptureFingerprint, stagedEntry.Path, stagedEntry.SHA256,
			stagingReceipt.EntryDigestSet, exportPlan.ExportFingerprint,
			commitRequest.OperationKeyDigest, commitRequest.RequestFingerprint,
			commitReceipt.CommittedDigestSet, root,
		} {
			if forbidden != "" && strings.Contains(event.PayloadJSON, forbidden) {
				t.Fatalf("%s leaked sensitive value %q in %s", assertion.eventType,
					forbidden, event.PayloadJSON)
			}
		}
	}
}

func requireDockerContainerIOEvent(t *testing.T, st *SQLiteStore, runID,
	eventType, subjectID string,
) events.Event {
	t.Helper()
	timeline, err := st.ListRunEvents(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	var matches []events.Event
	for _, event := range timeline {
		if event.Type == eventType {
			matches = append(matches, event)
		}
	}
	if len(matches) != 1 || matches[0].Source != "sandbox_docker_container_io" ||
		matches[0].SubjectID != subjectID {
		t.Fatalf("%s events=%#v", eventType, matches)
	}
	return matches[0]
}

func removeSchemaV98ForTestStatements() []string {
	return append(removeSchemaV99ForTestStatements(), []string{
		`DROP TABLE sandbox_docker_output_commit_entries`,
		`DROP TABLE sandbox_docker_output_commit_receipts`,
		`DROP TABLE sandbox_docker_output_staging_receipts`,
		`DROP TABLE sandbox_docker_log_capture_receipts`,
		`DROP TABLE sandbox_docker_input_projections`,
		`DELETE FROM schema_migrations WHERE version = 98`,
	}...)
}

func removeSchemaV99ForTestStatements() []string {
	return append(removeSchemaV100ForTestStatements(), []string{
		`DROP TABLE sandbox_docker_product_receipts`,
		`DROP TABLE sandbox_docker_product_launches`,
		`DROP TABLE sandbox_docker_product_start_requests`,
		`DROP TABLE sandbox_docker_product_cancellations`,
		`DROP TABLE sandbox_docker_product_admissions`,
		`DROP TABLE sandbox_docker_output_staging_entries`,
		`DROP INDEX idx_sandbox_docker_output_commit_receipts_attempt_v99`,
		`DROP INDEX idx_sandbox_docker_output_staging_receipts_attempt_v99`,
		`DROP INDEX idx_sandbox_docker_log_capture_receipts_attempt_v99`,
		`DELETE FROM schema_migrations WHERE version = 99`,
	}...)
}

func removeSchemaV100ForTestStatements() []string {
	return append(removeSchemaV101ForTestStatements(), []string{
		`DROP TABLE run_monetary_reservations`,
		`DROP TABLE run_monetary_usage`,
		`DROP TABLE provider_price_snapshots`,
		`DELETE FROM schema_migrations WHERE version = 100`,
	}...)
}

func removeSchemaV101ForTestStatements() []string {
	return append(removeSchemaV102ForTestStatements(), []string{
		`DROP TABLE agent_dependency_edge_operations`,
		`DROP TABLE agent_dependency_wakes`,
		`DROP TABLE agent_dependency_edges`,
		`DELETE FROM schema_migrations WHERE version = 101`,
	}...)
}
