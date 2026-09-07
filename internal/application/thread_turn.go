package application

import (
	"context"
	"fmt"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/hooks"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/runmutation"
)

const (
	threadTurnHandoffBatchSize = domain.MaxRunExecutionHandoffSteps
	threadTurnMaxBatches       = (domain.MaxPendingOperatorSteering +
		threadTurnHandoffBatchSize - 1) / threadTurnHandoffBatchSize
)

// ThreadTurnService is the product-facing execution facade. It owns the Run
// lifecycle and Supervisor handoff details so a client only submits one Thread
// turn. It composes existing durable operations and introduces no new stored
// authority, event protocol, or database record.
type ThreadTurnService struct {
	threads          *ThreadService
	lifecycle        *RunLifecycleControlService
	execution        *RunExecutionHandoffService
	recovery         *ThreadRunRecoveryService
	lifecycleHooks   *hooks.Engine
	runtimeAuthority *domain.ExecutionPermissionRuntimeAuthority
}

type threadEpochTransitionStore interface {
	GetThreadExecutionPermission(context.Context, string) (
		domain.ThreadExecutionPermissionSnapshot, error)
	GetRunExecutionPermission(context.Context, string) (
		domain.RunExecutionPermissionSnapshot, error)
	AdvanceThreadRunForPendingConfiguration(context.Context, string, string, string, string) (
		domain.Thread, domain.Run, bool, error)
}

type ExecuteThreadTurnRequest struct {
	Version      string
	ThreadID     string
	Content      string
	OperationKey string
	RequestedBy  string
}

type ExecuteThreadTurnResult struct {
	Submission       SubmitThreadMessageResult
	Execution        *ExecuteRunHandoffResult
	Replayed         bool
	ExecutionStarted bool
	ModelCalled      bool
	ToolCalled       bool
}

func NewThreadTurnService(store ThreadStore, lifecycle *RunLifecycleControlService,
	execution *RunExecutionHandoffService,
) *ThreadTurnService {
	service := &ThreadTurnService{threads: NewThreadService(store), lifecycle: lifecycle,
		execution: execution}
	if recoveryStore, ok := store.(ThreadRunRecoveryStore); ok {
		service.recovery = NewThreadRunRecoveryService(recoveryStore)
	}
	return service
}

func NewThreadTurnServiceWithExecutionCapabilities(store ThreadStore,
	lifecycle *RunLifecycleControlService, execution *RunExecutionHandoffService,
	capabilities domain.ExecutionPermissionRuntimeCapabilities,
) *ThreadTurnService {
	service := &ThreadTurnService{
		threads:   NewThreadServiceWithExecutionCapabilities(store, capabilities),
		lifecycle: lifecycle, execution: execution,
		runtimeAuthority: capabilities.RuntimeAuthority,
	}
	if recoveryStore, ok := store.(ThreadRunRecoveryStore); ok {
		service.recovery = NewThreadRunRecoveryService(recoveryStore).
			WithExecutionPermissionRuntimeAuthority(capabilities.RuntimeAuthority)
	}
	return service
}

func (s *ThreadTurnService) WithLifecycleHooks(engine *hooks.Engine) *ThreadTurnService {
	if s != nil {
		s.lifecycleHooks = engine
		if s.recovery != nil {
			s.recovery.WithLifecycleHooks(engine)
		}
	}
	return s
}

func (s *ThreadTurnService) WithExecutionPermissionRuntimeAuthority(
	authority *domain.ExecutionPermissionRuntimeAuthority,
) *ThreadTurnService {
	if s != nil {
		s.runtimeAuthority = authority
		if s.recovery != nil {
			s.recovery.WithExecutionPermissionRuntimeAuthority(authority)
		}
	}
	return s
}

func (s *ThreadTurnService) WithModelRouteRegistry(
	registry ThreadModelRouteRegistry,
) *ThreadTurnService {
	if s != nil && s.threads != nil {
		s.threads.WithModelRouteRegistry(registry)
	}
	return s
}

