package application

import (
	"context"
	"errors"
	"io"
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
	GetDockerLogCaptureReceiptByAttempt(ctx context.Context,
		attemptID string) (sandbox.DockerLogCaptureReceipt, bool, error)
	InsertDockerOutputStagingReceipt(ctx context.Context,
		receipt sandbox.DockerOutputStagingReceipt) (bool, error)
	GetDockerOutputStagingReceiptByAttempt(ctx context.Context,
		attemptID string) (sandbox.DockerOutputStagingReceipt, bool, error)
	CommitDockerOutputs(ctx context.Context, request sandbox.DockerOutputCommitRequest,
		receipt sandbox.DockerOutputCommitReceipt) (bool, error)
	GetDockerOutputCommitReceiptByAttempt(ctx context.Context,
		attemptID string) (sandbox.DockerOutputCommitReceipt, bool, error)
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
	return service.captureLogs(ctx, plan, func(captureCtx context.Context) (io.ReadCloser, error) {
		return service.transport.AttachLogs(captureCtx, plan)
	})
}

// CaptureOwnedLogs is the product-safe capture entry. The transport must bind
// the exact durable lifecycle request to a freshly inspected raw container ID
// before opening the attach stream.
func (service *DockerContainerIOService) CaptureOwnedLogs(ctx context.Context,
	request sandbox.DockerContainerLifecycleRequest, plan sandbox.DockerLogCapturePlan,
) (sandbox.DockerLogCaptureReceipt, bool, error) {
	if service == nil || service.store == nil || service.transport == nil {
		return sandbox.DockerLogCaptureReceipt{}, false, errors.New("docker container I/O service is unavailable")
	}
	if err := request.Validate(); err != nil {
		return sandbox.DockerLogCaptureReceipt{}, false, err
	}
	transport, ok := service.transport.(sandbox.DockerContainerOwnedIOTransport)
	if !ok {
		return sandbox.DockerLogCaptureReceipt{}, false,
			errors.New("docker container owned I/O transport is required")
	}
	return service.captureLogs(ctx, plan, func(captureCtx context.Context) (io.ReadCloser, error) {
		return transport.AttachOwnedLogs(captureCtx, request, plan)
	})
}

