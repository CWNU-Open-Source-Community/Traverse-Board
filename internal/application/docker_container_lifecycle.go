package application

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/sandbox"
)

const DockerContainerLifecycleRecoveryLimit = 64

// DockerContainerLifecycleStore is deliberately separate from the product
// SandboxManifestStore. This slice does not add a CLI, HTTP, Desktop, model, or
// generic Runner execution entry point.
type DockerContainerLifecycleStore interface {
	GetDockerContainerLifecycleByOperation(context.Context,
		string) (sandbox.DockerContainerLifecycleRecord, bool, error)
	BeginDockerContainerLifecycle(context.Context, sandbox.DockerContainerLaunchIntent,
		string, time.Duration) (sandbox.DockerContainerLifecycleRecord, bool, error)
	AcquireDockerContainerLifecycle(context.Context, string, string, string,
		time.Duration) (sandbox.DockerContainerLifecycleRecord, error)
	RenewDockerContainerLifecycleLease(context.Context,
		sandbox.DockerContainerLifecycleLease, time.Duration) (sandbox.DockerContainerLifecycleLease, error)
	ReleaseDockerContainerLifecycleLease(context.Context,
		sandbox.DockerContainerLifecycleLease) (sandbox.DockerContainerLifecycleLease, bool, error)
	FenceDockerContainerLifecycle(context.Context, sandbox.DockerContainerLifecycleLease) error
	PrepareDockerContainerLifecycleAction(context.Context,
		sandbox.DockerContainerLifecyclePreparedAction,
		sandbox.DockerContainerLifecycleLease) (sandbox.DockerContainerLifecycleRecord, bool, error)
	AppendDockerContainerLifecycleTransition(context.Context,
		sandbox.DockerContainerLifecycleTransition,
		sandbox.DockerContainerLifecycleLease) (sandbox.DockerContainerLifecycleRecord, bool, error)
	CompleteDockerContainerLifecycle(context.Context, sandbox.DockerContainerLifecycleReceipt,
		sandbox.DockerContainerLifecycleLease) (sandbox.DockerContainerLifecycleRecord, bool, error)
	GetDockerContainerLifecycle(context.Context, string) (sandbox.DockerContainerLifecycleRecord, error)
	ListRecoverableDockerContainerLifecycles(context.Context,
		int) ([]sandbox.DockerContainerLifecycleRecord, error)
}

// DockerContainerLifecycleAuthority revalidates the current Run, Workspace,
// permission snapshot, process capability, and exact v54 plan. Persistence is
// never treated as fresh authorization. Implementations must reconstruct the
// request from current authority, not from private data in the lifecycle ledger.
type DockerContainerLifecycleAuthority interface {
	RevalidateDockerContainerLifecycle(context.Context,
		sandbox.DockerContainerLaunchIntent) (sandbox.DockerContainerPlan,
		sandbox.DockerContainerWriteRequest, error)
}

type DockerContainerLifecycleSupervisor struct {
	store     DockerContainerLifecycleStore
	transport sandbox.DockerContainerLifecycleTransport
	authority DockerContainerLifecycleAuthority
	ownerID   string
	leaseTTL  time.Duration
	now       func() time.Time

	mu    sync.Mutex
	lease sandbox.DockerContainerLifecycleLease
}

func NewDockerContainerLifecycleSupervisor(store DockerContainerLifecycleStore,
	transport sandbox.DockerContainerLifecycleTransport,
	authority DockerContainerLifecycleAuthority, ownerID string,
	leaseTTL time.Duration,
) (*DockerContainerLifecycleSupervisor, error) {
	ownerID = strings.TrimSpace(ownerID)
	if store == nil || transport == nil || authority == nil || ownerID == "" ||
		sandbox.ValidateDockerContainerLifecycleLeaseTTL(leaseTTL) != nil ||
		transport.Endpoint().Validate() != nil {
		return nil, errors.New("Docker lifecycle Supervisor dependencies are invalid")
	}
	return &DockerContainerLifecycleSupervisor{store: store, transport: transport,
		authority: authority, ownerID: ownerID, leaseTTL: leaseTTL,
		now: func() time.Time { return time.Now().UTC() }}, nil
}

