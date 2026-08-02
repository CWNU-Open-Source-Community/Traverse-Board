package browserruntime

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	BrowserRuntimeCheckpointProtocolVersion = "browser_runtime_checkpoint.v1"
	BrowserRuntimeReceiptProtocolVersion    = "browser_runtime_receipt.v1"
	BrowserRuntimeCleanupTimeout            = 20 * time.Second
)

type BrowserRuntimeLifecycleStage string

const (
	BrowserRuntimeStageRunning          BrowserRuntimeLifecycleStage = "running"
	BrowserRuntimeStageCDPClosed        BrowserRuntimeLifecycleStage = "cdp_closed"
	BrowserRuntimeStageProcessQuiescent BrowserRuntimeLifecycleStage = "process_quiescent"
	BrowserRuntimeStageNetworkReleased  BrowserRuntimeLifecycleStage = "network_released"
	BrowserRuntimeStageProfileReleased  BrowserRuntimeLifecycleStage = "profile_released"
	BrowserRuntimeStageCompleted        BrowserRuntimeLifecycleStage = "completed"
	BrowserRuntimeStageFailed           BrowserRuntimeLifecycleStage = "failed"
)

type BrowserRuntimeCheckpoint struct {
	ProtocolVersion               string                       `json:"protocol_version"`
	ID                            string                       `json:"id"`
	RuntimeID                     string                       `json:"runtime_id"`
	RunID                         string                       `json:"run_id"`
	AttemptID                     string                       `json:"attempt_id"`
	AttemptFingerprint            string                       `json:"attempt_fingerprint"`
	AuthorizationFingerprint      string                       `json:"authorization_fingerprint"`
	ProcessStartSpecFingerprint   string                       `json:"process_start_spec_fingerprint"`
	ProfileOwnershipFingerprint   string                       `json:"profile_ownership_fingerprint"`
	ProfileLeaseFingerprint       string                       `json:"profile_lease_fingerprint"`
	ReleasedProfileFingerprint    string                       `json:"released_profile_fingerprint,omitempty"`
	PreviousCheckpointFingerprint string                       `json:"previous_checkpoint_fingerprint,omitempty"`
	Generation                    uint64                       `json:"generation"`
	Stage                         BrowserRuntimeLifecycleStage `json:"stage"`
	RestrictedCDPExpected         bool                         `json:"restricted_cdp_expected"`
	RestrictedCDPClosed           bool                         `json:"restricted_cdp_closed"`
	ProcessTerminationRequested   bool                         `json:"process_termination_requested"`
	ProcessTreeQuiescent          bool                         `json:"process_tree_quiescent"`
	NetworkCleanupVerified        bool                         `json:"network_cleanup_verified"`
	ProfileReleased               bool                         `json:"profile_released"`
	ProfileCleaned                bool                         `json:"profile_cleaned"`
	RecoveryRequired              bool                         `json:"recovery_required"`
	FailureCode                   string                       `json:"failure_code,omitempty"`
	RawOutputIncluded             bool                         `json:"raw_output_included"`
	PersonalProfileUsed           bool                         `json:"personal_profile_used"`
	FullCDPUsed                   bool                         `json:"full_cdp_used"`
	RecordedAt                    time.Time                    `json:"recorded_at"`
	Fingerprint                   string                       `json:"fingerprint"`
}

type BrowserRuntimeReceipt struct {
	ProtocolVersion            string    `json:"protocol_version"`
	ID                         string    `json:"id"`
	RuntimeID                  string    `json:"runtime_id"`
	RunID                      string    `json:"run_id"`
	AttemptFingerprint         string    `json:"attempt_fingerprint"`
	AuthorizationFingerprint   string    `json:"authorization_fingerprint"`
	FinalCheckpointFingerprint string    `json:"final_checkpoint_fingerprint"`
	ProcessExitFingerprint     string    `json:"process_exit_fingerprint,omitempty"`
	ReleasedProfileFingerprint string    `json:"released_profile_fingerprint,omitempty"`
	RestrictedCDPClosed        bool      `json:"restricted_cdp_closed"`
	ProcessTreeQuiescent       bool      `json:"process_tree_quiescent"`
	NetworkCleanupVerified     bool      `json:"network_cleanup_verified"`
	ProfileReleased            bool      `json:"profile_released"`
	ProfileCleaned             bool      `json:"profile_cleaned"`
	Succeeded                  bool      `json:"succeeded"`
	RecoveryRequired           bool      `json:"recovery_required"`
	FailureCode                string    `json:"failure_code,omitempty"`
	RawOutputIncluded          bool      `json:"raw_output_included"`
	PageContentIncluded        bool      `json:"page_content_included"`
	ScreenshotIncluded         bool      `json:"screenshot_included"`
	PersonalProfileUsed        bool      `json:"personal_profile_used"`
	FullCDPUsed                bool      `json:"full_cdp_used"`
	StartedAt                  time.Time `json:"started_at"`
	CompletedAt                time.Time `json:"completed_at"`
	Fingerprint                string    `json:"fingerprint"`
}

