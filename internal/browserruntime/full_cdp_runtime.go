package browserruntime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"cyberagent-workbench/internal/domain"
)

const (
	FullCDPRuntimeReceiptProtocolVersion = "full_cdp_runtime_receipt.v1"
	FullCDPRuntimeCleanupTimeout         = 20 * time.Second
)

// FullCDPManagedLaunchRequest binds one production launch to the immutable
// browser discovery, launch review, permission snapshots, and process-local
// execution fence prepared by the Application layer.
type FullCDPManagedLaunchRequest struct {
	RuntimeID              string
	Session                SessionPlan
	Identity               BrowserExecutableIdentity
	Acceptance             BrowserAcceptanceCandidate
	Ownership              ProfileOwnershipPlan
	Attempt                BrowserLaunchAttempt
	LaunchLease            BrowserLaunchLease
	Review                 BrowserLaunchReview
	Permission             domain.RunBrowserCDPPermissionSnapshot
	ExecutionPermission    domain.RunExecutionPermissionSnapshot
	RuntimeCapabilities    FullCDPRuntimeCapabilities
	PermissionCapabilities domain.BrowserCDPPermissionRuntimeCapabilities
	ExecutionCapabilities  domain.ExecutionPermissionRuntimeCapabilities
	ExecutionFence         uint64
	Confirmed              bool
	Now                    time.Time
}

// FullCDPRuntimeReceipt is the bounded terminal cleanup receipt for one
// process-local Full CDP session. It intentionally contains no endpoint, PID,
// cookie, header, body, page content, or raw browser output.
type FullCDPRuntimeReceipt struct {
	ProtocolVersion            string    `json:"protocol_version"`
	RuntimeID                  string    `json:"runtime_id"`
	RunID                      string    `json:"run_id"`
	SessionID                  string    `json:"session_id"`
	AttemptFingerprint         string    `json:"attempt_fingerprint"`
	StartAuthorization         string    `json:"start_authorization_fingerprint"`
	SessionAuthorization       string    `json:"session_authorization_fingerprint"`
	ProcessExitFingerprint     string    `json:"process_exit_fingerprint,omitempty"`
	ReleasedProfileFingerprint string    `json:"released_profile_fingerprint,omitempty"`
	CDPClosed                  bool      `json:"cdp_closed"`
	ProcessTreeQuiescent       bool      `json:"process_tree_quiescent"`
	ProfileReleased            bool      `json:"profile_released"`
	ProfileCleaned             bool      `json:"profile_cleaned"`
	Succeeded                  bool      `json:"succeeded"`
	RecoveryRequired           bool      `json:"recovery_required"`
	FailureCode                string    `json:"failure_code,omitempty"`
	RawOutputIncluded          bool      `json:"raw_output_included"`
	PageContentIncluded        bool      `json:"page_content_included"`
	CookieValueIncluded        bool      `json:"cookie_value_included"`
	PersonalProfileUsed        bool      `json:"personal_profile_used"`
	FullCDPUsed                bool      `json:"full_cdp_used"`
	StartedAt                  time.Time `json:"started_at"`
	CompletedAt                time.Time `json:"completed_at"`
	Fingerprint                string    `json:"fingerprint"`
}

// FullCDPManagedRuntime owns the only live handles for one Full CDP session.
// It is intentionally process-local and cannot be reconstructed from a PID or
// a durable record after restart.
type FullCDPManagedRuntime struct {
	runtimeID           string
	sessionPlan         SessionPlan
	attempt             BrowserLaunchAttempt
	startAuthorization  FullCDPStartAuthorization
	authorization       FullCDPAuthorization
	ownership           ProfileOwnershipPlan
	profileLease        ProfileRuntimeLease
	releasedProfile     ProfileRuntimeLease
	process             *BrowserProcess
	session             *FullCDPSession
	startedAt           time.Time
	terminalFailureCode string

	finalizeMu sync.Mutex
	finalizing bool
	finalized  bool
	finalDone  chan struct{}
	receipt    FullCDPRuntimeReceipt
	finalErr   error
}