// BeginAndRun commits launch intent before StageOwned is allowed to issue a
// create. It remains an internal, explicitly-called engineering surface.
func (s *DockerContainerLifecycleSupervisor) BeginAndRun(ctx context.Context,
	plan sandbox.DockerContainerPlan, writeRequest sandbox.DockerContainerWriteRequest,
	requestedBy, operationKey string,
) (sandbox.DockerContainerLifecycleRecord, error) {
	if ctx == nil || plan.Validate() != nil || writeRequest.Validate() != nil ||
		requestedBy != plan.RequestedBy || strings.TrimSpace(operationKey) == "" {
		return sandbox.DockerContainerLifecycleRecord{}, apperror.New(
			apperror.CodeInvalidArgument, "Docker lifecycle request is invalid")
	}
	endpoint := s.transport.Endpoint()
	now := s.now()
	operationKeyDigest := runmutation.Fingerprint("sandbox_docker_lifecycle_operation.v1",
		plan.ID, operationKey)
	existing, found, err := s.store.GetDockerContainerLifecycleByOperation(ctx,
		operationKeyDigest)
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, apperror.Normalize(err)
	}
	if found {
		if existing.Intent.OperationKeyDigest != operationKeyDigest ||
			existing.Intent.RequestedBy != requestedBy {
			return sandbox.DockerContainerLifecycleRecord{}, apperror.New(
				apperror.CodeConflict, "Docker lifecycle operation replay does not match its intent")
		}
		if err := validateDockerLifecycleAuthority(existing.Intent, plan, writeRequest,
			endpoint); err != nil {
			return sandbox.DockerContainerLifecycleRecord{}, err
		}
		existing.Replayed = true
		return existing, nil
	}
	intent, err := sandbox.NewDockerContainerLaunchIntent(
		idgen.New("sandbox-docker-lifecycle"), idgen.New("sandbox-docker-lifecycle-attempt"),
		operationKeyDigest, plan, writeRequest, endpoint, requestedBy, 1, now)
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, apperror.Wrap(
			apperror.CodeInvalidArgument, "Docker lifecycle intent assembly failed", err)
	}
	record, replayed, err := s.store.BeginDockerContainerLifecycle(ctx, intent, s.ownerID, s.leaseTTL)
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, apperror.Normalize(err)
	}
	if replayed {
		// Preparation replay is metadata-only. Resuming daemon work requires
		// RecoverOne so the caller must revalidate authority and acquire a
		// current generation instead of borrowing the existing owner's lease.
		return record, nil
	}
	currentPlan, currentRequest, err := s.authority.RevalidateDockerContainerLifecycle(ctx,
		record.Intent)
	if err != nil {
		return record, apperror.Wrap(apperror.CodeFailedPrecondition,
			"Docker lifecycle authority revalidation failed", err)
	}
	if err := validateDockerLifecycleAuthority(record.Intent, currentPlan, currentRequest,
		endpoint); err != nil {
		return record, err
	}
	return s.run(ctx, record, currentPlan, currentRequest)
}

// RecoverOne takes over an expired generation, revalidates current authority,
// and converges exact daemon state without enumerating containers.
func (s *DockerContainerLifecycleSupervisor) RecoverOne(ctx context.Context,
	intentID string,
) (sandbox.DockerContainerLifecycleRecord, error) {
	stored, err := s.store.GetDockerContainerLifecycle(ctx, strings.TrimSpace(intentID))
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, apperror.Normalize(err)
	}
	if stored.Receipt != nil {
		stored.Replayed = true
		return stored, nil
	}
	plan, request, err := s.authority.RevalidateDockerContainerLifecycle(ctx, stored.Intent)
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, apperror.Wrap(
			apperror.CodeFailedPrecondition, "Docker lifecycle authority revalidation failed", err)
	}
	if err := validateDockerLifecycleAuthority(stored.Intent, plan, request,
		s.transport.Endpoint()); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, err
	}
	record, err := s.store.AcquireDockerContainerLifecycle(ctx, stored.Intent.ID,
		stored.Intent.RequestedBy, s.ownerID, s.leaseTTL)
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, apperror.Normalize(err)
	}
	if record.Receipt != nil {
		return record, nil
	}
	return s.run(ctx, record, plan, request)
}