type BrowserRuntimeLifecycleSink interface {
	RecordBrowserRuntimeCheckpoint(context.Context, BrowserRuntimeCheckpoint) error
	RecordBrowserRuntimeReceipt(context.Context, BrowserRuntimeReceipt) error
}

type restrictedBrowserSessionCloser interface {
	Close(context.Context) error
}

type BrowserRuntimeLifecycleCoordinator struct {
	runtimeID     string
	attempt       BrowserLaunchAttempt
	authorization BrowserStartAuthorization
	ownership     ProfileOwnershipPlan
	profileLease  ProfileRuntimeLease
	process       *BrowserProcess
	cdp           restrictedBrowserSessionCloser
	sink          BrowserRuntimeLifecycleSink
	startedAt     time.Time
	checkpoint    BrowserRuntimeCheckpoint
	finalizeMu    sync.Mutex
	finalized     bool
}

func NewBrowserRuntimeLifecycleCoordinator(runtimeID string,
	attempt BrowserLaunchAttempt, authorization BrowserStartAuthorization,
	ownership ProfileOwnershipPlan, profileLease ProfileRuntimeLease,
	process *BrowserProcess, cdp *RestrictedBrowserSession,
	sink BrowserRuntimeLifecycleSink, startedAt time.Time,
) (*BrowserRuntimeLifecycleCoordinator, error) {
	return newBrowserRuntimeLifecycleCoordinator(runtimeID, attempt, authorization,
		ownership, profileLease, process, cdp, sink, startedAt)
}

func newBrowserRuntimeLifecycleCoordinator(runtimeID string,
	attempt BrowserLaunchAttempt, authorization BrowserStartAuthorization,
	ownership ProfileOwnershipPlan, profileLease ProfileRuntimeLease,
	process *BrowserProcess, cdp restrictedBrowserSessionCloser,
	sink BrowserRuntimeLifecycleSink, startedAt time.Time,
) (*BrowserRuntimeLifecycleCoordinator, error) {
	if !validPlanIdentity(runtimeID) || sink == nil || startedAt.IsZero() ||
		process == nil || process.PID() <= 0 ||
		authorization.AttemptFingerprint != attempt.Fingerprint ||
		authorization.ProfileOwnershipFingerprint != ownership.Fingerprint ||
		authorization.ProfileGeneration != ownership.Generation ||
		profileLease.AuthorizationFingerprint != authorization.Fingerprint ||
		profileLease.OwnershipPlanFingerprint != ownership.Fingerprint {
		return nil, errors.New("browser runtime lifecycle binding is invalid")
	}
	if err := ValidateStoredBrowserLaunchAttempt(attempt); err != nil {
		return nil, err
	}
	if err := ValidateProfileRuntimeLease(profileLease, authorization, ownership); err != nil {
		return nil, err
	}
	if _, exited := process.Exit(); exited {
		return nil, errors.New("browser runtime lifecycle cannot adopt an exited process")
	}
	spec := process.StartSpec()
	if spec.AuthorizationFingerprint != authorization.Fingerprint ||
		spec.ProfileOwnershipFingerprint != ownership.Fingerprint ||
		spec.ProfileLeaseFingerprint != profileLease.Fingerprint ||
		!validSHA256(spec.NetworkContainmentFingerprint) || spec.PersonalProfileUsed ||
		spec.ShellUsed {
		return nil, errors.New("browser runtime lifecycle process lost its exact binding")
	}
	coordinator := &BrowserRuntimeLifecycleCoordinator{
		runtimeID: runtimeID, attempt: attempt, authorization: authorization,
		ownership: ownership, profileLease: profileLease, process: process, cdp: cdp,
		sink: sink, startedAt: startedAt.UTC(),
	}
	coordinator.checkpoint = coordinator.newCheckpoint(BrowserRuntimeStageRunning,
		BrowserRuntimeCheckpoint{RestrictedCDPExpected: cdp != nil,
			RestrictedCDPClosed: cdp == nil}, startedAt)
	if err := ValidateBrowserRuntimeCheckpoint(coordinator.checkpoint, attempt,
		authorization, ownership, profileLease, spec, BrowserRuntimeCheckpoint{}); err != nil {
		return nil, err
	}
	return coordinator, nil
}