// LaunchManagedFullCDP creates a disposable Profile, starts a dedicated
// standard-user browser tree, dials its private DevTools endpoint, and returns
// one process-local owner. Every partial failure runs the same bounded cleanup
// path before it is returned.
func LaunchManagedFullCDP(ctx context.Context,
	controller *BrowserProcessController, request FullCDPManagedLaunchRequest,
) (*FullCDPManagedRuntime, error) {
	if controller == nil || !validPlanIdentity(request.RuntimeID) || !request.Confirmed {
		return nil, errors.New("managed full CDP launch boundary is invalid")
	}
	now := request.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	startAuthorization, err := AuthorizeFullCDPStart(request.Session,
		request.Identity, request.Acceptance, request.Ownership, request.Attempt,
		request.LaunchLease, request.Review, request.Permission,
		request.ExecutionPermission, request.RuntimeCapabilities,
		request.PermissionCapabilities, request.ExecutionCapabilities,
		request.ExecutionFence, now)
	if err != nil {
		return nil, err
	}
	profileLease, err := MaterializeFullCDPProfile(startAuthorization,
		request.Session, request.Identity, request.Acceptance, request.Ownership,
		request.Attempt, request.LaunchLease, request.Review, request.Permission,
		request.ExecutionPermission, request.ExecutionCapabilities, now)
	if err != nil {
		if profileLease.ProtocolVersion == ProfileRuntimeLeaseProtocolVersion &&
			profileLease.AuthorizationFingerprint == startAuthorization.Fingerprint &&
			profileLease.OwnershipPlanFingerprint == request.Ownership.Fingerprint {
			runtime := &FullCDPManagedRuntime{
				runtimeID: request.RuntimeID, sessionPlan: request.Session,
				attempt: request.Attempt, startAuthorization: startAuthorization,
				ownership: request.Ownership, startedAt: now,
				terminalFailureCode: "materialization_failed",
			}
			if profileLease.State == ProfileMarkerReleased {
				runtime.releasedProfile = profileLease
			} else {
				runtime.profileLease = profileLease
			}
			return retainRecoverableFullCDPLaunch(runtime, err)
		}
		return nil, err
	}
	runtime := &FullCDPManagedRuntime{
		runtimeID: request.RuntimeID, sessionPlan: request.Session,
		attempt: request.Attempt, startAuthorization: startAuthorization,
		ownership: request.Ownership, profileLease: profileLease,
		startedAt: now, terminalFailureCode: "launch_failed",
	}
	process, err := controller.StartFullCDP(ctx, startAuthorization,
		request.Session, request.Identity, request.Acceptance, request.Ownership,
		request.Attempt, request.LaunchLease, request.Review, request.Permission,
		request.ExecutionPermission, request.ExecutionCapabilities, profileLease,
		time.Now().UTC())
	if err != nil {
		return retainRecoverableFullCDPLaunch(runtime, err)
	}
	runtime.process = process
	authorization, err := AuthorizeFullCDP(request.Session, request.Identity,
		request.Acceptance, request.Permission, request.ExecutionPermission,
		request.RuntimeCapabilities, request.PermissionCapabilities,
		request.ExecutionCapabilities, request.ExecutionFence, request.Confirmed,
		time.Now().UTC())
	if err != nil {
		return retainRecoverableFullCDPLaunch(runtime, err)
	}
	runtime.authorization = authorization
	session, err := OpenFullCDPSession(ctx, authorization, request.Session,
		request.Identity, request.Acceptance, request.Ownership, request.Permission,
		request.ExecutionPermission, request.ExecutionCapabilities,
		request.ExecutionFence, profileLease, process)
	if err != nil {
		return retainRecoverableFullCDPLaunch(runtime, err)
	}
	runtime.session = session
	runtime.terminalFailureCode = ""
	return runtime, nil
}

// retainRecoverableFullCDPLaunch performs one bounded cleanup attempt and
// always returns the process-local owner once launch crossed into resource
// ownership. If cleanup is already resource-safe the owner is finalized and
// carries the stable terminal receipt; otherwise it retains the live Job or
// uncleared Profile for background recovery. Keeping both forms lets the
// Application layer durably audit every partial launch without reconstructing
// authority from a PID or path.
func retainRecoverableFullCDPLaunch(runtime *FullCDPManagedRuntime,
	launchErr error,
) (*FullCDPManagedRuntime, error) {
	_, cleanupErr := runtime.Close(context.Background(), "launch_failed")
	combined := errors.Join(launchErr, cleanupErr)
	return runtime, combined
}