func (s *ThreadTurnService) Execute(ctx context.Context,
	request ExecuteThreadTurnRequest,
) (ExecuteThreadTurnResult, error) {
	if s == nil || s.threads == nil || s.threads.store == nil || s.lifecycle == nil ||
		s.execution == nil || s.execution.store == nil || s.execution.supervisor == nil {
		return ExecuteThreadTurnResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"Thread turn dependencies are required")
	}
	normalized, err := normalizeSubmitThreadMessageRequest(SubmitThreadMessageRequest{
		Version: request.Version, ThreadID: request.ThreadID, Content: request.Content,
		OperationKey: request.OperationKey, RequestedBy: request.RequestedBy,
	})
	if err != nil {
		return ExecuteThreadTurnResult{}, err
	}
	if replay, found, err := s.findCompletedReplay(ctx, normalized); err != nil || found {
		return replay, err
	}
	if err := s.advancePastFailedTurn(ctx, normalized); err != nil {
		return ExecuteThreadTurnResult{}, err
	}
	if err := s.advanceForPendingConfiguration(ctx, normalized); err != nil {
		return ExecuteThreadTurnResult{}, err
	}

	submission, err := s.threads.Submit(ctx, normalized)
	if err != nil {
		return ExecuteThreadTurnResult{}, err
	}
	result := ExecuteThreadTurnResult{Submission: submission, Replayed: submission.Replayed}
	if submission.Message.Status == domain.OperatorSteeringCancelled ||
		submission.Run.Status == domain.RunWaitingApproval {
		return s.refresh(ctx, result)
	}

	switch submission.Run.Status {
	case domain.RunCreated:
		controlled, err := s.lifecycle.Apply(ctx, ControlRunLifecycleRequest{
			Version: domain.RunLifecycleControlProtocolVersion, RunID: submission.Run.ID,
			Action: domain.RunLifecycleStart,
			OperationKey: threadTurnLifecycleOperationKey(normalized, submission.Run.ID,
				domain.RunLifecycleStart),
			RequestedBy: normalized.RequestedBy,
		})
		if err != nil {
			return result, err
		}
		result.Submission.Run = controlled.Run
		result.Replayed = result.Replayed || controlled.Replayed
	case domain.RunPaused:
		controlled, err := s.lifecycle.Apply(ctx, ControlRunLifecycleRequest{
			Version: domain.RunLifecycleControlProtocolVersion, RunID: submission.Run.ID,
			Action: domain.RunLifecycleResume,
			OperationKey: threadTurnLifecycleOperationKey(normalized, submission.Run.ID,
				domain.RunLifecycleResume),
			RequestedBy: normalized.RequestedBy,
		})
		if err != nil {
			return result, err
		}
		result.Submission.Run = controlled.Run
		result.Replayed = result.Replayed || controlled.Replayed
	case domain.RunRunning:
		// The exact running Run is ready for the durable handoff below.
	default:
		return result, apperror.New(apperror.CodeFailedPrecondition,
			fmt.Sprintf("Thread turn Run %s is %s", submission.Run.ID,
				submission.Run.Status))
	}

	for batch := 1; batch <= threadTurnMaxBatches; batch++ {
		// A product Thread turn is driven by the operator message selected into
		// this handoff. The Supervisor may perform its complete model/tool loop
		// while committing that message, but a requested/effective `continue`
		// action must not manufacture another mission-driven Supervisor turn.
		// The next explicit operator message is the next product turn.
		executed, executeErr := s.execution.Execute(ctx,
			threadTurnExecutionRequest(normalized, result.Submission.Run.ID, batch))
		result.Execution = &executed
		result.Replayed = result.Replayed || executed.Replayed
		mergeThreadTurnExecution(&result, executed)
		if executeErr != nil {
			return result, executeErr
		}
		if err := storedThreadTurnExecutionError(executed.Handoff); err != nil {
			return result, err
		}
		message, err := s.execution.store.GetOperatorSteering(ctx,
			result.Submission.Message.ID)
		if err != nil {
			return result, apperror.Normalize(err)
		}
		result.Submission.Message = message
		if message.Status != domain.OperatorSteeringPending {
			return s.refresh(ctx, result)
		}
		current, err := s.threads.store.GetRun(ctx, result.Submission.Run.ID)
		if err != nil {
			return result, apperror.Normalize(err)
		}
		result.Submission.Run = current
		if current.Status != domain.RunRunning {
			return s.refresh(ctx, result)
		}
	}
	return result, apperror.New(apperror.CodeResourceExhausted,
		"Thread turn could not reach its queued message within the bounded handoff batches")
}