func (coordinator *BrowserRuntimeLifecycleCoordinator) Finalize(
	ctx context.Context,
) (BrowserRuntimeReceipt, error) {
	if coordinator == nil || coordinator.sink == nil {
		return BrowserRuntimeReceipt{}, errors.New("browser runtime lifecycle is unavailable or already finalized")
	}
	if err := coordinator.claimFinalize(); err != nil {
		return BrowserRuntimeReceipt{}, err
	}
	cleanupContext, cancel := context.WithTimeout(context.Background(), BrowserRuntimeCleanupTimeout)
	defer cancel()
	_ = ctx // Caller cancellation never skips bounded resource cleanup.

	var lifecycleErrors []error
	failureCode := ""
	persistenceHealthy := true
	noteFailure := func(code string, err error) {
		if err == nil {
			return
		}
		if failureCode == "" {
			failureCode = code
		}
		lifecycleErrors = append(lifecycleErrors, fmt.Errorf("%s: %w", code, err))
	}
	persistCurrent := func() {
		if !persistenceHealthy {
			return
		}
		if err := coordinator.recordCheckpoint(cleanupContext, coordinator.checkpoint); err != nil {
			persistenceHealthy = false
			noteFailure("checkpoint_persistence_failed", err)
		}
	}
	advance := func(stage BrowserRuntimeLifecycleStage, state BrowserRuntimeCheckpoint) {
		if err := coordinator.advance(stage, state); err != nil {
			noteFailure("checkpoint_validation_failed", err)
			return
		}
		persistCurrent()
	}

	persistCurrent()
	state := coordinator.checkpoint
	if coordinator.cdp != nil {
		if err := coordinator.cdp.Close(cleanupContext); err != nil {
			noteFailure("restricted_cdp_close_failed", err)
		} else {
			state.RestrictedCDPClosed = true
		}
	} else {
		state.RestrictedCDPClosed = true
	}
	if state.RestrictedCDPClosed {
		advance(BrowserRuntimeStageCDPClosed, state)
	}

	if _, exited := coordinator.process.Exit(); !exited {
		state.ProcessTerminationRequested = true
		if err := coordinator.process.Stop(cleanupContext); err != nil {
			noteFailure("process_termination_failed", err)
		}
	}
	select {
	case <-cleanupContext.Done():
		noteFailure("process_quiescence_timeout", cleanupContext.Err())
	case <-coordinator.process.Done():
	}
	exit, exited := coordinator.process.Exit()
	if exited && exit.TreeReaped && !exit.RawOutputIncluded &&
		exit.StartSpecFingerprint == coordinator.process.StartSpec().Fingerprint &&
		exit.Fingerprint == browserRuntimeFingerprint(exit) {
		state.ProcessTreeQuiescent = true
		if state.RestrictedCDPClosed {
			advance(BrowserRuntimeStageProcessQuiescent, state)
		}
	} else {
		noteFailure("process_tree_not_quiescent",
			errors.New("browser process tree did not publish a clean bound exit"))
	}

	if state.ProcessTreeQuiescent {
		verified, err := coordinator.process.WaitForContainmentCleanup(cleanupContext)
		if err != nil || !verified {
			noteFailure("network_cleanup_unverified",
				errors.Join(err, errors.New("browser network cleanup was not verified")))
		} else {
			state.NetworkCleanupVerified = true
			if state.RestrictedCDPClosed {
				advance(BrowserRuntimeStageNetworkReleased, state)
			}
		}
	}

	var released ProfileRuntimeLease
	if state.ProcessTreeQuiescent && state.NetworkCleanupVerified {
		var err error
		released, err = ReleaseDisposableProfile(coordinator.authorization,
			coordinator.profileLease, coordinator.ownership, true, lifecycleNow(coordinator.startedAt))
		if err != nil {
			noteFailure("profile_release_failed", err)
		} else {
			state.ProfileReleased = true
			state.ReleasedProfileFingerprint = released.Fingerprint
			if state.RestrictedCDPClosed {
				advance(BrowserRuntimeStageProfileReleased, state)
			}
		}
	}

	if state.ProfileReleased {
		if err := CleanupReleasedProfile(coordinator.authorization, released,
			coordinator.ownership, true); err != nil {
			noteFailure("profile_cleanup_failed", err)
		} else {
			state.ProfileCleaned = true
			state.RecoveryRequired = false
		}
	}

	if failureCode == "" && state.RestrictedCDPClosed && state.ProcessTreeQuiescent &&
		state.NetworkCleanupVerified && state.ProfileReleased && state.ProfileCleaned {
		advance(BrowserRuntimeStageCompleted, state)
	}
	if failureCode != "" || coordinator.checkpoint.Stage != BrowserRuntimeStageCompleted {
		if failureCode == "" {
			failureCode = "runtime_cleanup_incomplete"
			noteFailure(failureCode, errors.New("browser runtime cleanup did not reach completion"))
		}
		state.RecoveryRequired = !state.ProfileCleaned
		state.FailureCode = failureCode
		if err := coordinator.advance(BrowserRuntimeStageFailed, state); err != nil {
			noteFailure("checkpoint_validation_failed", err)
		} else {
			persistCurrent()
		}
	}

	succeeded := failureCode == "" && coordinator.checkpoint.Stage == BrowserRuntimeStageCompleted
	receipt := coordinator.newReceipt(exit, coordinator.checkpoint, succeeded, failureCode)
	if err := ValidateBrowserRuntimeReceipt(receipt, coordinator.checkpoint,
		coordinator.attempt, coordinator.authorization); err != nil {
		noteFailure("receipt_validation_failed", err)
		return receipt, errors.Join(lifecycleErrors...)
	}
	if persistenceHealthy {
		if err := coordinator.sink.RecordBrowserRuntimeReceipt(cleanupContext, receipt); err != nil {
			noteFailure("receipt_persistence_failed", err)
		}
	}
	if len(lifecycleErrors) > 0 {
		return receipt, errors.Join(lifecycleErrors...)
	}
	return receipt, nil
}