// Close closes the CDP transport, reaps the complete browser tree, and removes
// only the exact released disposable Profile. Concurrent and repeated callers
// receive the same terminal receipt.
func (runtime *FullCDPManagedRuntime) Close(ctx context.Context,
	reason string,
) (FullCDPRuntimeReceipt, error) {
	if runtime == nil {
		return FullCDPRuntimeReceipt{}, errors.New("full CDP runtime is required")
	}
	runtime.finalizeMu.Lock()
	if runtime.finalized {
		receipt, err := runtime.receipt, runtime.finalErr
		runtime.finalizeMu.Unlock()
		return receipt, err
	}
	if runtime.finalizing {
		done := runtime.finalDone
		runtime.finalizeMu.Unlock()
		select {
		case <-done:
			runtime.finalizeMu.Lock()
			receipt, err := runtime.receipt, runtime.finalErr
			runtime.finalizeMu.Unlock()
			return receipt, err
		case <-contextDone(ctx):
			return FullCDPRuntimeReceipt{}, contextError(ctx)
		}
	}
	runtime.finalizing = true
	runtime.finalDone = make(chan struct{})
	runtime.finalizeMu.Unlock()

	receipt, err := runtime.finalize(reason)
	runtime.finalizeMu.Lock()
	runtime.receipt, runtime.finalErr = receipt, err
	// A failed close that still owns a live process tree or an uncleared Profile
	// is recoverable and must not be memoized forever. Once cleanup no longer
	// requires recovery, later callers receive the stable terminal receipt.
	runtime.finalized = !receipt.RecoveryRequired
	runtime.finalizing = false
	close(runtime.finalDone)
	runtime.finalizeMu.Unlock()
	return receipt, err
}

func (runtime *FullCDPManagedRuntime) finalize(reason string) (
	FullCDPRuntimeReceipt, error,
) {
	cleanupContext, cancel := context.WithTimeout(context.Background(),
		FullCDPRuntimeCleanupTimeout)
	defer cancel()
	var lifecycleErrors []error
	failureCode := runtime.terminalFailureCode
	noteFailure := func(code string, err error) {
		if err == nil {
			return
		}
		if failureCode == "" {
			failureCode = code
		}
		lifecycleErrors = append(lifecycleErrors, errors.New(code+": "+err.Error()))
	}

	cdpClosed := false
	if runtime.session == nil || runtime.session.Close(cleanupContext) == nil {
		cdpClosed = true
	} else {
		noteFailure("full_cdp_close_failed", errors.New("CDP transport did not close"))
	}
	processTreeQuiescent := runtime.process == nil
	exit := BrowserProcessExit{}
	if runtime.process != nil {
		if _, exited := runtime.process.Exit(); !exited {
			noteFailure("process_termination_failed", runtime.process.Stop(cleanupContext))
		}
		select {
		case <-cleanupContext.Done():
			noteFailure("process_quiescence_timeout", cleanupContext.Err())
		case <-runtime.process.Done():
		}
		var exited bool
		exit, exited = runtime.process.Exit()
		processTreeQuiescent = exited && exit.TreeReaped && !exit.RawOutputIncluded &&
			exit.StartSpecFingerprint == runtime.process.StartSpec().Fingerprint &&
			exit.Fingerprint == browserRuntimeFingerprint(exit)
		if !processTreeQuiescent {
			noteFailure("process_tree_not_quiescent",
				errors.New("browser process tree did not publish a clean bound exit"))
		}
	}

	released := runtime.releasedProfile
	profileReleased := released.State == ProfileMarkerReleased
	if processTreeQuiescent {
		if !profileReleased {
			var releaseErr error
			released, releaseErr = ReleaseFullCDPProfile(runtime.startAuthorization,
				runtime.profileLease, runtime.ownership, true, time.Now().UTC())
			if releaseErr != nil {
				noteFailure("profile_release_failed", releaseErr)
			} else {
				profileReleased = true
				runtime.releasedProfile = released
			}
		}
	}
	profileCleaned := false
	if profileReleased {
		if cleanupErr := CleanupReleasedFullCDPProfile(runtime.startAuthorization,
			released, runtime.ownership, true); cleanupErr != nil {
			noteFailure("profile_cleanup_failed", cleanupErr)
		} else {
			profileCleaned = true
		}
	}
	if reason == "" {
		reason = "operator_closed"
	}
	_ = reason // The caller-visible receipt remains bounded to stable failure codes.
	succeeded := failureCode == "" && cdpClosed && processTreeQuiescent &&
		profileReleased && profileCleaned
	sessionAuthorization := runtime.authorization.Fingerprint
	if !validSHA256(sessionAuthorization) {
		sessionAuthorization = runtime.startAuthorization.Fingerprint
	}
	receipt := FullCDPRuntimeReceipt{
		ProtocolVersion: FullCDPRuntimeReceiptProtocolVersion,
		RuntimeID:       runtime.runtimeID, RunID: runtime.sessionPlan.RunID,
		SessionID:              runtime.sessionPlan.SessionID,
		AttemptFingerprint:     runtime.attempt.Fingerprint,
		StartAuthorization:     runtime.startAuthorization.Fingerprint,
		SessionAuthorization:   sessionAuthorization,
		ProcessExitFingerprint: exit.Fingerprint,
		CDPClosed:              cdpClosed, ProcessTreeQuiescent: processTreeQuiescent,
		ProfileReleased: profileReleased, ProfileCleaned: profileCleaned,
		Succeeded: succeeded, RecoveryRequired: !profileCleaned,
		FailureCode: failureCode, FullCDPUsed: true,
		StartedAt: runtime.startedAt, CompletedAt: time.Now().UTC(),
	}
	if profileReleased {
		receipt.ReleasedProfileFingerprint = released.Fingerprint
	}
	if !receipt.CompletedAt.After(receipt.StartedAt) {
		receipt.CompletedAt = receipt.StartedAt.Add(time.Nanosecond)
	}
	sealedReceipt, validateErr := SealFullCDPRuntimeReceipt(receipt)
	if validateErr != nil {
		lifecycleErrors = append(lifecycleErrors, validateErr)
	} else {
		receipt = sealedReceipt
	}
	return receipt, errors.Join(lifecycleErrors...)
}