func (s *ThreadTurnService) advanceForPendingConfiguration(ctx context.Context,
	request SubmitThreadMessageRequest,
) error {
	store, ok := s.threads.store.(threadEpochTransitionStore)
	if !ok {
		return nil
	}
	threadRecord, err := s.threads.store.GetThread(ctx, request.ThreadID)
	if err != nil {
		return apperror.Normalize(err)
	}
	if threadRecord.ActiveRunID == "" {
		return nil
	}
	active, err := s.threads.store.GetRun(ctx, threadRecord.ActiveRunID)
	if err != nil {
		return apperror.Normalize(err)
	}
	pending, err := s.pendingEpochConfiguration(ctx, store, threadRecord, active)
	if err != nil || !pending {
		return err
	}
	_, superseded, _, err := store.AdvanceThreadRunForPendingConfiguration(ctx,
		threadRecord.ID, active.ID, request.RequestedBy,
		"thread-turn-epoch-transition-"+runmutation.Fingerprint(
			"thread_turn_epoch_transition_operation.v1", request.ThreadID,
			active.ID, request.OperationKey))
	if err != nil {
		if apperror.CodeOf(err) == apperror.CodeConflict {
			current, currentErr := s.threads.store.GetThread(ctx, request.ThreadID)
			if currentErr == nil && current.ActiveRunID != active.ID {
				return nil
			}
		}
		return apperror.Normalize(err)
	}
	if s.runtimeAuthority != nil {
		s.runtimeAuthority.RevokeRun(superseded.ID)
	}
	mission, missionErr := s.threads.store.GetMission(ctx, superseded.MissionID)
	if missionErr == nil {
		_ = executeLifecycleBoundary(ctx, s.lifecycleHooks, hooks.RunCompleted,
			superseded.ID, mission.WorkspaceID, map[string]any{
				"session_id": superseded.SessionID, "from": active.Status,
				"to": domain.RunCancelled, "source": "thread_epoch_transition",
			})
	}
	if releaser, ok := s.threads.store.(threadRunRecoveryMonetaryReleaser); ok {
		_, _ = releaser.ReleaseOpenMonetaryReservations(ctx, superseded.ID)
	}
	if reconciler, ok := s.threads.store.(threadRunRecoveryDependencyReconciler); ok {
		_, _ = reconciler.ReconcileDependencyEdges(ctx, superseded.ID)
	}
	return nil
}

func (s *ThreadTurnService) pendingEpochConfiguration(ctx context.Context,
	store threadEpochTransitionStore, threadRecord domain.Thread, active domain.Run,
) (bool, error) {
	if reader, ok := s.threads.store.(threadModelRoutePreferenceReader); ok {
		preference, found, err := reader.GetThreadModelRoutePreference(ctx, threadRecord.ID)
		if err != nil {
			return false, apperror.Normalize(err)
		}
		if found {
			desiredRoute := ""
			if preference.Selected {
				desiredRoute = preference.Provider + "/" + preference.Model
			} else {
				mission, missionErr := s.threads.store.GetMission(ctx, threadRecord.MissionID)
				if missionErr != nil {
					return false, apperror.Normalize(missionErr)
				}
				desiredRoute = string(mission.Profile)
			}
			if s.threads.modelRoutes == nil {
				return false, apperror.New(apperror.CodeFailedPrecondition,
					"Thread model route Registry is required")
			}
			activeRef, activeErr := resolveConfiguredModelRef(
				s.threads.modelRoutes.Router(), active.Config.ModelRoute)
			desiredRef, desiredErr := resolveConfiguredModelRef(
				s.threads.modelRoutes.Router(), desiredRoute)
			if activeErr != nil || desiredErr != nil {
				return false, apperror.New(apperror.CodeFailedPrecondition,
					"Thread model route is no longer resolvable")
			}
			if activeRef != desiredRef {
				return true, nil
			}
		}
	}
	threadPermission, err := store.GetThreadExecutionPermission(ctx, threadRecord.ID)
	if err != nil {
		return false, apperror.Normalize(err)
	}
	runPermission, err := store.GetRunExecutionPermission(ctx, active.ID)
	if err != nil {
		return false, apperror.Normalize(err)
	}
	return threadPermission.Mode != runPermission.Mode, nil
}

func (s *ThreadTurnService) advancePastFailedTurn(ctx context.Context,
	request SubmitThreadMessageRequest,
) error {
	if s.recovery == nil || s.recovery.store == nil {
		return nil
	}
	recovery, found, err := s.recovery.store.GetThreadRunRecovery(ctx, request.ThreadID)
	if err != nil {
		return apperror.Normalize(err)
	}
	if !found {
		return nil
	}
	if !recovery.Quiescent {
		return apperror.New(apperror.CodeUnavailable,
			"The previous Thread turn is still stopping; retry this message shortly")
	}
	_, err = s.recovery.RecoverForNextTurn(ctx, RecoverThreadRunRequest{
		Version: domain.ThreadRunRecoveryProtocolVersion, ThreadID: request.ThreadID,
		RunID: recovery.RunID, HandoffOperationID: recovery.HandoffOperationID,
		OperationKey: "thread-turn-auto-recovery-" + runmutation.Fingerprint(
			"thread_turn_auto_recovery_operation.v1", request.ThreadID,
			recovery.RunID, recovery.HandoffOperationID, request.OperationKey),
		RequestedBy: request.RequestedBy,
	})
	if err == nil {
		return nil
	}
	// A concurrent explicit turn may have already advanced the same failed Run.
	// Once the Thread no longer points at that exact Run, normal Submit can join
	// the successor without exposing an internal recovery race to the user.
	if apperror.CodeOf(err) == apperror.CodeConflict {
		current, currentErr := s.threads.store.GetThread(ctx, request.ThreadID)
		if currentErr == nil && current.ActiveRunID != recovery.RunID {
			return nil
		}
	}
	return err
}