func (coordinator *BrowserRuntimeLifecycleCoordinator) claimFinalize() error {
	coordinator.finalizeMu.Lock()
	defer coordinator.finalizeMu.Unlock()
	if coordinator.finalized {
		return errors.New("browser runtime lifecycle is unavailable or already finalized")
	}
	coordinator.finalized = true
	return nil
}

func (coordinator *BrowserRuntimeLifecycleCoordinator) advance(
	stage BrowserRuntimeLifecycleStage, state BrowserRuntimeCheckpoint,
) error {
	previous := coordinator.checkpoint
	next := coordinator.newCheckpoint(stage, state, lifecycleNow(previous.RecordedAt))
	if err := ValidateBrowserRuntimeCheckpoint(next, coordinator.attempt,
		coordinator.authorization, coordinator.ownership, coordinator.profileLease,
		coordinator.process.StartSpec(), previous); err != nil {
		return err
	}
	coordinator.checkpoint = next
	return nil
}

func (coordinator *BrowserRuntimeLifecycleCoordinator) recordCheckpoint(ctx context.Context,
	checkpoint BrowserRuntimeCheckpoint,
) error {
	if err := coordinator.sink.RecordBrowserRuntimeCheckpoint(ctx, checkpoint); err != nil {
		return fmt.Errorf("persist browser runtime checkpoint: %w", err)
	}
	return nil
}

