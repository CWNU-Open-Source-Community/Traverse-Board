package browserruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"time"
)

const (
	BrowserLaunchAttemptProtocolVersion          = "browser_launch_attempt.v1"
	BrowserLaunchLeaseProtocolVersion            = "browser_launch_lease.v1"
	BrowserLifecycleObservationVersion           = "browser_lifecycle_observation.v1"
	BrowserLifecycleReconciliationVersion        = "browser_lifecycle_reconciliation.v1"
	BrowserProcessTreeOwnershipModel             = "per_attempt_generation_fenced_process_tree"
	BrowserLaunchAttemptPreparedState            = "prepared"
	BrowserLaunchLeaseActiveStatus               = "active"
	DisabledBrowserLifecycleAdapterName          = "disabled"
	FakeBrowserLifecycleAdapterName              = "fake"
	DefaultBrowserLifecycleDeadlineMS            = 5_000
	MaxBrowserLifecycleDeadlineMS                = 30_000
	MinBrowserLaunchLeaseTTL                     = 5 * time.Second
	MaxBrowserLaunchLeaseTTL                     = 2 * time.Minute
	MaxBrowserLaunchGeneration            uint64 = 1_000_000
	MaxSyntheticBrowserProcessCount              = 64
)

// BrowserLaunchAttempt is an immutable write-ahead launch intent. This release
// persists it before any future process adapter can be considered, while
// keeping every execution authority false.
type BrowserLaunchAttempt struct {
	ProtocolVersion               string           `json:"protocol_version"`
	ID                            string           `json:"id"`
	SessionID                     string           `json:"session_id"`
	RunID                         string           `json:"run_id"`
	WorkspaceID                   string           `json:"workspace_id"`
	SessionPlanFingerprint        string           `json:"session_plan_fingerprint"`
	ExecutableIdentityFingerprint string           `json:"executable_identity_fingerprint"`
	AcceptanceFingerprint         string           `json:"acceptance_fingerprint"`
	ProfileOwnershipFingerprint   string           `json:"profile_ownership_fingerprint"`
	ScopeFingerprint              string           `json:"scope_fingerprint"`
	BudgetFingerprint             string           `json:"budget_fingerprint"`
	ProfileGeneration             uint64           `json:"profile_generation"`
	Generation                    uint64           `json:"generation"`
	State                         string           `json:"state"`
	RequiredBackend               string           `json:"required_backend"`
	MaxRuntimeMS                  int              `json:"max_runtime_ms"`
	MaxRequests                   int              `json:"max_requests"`
	MaxResponseBytes              int              `json:"max_response_bytes"`
	ProcessTreeOwnershipModel     string           `json:"process_tree_ownership_model"`
	WriteAheadRequired            bool             `json:"write_ahead_required"`
	ExactGenerationRequired       bool             `json:"exact_generation_required"`
	CancellationFanoutRequired    bool             `json:"cancellation_fanout_required"`
	GracefulThenForcedTermination bool             `json:"graceful_then_forced_termination"`
	RestartReconciliationRequired bool             `json:"restart_reconciliation_required"`
	StartBlocked                  bool             `json:"start_blocked"`
	ProcessStartAuthorized        bool             `json:"process_start_authorized"`
	NetworkAuthorized             bool             `json:"network_authorized"`
	ProfileWriteAuthorized        bool             `json:"profile_write_authorized"`
	ArtifactCommitAuthorized      bool             `json:"artifact_commit_authorized"`
	Authority                     RuntimeAuthority `json:"authority"`
	CreatedAt                     time.Time        `json:"created_at"`
	Fingerprint                   string           `json:"fingerprint"`
}

type BrowserLaunchLease struct {
	ProtocolVersion               string           `json:"protocol_version"`
	ID                            string           `json:"id"`
	AttemptID                     string           `json:"attempt_id"`
	AttemptFingerprint            string           `json:"attempt_fingerprint"`
	Generation                    uint64           `json:"generation"`
	OwnerTokenSHA256              string           `json:"owner_token_sha256"`
	Status                        string           `json:"status"`
	AcquiredAt                    time.Time        `json:"acquired_at"`
	ExpiresAt                     time.Time        `json:"expires_at"`
	ExactGenerationRequired       bool             `json:"exact_generation_required"`
	CancellationFanoutRequired    bool             `json:"cancellation_fanout_required"`
	ProcessTreeTrackingRequired   bool             `json:"process_tree_tracking_required"`
	RestartReconciliationRequired bool             `json:"restart_reconciliation_required"`
	StartBlocked                  bool             `json:"start_blocked"`
	ProcessExecutionAuthorized    bool             `json:"process_execution_authorized"`
	ProcessTerminationAuthorized  bool             `json:"process_termination_authorized"`
	Authority                     RuntimeAuthority `json:"authority"`
	Fingerprint                   string           `json:"fingerprint"`
}

type BrowserLifecycleState string