func (s *DockerContainerLifecycleSupervisor) RecoverStartup(ctx context.Context) ([]sandbox.DockerContainerLifecycleRecord, error) {
	values, err := s.store.ListRecoverableDockerContainerLifecycles(ctx,
		DockerContainerLifecycleRecoveryLimit)
	if err != nil {
		return nil, apperror.Normalize(err)
	}
	results := make([]sandbox.DockerContainerLifecycleRecord, 0, len(values))
	for _, value := range values {
		result, recoverErr := s.RecoverOne(ctx, value.Intent.ID)
		if recoverErr != nil {
			return results, recoverErr
		}
		results = append(results, result)
	}
	return results, nil
}

func (s *DockerContainerLifecycleSupervisor) run(ctx context.Context,
	record sandbox.DockerContainerLifecycleRecord, plan sandbox.DockerContainerPlan,
	writeRequest sandbox.DockerContainerWriteRequest,
) (sandbox.DockerContainerLifecycleRecord, error) {
	if err := validateDockerLifecycleAuthority(record.Intent, plan, writeRequest,
		s.transport.Endpoint()); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, err
	}
	s.setLease(record.Lease)
	stopHeartbeat := s.startHeartbeat(ctx, record.Intent.ID)
	defer stopHeartbeat()
	ownership, err := record.Intent.LifecycleOwnership()
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, err
	}

	resourceID := lifecycleContainerIDFingerprint(record)
	if resourceID != "" {
		recovered, recoverErr := sandbox.NewRecoveredDockerContainerLifecycleRequest(
			record.Intent.AttemptID, record.Lease.Generation, writeRequest, resourceID,
			ownership, sandbox.DockerContainerLifecycleConfirmation)
		if recoverErr != nil {
			return sandbox.DockerContainerLifecycleRecord{}, recoverErr
		}
		observation, observeErr := s.transport.Observe(ctx, recovered)
		if observeErr != nil {
			return s.fail(ctx, record, lifecycleFailureReason(observeErr))
		}
		if observation.State == sandbox.DockerContainerLifecycleStateAbsent {
			if latestLifecycleTransition(record,
				sandbox.DockerContainerLifecycleTransitionCleaning) == nil {
				return s.fail(ctx, record,
					sandbox.DockerContainerLifecycleFailureConfigMismatch)
			}
			return s.finishAbsentRecovery(context.WithoutCancel(ctx), record)
		}
		return s.runObserved(ctx, record, recovered, observation)
	}

	// StageOwned can reconcile an uncertain create by exact name/config/labels.
	// Its create fence prepares and commits the action before the HTTP request.
	stage, err := s.stageOwned(ctx, record, writeRequest, ownership)
	if err != nil {
		return s.fail(ctx, record, lifecycleFailureReason(err))
	}
	request, err := sandbox.NewOwnedDockerContainerLifecycleRequest(record.Intent.AttemptID,
		record.Lease.Generation, writeRequest, stage, ownership,
		sandbox.DockerContainerLifecycleConfirmation)
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, err
	}

	observation, err := s.transport.Observe(ctx, request)
	if err != nil {
		return s.fail(ctx, record, lifecycleFailureReason(err))
	}
	record, err = s.checkpointObservation(ctx, record, observation)
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, err
	}
	return s.runObserved(ctx, record, request, observation)
}

func (s *DockerContainerLifecycleSupervisor) startHeartbeat(ctx context.Context,
	intentID string,
) context.CancelFunc {
	heartbeatCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	interval := s.leaseTTL / 3
	if interval > 30*time.Second {
		interval = 30 * time.Second
	}
	if interval < 250*time.Millisecond {
		interval = 250 * time.Millisecond
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				lease := s.currentLease()
				if lease.IntentID != intentID {
					return
				}
				renewed, err := s.store.RenewDockerContainerLifecycleLease(heartbeatCtx,
					lease, s.leaseTTL)
				if err != nil {
					// Retain the old fencing token. Every later daemon write must
					// pass the Store fence and therefore fails closed at expiry.
					return
				}
				s.setLease(renewed)
			}
		}
	}()
	return cancel
}