func (coordinator *BrowserRuntimeLifecycleCoordinator) newCheckpoint(
	stage BrowserRuntimeLifecycleStage, state BrowserRuntimeCheckpoint, recordedAt time.Time,
) BrowserRuntimeCheckpoint {
	generation := coordinator.checkpoint.Generation + 1
	previous := coordinator.checkpoint.Fingerprint
	checkpoint := BrowserRuntimeCheckpoint{
		ProtocolVersion: BrowserRuntimeCheckpointProtocolVersion,
		ID:              coordinator.runtimeID + "-checkpoint-" + strconv.FormatUint(generation, 10),
		RuntimeID:       coordinator.runtimeID, RunID: coordinator.attempt.RunID,
		AttemptID: coordinator.attempt.ID, AttemptFingerprint: coordinator.attempt.Fingerprint,
		AuthorizationFingerprint:      coordinator.authorization.Fingerprint,
		ProcessStartSpecFingerprint:   coordinator.process.StartSpec().Fingerprint,
		ProfileOwnershipFingerprint:   coordinator.ownership.Fingerprint,
		ProfileLeaseFingerprint:       coordinator.profileLease.Fingerprint,
		ReleasedProfileFingerprint:    state.ReleasedProfileFingerprint,
		PreviousCheckpointFingerprint: previous, Generation: generation, Stage: stage,
		RestrictedCDPExpected:       state.RestrictedCDPExpected,
		RestrictedCDPClosed:         state.RestrictedCDPClosed,
		ProcessTerminationRequested: state.ProcessTerminationRequested,
		ProcessTreeQuiescent:        state.ProcessTreeQuiescent,
		NetworkCleanupVerified:      state.NetworkCleanupVerified,
		ProfileReleased:             state.ProfileReleased, ProfileCleaned: state.ProfileCleaned,
		RecoveryRequired: state.RecoveryRequired, FailureCode: state.FailureCode,
		RecordedAt: recordedAt.UTC(),
	}
	checkpoint.Fingerprint = browserRuntimeFingerprint(checkpoint)
	return checkpoint
}

func (coordinator *BrowserRuntimeLifecycleCoordinator) newReceipt(exit BrowserProcessExit,
	checkpoint BrowserRuntimeCheckpoint, succeeded bool, failureCode string,
) BrowserRuntimeReceipt {
	receipt := BrowserRuntimeReceipt{
		ProtocolVersion: BrowserRuntimeReceiptProtocolVersion,
		ID:              coordinator.runtimeID + "-receipt", RuntimeID: coordinator.runtimeID,
		RunID: coordinator.attempt.RunID, AttemptFingerprint: coordinator.attempt.Fingerprint,
		AuthorizationFingerprint:   coordinator.authorization.Fingerprint,
		FinalCheckpointFingerprint: checkpoint.Fingerprint,
		ProcessExitFingerprint:     exit.Fingerprint,
		ReleasedProfileFingerprint: checkpoint.ReleasedProfileFingerprint,
		RestrictedCDPClosed:        checkpoint.RestrictedCDPClosed,
		ProcessTreeQuiescent:       checkpoint.ProcessTreeQuiescent,
		NetworkCleanupVerified:     checkpoint.NetworkCleanupVerified,
		ProfileReleased:            checkpoint.ProfileReleased, ProfileCleaned: checkpoint.ProfileCleaned,
		Succeeded: succeeded, RecoveryRequired: !checkpoint.ProfileCleaned,
		FailureCode: strings.TrimSpace(failureCode), StartedAt: coordinator.startedAt,
		CompletedAt: lifecycleNow(coordinator.startedAt),
	}
	receipt.Fingerprint = browserRuntimeFingerprint(receipt)
	return receipt
}

func ValidateBrowserRuntimeCheckpoint(checkpoint BrowserRuntimeCheckpoint,
	attempt BrowserLaunchAttempt, authorization BrowserStartAuthorization,
	ownership ProfileOwnershipPlan, profileLease ProfileRuntimeLease,
	spec BrowserStartSpec, previous BrowserRuntimeCheckpoint,
) error {
	if err := ValidateStoredBrowserRuntimeCheckpoint(checkpoint); err != nil {
		return err
	}
	if checkpoint.RunID != attempt.RunID || checkpoint.AttemptID != attempt.ID ||
		checkpoint.AttemptFingerprint != attempt.Fingerprint ||
		checkpoint.AuthorizationFingerprint != authorization.Fingerprint ||
		checkpoint.ProcessStartSpecFingerprint != spec.Fingerprint ||
		checkpoint.ProfileOwnershipFingerprint != ownership.Fingerprint ||
		checkpoint.ProfileLeaseFingerprint != profileLease.Fingerprint {
		return errors.New("browser runtime checkpoint lost its exact redacted binding")
	}
	return ValidateStoredBrowserRuntimeCheckpointSuccessor(checkpoint, previous)
}