const (
	BrowserLifecycleDisabled        BrowserLifecycleState = "disabled"
	BrowserLifecyclePrepared        BrowserLifecycleState = "prepared"
	BrowserLifecycleStartSubmitted  BrowserLifecycleState = "start_submitted"
	BrowserLifecycleRunning         BrowserLifecycleState = "running"
	BrowserLifecycleCancelRequested BrowserLifecycleState = "cancel_requested"
	BrowserLifecycleExited          BrowserLifecycleState = "exited"
	BrowserLifecycleOrphaned        BrowserLifecycleState = "orphaned"
	BrowserLifecycleReconciled      BrowserLifecycleState = "reconciled"
	BrowserLifecycleTimedOut        BrowserLifecycleState = "timed_out"
	BrowserLifecycleCancelled       BrowserLifecycleState = "cancelled"
)

type BrowserLifecycleObservation struct {
	ProtocolVersion            string                `json:"protocol_version"`
	AttemptFingerprint         string                `json:"attempt_fingerprint"`
	LeaseFingerprint           string                `json:"lease_fingerprint"`
	LeaseGeneration            uint64                `json:"lease_generation"`
	Adapter                    string                `json:"adapter"`
	State                      BrowserLifecycleState `json:"state"`
	DeadlineMS                 int                   `json:"deadline_ms"`
	SyntheticProcessCount      int                   `json:"synthetic_process_count"`
	SyntheticRootProcessToken  string                `json:"synthetic_root_process_token,omitempty"`
	Synthetic                  bool                  `json:"synthetic"`
	MetadataOnly               bool                  `json:"metadata_only"`
	Completed                  bool                  `json:"completed"`
	ProcessTreeQuiescent       bool                  `json:"process_tree_quiescent"`
	ActualProcessObserved      bool                  `json:"actual_process_observed"`
	ProcessStarted             bool                  `json:"process_started"`
	NetworkUsed                bool                  `json:"network_used"`
	ProfileWritten             bool                  `json:"profile_written"`
	TerminationSignalSent      bool                  `json:"termination_signal_sent"`
	FilesystemCleanupPerformed bool                  `json:"filesystem_cleanup_performed"`
	ArtifactCommitted          bool                  `json:"artifact_committed"`
	ProductExecutionEnabled    bool                  `json:"product_execution_enabled"`
	Authority                  RuntimeAuthority      `json:"authority"`
	Fingerprint                string                `json:"fingerprint"`
}

type BrowserLifecycleDecision string

const (
	BrowserLifecycleDecisionRemainBlocked       BrowserLifecycleDecision = "remain_blocked"
	BrowserLifecycleDecisionWaitForQuiescence   BrowserLifecycleDecision = "wait_for_quiescence"
	BrowserLifecycleDecisionCancelTreeCandidate BrowserLifecycleDecision = "cancel_tree_candidate"
	BrowserLifecycleDecisionRecoverCandidate    BrowserLifecycleDecision = "recover_orphan_candidate"
	BrowserLifecycleDecisionCleanupCandidate    BrowserLifecycleDecision = "cleanup_candidate"
)

type BrowserLifecycleReconciliation struct {
	ProtocolVersion               string                   `json:"protocol_version"`
	AttemptFingerprint            string                   `json:"attempt_fingerprint"`
	LeaseFingerprint              string                   `json:"lease_fingerprint"`
	ObservationFingerprint        string                   `json:"observation_fingerprint"`
	Decision                      BrowserLifecycleDecision `json:"decision"`
	ExactGenerationVerified       bool                     `json:"exact_generation_verified"`
	ProcessTreeQuiescenceRequired bool                     `json:"process_tree_quiescence_required"`
	CancellationFanoutRequired    bool                     `json:"cancellation_fanout_required"`
	GracefulThenForcedTermination bool                     `json:"graceful_then_forced_termination"`
	RestartRecoveryCandidate      bool                     `json:"restart_recovery_candidate"`
	CleanupCandidate              bool                     `json:"cleanup_candidate"`
	StartBlocked                  bool                     `json:"start_blocked"`
	ApplyBlocked                  bool                     `json:"apply_blocked"`
	ProcessExecutionAuthorized    bool                     `json:"process_execution_authorized"`
	ProcessTerminationAuthorized  bool                     `json:"process_termination_authorized"`
	FilesystemCleanupAuthorized   bool                     `json:"filesystem_cleanup_authorized"`
	Authority                     RuntimeAuthority         `json:"authority"`
	Fingerprint                   string                   `json:"fingerprint"`
}