func (runtime *FullCDPManagedRuntime) Done() <-chan struct{} {
	if runtime == nil || runtime.process == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return runtime.process.Done()
}

func (runtime *FullCDPManagedRuntime) ExpiresAt() time.Time {
	if runtime == nil {
		return time.Time{}
	}
	if !runtime.authorization.ExpiresAt.IsZero() {
		return runtime.authorization.ExpiresAt
	}
	return runtime.startAuthorization.RuntimeDeadline
}

func (runtime *FullCDPManagedRuntime) AuthorizationFingerprint() string {
	if runtime == nil {
		return ""
	}
	if validSHA256(runtime.authorization.Fingerprint) {
		return runtime.authorization.Fingerprint
	}
	return runtime.startAuthorization.Fingerprint
}

// SealFullCDPRuntimeReceipt computes the receipt fingerprint and validates the
// complete bounded cleanup proof. It is kept in browserruntime so callers
// cannot accidentally reproduce only a subset of the receipt contract.
func SealFullCDPRuntimeReceipt(receipt FullCDPRuntimeReceipt) (
	FullCDPRuntimeReceipt, error,
) {
	if receipt.Fingerprint != "" {
		return FullCDPRuntimeReceipt{},
			errors.New("full CDP runtime receipt is already sealed")
	}
	receipt.Fingerprint = browserRuntimeFingerprint(receipt)
	if err := ValidateFullCDPRuntimeReceipt(receipt); err != nil {
		return FullCDPRuntimeReceipt{}, err
	}
	return receipt, nil
}

func ValidateFullCDPRuntimeReceipt(receipt FullCDPRuntimeReceipt) error {
	if receipt.ProtocolVersion != FullCDPRuntimeReceiptProtocolVersion ||
		!validPlanIdentity(receipt.RuntimeID) || !validPlanIdentity(receipt.RunID) ||
		!validPlanIdentity(receipt.SessionID) ||
		!validSHA256(receipt.AttemptFingerprint) ||
		!validSHA256(receipt.StartAuthorization) ||
		!validSHA256(receipt.SessionAuthorization) ||
		(receipt.ProcessExitFingerprint != "" &&
			!validSHA256(receipt.ProcessExitFingerprint)) ||
		receipt.ProfileReleased != validSHA256(receipt.ReleasedProfileFingerprint) ||
		receipt.ProfileCleaned && !receipt.ProfileReleased ||
		receipt.RecoveryRequired != !receipt.ProfileCleaned ||
		receipt.RawOutputIncluded || receipt.PageContentIncluded ||
		receipt.CookieValueIncluded || receipt.PersonalProfileUsed ||
		!receipt.FullCDPUsed || receipt.StartedAt.IsZero() ||
		!receipt.CompletedAt.After(receipt.StartedAt) ||
		receipt.Fingerprint != browserRuntimeFingerprint(receipt) {
		return errors.New("full CDP runtime receipt is invalid or unredacted")
	}
	if receipt.Succeeded {
		if receipt.FailureCode != "" || receipt.RecoveryRequired ||
			!receipt.CDPClosed || !receipt.ProcessTreeQuiescent ||
			!receipt.ProfileReleased || !receipt.ProfileCleaned ||
			!validSHA256(receipt.ProcessExitFingerprint) {
			return errors.New("successful full CDP runtime receipt is incomplete")
		}
	} else if !validFullCDPFailureCode(receipt.FailureCode) {
		return errors.New("failed full CDP runtime receipt has an invalid failure code")
	}
	return nil
}

func validFullCDPFailureCode(value string) bool {
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