func (s *DockerContainerLifecycleSupervisor) runObserved(ctx context.Context,
	record sandbox.DockerContainerLifecycleRecord, request sandbox.DockerContainerLifecycleRequest,
	observation sandbox.DockerContainerLifecycleObservation,
) (sandbox.DockerContainerLifecycleRecord, error) {
	writeRequest := request.WriteRequest
	var err error
	if observation.State == sandbox.DockerContainerLifecycleStateCreated {
		startCtx, startCancel := s.leaseBoundContext(ctx)
		observation, _, err = s.transport.Start(startCtx, request, s.fence(record.Intent.ID))
		startCancel()
		if err != nil {
			return s.fail(ctx, record, lifecycleFailureReason(err))
		}
		// Start returns the pre-start observation. Exact observe resolves the
		// response/commit crash window and never starts running/exited twice.
		observation, err = s.transport.Observe(ctx, request)
		if err != nil {
			return s.fail(ctx, record, lifecycleFailureReason(err))
		}
		record, err = s.checkpointObservation(ctx, record, observation)
		if err != nil {
			return sandbox.DockerContainerLifecycleRecord{}, err
		}
	}

	if observation.State == sandbox.DockerContainerLifecycleStateRunning {
		cleaning := latestLifecycleTransition(record,
			sandbox.DockerContainerLifecycleTransitionCleaning)
		if cleaning == nil {
			waitCtx, cancel := context.WithTimeout(ctx,
				time.Duration(writeRequest.Spec.Termination.TimeoutSeconds)*time.Second)
			observation, err = s.transport.Wait(waitCtx, request, s.fence(record.Intent.ID))
			cancel()
			if err != nil && !errors.Is(err, context.DeadlineExceeded) &&
				!errors.Is(err, context.Canceled) {
				return s.fail(ctx, record, lifecycleFailureReason(err))
			}
			if err == nil {
				record, err = s.checkpointObservation(ctx, record, observation)
				if err != nil {
					return sandbox.DockerContainerLifecycleRecord{}, err
				}
			}
		}
		if cleaning != nil || err != nil {
			reason := sandbox.DockerContainerLifecycleReasonTimeout
			if cleaning != nil {
				reason = cleaning.ReasonCode
			} else if ctx.Err() != nil {
				reason = sandbox.DockerContainerLifecycleReasonCancelled
			}
			record, err = s.ensureCleaning(context.WithoutCancel(ctx), record, reason)
			if err != nil {
				return sandbox.DockerContainerLifecycleRecord{}, err
			}
			cleanupCtx, cleanupCancel := s.cleanupContext(ctx)
			termination, terminateErr := s.transport.Terminate(cleanupCtx, request,
				s.fence(record.Intent.ID))
			cleanupCancel()
			if terminateErr != nil {
				return s.fail(context.WithoutCancel(ctx), record, lifecycleFailureReason(terminateErr))
			}
			exitObservation := termination.Observation
			exitObservation.State = sandbox.DockerContainerLifecycleStateExited
			exitObservation.Running = false
			exitObservation.ExitCode = termination.ExitCode
			// Terminate's aggregate observation is intentionally not reused as a
			// transport receipt; observe exact final state before checkpoint.
			exitObservation, err = s.transport.Observe(context.WithoutCancel(ctx), request)
			if err != nil {
				return s.fail(context.WithoutCancel(ctx), record, lifecycleFailureReason(err))
			}
			record, err = s.checkpointObservation(context.WithoutCancel(ctx), record, exitObservation)
			if err != nil {
				return sandbox.DockerContainerLifecycleRecord{}, err
			}
			observation = exitObservation
		}
	}

	if observation.State != sandbox.DockerContainerLifecycleStateExited {
		return s.fail(context.WithoutCancel(ctx), record,
			sandbox.DockerContainerLifecycleReasonWaitFailed)
	}
	reason := sandbox.DockerContainerLifecycleReasonNaturalExit
	if cleaning := latestLifecycleTransition(record, sandbox.DockerContainerLifecycleTransitionCleaning); cleaning != nil {
		reason = cleaning.ReasonCode
	}
	record, err = s.ensureCleaning(context.WithoutCancel(ctx), record, reason)
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, err
	}
	cleanupCtx, cleanupCancel := s.cleanupContext(ctx)
	cleanup, err := s.transport.Cleanup(cleanupCtx, request, s.fence(record.Intent.ID))
	cleanupCancel()
	if err != nil {
		return s.fail(context.WithoutCancel(ctx), record, lifecycleFailureReason(err))
	}
	return s.finish(context.WithoutCancel(ctx), record, observation, cleanup, reason)
}