func BuildBrowserLaunchAttempt(session SessionPlan, identity BrowserExecutableIdentity,
	acceptance BrowserAcceptanceCandidate, ownership ProfileOwnershipPlan,
	attemptID string, createdAt time.Time,
) (BrowserLaunchAttempt, error) {
	if err := ValidateProfileOwnershipPlan(ownership, session, identity); err != nil {
		return BrowserLaunchAttempt{}, err
	}
	if err := ValidateBrowserAcceptanceCandidate(acceptance, identity); err != nil {
		return BrowserLaunchAttempt{}, err
	}
	if acceptance.Decision != BrowserAcceptanceAccepted || !acceptance.ReviewEligible {
		return BrowserLaunchAttempt{}, errors.New("browser launch attempt requires a publisher-accepted review candidate")
	}
	if !validPlanIdentity(attemptID) || createdAt.IsZero() {
		return BrowserLaunchAttempt{}, errors.New("browser launch attempt id or timestamp is invalid")
	}
	attempt := BrowserLaunchAttempt{
		ProtocolVersion: BrowserLaunchAttemptProtocolVersion, ID: attemptID,
		SessionID: session.SessionID, RunID: session.RunID, WorkspaceID: session.WorkspaceID,
		SessionPlanFingerprint:        session.Fingerprint,
		ExecutableIdentityFingerprint: identity.Fingerprint,
		AcceptanceFingerprint:         acceptance.Fingerprint,
		ProfileOwnershipFingerprint:   ownership.Fingerprint,
		ScopeFingerprint:              session.Scope.Fingerprint,
		BudgetFingerprint:             browserLaunchBudgetFingerprint(session),
		ProfileGeneration:             ownership.Generation, Generation: 1,
		State: BrowserLaunchAttemptPreparedState, RequiredBackend: session.RequiredBackend,
		MaxRuntimeMS: session.Limits.TimeoutMS, MaxRequests: session.Limits.MaxRequests,
		MaxResponseBytes:          session.Limits.MaxResponseBytes,
		ProcessTreeOwnershipModel: BrowserProcessTreeOwnershipModel,
		WriteAheadRequired:        true, ExactGenerationRequired: true,
		CancellationFanoutRequired: true, GracefulThenForcedTermination: true,
		RestartReconciliationRequired: true, StartBlocked: true,
		CreatedAt: createdAt.UTC(),
	}
	var err error
	attempt.Fingerprint, err = browserLaunchAttemptFingerprint(attempt)
	if err != nil {
		return BrowserLaunchAttempt{}, err
	}
	if err := ValidateBrowserLaunchAttempt(attempt, session, identity, acceptance, ownership); err != nil {
		return BrowserLaunchAttempt{}, err
	}
	return attempt, nil
}

func ValidateBrowserLaunchAttempt(attempt BrowserLaunchAttempt, session SessionPlan,
	identity BrowserExecutableIdentity, acceptance BrowserAcceptanceCandidate,
	ownership ProfileOwnershipPlan,
) error {
	if err := ValidateProfileOwnershipPlan(ownership, session, identity); err != nil {
		return err
	}
	if err := ValidateBrowserAcceptanceCandidate(acceptance, identity); err != nil {
		return err
	}
	if acceptance.Decision != BrowserAcceptanceAccepted || !acceptance.ReviewEligible ||
		attempt.ProtocolVersion != BrowserLaunchAttemptProtocolVersion ||
		!validPlanIdentity(attempt.ID) || attempt.SessionID != session.SessionID ||
		attempt.RunID != session.RunID || attempt.WorkspaceID != session.WorkspaceID ||
		attempt.SessionPlanFingerprint != session.Fingerprint ||
		attempt.ExecutableIdentityFingerprint != identity.Fingerprint ||
		attempt.AcceptanceFingerprint != acceptance.Fingerprint ||
		attempt.ProfileOwnershipFingerprint != ownership.Fingerprint ||
		attempt.ScopeFingerprint != session.Scope.Fingerprint ||
		attempt.BudgetFingerprint != browserLaunchBudgetFingerprint(session) ||
		attempt.ProfileGeneration != ownership.Generation || attempt.Generation != 1 ||
		attempt.State != BrowserLaunchAttemptPreparedState ||
		attempt.RequiredBackend != session.RequiredBackend ||
		attempt.MaxRuntimeMS != session.Limits.TimeoutMS ||
		attempt.MaxRequests != session.Limits.MaxRequests ||
		attempt.MaxResponseBytes != session.Limits.MaxResponseBytes ||
		attempt.ProcessTreeOwnershipModel != BrowserProcessTreeOwnershipModel ||
		!attempt.WriteAheadRequired || !attempt.ExactGenerationRequired ||
		!attempt.CancellationFanoutRequired || !attempt.GracefulThenForcedTermination ||
		!attempt.RestartReconciliationRequired || !attempt.StartBlocked ||
		attempt.ProcessStartAuthorized || attempt.NetworkAuthorized ||
		attempt.ProfileWriteAuthorized || attempt.ArtifactCommitAuthorized ||
		attempt.Authority != (RuntimeAuthority{}) || attempt.CreatedAt.IsZero() {
		return errors.New("browser launch attempt lost a durable non-authorizing boundary")
	}
	expected, err := browserLaunchAttemptFingerprint(attempt)
	if err != nil || attempt.Fingerprint != expected {
		return errors.New("browser launch attempt fingerprint mismatch")
	}
	return nil
}