func (service *DockerContainerIOService) captureLogs(ctx context.Context,
	plan sandbox.DockerLogCapturePlan,
	open func(context.Context) (io.ReadCloser, error),
) (sandbox.DockerLogCaptureReceipt, bool, error) {
	if err := plan.Validate(); err != nil {
		return sandbox.DockerLogCaptureReceipt{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return sandbox.DockerLogCaptureReceipt{}, false, err
	}
	if existing, found, err := service.store.GetDockerLogCaptureReceiptByAttempt(
		ctx, plan.AttemptID); err != nil {
		return sandbox.DockerLogCaptureReceipt{}, false, err
	} else if found {
		if err := dockerLogCaptureReceiptBindsPlan(existing, plan); err != nil {
			return sandbox.DockerLogCaptureReceipt{}, false, err
		}
		return existing, false, nil
	}
	captureCtx, cancel := context.WithTimeout(ctx, time.Duration(plan.DurationSeconds)*time.Second)
	defer cancel()
	stream, err := open(captureCtx)
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
	if !inserted {
		existing, found, loadErr := service.store.GetDockerLogCaptureReceiptByAttempt(
			ctx, plan.AttemptID)
		if loadErr != nil {
			return sandbox.DockerLogCaptureReceipt{}, false, loadErr
		}
		if !found || dockerLogCaptureReceiptBindsPlan(existing, plan) != nil {
			return sandbox.DockerLogCaptureReceipt{}, false,
				errors.New("durable docker log capture replay does not match the plan")
		}
		return existing, false, nil
	}
	return receipt, inserted, nil
}

func dockerLogCaptureReceiptBindsPlan(receipt sandbox.DockerLogCaptureReceipt,
	plan sandbox.DockerLogCapturePlan,
) error {
	if receipt.Validate() != nil || plan.Validate() != nil ||
		receipt.AttemptID != plan.AttemptID || receipt.Generation != plan.Generation ||
		receipt.RunID != plan.RunID ||
		receipt.ContainerIDFingerprint != plan.ContainerIDFingerprint ||
		receipt.CaptureMaxBytes != plan.MaxBytes ||
		receipt.CaptureMaxLines != plan.MaxLines ||
		receipt.CaptureFingerprint != plan.CaptureFingerprint {
		return errors.New("durable docker log capture receipt does not match the plan")
	}
	return nil
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
	return service.stageOutputs(ctx, plan, stagingRoot,
		func(exportCtx context.Context) (io.ReadCloser, error) {
			return service.transport.ExportOutputs(exportCtx, plan)
		})
}

// StageOwnedOutputs is the product-safe export entry. The exact lifecycle
// ownership is re-inspected before the dedicated output archive is opened.
func (service *DockerContainerIOService) StageOwnedOutputs(ctx context.Context,
	request sandbox.DockerContainerLifecycleRequest, plan sandbox.DockerOutputExportPlan,
	stagingRoot string,
) (sandbox.DockerOutputStagingReceipt, bool, error) {
	if service == nil || service.store == nil || service.transport == nil {
		return sandbox.DockerOutputStagingReceipt{}, false, errors.New("docker container I/O service is unavailable")
	}
	if err := request.Validate(); err != nil {
		return sandbox.DockerOutputStagingReceipt{}, false, err
	}
	transport, ok := service.transport.(sandbox.DockerContainerOwnedIOTransport)
	if !ok {
		return sandbox.DockerOutputStagingReceipt{}, false,
			errors.New("docker container owned I/O transport is required")
	}
	return service.stageOutputs(ctx, plan, stagingRoot,
		func(exportCtx context.Context) (io.ReadCloser, error) {
			return transport.ExportOwnedOutputs(exportCtx, request, plan)
		})
}

func (service *DockerContainerIOService) stageOutputs(ctx context.Context,
	plan sandbox.DockerOutputExportPlan, stagingRoot string,
	open func(context.Context) (io.ReadCloser, error),
) (sandbox.DockerOutputStagingReceipt, bool, error) {
	if err := plan.Validate(); err != nil {
		return sandbox.DockerOutputStagingReceipt{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return sandbox.DockerOutputStagingReceipt{}, false, err
	}
	if existing, found, err := service.store.GetDockerOutputStagingReceiptByAttempt(
		ctx, plan.AttemptID); err != nil {
		return sandbox.DockerOutputStagingReceipt{}, false, err
	} else if found {
		if err := dockerOutputStagingReceiptBindsPlan(existing, plan); err != nil {
			return sandbox.DockerOutputStagingReceipt{}, false, err
		}
		return existing, false, nil
	}
	stream, err := open(ctx)
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
	if !inserted {
		existing, found, loadErr := service.store.GetDockerOutputStagingReceiptByAttempt(
			ctx, plan.AttemptID)
		if loadErr != nil {
			return sandbox.DockerOutputStagingReceipt{}, false, loadErr
		}
		if !found || dockerOutputStagingReceiptBindsPlan(existing, plan) != nil {
			return sandbox.DockerOutputStagingReceipt{}, false,
				errors.New("durable docker output staging replay does not match the plan")
		}
		return existing, false, nil
	}
	return receipt, inserted, nil
}

func dockerOutputStagingReceiptBindsPlan(receipt sandbox.DockerOutputStagingReceipt,
	plan sandbox.DockerOutputExportPlan,
) error {
	if receipt.Validate() != nil || plan.Validate() != nil ||
		receipt.AttemptID != plan.AttemptID || receipt.Generation != plan.Generation ||
		receipt.RunID != plan.RunID ||
		receipt.ContainerIDFingerprint != plan.ContainerIDFingerprint ||
		receipt.ExportFingerprint != plan.ExportFingerprint {
		return errors.New("durable docker output staging receipt does not match the plan")
	}
	return nil
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
	if err := ctx.Err(); err != nil {
		return sandbox.DockerOutputCommitReceipt{}, false, err
	}
	if existing, found, err := service.store.GetDockerOutputCommitReceiptByAttempt(
		ctx, request.AttemptID); err != nil {
		return sandbox.DockerOutputCommitReceipt{}, false, err
	} else if found {
		if err := dockerOutputCommitReceiptBindsRequest(existing, request); err != nil {
			return sandbox.DockerOutputCommitReceipt{}, false, err
		}
		return existing, false, nil
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
	if !committed {
		existing, found, loadErr := service.store.GetDockerOutputCommitReceiptByAttempt(
			ctx, request.AttemptID)
		if loadErr != nil {
			return sandbox.DockerOutputCommitReceipt{}, false, loadErr
		}
		if !found || dockerOutputCommitReceiptBindsRequest(existing, request) != nil {
			return sandbox.DockerOutputCommitReceipt{}, false,
				errors.New("durable docker output commit replay does not match the request")
		}
		return existing, false, nil
	}
	return receipt, committed, nil
}

func dockerOutputCommitReceiptBindsRequest(receipt sandbox.DockerOutputCommitReceipt,
	request sandbox.DockerOutputCommitRequest,
) error {
	if receipt.Validate() != nil || request.Validate() != nil ||
		receipt.AttemptID != request.AttemptID ||
		receipt.Generation != request.Generation || receipt.RunID != request.RunID ||
		receipt.WorkspaceID != request.WorkspaceID ||
		receipt.OperationKeyDigest != request.OperationKeyDigest ||
		receipt.RequestFingerprint != request.RequestFingerprint {
		return errors.New("durable docker output commit receipt does not match the request")
	}
	return nil
}
