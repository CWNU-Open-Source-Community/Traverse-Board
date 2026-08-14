package application

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
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
	if store.failLogReceipt {
		return false, errors.New("log store failed")
	}
	store.logReceipts = append(store.logReceipts, receipt)
	return true, nil
}

func (store *fakeDockerContainerIOStore) InsertDockerOutputStagingReceipt(ctx context.Context,
	receipt sandbox.DockerOutputStagingReceipt) (bool, error) {
	if store.failStaging {
		return false, errors.New("staging store failed")
	}
	store.stagingReceipts = append(store.stagingReceipts, receipt)
	return true, nil
}

func (store *fakeDockerContainerIOStore) CommitDockerOutputs(ctx context.Context,
	request sandbox.DockerOutputCommitRequest, receipt sandbox.DockerOutputCommitReceipt) (bool, error) {
	if store.failCommit {
		return false, errors.New("commit store failed")
	}
	for _, existing := range store.commits {
		if existing.OperationKeyDigest == receipt.OperationKeyDigest {
			return false, nil
		}
	}
	store.commits = append(store.commits, receipt)
	return true, nil
}

type fakeDockerContainerIOTransport struct {
	failAttach bool
	failExport bool
	attachBody []byte
	exportBody []byte
	attaches   int
	exports    int
}

func (transport *fakeDockerContainerIOTransport) Endpoint() sandbox.DockerObservationEndpoint {
	endpoint, _ := sandbox.NewDockerObservationEndpoint(sandbox.DockerObservationEndpointLocalUnix)
	return endpoint
}

func (transport *fakeDockerContainerIOTransport) AttachLogs(ctx context.Context,
	plan sandbox.DockerLogCapturePlan) (io.ReadCloser, error) {
	transport.attaches++
	if transport.failAttach {
		return nil, errors.New("attach failed")
	}
	return io.NopCloser(bytes.NewReader(transport.attachBody)), nil
}

func (transport *fakeDockerContainerIOTransport) ExportOutputs(ctx context.Context,
	plan sandbox.DockerOutputExportPlan) (io.ReadCloser, error) {
	transport.exports++
	if transport.failExport {
		return nil, errors.New("export failed")
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
		transport.failAttach = true
		if _, _, err := service.CaptureLogs(ctx, plan); err == nil {
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
		_, replayed, err := service.CommitOutputs(ctx, request, staging, stagingRoot)
		if err != nil || replayed || len(store.commits) != 1 {
			t.Fatalf("commit replay = %t, %v", replayed, err)
		}
		store.failCommit = true
		if _, _, err := service.CommitOutputs(ctx, request, staging, stagingRoot); err == nil {
			t.Fatal("commit failure was swallowed")
		}
		if len(store.commits) != 1 {
			t.Fatal("failed commit left partial rows")
		}
		store.failCommit = false
		transport.failExport = true
		if _, _, err := service.StageOutputs(ctx, exportPlan, t.TempDir()); err == nil {
			t.Fatal("export failure was swallowed")
		}
		transport.failExport = false
	})
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