func BuildBrowserLaunchLease(attempt BrowserLaunchAttempt, leaseID string,
	ownerIdentity string, acquiredAt time.Time, ttl time.Duration,
) (BrowserLaunchLease, error) {
	if err := validateBrowserLaunchAttemptStructure(attempt); err != nil {
		return BrowserLaunchLease{}, err
	}
	if !validPlanIdentity(leaseID) || !validPlanIdentity(ownerIdentity) ||
		acquiredAt.IsZero() || ttl < MinBrowserLaunchLeaseTTL || ttl > MaxBrowserLaunchLeaseTTL {
		return BrowserLaunchLease{}, errors.New("browser launch lease identity, timestamp, or ttl is invalid")
	}
	lease := BrowserLaunchLease{
		ProtocolVersion: BrowserLaunchLeaseProtocolVersion, ID: leaseID,
		AttemptID: attempt.ID, AttemptFingerprint: attempt.Fingerprint,
		Generation:       attempt.Generation,
		OwnerTokenSHA256: browserLaunchOwnerToken(attempt, ownerIdentity),
		Status:           BrowserLaunchLeaseActiveStatus, AcquiredAt: acquiredAt.UTC(),
		ExpiresAt:               acquiredAt.UTC().Add(ttl),
		ExactGenerationRequired: true, CancellationFanoutRequired: true,
		ProcessTreeTrackingRequired: true, RestartReconciliationRequired: true,
		StartBlocked: true,
	}
	var err error
	lease.Fingerprint, err = browserLaunchLeaseFingerprint(lease)
	if err != nil {
		return BrowserLaunchLease{}, err
	}
	if err := ValidateBrowserLaunchLease(lease, attempt); err != nil {
		return BrowserLaunchLease{}, err
	}
	return lease, nil
}

func ValidateBrowserLaunchLease(lease BrowserLaunchLease, attempt BrowserLaunchAttempt) error {
	if err := validateBrowserLaunchAttemptStructure(attempt); err != nil {
		return err
	}
	if lease.ProtocolVersion != BrowserLaunchLeaseProtocolVersion ||
		!validPlanIdentity(lease.ID) || lease.AttemptID != attempt.ID ||
		lease.AttemptFingerprint != attempt.Fingerprint ||
		lease.Generation != attempt.Generation || !validSHA256(lease.OwnerTokenSHA256) ||
		lease.Status != BrowserLaunchLeaseActiveStatus || lease.AcquiredAt.IsZero() ||
		!lease.ExpiresAt.After(lease.AcquiredAt) ||
		lease.ExpiresAt.Sub(lease.AcquiredAt) < MinBrowserLaunchLeaseTTL ||
		lease.ExpiresAt.Sub(lease.AcquiredAt) > MaxBrowserLaunchLeaseTTL ||
		!lease.ExactGenerationRequired || !lease.CancellationFanoutRequired ||
		!lease.ProcessTreeTrackingRequired || !lease.RestartReconciliationRequired ||
		!lease.StartBlocked || lease.ProcessExecutionAuthorized ||
		lease.ProcessTerminationAuthorized || lease.Authority != (RuntimeAuthority{}) {
		return errors.New("browser launch lease lost a generation-fenced non-authorizing boundary")
	}
	expected, err := browserLaunchLeaseFingerprint(lease)
	if err != nil || lease.Fingerprint != expected {
		return errors.New("browser launch lease fingerprint mismatch")
	}
	return nil
}

// BrowserLifecycleAdapter is package-sealed. No external package can provide a
// process-starting implementation before the browser release gate changes.
type BrowserLifecycleAdapter interface {
	browserLifecycleAdapter()
	name() string
	observe(context.Context, BrowserLaunchAttempt, BrowserLaunchLease) (BrowserLifecycleState, int, error)
}

type DisabledBrowserLifecycleAdapter struct{}

func (DisabledBrowserLifecycleAdapter) browserLifecycleAdapter() {}
func (DisabledBrowserLifecycleAdapter) name() string             { return DisabledBrowserLifecycleAdapterName }
func (DisabledBrowserLifecycleAdapter) observe(
	context.Context, BrowserLaunchAttempt, BrowserLaunchLease,
) (BrowserLifecycleState, int, error) {
	return BrowserLifecycleDisabled, 0, nil
}

type FakeBrowserLifecyclePlan struct {
	State                 BrowserLifecycleState
	SyntheticProcessCount int
	Delay                 time.Duration
}

type FakeBrowserLifecycleAdapter struct {
	attemptFingerprint    string
	leaseFingerprint      string
	state                 BrowserLifecycleState
	syntheticProcessCount int
	delay                 time.Duration
}

