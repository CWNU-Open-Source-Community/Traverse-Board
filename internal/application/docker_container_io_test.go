package application

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/sandbox"
)

func serviceTestDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

type fakeDockerContainerIOStore struct {
	mu              sync.Mutex
	failProjection  bool
	failLogReceipt  bool
	failStaging     bool
	failCommit      bool
	projections     []sandbox.DockerInputProjection
	logReceipts     []sandbox.DockerLogCaptureReceipt
	stagingReceipts []sandbox.DockerOutputStagingReceipt
	commits         []sandbox.DockerOutputCommitReceipt
}

func (store *fakeDockerContainerIOStore) InsertDockerInputProjection(ctx context.Context,
	projection sandbox.DockerInputProjection) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failProjection {
		return false, errors.New("projection store failed")
	}
	for _, existing := range store.projections {
		if existing.ProjectionFingerprint == projection.ProjectionFingerprint {
			return false, nil
		}
	}
	store.projections = append(store.projections, projection)
	return true, nil
}

func (store *fakeDockerContainerIOStore) InsertDockerLogCaptureReceipt(ctx context.Context,
	receipt sandbox.DockerLogCaptureReceipt) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failLogReceipt {
		return false, errors.New("log store failed")
	}
	for _, existing := range store.logReceipts {
		if existing.AttemptID == receipt.AttemptID {
			return false, nil
		}
	}
	store.logReceipts = append(store.logReceipts, receipt)
	return true, nil
}

func (store *fakeDockerContainerIOStore) GetDockerLogCaptureReceiptByAttempt(
	ctx context.Context, attemptID string,
) (sandbox.DockerLogCaptureReceipt, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, receipt := range store.logReceipts {
		if receipt.AttemptID == attemptID {
			return receipt, true, nil
		}
	}
	return sandbox.DockerLogCaptureReceipt{}, false, nil
}

func (store *fakeDockerContainerIOStore) InsertDockerOutputStagingReceipt(ctx context.Context,
	receipt sandbox.DockerOutputStagingReceipt) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failStaging {
		return false, errors.New("staging store failed")
	}
	for _, existing := range store.stagingReceipts {
		if existing.AttemptID == receipt.AttemptID {
			return false, nil
		}
	}
	store.stagingReceipts = append(store.stagingReceipts, receipt)
	return true, nil
}

func (store *fakeDockerContainerIOStore) GetDockerOutputStagingReceiptByAttempt(
	ctx context.Context, attemptID string,
) (sandbox.DockerOutputStagingReceipt, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, receipt := range store.stagingReceipts {
		if receipt.AttemptID == attemptID {
			return receipt, true, nil
		}
	}
	return sandbox.DockerOutputStagingReceipt{}, false, nil
}

func (store *fakeDockerContainerIOStore) CommitDockerOutputs(ctx context.Context,
	request sandbox.DockerOutputCommitRequest, receipt sandbox.DockerOutputCommitReceipt) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failCommit {
		return false, errors.New("commit store failed")
	}
	for _, existing := range store.commits {
		if existing.OperationKeyDigest == receipt.OperationKeyDigest ||
			existing.AttemptID == receipt.AttemptID {
			return false, nil
		}
	}
	store.commits = append(store.commits, receipt)
	return true, nil
}

func (store *fakeDockerContainerIOStore) GetDockerOutputCommitReceiptByAttempt(
	ctx context.Context, attemptID string,
) (sandbox.DockerOutputCommitReceipt, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, receipt := range store.commits {
		if receipt.AttemptID == attemptID {
			return receipt, true, nil
		}
	}
	return sandbox.DockerOutputCommitReceipt{}, false, nil
}

type fakeDockerContainerIOTransport struct {
	mu            sync.Mutex
	failAttach    bool
	failExport    bool
	attachBody    []byte
	exportBody    []byte
	attaches      int
	exports       int
	ownedAttaches int
	ownedExports  int
}