func (s *DockerContainerLifecycleSupervisor) stageOwned(ctx context.Context,
	record sandbox.DockerContainerLifecycleRecord, request sandbox.DockerContainerWriteRequest,
	ownership sandbox.DockerContainerLifecycleOwnership,
) (sandbox.DockerContainerStageResult, error) {
	writeCtx, cancel := s.leaseBoundContext(ctx)
	defer cancel()
	return s.transport.StageOwned(writeCtx, request, ownership, s.fence(record.Intent.ID))
}

func (s *DockerContainerLifecycleSupervisor) fence(intentID string) sandbox.DockerContainerLifecycleFence {
	return func(ctx context.Context, action sandbox.DockerContainerLifecycleActionKind) error {
		lease := s.currentLease()
		if lease.IntentID != intentID {
			return apperror.New(apperror.CodeConflict, "Docker lifecycle fence intent changed")
		}
		// wait is a daemon read despite using POST. It is fenced, but does not
		// enter the external-write WAL table.
		if action != sandbox.DockerContainerLifecycleActionWait {
			deadline, ok := ctx.Deadline()
			if !ok || deadline.After(lease.ExpiresAt) {
				return apperror.New(apperror.CodeConflict,
					"Docker lifecycle daemon request is not bounded by the active lease")
			}
		}
		if action != sandbox.DockerContainerLifecycleActionWait {
			record, err := s.store.GetDockerContainerLifecycle(ctx, intentID)
			if err != nil {
				return err
			}
			for _, existing := range record.Actions {
				if existing.LeaseGeneration == lease.Generation &&
					existing.Verb == string(action) {
					return s.store.FenceDockerContainerLifecycle(ctx, lease)
				}
			}
			actionIntent, err := sandbox.NewDockerContainerLifecyclePreparedAction(intentID,
				len(record.Actions)+1, lease, string(action), s.now())
			if err != nil {
				return err
			}
			if _, _, err := s.store.PrepareDockerContainerLifecycleAction(ctx,
				actionIntent, lease); err != nil {
				return err
			}
		}
		return s.store.FenceDockerContainerLifecycle(ctx, lease)
	}
}

func (s *DockerContainerLifecycleSupervisor) checkpointObservation(ctx context.Context,
	record sandbox.DockerContainerLifecycleRecord,
	observation sandbox.DockerContainerLifecycleObservation,
) (sandbox.DockerContainerLifecycleRecord, error) {
	var state, reason string
	var exitCode *int
	switch observation.State {
	case sandbox.DockerContainerLifecycleStateCreated:
		state, reason = sandbox.DockerContainerLifecycleTransitionCreated,
			sandbox.DockerContainerLifecycleReasonCreated
	case sandbox.DockerContainerLifecycleStateRunning:
		state, reason = sandbox.DockerContainerLifecycleTransitionStarted,
			sandbox.DockerContainerLifecycleReasonStarted
	case sandbox.DockerContainerLifecycleStateExited:
		if latestLifecycleTransition(record,
			sandbox.DockerContainerLifecycleTransitionStarted) == nil {
			var startErr error
			record, startErr = s.appendTransition(ctx, record,
				sandbox.DockerContainerLifecycleTransitionStarted,
				sandbox.DockerContainerLifecycleReasonRestartRecovery, nil,
				observation.ContainerIDFingerprint)
			if startErr != nil {
				return sandbox.DockerContainerLifecycleRecord{}, startErr
			}
		}
		state = sandbox.DockerContainerLifecycleTransitionExited
		reason = sandbox.DockerContainerLifecycleReasonNaturalExit
		if cleaning := latestLifecycleTransition(record,
			sandbox.DockerContainerLifecycleTransitionCleaning); cleaning != nil {
			reason = cleaning.ReasonCode
		}
		value := observation.ExitCode
		exitCode = &value
	case sandbox.DockerContainerLifecycleStateAbsent:
		return record, nil
	default:
		return sandbox.DockerContainerLifecycleRecord{}, apperror.New(
			apperror.CodeFailedPrecondition, "Docker lifecycle observation is inconsistent")
	}
	if record.TookOver && (state == sandbox.DockerContainerLifecycleTransitionCreated ||
		state == sandbox.DockerContainerLifecycleTransitionStarted) {
		reason = sandbox.DockerContainerLifecycleReasonRestartRecovery
	}
	if latestLifecycleTransition(record, state) != nil {
		return record, nil
	}
	return s.appendTransition(ctx, record, state, reason, exitCode,
		observation.ContainerIDFingerprint)
}