func ValidateStoredBrowserRuntimeCheckpointSuccessor(checkpoint BrowserRuntimeCheckpoint,
	previous BrowserRuntimeCheckpoint,
) error {
	if err := ValidateStoredBrowserRuntimeCheckpoint(checkpoint); err != nil {
		return err
	}
	if previous.ProtocolVersion == "" {
		if checkpoint.Generation != 1 {
			return errors.New("browser runtime initial checkpoint is invalid")
		}
	} else {
		if err := ValidateStoredBrowserRuntimeCheckpoint(previous); err != nil {
			return fmt.Errorf("validate previous browser runtime checkpoint: %w", err)
		}
		if checkpoint.Generation != previous.Generation+1 ||
			checkpoint.PreviousCheckpointFingerprint != previous.Fingerprint ||
			checkpoint.RuntimeID != previous.RuntimeID || checkpoint.RunID != previous.RunID ||
			checkpoint.AttemptID != previous.AttemptID ||
			checkpoint.AuthorizationFingerprint != previous.AuthorizationFingerprint ||
			checkpoint.RestrictedCDPExpected != previous.RestrictedCDPExpected ||
			checkpoint.RecordedAt.Before(previous.RecordedAt) ||
			!browserRuntimeCleanupStateMonotonic(previous, checkpoint) ||
			!validBrowserRuntimeStageTransition(previous.Stage, checkpoint.Stage) {
			return errors.New("browser runtime checkpoint generation or ancestry changed")
		}
	}
	return nil
}

func ValidateStoredBrowserRuntimeCheckpoint(checkpoint BrowserRuntimeCheckpoint) error {
	if checkpoint.ProtocolVersion != BrowserRuntimeCheckpointProtocolVersion ||
		!validPlanIdentity(checkpoint.ID) || !validPlanIdentity(checkpoint.RuntimeID) ||
		!validPlanIdentity(checkpoint.RunID) || !validPlanIdentity(checkpoint.AttemptID) ||
		!validSHA256(checkpoint.AttemptFingerprint) ||
		!validSHA256(checkpoint.AuthorizationFingerprint) ||
		!validSHA256(checkpoint.ProcessStartSpecFingerprint) ||
		!validSHA256(checkpoint.ProfileOwnershipFingerprint) ||
		!validSHA256(checkpoint.ProfileLeaseFingerprint) ||
		checkpoint.Generation == 0 || checkpoint.RecordedAt.IsZero() ||
		checkpoint.RawOutputIncluded || checkpoint.PersonalProfileUsed || checkpoint.FullCDPUsed ||
		checkpoint.Fingerprint != browserRuntimeFingerprint(checkpoint) {
		return errors.New("stored browser runtime checkpoint is invalid or unredacted")
	}
	if checkpoint.Generation == 1 {
		if checkpoint.PreviousCheckpointFingerprint != "" ||
			checkpoint.Stage != BrowserRuntimeStageRunning {
			return errors.New("stored browser runtime initial checkpoint is invalid")
		}
	} else if !validSHA256(checkpoint.PreviousCheckpointFingerprint) ||
		checkpoint.Stage == BrowserRuntimeStageRunning {
		return errors.New("stored browser runtime checkpoint ancestry is invalid")
	}
	if !checkpoint.RestrictedCDPExpected && !checkpoint.RestrictedCDPClosed {
		return errors.New("browser runtime without CDP must preserve the closed invariant")
	}
	if checkpoint.NetworkCleanupVerified && !checkpoint.ProcessTreeQuiescent ||
		checkpoint.ProfileReleased && !checkpoint.NetworkCleanupVerified ||
		checkpoint.ProfileCleaned && !checkpoint.ProfileReleased ||
		checkpoint.ProfileReleased != validSHA256(checkpoint.ReleasedProfileFingerprint) {
		return errors.New("browser runtime checkpoint cleanup order is invalid")
	}
	switch checkpoint.Stage {
	case BrowserRuntimeStageRunning:
		if checkpoint.Generation != 1 || checkpoint.ProcessTreeQuiescent ||
			checkpoint.NetworkCleanupVerified || checkpoint.ProfileReleased ||
			checkpoint.ProfileCleaned || checkpoint.RecoveryRequired || checkpoint.FailureCode != "" {
			return errors.New("running browser runtime checkpoint contains cleanup state")
		}
	case BrowserRuntimeStageCDPClosed:
		if !checkpoint.RestrictedCDPClosed || checkpoint.ProcessTreeQuiescent {
			return errors.New("CDP-closed checkpoint is invalid")
		}
	case BrowserRuntimeStageProcessQuiescent:
		if !checkpoint.RestrictedCDPClosed || !checkpoint.ProcessTreeQuiescent ||
			checkpoint.NetworkCleanupVerified {
			return errors.New("process-quiescent checkpoint is invalid")
		}
	case BrowserRuntimeStageNetworkReleased:
		if !checkpoint.RestrictedCDPClosed || !checkpoint.ProcessTreeQuiescent ||
			!checkpoint.NetworkCleanupVerified || checkpoint.ProfileReleased {
			return errors.New("network-released checkpoint is invalid")
		}
	case BrowserRuntimeStageProfileReleased:
		if !checkpoint.NetworkCleanupVerified || !checkpoint.ProfileReleased ||
			checkpoint.ProfileCleaned {
			return errors.New("profile-released checkpoint is invalid")
		}
	case BrowserRuntimeStageCompleted:
		if !checkpoint.RestrictedCDPClosed || !checkpoint.ProcessTreeQuiescent ||
			!checkpoint.NetworkCleanupVerified || !checkpoint.ProfileReleased ||
			!checkpoint.ProfileCleaned || checkpoint.RecoveryRequired || checkpoint.FailureCode != "" {
			return errors.New("completed browser runtime checkpoint is incomplete")
		}
	case BrowserRuntimeStageFailed:
		if !validBrowserRuntimeFailureCode(checkpoint.FailureCode) ||
			checkpoint.RecoveryRequired == checkpoint.ProfileCleaned {
			return errors.New("failed browser runtime checkpoint has invalid recovery state")
		}
	default:
		return errors.New("browser runtime checkpoint stage is invalid")
	}
	return nil
}