func NewFakeBrowserLifecycleAdapter(attempt BrowserLaunchAttempt, lease BrowserLaunchLease,
	plan FakeBrowserLifecyclePlan,
) (*FakeBrowserLifecycleAdapter, error) {
	if err := ValidateBrowserLaunchLease(lease, attempt); err != nil {
		return nil, err
	}
	if !validFakeBrowserLifecycleState(plan.State) ||
		plan.SyntheticProcessCount < 0 ||
		plan.SyntheticProcessCount > MaxSyntheticBrowserProcessCount ||
		plan.Delay < 0 || plan.Delay > time.Duration(MaxBrowserLifecycleDeadlineMS+1_000)*time.Millisecond {
		return nil, errors.New("fake browser lifecycle plan is outside its fixed bounds")
	}
	if lifecycleStateRequiresSyntheticProcess(plan.State) != (plan.SyntheticProcessCount > 0) {
		return nil, errors.New("fake browser lifecycle process count does not match its state")
	}
	return &FakeBrowserLifecycleAdapter{
		attemptFingerprint: attempt.Fingerprint, leaseFingerprint: lease.Fingerprint,
		state: plan.State, syntheticProcessCount: plan.SyntheticProcessCount,
		delay: plan.Delay,
	}, nil
}

func (*FakeBrowserLifecycleAdapter) browserLifecycleAdapter() {}
func (*FakeBrowserLifecycleAdapter) name() string             { return FakeBrowserLifecycleAdapterName }
func (adapter *FakeBrowserLifecycleAdapter) observe(ctx context.Context,
	attempt BrowserLaunchAttempt, lease BrowserLaunchLease,
) (BrowserLifecycleState, int, error) {
	if adapter.attemptFingerprint != attempt.Fingerprint ||
		adapter.leaseFingerprint != lease.Fingerprint {
		return "", 0, errors.New("fake browser lifecycle binding mismatch")
	}
	if adapter.delay > 0 {
		timer := time.NewTimer(adapter.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return "", 0, ctx.Err()
		case <-timer.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	return adapter.state, adapter.syntheticProcessCount, nil
}

type BrowserLifecycleBridge struct {
	adapter BrowserLifecycleAdapter
}

func NewBrowserLifecycleBridge(adapter BrowserLifecycleAdapter) (*BrowserLifecycleBridge, error) {
	if adapter == nil {
		return nil, errors.New("browser lifecycle adapter is required")
	}
	switch value := adapter.(type) {
	case DisabledBrowserLifecycleAdapter:
	case *FakeBrowserLifecycleAdapter:
		if value == nil {
			return nil, errors.New("browser lifecycle adapter is required")
		}
	default:
		return nil, errors.New("browser lifecycle adapter is not admitted before the release gate")
	}
	return &BrowserLifecycleBridge{adapter: adapter}, nil
}

func (bridge *BrowserLifecycleBridge) Observe(ctx context.Context,
	attempt BrowserLaunchAttempt, lease BrowserLaunchLease,
) (BrowserLifecycleObservation, error) {
	if bridge == nil || bridge.adapter == nil {
		return BrowserLifecycleObservation{}, errors.New("browser lifecycle bridge is not initialized")
	}
	if err := ValidateBrowserLaunchLease(lease, attempt); err != nil {
		return BrowserLifecycleObservation{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	deadlineContext, cancel := context.WithTimeout(ctx,
		time.Duration(DefaultBrowserLifecycleDeadlineMS)*time.Millisecond)
	defer cancel()
	state, processCount, observeErr := bridge.adapter.observe(deadlineContext, attempt, lease)
	if observeErr != nil {
		switch {
		case errors.Is(deadlineContext.Err(), context.DeadlineExceeded):
			state = BrowserLifecycleTimedOut
		case errors.Is(ctx.Err(), context.Canceled), errors.Is(observeErr, context.Canceled):
			state = BrowserLifecycleCancelled
		default:
			return BrowserLifecycleObservation{}, observeErr
		}
		processCount = 0
	}
	observation := BrowserLifecycleObservation{
		ProtocolVersion:    BrowserLifecycleObservationVersion,
		AttemptFingerprint: attempt.Fingerprint, LeaseFingerprint: lease.Fingerprint,
		LeaseGeneration: lease.Generation, Adapter: bridge.adapter.name(), State: state,
		DeadlineMS: DefaultBrowserLifecycleDeadlineMS, SyntheticProcessCount: processCount,
		Synthetic:    bridge.adapter.name() == FakeBrowserLifecycleAdapterName,
		MetadataOnly: true, Completed: true,
		ProcessTreeQuiescent: lifecycleStateQuiescent(state),
	}
	if processCount > 0 {
		observation.SyntheticRootProcessToken = syntheticBrowserProcessToken(attempt, lease, state)
	}
	var err error
	observation.Fingerprint, err = browserLifecycleObservationFingerprint(observation)
	if err != nil {
		return BrowserLifecycleObservation{}, err
	}
	if err := ValidateBrowserLifecycleObservation(observation, attempt, lease); err != nil {
		return BrowserLifecycleObservation{}, err
	}
	return observation, nil
}

func ValidateBrowserLifecycleObservation(observation BrowserLifecycleObservation,
	attempt BrowserLaunchAttempt, lease BrowserLaunchLease,
) error {
	if err := ValidateBrowserLaunchLease(lease, attempt); err != nil {
		return err
	}
	if observation.ProtocolVersion != BrowserLifecycleObservationVersion ||
		observation.AttemptFingerprint != attempt.Fingerprint ||
		observation.LeaseFingerprint != lease.Fingerprint ||
		observation.LeaseGeneration != lease.Generation ||
		(observation.Adapter != DisabledBrowserLifecycleAdapterName &&
			observation.Adapter != FakeBrowserLifecycleAdapterName) ||
		!validObservedBrowserLifecycleState(observation.State) ||
		observation.DeadlineMS != DefaultBrowserLifecycleDeadlineMS ||
		observation.SyntheticProcessCount < 0 ||
		observation.SyntheticProcessCount > MaxSyntheticBrowserProcessCount ||
		observation.Synthetic != (observation.Adapter == FakeBrowserLifecycleAdapterName) ||
		!observation.MetadataOnly || !observation.Completed ||
		observation.ProcessTreeQuiescent != lifecycleStateQuiescent(observation.State) ||
		observation.ActualProcessObserved || observation.ProcessStarted ||
		observation.NetworkUsed || observation.ProfileWritten ||
		observation.TerminationSignalSent || observation.FilesystemCleanupPerformed ||
		observation.ArtifactCommitted || observation.ProductExecutionEnabled ||
		observation.Authority != (RuntimeAuthority{}) {
		return errors.New("browser lifecycle observation lost a synthetic metadata-only boundary")
	}
	if lifecycleStateRequiresSyntheticProcess(observation.State) !=
		(observation.SyntheticProcessCount > 0) {
		return errors.New("browser lifecycle synthetic process count is inconsistent")
	}
	if (observation.SyntheticProcessCount == 0 && observation.SyntheticRootProcessToken != "") ||
		(observation.SyntheticProcessCount > 0 && !validSHA256(observation.SyntheticRootProcessToken)) {
		return errors.New("browser lifecycle synthetic root token is invalid")
	}
	if observation.Adapter == DisabledBrowserLifecycleAdapterName &&
		observation.State != BrowserLifecycleDisabled {
		return errors.New("disabled browser lifecycle adapter returned another state")
	}
	expected, err := browserLifecycleObservationFingerprint(observation)
	if err != nil || observation.Fingerprint != expected {
		return errors.New("browser lifecycle observation fingerprint mismatch")
	}
	return nil
}

func BuildBrowserLifecycleReconciliation(attempt BrowserLaunchAttempt,
	lease BrowserLaunchLease, observation BrowserLifecycleObservation,
) (BrowserLifecycleReconciliation, error) {
	if err := ValidateBrowserLifecycleObservation(observation, attempt, lease); err != nil {
		return BrowserLifecycleReconciliation{}, err
	}
	value := BrowserLifecycleReconciliation{
		ProtocolVersion:    BrowserLifecycleReconciliationVersion,
		AttemptFingerprint: attempt.Fingerprint, LeaseFingerprint: lease.Fingerprint,
		ObservationFingerprint:  observation.Fingerprint,
		ExactGenerationVerified: true, StartBlocked: true, ApplyBlocked: true,
	}
	switch observation.State {
	case BrowserLifecycleDisabled, BrowserLifecyclePrepared,
		BrowserLifecycleTimedOut, BrowserLifecycleCancelled:
		value.Decision = BrowserLifecycleDecisionRemainBlocked
	case BrowserLifecycleStartSubmitted, BrowserLifecycleRunning:
		value.Decision = BrowserLifecycleDecisionCancelTreeCandidate
		value.ProcessTreeQuiescenceRequired = true
		value.CancellationFanoutRequired = true
		value.GracefulThenForcedTermination = true
	case BrowserLifecycleCancelRequested:
		value.Decision = BrowserLifecycleDecisionWaitForQuiescence
		value.ProcessTreeQuiescenceRequired = true
	case BrowserLifecycleExited, BrowserLifecycleReconciled:
		value.Decision = BrowserLifecycleDecisionCleanupCandidate
		value.CleanupCandidate = true
	case BrowserLifecycleOrphaned:
		value.Decision = BrowserLifecycleDecisionRecoverCandidate
		value.ProcessTreeQuiescenceRequired = true
		value.CancellationFanoutRequired = true
		value.GracefulThenForcedTermination = true
		value.RestartRecoveryCandidate = true
	default:
		return BrowserLifecycleReconciliation{}, errors.New("unsupported browser lifecycle observation")
	}
	var err error
	value.Fingerprint, err = browserLifecycleReconciliationFingerprint(value)
	if err != nil {
		return BrowserLifecycleReconciliation{}, err
	}
	if err := ValidateBrowserLifecycleReconciliation(value, attempt, lease, observation); err != nil {
		return BrowserLifecycleReconciliation{}, err
	}
	return value, nil
}

func ValidateBrowserLifecycleReconciliation(value BrowserLifecycleReconciliation,
	attempt BrowserLaunchAttempt, lease BrowserLaunchLease,
	observation BrowserLifecycleObservation,
) error {
	if err := ValidateBrowserLifecycleObservation(observation, attempt, lease); err != nil {
		return err
	}
	rebuilt := value
	rebuilt.Fingerprint = ""
	expected, err := buildBrowserLifecycleReconciliationUnchecked(attempt, lease, observation)
	if err != nil {
		return err
	}
	expected.Fingerprint = ""
	if value.ProtocolVersion != BrowserLifecycleReconciliationVersion ||
		!reflect.DeepEqual(rebuilt, expected) || !value.StartBlocked || !value.ApplyBlocked ||
		value.ProcessExecutionAuthorized || value.ProcessTerminationAuthorized ||
		value.FilesystemCleanupAuthorized || value.Authority != (RuntimeAuthority{}) {
		return errors.New("browser lifecycle reconciliation grants authority or changed its decision")
	}
	fingerprint, err := browserLifecycleReconciliationFingerprint(value)
	if err != nil || value.Fingerprint != fingerprint {
		return errors.New("browser lifecycle reconciliation fingerprint mismatch")
	}
	return nil
}

func buildBrowserLifecycleReconciliationUnchecked(attempt BrowserLaunchAttempt,
	lease BrowserLaunchLease, observation BrowserLifecycleObservation,
) (BrowserLifecycleReconciliation, error) {
	copyObservation := observation
	copyObservation.Fingerprint = observation.Fingerprint
	// Build through the public constructor after validation without recursing
	// into ValidateBrowserLifecycleReconciliation.
	value := BrowserLifecycleReconciliation{
		ProtocolVersion:    BrowserLifecycleReconciliationVersion,
		AttemptFingerprint: attempt.Fingerprint, LeaseFingerprint: lease.Fingerprint,
		ObservationFingerprint:  copyObservation.Fingerprint,
		ExactGenerationVerified: true, StartBlocked: true, ApplyBlocked: true,
	}
	switch observation.State {
	case BrowserLifecycleDisabled, BrowserLifecyclePrepared,
		BrowserLifecycleTimedOut, BrowserLifecycleCancelled:
		value.Decision = BrowserLifecycleDecisionRemainBlocked
	case BrowserLifecycleStartSubmitted, BrowserLifecycleRunning:
		value.Decision = BrowserLifecycleDecisionCancelTreeCandidate
		value.ProcessTreeQuiescenceRequired = true
		value.CancellationFanoutRequired = true
		value.GracefulThenForcedTermination = true
	case BrowserLifecycleCancelRequested:
		value.Decision = BrowserLifecycleDecisionWaitForQuiescence
		value.ProcessTreeQuiescenceRequired = true
	case BrowserLifecycleExited, BrowserLifecycleReconciled:
		value.Decision = BrowserLifecycleDecisionCleanupCandidate
		value.CleanupCandidate = true
	case BrowserLifecycleOrphaned:
		value.Decision = BrowserLifecycleDecisionRecoverCandidate
		value.ProcessTreeQuiescenceRequired = true
		value.CancellationFanoutRequired = true
		value.GracefulThenForcedTermination = true
		value.RestartRecoveryCandidate = true
	default:
		return BrowserLifecycleReconciliation{}, errors.New("unsupported browser lifecycle observation")
	}
	return value, nil
}

func validateBrowserLaunchAttemptStructure(attempt BrowserLaunchAttempt) error {
	if attempt.ProtocolVersion != BrowserLaunchAttemptProtocolVersion ||
		!validPlanIdentity(attempt.ID) || !validPlanIdentity(attempt.SessionID) ||
		!validPlanIdentity(attempt.RunID) || !validPlanIdentity(attempt.WorkspaceID) ||
		!validSHA256(attempt.SessionPlanFingerprint) ||
		!validSHA256(attempt.ExecutableIdentityFingerprint) ||
		!validSHA256(attempt.AcceptanceFingerprint) ||
		!validSHA256(attempt.ProfileOwnershipFingerprint) ||
		!validSHA256(attempt.ScopeFingerprint) || !validSHA256(attempt.BudgetFingerprint) ||
		attempt.ProfileGeneration == 0 || attempt.ProfileGeneration > MaxProfileOwnershipGeneration ||
		attempt.Generation == 0 || attempt.Generation > MaxBrowserLaunchGeneration ||
		attempt.State != BrowserLaunchAttemptPreparedState ||
		attempt.RequiredBackend == "" || attempt.MaxRuntimeMS < 1 ||
		attempt.MaxRequests < 1 || attempt.MaxResponseBytes < 1 ||
		attempt.ProcessTreeOwnershipModel != BrowserProcessTreeOwnershipModel ||
		!attempt.WriteAheadRequired || !attempt.ExactGenerationRequired ||
		!attempt.CancellationFanoutRequired || !attempt.GracefulThenForcedTermination ||
		!attempt.RestartReconciliationRequired || !attempt.StartBlocked ||
		attempt.ProcessStartAuthorized || attempt.NetworkAuthorized ||
		attempt.ProfileWriteAuthorized || attempt.ArtifactCommitAuthorized ||
		attempt.Authority != (RuntimeAuthority{}) || attempt.CreatedAt.IsZero() {
		return errors.New("browser launch attempt structure is invalid")
	}
	expected, err := browserLaunchAttemptFingerprint(attempt)
	if err != nil || attempt.Fingerprint != expected {
		return errors.New("browser launch attempt fingerprint mismatch")
	}
	return nil
}

// ValidateStoredBrowserLaunchAttempt validates an immutable attempt without
// requiring the discovery inputs that were consumed before persistence.
func ValidateStoredBrowserLaunchAttempt(attempt BrowserLaunchAttempt) error {
	return validateBrowserLaunchAttemptStructure(attempt)
}

func validFakeBrowserLifecycleState(state BrowserLifecycleState) bool {
	switch state {
	case BrowserLifecyclePrepared, BrowserLifecycleStartSubmitted, BrowserLifecycleRunning,
		BrowserLifecycleCancelRequested, BrowserLifecycleExited, BrowserLifecycleOrphaned,
		BrowserLifecycleReconciled:
		return true
	default:
		return false
	}
}

func validObservedBrowserLifecycleState(state BrowserLifecycleState) bool {
	return state == BrowserLifecycleDisabled || validFakeBrowserLifecycleState(state) ||
		state == BrowserLifecycleTimedOut || state == BrowserLifecycleCancelled
}

func lifecycleStateRequiresSyntheticProcess(state BrowserLifecycleState) bool {
	switch state {
	case BrowserLifecycleStartSubmitted, BrowserLifecycleRunning,
		BrowserLifecycleCancelRequested, BrowserLifecycleOrphaned:
		return true
	default:
		return false
	}
}

func lifecycleStateQuiescent(state BrowserLifecycleState) bool {
	switch state {
	case BrowserLifecycleDisabled, BrowserLifecyclePrepared, BrowserLifecycleExited,
		BrowserLifecycleReconciled, BrowserLifecycleTimedOut, BrowserLifecycleCancelled:
		return true
	default:
		return false
	}
}

func browserLaunchBudgetFingerprint(session SessionPlan) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{
		"browser-launch-budget.v1", session.Fingerprint,
		strconv.Itoa(session.Limits.TimeoutMS), strconv.Itoa(session.Limits.MaxRequests),
		strconv.Itoa(session.Limits.MaxResponseBytes), strconv.Itoa(session.Limits.MaxDownloadBytes),
	}, "\x00")))
	return hex.EncodeToString(digest[:])
}