func (s *DockerContainerLifecycleSupervisor) ensureCleaning(ctx context.Context,
	record sandbox.DockerContainerLifecycleRecord, reason string,
) (sandbox.DockerContainerLifecycleRecord, error) {
	if latestLifecycleTransition(record, sandbox.DockerContainerLifecycleTransitionCleaning) != nil {
		return record, nil
	}
	return s.appendTransition(ctx, record, sandbox.DockerContainerLifecycleTransitionCleaning,
		reason, nil, "")
}

func (s *DockerContainerLifecycleSupervisor) appendTransition(ctx context.Context,
	record sandbox.DockerContainerLifecycleRecord, state, reason string, exitCode *int,
	containerIDFingerprint string,
) (sandbox.DockerContainerLifecycleRecord, error) {
	previous := ""
	if len(record.Transitions) > 0 {
		previous = record.Transitions[len(record.Transitions)-1].TransitionFingerprint
	}
	transition, err := sandbox.NewDockerContainerLifecycleTransition(record.Intent.ID,
		len(record.Transitions)+1, s.currentLease(), state, reason, exitCode,
		containerIDFingerprint, previous, s.now())
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, err
	}
	stored, _, err := s.store.AppendDockerContainerLifecycleTransition(ctx, transition,
		s.currentLease())
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, apperror.Normalize(err)
	}
	return stored, nil
}

func (s *DockerContainerLifecycleSupervisor) fail(ctx context.Context,
	record sandbox.DockerContainerLifecycleRecord, reason string,
) (sandbox.DockerContainerLifecycleRecord, error) {
	if reason == "" {
		reason = sandbox.DockerContainerLifecycleReasonWaitFailed
	}
	failed, appendErr := s.appendTransition(ctx, record,
		sandbox.DockerContainerLifecycleTransitionFailed, reason, nil, "")
	if appendErr != nil {
		return record, appendErr
	}
	return failed, apperror.New(dockerLifecycleApplicationCode(reason),
		"Docker lifecycle failed closed: "+reason)
}

func (s *DockerContainerLifecycleSupervisor) finish(ctx context.Context,
	record sandbox.DockerContainerLifecycleRecord,
	exit sandbox.DockerContainerLifecycleObservation,
	cleanup sandbox.DockerContainerLifecycleCleanupResult, reason string,
) (sandbox.DockerContainerLifecycleRecord, error) {
	cleaned, err := s.appendTransition(ctx, record,
		sandbox.DockerContainerLifecycleTransitionCleaned,
		sandbox.DockerContainerLifecycleReasonCleanupCompleted, nil, "")
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, err
	}
	outcome := sandbox.DockerContainerLifecycleOutcomeNaturalExit
	switch reason {
	case sandbox.DockerContainerLifecycleReasonTimeout:
		outcome = sandbox.DockerContainerLifecycleOutcomeTimedOut
	case sandbox.DockerContainerLifecycleReasonCancelled:
		outcome = sandbox.DockerContainerLifecycleOutcomeCancelled
	}
	exitCode := exit.ExitCode
	final := cleaned.Transitions[len(cleaned.Transitions)-1]
	receipt, err := sandbox.NewDockerContainerLifecycleReceipt(cleaned.Intent.ID,
		s.currentLease(), final, exit.ContainerIDFingerprint, outcome, &exitCode,
		cleanup.ContainerRemoved, cleanup.AlreadyAbsent, s.now())
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, err
	}
	stored, _, err := s.store.CompleteDockerContainerLifecycle(ctx, receipt, s.currentLease())
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, apperror.Normalize(err)
	}
	if released, _, releaseErr := s.store.ReleaseDockerContainerLifecycleLease(ctx,
		s.currentLease()); releaseErr == nil {
		s.setLease(released)
		stored.Lease = released
	} else {
		return sandbox.DockerContainerLifecycleRecord{}, apperror.Normalize(releaseErr)
	}
	return stored, nil
}