func (transport *fakeDockerContainerIOTransport) Endpoint() sandbox.DockerObservationEndpoint {
	endpoint, _ := sandbox.NewDockerObservationEndpoint(sandbox.DockerObservationEndpointLocalUnix)
	return endpoint
}

func (transport *fakeDockerContainerIOTransport) AttachLogs(ctx context.Context,
	plan sandbox.DockerLogCapturePlan) (io.ReadCloser, error) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.attaches++
	if transport.failAttach {
		return nil, errors.New("attach failed")
	}
	return io.NopCloser(bytes.NewReader(transport.attachBody)), nil
}

func (transport *fakeDockerContainerIOTransport) ExportOutputs(ctx context.Context,
	plan sandbox.DockerOutputExportPlan) (io.ReadCloser, error) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.exports++
	if transport.failExport {
		return nil, errors.New("export failed")
	}
	return io.NopCloser(bytes.NewReader(transport.exportBody)), nil
}

func (transport *fakeDockerContainerIOTransport) AttachOwnedLogs(ctx context.Context,
	request sandbox.DockerContainerLifecycleRequest,
	plan sandbox.DockerLogCapturePlan,
) (io.ReadCloser, error) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.ownedAttaches++
	if transport.failAttach {
		return nil, errors.New("owned attach failed")
	}
	return io.NopCloser(bytes.NewReader(transport.attachBody)), nil
}

func (transport *fakeDockerContainerIOTransport) ExportOwnedOutputs(ctx context.Context,
	request sandbox.DockerContainerLifecycleRequest,
	plan sandbox.DockerOutputExportPlan,
) (io.ReadCloser, error) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.ownedExports++
	if transport.failExport {
		return nil, errors.New("owned export failed")
	}
	return io.NopCloser(bytes.NewReader(transport.exportBody)), nil
}

func dockerLogFramePayload(stream byte, payload string) []byte {
	frame := make([]byte, 8+len(payload))
	frame[0] = stream
	frame[4] = byte(len(payload) >> 24)
	frame[5] = byte(len(payload) >> 16)
	frame[6] = byte(len(payload) >> 8)
	frame[7] = byte(len(payload))
	copy(frame[8:], payload)
	return frame
}