func ValidateBrowserRuntimeReceipt(receipt BrowserRuntimeReceipt,
	checkpoint BrowserRuntimeCheckpoint, attempt BrowserLaunchAttempt,
	authorization BrowserStartAuthorization,
) error {
	if err := ValidateStoredBrowserRuntimeReceipt(receipt); err != nil {
		return err
	}
	if receipt.RunID != attempt.RunID || receipt.AttemptFingerprint != attempt.Fingerprint ||
		receipt.AuthorizationFingerprint != authorization.Fingerprint ||
		checkpoint.AttemptFingerprint != attempt.Fingerprint ||
		checkpoint.AuthorizationFingerprint != authorization.Fingerprint {
		return errors.New("browser runtime receipt lost its Run authorization binding")
	}
	return ValidateStoredBrowserRuntimeReceiptForCheckpoint(receipt, checkpoint)
}

func ValidateStoredBrowserRuntimeReceiptForCheckpoint(receipt BrowserRuntimeReceipt,
	checkpoint BrowserRuntimeCheckpoint,
) error {
	if err := ValidateStoredBrowserRuntimeReceipt(receipt); err != nil {
		return err
	}
	if err := ValidateStoredBrowserRuntimeCheckpoint(checkpoint); err != nil {
		return err
	}
	if receipt.RuntimeID != checkpoint.RuntimeID || receipt.RunID != checkpoint.RunID ||
		receipt.AttemptFingerprint != checkpoint.AttemptFingerprint ||
		receipt.AuthorizationFingerprint != checkpoint.AuthorizationFingerprint ||
		receipt.FinalCheckpointFingerprint != checkpoint.Fingerprint ||
		receipt.ReleasedProfileFingerprint != checkpoint.ReleasedProfileFingerprint ||
		receipt.RestrictedCDPClosed != checkpoint.RestrictedCDPClosed ||
		receipt.ProcessTreeQuiescent != checkpoint.ProcessTreeQuiescent ||
		receipt.NetworkCleanupVerified != checkpoint.NetworkCleanupVerified ||
		receipt.ProfileReleased != checkpoint.ProfileReleased ||
		receipt.ProfileCleaned != checkpoint.ProfileCleaned {
		return errors.New("browser runtime receipt lost its redacted lifecycle binding")
	}
	if receipt.Succeeded {
		if checkpoint.Stage != BrowserRuntimeStageCompleted || receipt.FailureCode != "" ||
			!receipt.RestrictedCDPClosed || !receipt.ProcessTreeQuiescent ||
			!receipt.NetworkCleanupVerified || !receipt.ProfileReleased ||
			!receipt.ProfileCleaned || receipt.RecoveryRequired ||
			!validSHA256(receipt.ProcessExitFingerprint) {
			return errors.New("successful browser runtime receipt is incomplete")
		}
	} else if checkpoint.Stage != BrowserRuntimeStageFailed {
		return errors.New("failed browser runtime receipt is invalid")
	}
	return nil
}

