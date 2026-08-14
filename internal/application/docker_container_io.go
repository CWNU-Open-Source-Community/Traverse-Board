package application

import (
	"context"
	"errors"
	"time"

	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/sandbox"
)

// dockerContainerIOStore is the durable boundary satisfied by *store.SQLiteStore.
// Raw container bytes never reach it.
type dockerContainerIOStore interface {
	InsertDockerInputProjection(ctx context.Context,
		projection sandbox.DockerInputProjection) (bool, error)
	InsertDockerLogCaptureReceipt(ctx context.Context,
		receipt sandbox.DockerLogCaptureReceipt) (bool, error)
	InsertDockerOutputStagingReceipt(ctx context.Context,
		receipt sandbox.DockerOutputStagingReceipt) (bool, error)
	CommitDockerOutputs(ctx context.Context, request sandbox.DockerOutputCommitRequest,
		receipt sandbox.DockerOutputCommitReceipt) (bool, error)
}

// DockerContainerIOService owns the bounded container I/O contract: sealed
// read-only input projections, bounded log capture, strict output staging,
// and atomic output commit. It grants no product execution authority and is
// not wired to any CLI, HTTP, or Desktop entry.
type DockerContainerIOService struct {
	store     dockerContainerIOStore
	transport sandbox.DockerContainerIOTransport
	now       func() time.Time
}

func NewDockerContainerIOService(store dockerContainerIOStore,
	transport sandbox.DockerContainerIOTransport,
) *DockerContainerIOService {
	return &DockerContainerIOService{store: store, transport: transport, now: time.Now}
}

// ProjectInputs durably records the sealed read-only input projection. The
// returned flag is false when an identical projection already exists.
func (service *DockerContainerIOService) ProjectInputs(ctx context.Context,
	projection sandbox.DockerInputProjection,
) (bool, error) {
	if service == nil || service.store == nil {
		return false, errors.New("docker container I/O store is required")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return service.store.InsertDockerInputProjection(ctx, projection)
}

// CaptureLogs attaches to the exited container under the plan bounds,
// demuxes and redacts the raw stream in memory, and durably records the
// content-free receipt. Raw bytes never persist.
func (service *DockerContainerIOService) CaptureLogs(ctx context.Context,
	plan sandbox.DockerLogCapturePlan,
) (sandbox.DockerLogCaptureReceipt, bool, error) {
	if service == nil || service.store == nil || service.transport == nil {
		return sandbox.DockerLogCaptureReceipt{}, false, errors.New("docker container I/O service is unavailable")
	}
	if err := plan.Validate(); err != nil {
		return sandbox.DockerLogCaptureReceipt{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return sandbox.DockerLogCaptureReceipt{}, false, err
	}
	captureCtx, cancel := context.WithTimeout(ctx, time.Duration(plan.DurationSeconds)*time.Second)
	defer cancel()
	stream, err := service.transport.AttachLogs(captureCtx, plan)
	if err != nil {
		return sandbox.DockerLogCaptureReceipt{}, false, err
	}
	defer stream.Close()
	records, status, err := sandbox.DecodeDockerLogFrames(captureCtx, plan, stream)
	if err != nil {
		return sandbox.DockerLogCaptureReceipt{}, false, err
	}
	receipt, err := sandbox.NewDockerLogCaptureReceipt(
		idgen.New("sandbox-docker-log-capture"), plan.AttemptID, plan.Generation, plan.RunID,
		plan.ContainerIDFingerprint, plan, records, status, service.now().UTC())
	if err != nil {
		return sandbox.DockerLogCaptureReceipt{}, false, err
	}
	inserted, err := service.store.InsertDockerLogCaptureReceipt(ctx, receipt)
	if err != nil {
		return sandbox.DockerLogCaptureReceipt{}, false, err
	}
	return receipt, inserted, nil
}

// StageOutputs exports the dedicated output mount as a tar archive, walks it
// under the plan bounds, redacts text content, and records the staging
// receipt. Staged files stay in the caller-owned staging directory.
func (service *DockerContainerIOService) StageOutputs(ctx context.Context,
	plan sandbox.DockerOutputExportPlan, stagingRoot string,
) (sandbox.DockerOutputStagingReceipt, bool, error) {
	if service == nil || service.store == nil || service.transport == nil {
		return sandbox.DockerOutputStagingReceipt{}, false, errors.New("docker container I/O service is unavailable")
	}
	if err := plan.Validate(); err != nil {
		return sandbox.DockerOutputStagingReceipt{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return sandbox.DockerOutputStagingReceipt{}, false, err
	}
	stream, err := service.transport.ExportOutputs(ctx, plan)
	if err != nil {
		return sandbox.DockerOutputStagingReceipt{}, false, err
	}
	defer stream.Close()
	receipt, err := sandbox.StageDockerOutputArchive(ctx, plan, stream, stagingRoot,
		idgen.New("sandbox-docker-output-staging"), service.now().UTC())
	if err != nil {
		return sandbox.DockerOutputStagingReceipt{}, false, err
	}
	inserted, err := service.store.InsertDockerOutputStagingReceipt(ctx, receipt)
	if err != nil {
		return sandbox.DockerOutputStagingReceipt{}, false, err
	}
	return receipt, inserted, nil
}

// CommitOutputs verifies the accepted manifest against the completed staging
// receipt and the staged files, then commits the receipt and every entry in
// one atomic store transaction. A failed commit leaves no partial rows.
func (service *DockerContainerIOService) CommitOutputs(ctx context.Context,
	request sandbox.DockerOutputCommitRequest, staging sandbox.DockerOutputStagingReceipt,
	stagingRoot string,
) (sandbox.DockerOutputCommitReceipt, bool, error) {
	if service == nil || service.store == nil {
		return sandbox.DockerOutputCommitReceipt{}, false, errors.New("docker container I/O store is required")
	}
	if err := request.Binds(staging); err != nil {
		return sandbox.DockerOutputCommitReceipt{}, false, err
	}
	if _, err := sandbox.VerifyDockerOutputCommit(stagingRoot, request); err != nil {
		return sandbox.DockerOutputCommitReceipt{}, false, err
	}
	receipt, err := sandbox.NewDockerOutputCommitReceipt(
		idgen.New("sandbox-docker-output-commit"), request.AttemptID, request.Generation,
		request.RunID, request.WorkspaceID, request, service.now().UTC())
	if err != nil {
		return sandbox.DockerOutputCommitReceipt{}, false, err
	}
	committed, err := service.store.CommitDockerOutputs(ctx, request, receipt)
	if err != nil {
		return sandbox.DockerOutputCommitReceipt{}, false, err
	}
	return receipt, committed, nil
}