func (s *DockerContainerLifecycleSupervisor) finishAbsentRecovery(ctx context.Context,
	record sandbox.DockerContainerLifecycleRecord,
) (sandbox.DockerContainerLifecycleRecord, error) {
	if latestLifecycleTransition(record, sandbox.DockerContainerLifecycleTransitionCleaned) == nil {
		var err error
		record, err = s.appendTransition(ctx, record,
			sandbox.DockerContainerLifecycleTransitionCleaned,
			sandbox.DockerContainerLifecycleReasonRestartRecovery, nil, "")
		if err != nil {
			return sandbox.DockerContainerLifecycleRecord{}, err
		}
	}
	reason := sandbox.DockerContainerLifecycleReasonRestartRecovery
	if cleaning := latestLifecycleTransition(record,
		sandbox.DockerContainerLifecycleTransitionCleaning); cleaning != nil {
		reason = cleaning.ReasonCode
	}
	outcome := sandbox.DockerContainerLifecycleOutcomeFailed
	switch reason {
	case sandbox.DockerContainerLifecycleReasonNaturalExit:
		outcome = sandbox.DockerContainerLifecycleOutcomeNaturalExit
	case sandbox.DockerContainerLifecycleReasonTimeout:
		outcome = sandbox.DockerContainerLifecycleOutcomeTimedOut
	case sandbox.DockerContainerLifecycleReasonCancelled:
		outcome = sandbox.DockerContainerLifecycleOutcomeCancelled
	}
	var exitCode *int
	if exited := latestLifecycleTransition(record,
		sandbox.DockerContainerLifecycleTransitionExited); exited != nil {
		exitCode = exited.ExitCode
	}
	final := record.Transitions[len(record.Transitions)-1]
	receipt, err := sandbox.NewDockerContainerLifecycleReceipt(record.Intent.ID,
		s.currentLease(), final, lifecycleContainerIDFingerprint(record), outcome,
		exitCode, false, true, s.now())
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, err
	}
	stored, _, err := s.store.CompleteDockerContainerLifecycle(ctx, receipt, s.currentLease())
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, apperror.Normalize(err)
	}
	released, _, err := s.store.ReleaseDockerContainerLifecycleLease(ctx, s.currentLease())
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, apperror.Normalize(err)
	}
	s.setLease(released)
	stored.Lease = released
	return stored, nil
}

func (s *DockerContainerLifecycleSupervisor) cleanupContext(parent context.Context) (context.Context,
	context.CancelFunc,
) {
	base := context.WithoutCancel(parent)
	lease := s.currentLease()
	remaining := time.Until(lease.ExpiresAt)
	if remaining <= 0 {
		remaining = time.Millisecond
	}
	if remaining > 15*time.Second {
		remaining = 15 * time.Second
	}
	return context.WithTimeout(base, remaining)
}

func (s *DockerContainerLifecycleSupervisor) leaseBoundContext(parent context.Context) (
	context.Context, context.CancelFunc,
) {
	lease := s.currentLease()
	deadline := lease.ExpiresAt
	if parentDeadline, ok := parent.Deadline(); ok && parentDeadline.Before(deadline) {
		deadline = parentDeadline
	}
	return context.WithDeadline(parent, deadline)
}