func (s *ThreadTurnService) findCompletedReplay(ctx context.Context,
	request SubmitThreadMessageRequest,
) (ExecuteThreadTurnResult, bool, error) {
	threadRecord, err := s.threads.store.GetThread(ctx, request.ThreadID)
	if err != nil {
		return ExecuteThreadTurnResult{}, false, apperror.Normalize(err)
	}
	bindings, err := s.threads.store.ListThreadRuns(ctx, request.ThreadID)
	if err != nil {
		return ExecuteThreadTurnResult{}, false, apperror.Normalize(err)
	}
	expectedContent, err := domain.NormalizeOperatorSteeringContent(
		redact.String(request.Content))
	if err != nil {
		return ExecuteThreadTurnResult{}, false, apperror.Normalize(err)
	}
	expectedDigest := domain.OperatorSteeringContentSHA256(expectedContent)
	for bindingIndex := len(bindings) - 1; bindingIndex >= 0; bindingIndex-- {
		binding := bindings[bindingIndex]
		run, err := s.threads.store.GetRun(ctx, binding.RunID)
		if err != nil {
			return ExecuteThreadTurnResult{}, false, apperror.Normalize(err)
		}
		for batch := threadTurnMaxBatches; batch >= 1; batch-- {
			executionRequest := threadTurnExecutionRequest(request, run.ID, batch)
			normalizedExecution, err := normalizeRunExecutionHandoffRequest(executionRequest)
			if err != nil {
				return ExecuteThreadTurnResult{}, false, err
			}
			keyDigest := runmutation.RunExecutionHandoffOperationDigest(run.ID,
				normalizedExecution.OperationKey)
			handoff, found, err := s.execution.store.GetRunExecutionHandoff(ctx, keyDigest)
			if err != nil {
				return ExecuteThreadTurnResult{}, false, apperror.Normalize(err)
			}
			if !found || handoff.Result == nil {
				continue
			}
			fingerprint := runmutation.RunExecutionHandoffRequestFingerprint(run.ID,
				normalizedExecution.RequestedBy, normalizedExecution.MaxSteps)
			if err := validateRunExecutionHandoffReplay(handoff, normalizedExecution,
				keyDigest, fingerprint); err != nil {
				return ExecuteThreadTurnResult{}, false, err
			}
			for _, item := range handoff.Items {
				message, err := s.execution.store.GetOperatorSteering(ctx, item.MessageID)
				if err != nil {
					return ExecuteThreadTurnResult{}, false, apperror.Normalize(err)
				}
				if message.RequestedBy != request.RequestedBy ||
					message.ContentSHA256 != expectedDigest {
					continue
				}
				queued, err := s.threads.store.EnqueueOperatorSteering(ctx,
					domain.EnqueueOperatorSteeringRequest{RunID: run.ID,
						SessionID: run.SessionID, Content: request.Content,
						OperationKey: request.OperationKey, RequestedBy: request.RequestedBy})
				if err != nil {
					return ExecuteThreadTurnResult{}, false, apperror.Normalize(err)
				}
				if !queued.Replayed || queued.Message.ID != message.ID {
					return ExecuteThreadTurnResult{}, false, apperror.New(
						apperror.CodeConflict,
						"Thread turn replay does not match its durable operator message")
				}
				if queued.Message.Status == domain.OperatorSteeringPending {
					if handoff.Result.Status == domain.RunExecutionHandoffFailed {
						linkedSession, sessionErr := s.threads.store.GetSession(ctx,
							run.SessionID)
						if sessionErr != nil {
							return ExecuteThreadTurnResult{}, false,
								apperror.Normalize(sessionErr)
						}
						executed := ExecuteRunHandoffResult{Handoff: handoff,
							Execution: executionResultFromHandoff(handoff), Replayed: true}
						result := ExecuteThreadTurnResult{
							Submission: SubmitThreadMessageResult{Thread: threadRecord,
								Run: run, Session: linkedSession, Message: queued.Message,
								PredecessorRunID: binding.PredecessorRunID, Replayed: true},
							Execution: &executed, Replayed: true,
						}
						mergeThreadTurnExecution(&result, executed)
						return result, true, storedThreadTurnExecutionError(handoff)
					}
					// The previous batch reached approval, wait, or another safe boundary
					// before this exact message. A later retry may resume the same Run and
					// advance to the next deterministic batch without duplicating input.
					continue
				}
				linkedSession, err := s.threads.store.GetSession(ctx, run.SessionID)
				if err != nil {
					return ExecuteThreadTurnResult{}, false, apperror.Normalize(err)
				}
				executed := ExecuteRunHandoffResult{Handoff: handoff,
					Execution: executionResultFromHandoff(handoff), Replayed: true}
				result := ExecuteThreadTurnResult{
					Submission: SubmitThreadMessageResult{Thread: threadRecord, Run: run,
						Session: linkedSession, Message: queued.Message,
						PredecessorRunID: binding.PredecessorRunID,
						SuccessorCreated: binding.PredecessorRunID != "" &&
							queued.Message.Sequence == 1,
						Replayed: true},
					Execution: &executed, Replayed: true,
				}
				mergeThreadTurnExecution(&result, executed)
				if err := storedThreadTurnExecutionError(handoff); err != nil {
					return result, true, err
				}
				return result, true, nil
			}
		}
	}
	return ExecuteThreadTurnResult{}, false, nil
}