func serviceTestExportPlan(t *testing.T, attemptID string) sandbox.DockerOutputExportPlan {
	t.Helper()
	plan, err := sandbox.NewDockerOutputExportPlan(attemptID, 1,
		idgen.New("sandbox-docker-run"), serviceTestDigest("container"),
		"/run/cyberagent/outputs", sandbox.MaxDockerOutputFiles,
		sandbox.MaxDockerOutputFileBytes, sandbox.MaxDockerOutputTotalBytes)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func serviceTestOwnedLifecycleRequest(t *testing.T,
) sandbox.DockerContainerLifecycleRequest {
	t.Helper()
	_, writeRequest := newDockerLifecycleSupervisorPlan(t, "docker-io-owned")
	ownership, err := sandbox.NewDockerContainerLifecycleOwnership(
		"docker-io-owned-attempt", 1, serviceTestDigest("docker-io-owned-intent"),
		writeRequest.Spec.LabelPlanFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, _ := sandbox.NewDockerObservationEndpoint(
		sandbox.DockerObservationEndpointLocalUnix)
	stage, err := sandbox.NewDockerContainerStageResult(endpoint, writeRequest,
		strings.Repeat("d", 64), false)
	if err != nil {
		t.Fatal(err)
	}
	request, err := sandbox.NewOwnedDockerContainerLifecycleRequest(
		ownership.AttemptID, 1, writeRequest, stage, ownership,
		sandbox.DockerContainerLifecycleConfirmation)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func TestDockerContainerIOServiceFullContractFlow(t *testing.T) {
	store := &fakeDockerContainerIOStore{}
	transport := &fakeDockerContainerIOTransport{}
	service := NewDockerContainerIOService(store, transport)
	ctx := context.Background()
	attemptID := idgen.New("sandbox-docker-attempt")

	t.Run("input projection", func(t *testing.T) {
		projection, err := sandbox.NewDockerInputProjection(idgen.New("sandbox-docker-input"),
			attemptID, 1, idgen.New("sandbox-docker-plan"), idgen.New("sandbox-docker-observation"),
			idgen.New("sandbox-docker-run"), idgen.New("sandbox-docker-mission"),
			idgen.New("sandbox-docker-workspace"), serviceTestDigest("inputs"),
			serviceTestDigest("spec"), serviceTestDigest("authority"),
			sandbox.DockerInputArtifactMountTarget,
			[]sandbox.DockerInputProjectionEntry{{Ordinal: 1, Path: "data/in.json",
				SHA256: serviceTestDigest("in"), SizeBytes: 2, MediaType: "application/json"}},
			time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		inserted, err := service.ProjectInputs(ctx, projection)
		if err != nil || !inserted || len(store.projections) != 1 {
			t.Fatalf("project = %t, %v", inserted, err)
		}
		store.failProjection = true
		if _, err := service.ProjectInputs(ctx, projection); err == nil {
			t.Fatal("store failure was swallowed")
		}
		store.failProjection = false
	})

	t.Run("log capture", func(t *testing.T) {
		transport.attachBody = append(dockerLogFramePayload(1, "hello\n"),
			dockerLogFramePayload(2, "oops\n")...)
		plan, err := sandbox.NewDockerLogCapturePlan(attemptID, 1,
			idgen.New("sandbox-docker-run"), serviceTestDigest("container"), 4096, 64, 30)
		if err != nil {
			t.Fatal(err)
		}
		receipt, inserted, err := service.CaptureLogs(ctx, plan)
		if err != nil || !inserted || receipt.Status != sandbox.DockerLogCaptureStatusCompleted ||
			receipt.TotalBytes != 11 || len(store.logReceipts) != 1 {
			t.Fatalf("capture = %#v, %t, %v", receipt, inserted, err)
		}
		replayedReceipt, replayed, err := service.CaptureLogs(ctx, plan)
		if err != nil || replayed || replayedReceipt.ID != receipt.ID ||
			transport.attaches != 1 {
			t.Fatalf("capture replay = %#v, %t, %v", replayedReceipt, replayed, err)
		}
		transport.failAttach = true
		failurePlan, err := sandbox.NewDockerLogCapturePlan(
			idgen.New("sandbox-docker-attempt"), 1, plan.RunID,
			plan.ContainerIDFingerprint, 4096, 64, 30)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := service.CaptureLogs(ctx, failurePlan); err == nil {
			t.Fatal("attach failure was swallowed")
		}
		transport.failAttach = false
	})

	t.Run("staging and atomic commit", func(t *testing.T) {
		exportPlan := serviceTestExportPlan(t, attemptID)
		transport.exportBody = buildServiceOutputTar(t, map[string]string{
			"report/result.json": "{\"ok\": true}\n",
		})
		stagingRoot := t.TempDir()
		staging, inserted, err := service.StageOutputs(ctx, exportPlan, stagingRoot)
		if err != nil || !inserted || staging.Status != sandbox.DockerOutputStagingStatusCompleted ||
			len(store.stagingReceipts) != 1 || staging.FileCount != 1 {
			t.Fatalf("stage = %#v, %t, %v", staging, inserted, err)
		}
		transport.failExport = true
		replayedStaging, replayed, err := service.StageOutputs(ctx, exportPlan, t.TempDir())
		if err != nil || replayed || replayedStaging.ID != staging.ID ||
			transport.exports != 1 {
			t.Fatalf("staging replay = %#v, %t, %v", replayedStaging, replayed, err)
		}
		transport.failExport = false
		accepted := make([]sandbox.DockerOutputCommitEntry, 0, len(staging.Entries))
		for _, entry := range staging.Entries {
			accepted = append(accepted, sandbox.DockerOutputCommitEntry{
				Path: entry.Path, SHA256: entry.SHA256, SizeBytes: entry.SizeBytes,
				MediaType: entry.MediaType,
			})
		}
		request, err := sandbox.NewDockerOutputCommitRequest(attemptID, 1, staging.RunID,
			idgen.New("sandbox-docker-workspace"), staging.ID, serviceTestDigest("operation"),
			accepted)
		if err != nil {
			t.Fatal(err)
		}
		receipt, committed, err := service.CommitOutputs(ctx, request, staging, stagingRoot)
		if err != nil || !committed || len(store.commits) != 1 || receipt.CommittedCount != 1 {
			t.Fatalf("commit = %#v, %t, %v", receipt, committed, err)
		}
		if err := os.Remove(filepath.Join(stagingRoot,
			filepath.FromSlash(staging.Entries[0].Path))); err != nil {
			t.Fatal(err)
		}
		replayedReceipt, replayed, err := service.CommitOutputs(ctx, request, staging, stagingRoot)
		if err != nil || replayed || len(store.commits) != 1 ||
			replayedReceipt.ID != receipt.ID {
			t.Fatalf("commit replay = %#v, %t, %v", replayedReceipt, replayed, err)
		}

		failurePlan := serviceTestExportPlan(t, idgen.New("sandbox-docker-attempt"))
		failureRoot := t.TempDir()
		failureStaging, inserted, err := service.StageOutputs(ctx, failurePlan, failureRoot)
		if err != nil || !inserted {
			t.Fatalf("failure staging setup = %#v, %t, %v", failureStaging, inserted, err)
		}
		failureAccepted := make([]sandbox.DockerOutputCommitEntry, 0,
			len(failureStaging.Entries))
		for _, entry := range failureStaging.Entries {
			failureAccepted = append(failureAccepted, sandbox.DockerOutputCommitEntry{
				Path: entry.Path, SHA256: entry.SHA256, SizeBytes: entry.SizeBytes,
				MediaType: entry.MediaType,
			})
		}
		failureRequest, err := sandbox.NewDockerOutputCommitRequest(
			failureStaging.AttemptID, 1, failureStaging.RunID,
			idgen.New("sandbox-docker-workspace"), failureStaging.ID,
			serviceTestDigest("failure-operation"), failureAccepted)
		if err != nil {
			t.Fatal(err)
		}
		store.failCommit = true
		if _, _, err := service.CommitOutputs(ctx, failureRequest, failureStaging,
			failureRoot); err == nil {
			t.Fatal("commit failure was swallowed")
		}
		if len(store.commits) != 1 {
			t.Fatal("failed commit left partial rows")
		}
		store.failCommit = false
		transport.failExport = true
		unseenPlan := serviceTestExportPlan(t, idgen.New("sandbox-docker-attempt"))
		if _, _, err := service.StageOutputs(ctx, unseenPlan, t.TempDir()); err == nil {
			t.Fatal("export failure was swallowed")
		}
		transport.failExport = false
	})
}

func TestDockerContainerIOServiceOwnedCaptureAndExport(t *testing.T) {
	store := &fakeDockerContainerIOStore{}
	transport := &fakeDockerContainerIOTransport{}
	service := NewDockerContainerIOService(store, transport)
	request := serviceTestOwnedLifecycleRequest(t)
	transport.attachBody = dockerLogFramePayload(1, "owned\n")
	logPlan, err := sandbox.NewDockerLogCapturePlan(request.AttemptID,
		request.Ownership.ResourceGeneration, request.WriteRequest.Spec.RunID,
		request.ResourceIDFingerprint, 4096, 64, 30)
	if err != nil {
		t.Fatal(err)
	}
	receipt, inserted, err := service.CaptureOwnedLogs(context.Background(),
		request, logPlan)
	if err != nil || !inserted || receipt.TotalBytes != 6 ||
		transport.ownedAttaches != 1 || transport.attaches != 0 {
		t.Fatalf("owned capture = %#v inserted=%t err=%v transport=%#v",
			receipt, inserted, err, transport)
	}

	outputTarget := ""
	for _, mount := range request.WriteRequest.Spec.Mounts {
		if mount.DedicatedOutput {
			outputTarget = mount.Target
			break
		}
	}
	exportPlan, err := sandbox.NewDockerOutputExportPlan(request.AttemptID,
		request.Ownership.ResourceGeneration, request.WriteRequest.Spec.RunID,
		request.ResourceIDFingerprint, outputTarget, sandbox.MaxDockerOutputFiles,
		sandbox.MaxDockerOutputFileBytes, sandbox.MaxDockerOutputTotalBytes)
	if err != nil {
		t.Fatal(err)
	}
	transport.failExport = true
	if _, _, err := service.StageOwnedOutputs(context.Background(), request,
		exportPlan, t.TempDir()); err == nil || transport.ownedExports != 1 ||
		transport.exports != 0 {
		t.Fatalf("owned export failure did not stay on owned transport: err=%v transport=%#v",
			err, transport)
	}
	transport.failExport = false

	if _, _, err := service.CaptureOwnedLogs(context.Background(),
		sandbox.DockerContainerLifecycleRequest{}, logPlan); err == nil ||
		transport.ownedAttaches != 1 {
		t.Fatal("invalid lifecycle request reached the owned transport")
	}
}

func TestDockerContainerIOServiceConcurrentReplayReturnsPersistedReceipts(t *testing.T) {
	const workers = 8
	ctx := context.Background()
	store := &fakeDockerContainerIOStore{}
	transport := &fakeDockerContainerIOTransport{
		attachBody: dockerLogFramePayload(1, "concurrent\n"),
		exportBody: buildServiceOutputTar(t, map[string]string{
			"concurrent/result.json": "{\"ok\":true}\n",
		}),
	}
	service := NewDockerContainerIOService(store, transport)

	logPlan, err := sandbox.NewDockerLogCapturePlan(idgen.New("sandbox-docker-attempt"),
		1, idgen.New("sandbox-docker-run"), serviceTestDigest("concurrent-container"),
		4096, 64, 30)
	if err != nil {
		t.Fatal(err)
	}
	type logResult struct {
		receipt  sandbox.DockerLogCaptureReceipt
		inserted bool
		err      error
	}
	logResults := make(chan logResult, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			receipt, inserted, err := service.CaptureLogs(ctx, logPlan)
			logResults <- logResult{receipt: receipt, inserted: inserted, err: err}
		}()
	}
	group.Wait()
	close(logResults)
	logID, logInserts := "", 0
	for result := range logResults {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if logID == "" {
			logID = result.receipt.ID
		}
		if result.receipt.ID != logID {
			t.Fatalf("concurrent log replay returned candidate IDs %q and %q",
				logID, result.receipt.ID)
		}
		if result.inserted {
			logInserts++
		}
	}
	if logInserts != 1 || len(store.logReceipts) != 1 ||
		store.logReceipts[0].ID != logID {
		t.Fatalf("concurrent log persistence inserts=%d receipts=%#v", logInserts,
			store.logReceipts)
	}

	exportPlan := serviceTestExportPlan(t, idgen.New("sandbox-docker-attempt"))
	type stagingResult struct {
		receipt  sandbox.DockerOutputStagingReceipt
		inserted bool
		err      error
	}
	stagingResults := make(chan stagingResult, workers)
	stagingRoots := make([]string, workers)
	for index := range workers {
		stagingRoots[index] = t.TempDir()
		group.Add(1)
		go func(root string) {
			defer group.Done()
			receipt, inserted, err := service.StageOutputs(ctx, exportPlan, root)
			stagingResults <- stagingResult{receipt: receipt, inserted: inserted, err: err}
		}(stagingRoots[index])
	}
	group.Wait()
	close(stagingResults)
	var staging sandbox.DockerOutputStagingReceipt
	stagingInserts := 0
	for result := range stagingResults {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if staging.ID == "" {
			staging = result.receipt
		}
		if result.receipt.ID != staging.ID {
			t.Fatalf("concurrent staging replay returned candidate IDs %q and %q",
				staging.ID, result.receipt.ID)
		}
		if result.inserted {
			stagingInserts++
		}
	}
	if stagingInserts != 1 || len(store.stagingReceipts) != 1 ||
		store.stagingReceipts[0].ID != staging.ID {
		t.Fatalf("concurrent staging persistence inserts=%d receipts=%#v",
			stagingInserts, store.stagingReceipts)
	}

	accepted := make([]sandbox.DockerOutputCommitEntry, 0, len(staging.Entries))
	for _, entry := range staging.Entries {
		accepted = append(accepted, sandbox.DockerOutputCommitEntry{
			Path: entry.Path, SHA256: entry.SHA256, SizeBytes: entry.SizeBytes,
			MediaType: entry.MediaType,
		})
	}
	commitRequest, err := sandbox.NewDockerOutputCommitRequest(staging.AttemptID, 1,
		staging.RunID, idgen.New("sandbox-docker-workspace"), staging.ID,
		serviceTestDigest("concurrent-commit"), accepted)
	if err != nil {
		t.Fatal(err)
	}
	type commitResult struct {
		receipt   sandbox.DockerOutputCommitReceipt
		committed bool
		err       error
	}
	commitResults := make(chan commitResult, workers)
	commitRoot := ""
	for _, root := range stagingRoots {
		if _, err := os.Stat(filepath.Join(root,
			filepath.FromSlash(staging.Entries[0].Path))); err == nil {
			commitRoot = root
			break
		}
	}
	if commitRoot == "" {
		t.Fatal("concurrent staging left no caller-owned files for commit")
	}
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			receipt, committed, err := service.CommitOutputs(ctx, commitRequest,
				staging, commitRoot)
			commitResults <- commitResult{receipt: receipt, committed: committed, err: err}
		}()
	}
	group.Wait()
	close(commitResults)
	commitID, commitInserts := "", 0
	for result := range commitResults {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if commitID == "" {
			commitID = result.receipt.ID
		}
		if result.receipt.ID != commitID {
			t.Fatalf("concurrent commit replay returned candidate IDs %q and %q",
				commitID, result.receipt.ID)
		}
		if result.committed {
			commitInserts++
		}
	}
	if commitInserts != 1 || len(store.commits) != 1 || store.commits[0].ID != commitID {
		t.Fatalf("concurrent commit persistence inserts=%d receipts=%#v",
			commitInserts, store.commits)
	}
}

func buildServiceOutputTar(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for name, content := range files {
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o644,
			Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestDockerContainerIOServiceUnavailableTransport(t *testing.T) {
	store := &fakeDockerContainerIOStore{}
	transport := sandbox.NewUnavailableDockerContainerIOTransport("unavailable", "unsupported")
	service := NewDockerContainerIOService(store, transport)
	plan, err := sandbox.NewDockerLogCapturePlan(idgen.New("sandbox-docker-attempt"), 1,
		idgen.New("sandbox-docker-run"), serviceTestDigest("container"), 1024, 64, 60)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.CaptureLogs(context.Background(), plan); err == nil {
		t.Fatal("unavailable attach succeeded")
	}
	if _, _, err := service.StageOutputs(context.Background(),
		serviceTestExportPlan(t, idgen.New("sandbox-docker-attempt")), t.TempDir()); err == nil {
		t.Fatal("unavailable export succeeded")
	}
}