func browserLaunchOwnerToken(attempt BrowserLaunchAttempt, ownerIdentity string) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{
		"browser-launch-owner.v1", attempt.Fingerprint,
		strconv.FormatUint(attempt.Generation, 10), ownerIdentity,
	}, "\x00")))
	return hex.EncodeToString(digest[:])
}

func syntheticBrowserProcessToken(attempt BrowserLaunchAttempt, lease BrowserLaunchLease,
	state BrowserLifecycleState,
) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{
		"browser-synthetic-process.v1", attempt.Fingerprint, lease.Fingerprint, string(state),
	}, "\x00")))
	return hex.EncodeToString(digest[:])
}

func browserLaunchAttemptFingerprint(value BrowserLaunchAttempt) (string, error) {
	copyValue := value
	copyValue.Fingerprint = ""
	return fingerprintJSON(copyValue, "browser launch attempt")
}

func browserLaunchLeaseFingerprint(value BrowserLaunchLease) (string, error) {
	copyValue := value
	copyValue.Fingerprint = ""
	return fingerprintJSON(copyValue, "browser launch lease")
}

func browserLifecycleObservationFingerprint(value BrowserLifecycleObservation) (string, error) {
	copyValue := value
	copyValue.Fingerprint = ""
	return fingerprintJSON(copyValue, "browser lifecycle observation")
}

func browserLifecycleReconciliationFingerprint(
	value BrowserLifecycleReconciliation,
) (string, error) {
	copyValue := value
	copyValue.Fingerprint = ""
	return fingerprintJSON(copyValue, "browser lifecycle reconciliation")
}