func (s *ThreadTurnService) refresh(ctx context.Context,
	result ExecuteThreadTurnResult,
) (ExecuteThreadTurnResult, error) {
	run, err := s.threads.store.GetRun(ctx, result.Submission.Run.ID)
	if err != nil {
		return result, apperror.Normalize(err)
	}
	threadRecord, err := s.threads.store.GetThread(ctx, result.Submission.Thread.ID)
	if err != nil {
		return result, apperror.Normalize(err)
	}
	result.Submission.Run = run
	result.Submission.Thread = threadRecord
	return result, nil
}

func threadTurnLifecycleOperationKey(request SubmitThreadMessageRequest, runID string,
	action domain.RunLifecycleAction,
) string {
	return "thread-turn-lifecycle-" + runmutation.Fingerprint(
		"thread_turn_lifecycle_operation.v1", request.ThreadID, runID,
		request.OperationKey, string(action))
}

func threadTurnExecutionRequest(request SubmitThreadMessageRequest, runID string,
	batch int,
) ExecuteRunHandoffRequest {
	return ExecuteRunHandoffRequest{Version: domain.RunExecutionHandoffProtocolVersion,
		RunID: runID, MaxSteps: threadTurnHandoffBatchSize,
		OperationKey: "thread-turn-handoff-" + runmutation.Fingerprint(
			"thread_turn_handoff_operation.v1", request.ThreadID, runID,
			request.OperationKey, fmt.Sprint(batch)),
		RequestedBy: request.RequestedBy}
}

func mergeThreadTurnExecution(result *ExecuteThreadTurnResult,
	executed ExecuteRunHandoffResult,
) {
	if result == nil || executed.Handoff.Result == nil {
		return
	}
	completed := executed.Handoff.Result
	result.ExecutionStarted = result.ExecutionStarted || completed.LeaseID != ""
	result.ModelCalled = result.ModelCalled || completed.ModelCalled
	result.ToolCalled = result.ToolCalled || completed.ToolCalled
}

func storedThreadTurnExecutionError(handoff domain.RunExecutionHandoff) error {
	if handoff.Result == nil || handoff.Result.Status != domain.RunExecutionHandoffFailed {
		return nil
	}
	code := apperror.Code(strings.ToUpper(strings.TrimSpace(handoff.Result.ErrorCode)))
	if code == "" {
		code = apperror.CodeInternal
	}
	message := "This Thread turn stopped at a durable failure boundary; send the next message to continue in a fresh execution context"
	switch code {
	case apperror.CodeFailedPrecondition:
		message = "The current Thread turn could not continue because an execution precondition was not met; send another message to retry this Thread"
	case apperror.CodeUnavailable:
		message = "The model service is unavailable; retry or send another message to continue this Thread"
	case apperror.CodeDeadlineExceeded:
		message = "The current turn timed out; send the next message to continue this Thread"
	case apperror.CodeResourceExhausted:
		message = "The current execution reached a resource limit; adjust the settings and send the next message to continue"
	}
	return apperror.New(code, message)
}