func (s *DockerContainerLifecycleSupervisor) setLease(lease sandbox.DockerContainerLifecycleLease) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lease = lease
}

func (s *DockerContainerLifecycleSupervisor) currentLease() sandbox.DockerContainerLifecycleLease {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lease
}

func validateDockerLifecycleAuthority(intent sandbox.DockerContainerLaunchIntent,
	plan sandbox.DockerContainerPlan, request sandbox.DockerContainerWriteRequest,
	endpoint sandbox.DockerObservationEndpoint,
) error {
	if intent.Validate() != nil || plan.Validate() != nil || request.Validate() != nil ||
		endpoint.Validate() != nil || intent.PlanID != plan.ID || intent.RunID != plan.RunID ||
		intent.MissionID != plan.MissionID || intent.WorkspaceID != plan.WorkspaceID ||
		intent.RequestedBy != plan.RequestedBy || intent.RequestFingerprint != request.RequestFingerprint ||
		intent.SpecFingerprint != plan.SpecFingerprint || intent.PlanFingerprint != plan.PlanFingerprint ||
		intent.AuthorityFingerprint != plan.AuthorityFingerprint ||
		intent.BaseLabelPlanFingerprint != plan.LabelPlanFingerprint ||
		intent.ContainerNameFingerprint != plan.ContainerNameFingerprint ||
		intent.EndpointClass != endpoint.Class || intent.EndpointFingerprint != endpoint.Fingerprint ||
		sandbox.DockerContainerPlanMatchesSpec(plan, request.Spec) != nil ||
		plan.NetworkMode != "disabled" || plan.NetworkTargetCount != 0 ||
		plan.EnvironmentCount != 0 || plan.SecretReferenceCount != 0 ||
		!plan.SimulationOnly || plan.ProductionSubmitted || plan.ProductionVerified ||
		plan.BackendAvailable || plan.BackendEnabled || plan.ExecutionAuthorized ||
		plan.ArtifactCommitAuthorized {
		return apperror.New(apperror.CodeFailedPrecondition,
			"Docker lifecycle current authority does not exactly match durable intent")
	}
	return nil
}

func latestLifecycleTransition(record sandbox.DockerContainerLifecycleRecord,
	state string,
) *sandbox.DockerContainerLifecycleTransition {
	for index := len(record.Transitions) - 1; index >= 0; index-- {
		if record.Transitions[index].State == state {
			value := record.Transitions[index]
			return &value
		}
	}
	return nil
}

func lifecycleContainerIDFingerprint(record sandbox.DockerContainerLifecycleRecord) string {
	for index := len(record.Transitions) - 1; index >= 0; index-- {
		if record.Transitions[index].ContainerIDFingerprint != "" {
			return record.Transitions[index].ContainerIDFingerprint
		}
	}
	return ""
}

func lifecycleFailureReason(err error) string {
	if code := sandbox.DockerContainerLifecycleErrorCode(err); code != "" {
		return code
	}
	switch {
	case errors.Is(err, context.Canceled):
		return sandbox.DockerContainerLifecycleReasonCancelled
	case errors.Is(err, context.DeadlineExceeded):
		return sandbox.DockerContainerLifecycleReasonTimeout
	default:
		return sandbox.DockerContainerLifecycleReasonWaitFailed
	}
}

func dockerLifecycleApplicationCode(reason string) apperror.Code {
	switch reason {
	case sandbox.DockerContainerLifecycleFailureDisabled,
		sandbox.DockerContainerLifecycleFailureUnsupported,
		sandbox.DockerContainerLifecycleFailureConnection:
		return apperror.CodeUnavailable
	case sandbox.DockerContainerLifecycleReasonCancelled:
		return apperror.CodeCancelled
	case sandbox.DockerContainerLifecycleReasonTimeout:
		return apperror.CodeDeadlineExceeded
	case sandbox.DockerContainerLifecycleFailureConfigMismatch,
		sandbox.DockerContainerLifecycleFailureUnsafeExisting,
		sandbox.DockerContainerLifecycleFailureInvalidResponse:
		return apperror.CodeFailedPrecondition
	default:
		return apperror.CodeFailedPrecondition
	}
}