func ValidateStoredBrowserRuntimeReceipt(receipt BrowserRuntimeReceipt) error {
	if receipt.ProtocolVersion != BrowserRuntimeReceiptProtocolVersion ||
		!validPlanIdentity(receipt.ID) || !validPlanIdentity(receipt.RuntimeID) ||
		!validPlanIdentity(receipt.RunID) || !validSHA256(receipt.AttemptFingerprint) ||
		!validSHA256(receipt.AuthorizationFingerprint) ||
		!validSHA256(receipt.FinalCheckpointFingerprint) ||
		receipt.ProfileReleased != validSHA256(receipt.ReleasedProfileFingerprint) ||
		receipt.NetworkCleanupVerified && !receipt.ProcessTreeQuiescent ||
		receipt.ProfileReleased && !receipt.NetworkCleanupVerified ||
		receipt.ProfileCleaned && !receipt.ProfileReleased ||
		receipt.RecoveryRequired != !receipt.ProfileCleaned || receipt.RawOutputIncluded ||
		receipt.PageContentIncluded || receipt.ScreenshotIncluded ||
		receipt.PersonalProfileUsed || receipt.FullCDPUsed || receipt.StartedAt.IsZero() ||
		!receipt.CompletedAt.After(receipt.StartedAt) ||
		receipt.Fingerprint != browserRuntimeFingerprint(receipt) {
		return errors.New("stored browser runtime receipt is invalid or unredacted")
	}
	if receipt.Succeeded {
		if receipt.FailureCode != "" || receipt.RecoveryRequired ||
			!receipt.RestrictedCDPClosed || !receipt.ProcessTreeQuiescent ||
			!receipt.NetworkCleanupVerified || !receipt.ProfileReleased ||
			!receipt.ProfileCleaned || !validSHA256(receipt.ProcessExitFingerprint) {
			return errors.New("stored successful browser runtime receipt is incomplete")
		}
	} else if !validBrowserRuntimeFailureCode(receipt.FailureCode) ||
		(receipt.ProcessExitFingerprint != "" && !validSHA256(receipt.ProcessExitFingerprint)) {
		return errors.New("stored failed browser runtime receipt is invalid")
	}
	return nil
}

func browserRuntimeCleanupStateMonotonic(previous BrowserRuntimeCheckpoint,
	next BrowserRuntimeCheckpoint,
) bool {
	if previous.RestrictedCDPClosed && !next.RestrictedCDPClosed ||
		previous.ProcessTerminationRequested && !next.ProcessTerminationRequested ||
		previous.ProcessTreeQuiescent && !next.ProcessTreeQuiescent ||
		previous.NetworkCleanupVerified && !next.NetworkCleanupVerified ||
		previous.ProfileReleased && !next.ProfileReleased ||
		previous.ProfileCleaned && !next.ProfileCleaned {
		return false
	}
	if previous.ReleasedProfileFingerprint != "" &&
		previous.ReleasedProfileFingerprint != next.ReleasedProfileFingerprint {
		return false
	}
	return true
}

func validBrowserRuntimeStageTransition(previous BrowserRuntimeLifecycleStage,
	next BrowserRuntimeLifecycleStage,
) bool {
	if next == BrowserRuntimeStageFailed {
		return previous != BrowserRuntimeStageCompleted && previous != BrowserRuntimeStageFailed
	}
	switch previous {
	case BrowserRuntimeStageRunning:
		return next == BrowserRuntimeStageCDPClosed
	case BrowserRuntimeStageCDPClosed:
		return next == BrowserRuntimeStageProcessQuiescent
	case BrowserRuntimeStageProcessQuiescent:
		return next == BrowserRuntimeStageNetworkReleased
	case BrowserRuntimeStageNetworkReleased:
		return next == BrowserRuntimeStageProfileReleased
	case BrowserRuntimeStageProfileReleased:
		return next == BrowserRuntimeStageCompleted
	default:
		return false
	}
}

func lifecycleNow(after time.Time) time.Time {
	now := time.Now().UTC()
	if !now.After(after) {
		return after.UTC().Add(time.Nanosecond)
	}
	return now
}

func validBrowserRuntimeFailureCode(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 96 {
		return false
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') || character == '_') {
			return false
		}
	}
	return true
}
