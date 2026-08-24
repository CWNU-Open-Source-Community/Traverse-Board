package sandbox

import (
	"errors"
	"strconv"
	"time"
)

const (
	DockerContainerLaunchIntentProtocolVersion        = "sandbox_docker_lifecycle_intent.v1"
	DockerContainerLifecycleActionProtocolVersion     = "sandbox_docker_lifecycle_action.v1"
	DockerContainerLifecycleTransitionProtocolVersion = "sandbox_docker_lifecycle_transition.v1"
	DockerContainerLifecycleReceiptProtocolVersion    = "sandbox_docker_lifecycle_receipt.v1"

	DockerContainerLifecycleLeaseActive   = "active"
	DockerContainerLifecycleLeaseReleased = "released"

	DockerContainerLifecycleTransitionCreated  = "created"
	DockerContainerLifecycleTransitionStarted  = "started"
	DockerContainerLifecycleTransitionExited   = "exited"
	DockerContainerLifecycleTransitionCleaning = "cleaning"
	DockerContainerLifecycleTransitionCleaned  = "cleaned"
	DockerContainerLifecycleTransitionFailed   = "failed"

	DockerContainerLifecycleActionPrepared = "prepared"

	DockerContainerLifecycleReasonCreated          = "created"
	DockerContainerLifecycleReasonStarted          = "started"
	DockerContainerLifecycleReasonNaturalExit      = "natural_exit"
	DockerContainerLifecycleReasonTimeout          = "timeout"
	DockerContainerLifecycleReasonCancelled        = "cancelled"
	DockerContainerLifecycleReasonRestartRecovery  = "restart_recovery"
	DockerContainerLifecycleReasonCleanupStarted   = "cleanup_started"
	DockerContainerLifecycleReasonCleanupCompleted = "cleanup_completed"
	DockerContainerLifecycleReasonCreateFailed     = "create_failed"
	DockerContainerLifecycleReasonStartFailed      = "start_failed"
	DockerContainerLifecycleReasonWaitFailed       = "wait_failed"
	DockerContainerLifecycleReasonTerminateFailed  = "terminate_failed"
	DockerContainerLifecycleReasonCleanupFailed    = "cleanup_failed"

	DockerContainerLifecycleOutcomeNaturalExit = "natural_exit"
	DockerContainerLifecycleOutcomeTimedOut    = "timed_out"
	DockerContainerLifecycleOutcomeCancelled   = "cancelled"
	DockerContainerLifecycleOutcomeFailed      = "failed"

	DefaultDockerContainerLifecycleLeaseTTL = 2 * time.Minute
	MinDockerContainerLifecycleLeaseTTL     = time.Second
	MaxDockerContainerLifecycleLeaseTTL     = 10 * time.Minute
	MaxDockerContainerLifecycleActions      = 64
	MaxDockerContainerLifecycleTransitions  = 64
)

// DockerContainerLaunchIntent is the immutable write-ahead ownership record for
// one container resource generation. It is evidence of intent, not authority to
// start or recover a process.
type DockerContainerLaunchIntent struct {
	ID                        string
	AttemptID                 string
	PlanID                    string
	RunID                     string
	MissionID                 string
	WorkspaceID               string
	ProtocolVersion           string
	ResourceGeneration        int64
	OperationKeyDigest        string
	RequestFingerprint        string
	SpecFingerprint           string
	PlanFingerprint           string
	AuthorityFingerprint      string
	BaseLabelPlanFingerprint  string
	OwnershipLabelFingerprint string
	ContainerNameFingerprint  string
	EndpointClass             string
	EndpointFingerprint       string
	IntentFingerprint         string
	ProductEntryEnabled       bool
	ExecutionAuthorized       bool
	ArtifactCommitAuthorized  bool
	RequestedBy               string
	CreatedAt                 time.Time
}

func NewDockerContainerLaunchIntent(id, attemptID, operationKeyDigest string,
	plan DockerContainerPlan, request DockerContainerWriteRequest,
	endpoint DockerObservationEndpoint, requestedBy string, resourceGeneration int64,
	createdAt time.Time,
) (DockerContainerLaunchIntent, error) {
	intent := DockerContainerLaunchIntent{
		ID: id, AttemptID: attemptID, PlanID: plan.ID, RunID: plan.RunID,
		MissionID: plan.MissionID, WorkspaceID: plan.WorkspaceID,
		ProtocolVersion:    DockerContainerLaunchIntentProtocolVersion,
		ResourceGeneration: resourceGeneration, OperationKeyDigest: operationKeyDigest,
		RequestFingerprint: request.RequestFingerprint, SpecFingerprint: plan.SpecFingerprint,
		PlanFingerprint: plan.PlanFingerprint, AuthorityFingerprint: plan.AuthorityFingerprint,
		BaseLabelPlanFingerprint: plan.LabelPlanFingerprint,
		ContainerNameFingerprint: plan.ContainerNameFingerprint,
		EndpointClass:            endpoint.Class, EndpointFingerprint: endpoint.Fingerprint,
		RequestedBy: requestedBy, CreatedAt: createdAt.UTC(),
	}
	intent.IntentFingerprint = dockerContainerLaunchIntentFingerprint(intent)
	ownership, err := NewDockerContainerLifecycleOwnership(intent.AttemptID,
		intent.ResourceGeneration, intent.IntentFingerprint, intent.BaseLabelPlanFingerprint)
	if err != nil {
		return DockerContainerLaunchIntent{}, err
	}
	intent.OwnershipLabelFingerprint = ownership.OwnershipLabelFingerprint
	if plan.Validate() != nil || request.Validate() != nil || endpoint.Validate() != nil ||
		DockerContainerPlanMatchesSpec(plan, request.Spec) != nil || intent.Validate() != nil {
		return DockerContainerLaunchIntent{}, errors.New("docker container launch intent is invalid")
	}
	return intent, nil
}

func (intent DockerContainerLaunchIntent) Validate() error {
	for label, value := range map[string]string{
		"lifecycle intent id": intent.ID, "lifecycle attempt id": intent.AttemptID,
		"lifecycle plan id": intent.PlanID, "lifecycle Run id": intent.RunID,
		"lifecycle Mission id": intent.MissionID, "lifecycle Workspace id": intent.WorkspaceID,
		"lifecycle requester": intent.RequestedBy,
	} {
		if validateStoredIdentity(label, value) != nil {
			return errors.New("docker container launch intent identity is invalid")
		}
	}
	for _, value := range []string{intent.OperationKeyDigest, intent.RequestFingerprint,
		intent.SpecFingerprint, intent.PlanFingerprint, intent.AuthorityFingerprint,
		intent.BaseLabelPlanFingerprint, intent.OwnershipLabelFingerprint,
		intent.ContainerNameFingerprint, intent.EndpointFingerprint, intent.IntentFingerprint} {
		if !validDigest(value) {
			return errors.New("docker container launch intent fingerprint is invalid")
		}
	}
	endpoint, err := NewDockerObservationEndpoint(intent.EndpointClass)
	if err != nil || !validDockerContainerLocalEndpoint(endpoint) ||
		intent.ProtocolVersion != DockerContainerLaunchIntentProtocolVersion ||
		intent.ResourceGeneration != 1 || intent.EndpointFingerprint != endpoint.Fingerprint ||
		intent.ProductEntryEnabled || intent.ExecutionAuthorized ||
		intent.ArtifactCommitAuthorized || intent.CreatedAt.IsZero() ||
		intent.IntentFingerprint != dockerContainerLaunchIntentFingerprint(intent) {
		return errors.New("docker container launch intent violates the durable boundary")
	}
	ownership, err := intent.LifecycleOwnership()
	if err != nil || ownership.OwnershipLabelFingerprint != intent.OwnershipLabelFingerprint {
		return errors.New("docker container launch ownership labels changed")
	}
	return nil
}

func (intent DockerContainerLaunchIntent) LifecycleOwnership() (DockerContainerLifecycleOwnership, error) {
	return NewDockerContainerLifecycleOwnership(intent.AttemptID, intent.ResourceGeneration,
		intent.IntentFingerprint, intent.BaseLabelPlanFingerprint)
}

func dockerContainerLaunchIntentFingerprint(intent DockerContainerLaunchIntent) string {
	// OwnershipLabelFingerprint is derived from this fingerprint and is therefore
	// deliberately not part of its own preimage.
	return fingerprint(DockerContainerLaunchIntentProtocolVersion, intent.ID, intent.AttemptID,
		intent.PlanID, intent.RunID, intent.MissionID, intent.WorkspaceID,
		strconv.FormatInt(intent.ResourceGeneration, 10), intent.OperationKeyDigest,
		intent.RequestFingerprint, intent.SpecFingerprint, intent.PlanFingerprint,
		intent.AuthorityFingerprint, intent.BaseLabelPlanFingerprint,
		intent.ContainerNameFingerprint, intent.EndpointClass, intent.EndpointFingerprint,
		strconv.FormatBool(intent.ProductEntryEnabled),
		strconv.FormatBool(intent.ExecutionAuthorized),
		strconv.FormatBool(intent.ArtifactCommitAuthorized), intent.RequestedBy,
		intent.CreatedAt.Format(time.RFC3339Nano))
}

type DockerContainerLifecycleLease struct {
	IntentID           string
	LeaseID            string
	OwnerID            string
	ResourceGeneration int64
	Generation         int64
	Status             string
	AcquiredAt         time.Time
	RenewedAt          time.Time
	ExpiresAt          time.Time
	ReleasedAt         *time.Time
}

func NewDockerContainerLifecycleLease(intent DockerContainerLaunchIntent, leaseID, ownerID string,
	generation int64, acquiredAt time.Time, ttl time.Duration,
) (DockerContainerLifecycleLease, error) {
	if ValidateDockerContainerLifecycleLeaseTTL(ttl) != nil {
		return DockerContainerLifecycleLease{}, errors.New("docker lifecycle lease TTL is invalid")
	}
	acquiredAt = acquiredAt.UTC()
	lease := DockerContainerLifecycleLease{IntentID: intent.ID, LeaseID: leaseID,
		OwnerID: ownerID, ResourceGeneration: intent.ResourceGeneration,
		Generation: generation, Status: DockerContainerLifecycleLeaseActive,
		AcquiredAt: acquiredAt, RenewedAt: acquiredAt, ExpiresAt: acquiredAt.Add(ttl)}
	if intent.Validate() != nil || lease.Validate() != nil {
		return DockerContainerLifecycleLease{}, errors.New("docker lifecycle lease is invalid")
	}
	return lease, nil
}

func ValidateDockerContainerLifecycleLeaseTTL(ttl time.Duration) error {
	if ttl < MinDockerContainerLifecycleLeaseTTL || ttl > MaxDockerContainerLifecycleLeaseTTL {
		return errors.New("docker lifecycle lease TTL is outside the supported range")
	}
	return nil
}

func (lease DockerContainerLifecycleLease) Validate() error {
	if validateStoredIdentity("Docker lifecycle lease intent id", lease.IntentID) != nil ||
		validateStoredIdentity("Docker lifecycle lease id", lease.LeaseID) != nil ||
		validateStoredIdentity("Docker lifecycle lease owner", lease.OwnerID) != nil ||
		lease.ResourceGeneration != 1 || lease.Generation < 1 ||
		(lease.Status != DockerContainerLifecycleLeaseActive &&
			lease.Status != DockerContainerLifecycleLeaseReleased) ||
		lease.AcquiredAt.IsZero() || lease.RenewedAt.Before(lease.AcquiredAt) ||
		!lease.ExpiresAt.After(lease.RenewedAt) {
		return errors.New("docker lifecycle lease is invalid")
	}
	if lease.Status == DockerContainerLifecycleLeaseActive && lease.ReleasedAt != nil {
		return errors.New("active Docker lifecycle lease cannot have a release time")
	}
	if lease.Status == DockerContainerLifecycleLeaseReleased &&
		(lease.ReleasedAt == nil || lease.ReleasedAt.Before(lease.AcquiredAt)) {
		return errors.New("released Docker lifecycle lease requires a release time")
	}
	return nil
}

func (lease DockerContainerLifecycleLease) ActiveAt(now time.Time) bool {
	return lease.Status == DockerContainerLifecycleLeaseActive && now.Before(lease.ExpiresAt)
}

func (lease DockerContainerLifecycleLease) Fences(expected DockerContainerLifecycleLease,
	now time.Time,
) bool {
	return lease.IntentID == expected.IntentID && lease.LeaseID == expected.LeaseID &&
		lease.OwnerID == expected.OwnerID &&
		lease.ResourceGeneration == expected.ResourceGeneration &&
		lease.Generation == expected.Generation && lease.Status == expected.Status &&
		lease.AcquiredAt.Equal(expected.AcquiredAt) && lease.RenewedAt.Equal(expected.RenewedAt) &&
		lease.ExpiresAt.Equal(expected.ExpiresAt) && lease.ActiveAt(now)
}

func (lease DockerContainerLifecycleLease) Renew(now time.Time,
	ttl time.Duration,
) (DockerContainerLifecycleLease, error) {
	now = now.UTC()
	if lease.Validate() != nil || !lease.ActiveAt(now) ||
		ValidateDockerContainerLifecycleLeaseTTL(ttl) != nil {
		return DockerContainerLifecycleLease{}, errors.New("docker lifecycle lease cannot be renewed")
	}
	lease.RenewedAt, lease.ExpiresAt = now, now.Add(ttl)
	if lease.Validate() != nil {
		return DockerContainerLifecycleLease{}, errors.New("renewed Docker lifecycle lease is invalid")
	}
	return lease, nil
}

type DockerContainerLifecycleLeaseAcquisition struct {
	Lease    DockerContainerLifecycleLease
	Replayed bool
	TookOver bool
}

type DockerContainerLifecyclePreparedAction struct {
	IntentID           string
	Ordinal            int
	LeaseID            string
	OwnerID            string
	LeaseGeneration    int64
	ResourceGeneration int64
	Verb               string
	State              string
	ActionFingerprint  string
	PreparedAt         time.Time
}

func NewDockerContainerLifecyclePreparedAction(intentID string, ordinal int,
	lease DockerContainerLifecycleLease, verb string, preparedAt time.Time,
) (DockerContainerLifecyclePreparedAction, error) {
	action := DockerContainerLifecyclePreparedAction{IntentID: intentID, Ordinal: ordinal,
		LeaseID: lease.LeaseID, OwnerID: lease.OwnerID, LeaseGeneration: lease.Generation,
		ResourceGeneration: lease.ResourceGeneration, Verb: verb,
		State: DockerContainerLifecycleActionPrepared, PreparedAt: preparedAt.UTC()}
	action.ActionFingerprint = dockerContainerLifecycleActionFingerprint(action)
	if lease.Validate() != nil || lease.IntentID != intentID || action.Validate() != nil {
		return DockerContainerLifecyclePreparedAction{}, errors.New("docker lifecycle action is invalid")
	}
	return action, nil
}

func (action DockerContainerLifecyclePreparedAction) Validate() error {
	if validateStoredIdentity("Docker lifecycle action intent id", action.IntentID) != nil ||
		validateStoredIdentity("Docker lifecycle action lease id", action.LeaseID) != nil ||
		validateStoredIdentity("Docker lifecycle action owner", action.OwnerID) != nil ||
		action.Ordinal < 1 || action.Ordinal > MaxDockerContainerLifecycleActions ||
		action.LeaseGeneration < 1 || action.ResourceGeneration != 1 ||
		!validDockerContainerLifecycleWriteVerb(action.Verb) ||
		action.State != DockerContainerLifecycleActionPrepared || action.PreparedAt.IsZero() ||
		!validDigest(action.ActionFingerprint) ||
		action.ActionFingerprint != dockerContainerLifecycleActionFingerprint(action) {
		return errors.New("docker lifecycle action is invalid")
	}
	return nil
}

func validDockerContainerLifecycleWriteVerb(verb string) bool {
	return verb == string(DockerContainerLifecycleActionCreate) ||
		verb == string(DockerContainerLifecycleActionStart) ||
		verb == string(DockerContainerLifecycleActionAttachStdin) ||
		verb == string(DockerContainerLifecycleActionTERM) ||
		verb == string(DockerContainerLifecycleActionKILL) ||
		verb == string(DockerContainerLifecycleActionDelete)
}

func dockerContainerLifecycleActionFingerprint(action DockerContainerLifecyclePreparedAction) string {
	return fingerprint(DockerContainerLifecycleActionProtocolVersion, action.IntentID,
		strconv.Itoa(action.Ordinal), action.LeaseID, action.OwnerID,
		strconv.FormatInt(action.LeaseGeneration, 10),
		strconv.FormatInt(action.ResourceGeneration, 10), action.Verb, action.State,
		action.PreparedAt.Format(time.RFC3339Nano))
}

type DockerContainerLifecycleTransition struct {
	IntentID               string
	Ordinal                int
	LeaseID                string
	OwnerID                string
	LeaseGeneration        int64
	ResourceGeneration     int64
	State                  string
	ReasonCode             string
	ExitCode               *int
	ContainerIDFingerprint string
	PreviousFingerprint    string
	TransitionFingerprint  string
	RecordedAt             time.Time
}

func NewDockerContainerLifecycleTransition(intentID string, ordinal int,
	lease DockerContainerLifecycleLease, state, reasonCode string, exitCode *int,
	containerIDFingerprint, previousFingerprint string, recordedAt time.Time,
) (DockerContainerLifecycleTransition, error) {
	transition := DockerContainerLifecycleTransition{IntentID: intentID, Ordinal: ordinal,
		LeaseID: lease.LeaseID, OwnerID: lease.OwnerID, LeaseGeneration: lease.Generation,
		ResourceGeneration: lease.ResourceGeneration, State: state, ReasonCode: reasonCode,
		ExitCode: cloneInt(exitCode), ContainerIDFingerprint: containerIDFingerprint,
		PreviousFingerprint: previousFingerprint, RecordedAt: recordedAt.UTC()}
	transition.TransitionFingerprint = dockerContainerLifecycleTransitionFingerprint(transition)
	if lease.Validate() != nil || lease.IntentID != intentID || transition.Validate() != nil {
		return DockerContainerLifecycleTransition{}, errors.New("docker lifecycle transition is invalid")
	}
	return transition, nil
}

func (transition DockerContainerLifecycleTransition) Validate() error {
	if validateStoredIdentity("Docker lifecycle transition intent id", transition.IntentID) != nil ||
		validateStoredIdentity("Docker lifecycle transition lease id", transition.LeaseID) != nil ||
		validateStoredIdentity("Docker lifecycle transition owner", transition.OwnerID) != nil ||
		transition.Ordinal < 1 || transition.Ordinal > MaxDockerContainerLifecycleTransitions ||
		transition.LeaseGeneration < 1 || transition.ResourceGeneration != 1 ||
		!validDockerContainerLifecycleTransitionState(transition.State) ||
		!validDockerContainerLifecycleReason(transition.ReasonCode) ||
		transition.RecordedAt.IsZero() || !validDigest(transition.TransitionFingerprint) ||
		(transition.Ordinal == 1 && transition.PreviousFingerprint != "") ||
		(transition.Ordinal > 1 && !validDigest(transition.PreviousFingerprint)) ||
		(transition.ContainerIDFingerprint != "" &&
			!validDigest(transition.ContainerIDFingerprint)) ||
		transition.TransitionFingerprint != dockerContainerLifecycleTransitionFingerprint(transition) {
		return errors.New("docker lifecycle transition is invalid")
	}
	if transition.State == DockerContainerLifecycleTransitionExited {
		if transition.ExitCode == nil || *transition.ExitCode < 0 || *transition.ExitCode > 255 ||
			!validDigest(transition.ContainerIDFingerprint) {
			return errors.New("Docker lifecycle exit transition is incomplete")
		}
	} else if transition.ExitCode != nil {
		return errors.New("non-exit Docker lifecycle transition contains an exit code")
	}
	if (transition.State == DockerContainerLifecycleTransitionCreated ||
		transition.State == DockerContainerLifecycleTransitionStarted) &&
		!validDigest(transition.ContainerIDFingerprint) {
		return errors.New("Docker lifecycle container transition lacks identity")
	}
	if !validDockerContainerLifecycleStateReason(transition.State, transition.ReasonCode) {
		return errors.New("Docker lifecycle transition reason does not match its state")
	}
	return nil
}

func validDockerContainerLifecycleTransitionState(state string) bool {
	return state == DockerContainerLifecycleTransitionCreated ||
		state == DockerContainerLifecycleTransitionStarted ||
		state == DockerContainerLifecycleTransitionExited ||
		state == DockerContainerLifecycleTransitionCleaning ||
		state == DockerContainerLifecycleTransitionCleaned ||
		state == DockerContainerLifecycleTransitionFailed
}

func validDockerContainerLifecycleReason(reason string) bool {
	switch reason {
	case DockerContainerLifecycleReasonCreated, DockerContainerLifecycleReasonStarted,
		DockerContainerLifecycleReasonNaturalExit, DockerContainerLifecycleReasonTimeout,
		DockerContainerLifecycleReasonCancelled, DockerContainerLifecycleReasonRestartRecovery,
		DockerContainerLifecycleReasonCleanupStarted, DockerContainerLifecycleReasonCleanupCompleted,
		DockerContainerLifecycleReasonCreateFailed, DockerContainerLifecycleReasonStartFailed,
		DockerContainerLifecycleReasonWaitFailed, DockerContainerLifecycleReasonTerminateFailed,
		DockerContainerLifecycleReasonCleanupFailed,
		DockerContainerLifecycleFailureDisabled, DockerContainerLifecycleFailureUnsupported,
		DockerContainerLifecycleFailureConnection, DockerContainerLifecycleFailureInvalidResponse,
		DockerContainerLifecycleFailureConfigMismatch, DockerContainerLifecycleFailureUnsafeExisting:
		return true
	default:
		return false
	}
}

func validDockerContainerLifecycleStateReason(state, reason string) bool {
	switch state {
	case DockerContainerLifecycleTransitionCreated:
		return reason == DockerContainerLifecycleReasonCreated ||
			reason == DockerContainerLifecycleReasonRestartRecovery
	case DockerContainerLifecycleTransitionStarted:
		return reason == DockerContainerLifecycleReasonStarted ||
			reason == DockerContainerLifecycleReasonRestartRecovery
	case DockerContainerLifecycleTransitionExited:
		return reason == DockerContainerLifecycleReasonNaturalExit ||
			reason == DockerContainerLifecycleReasonTimeout ||
			reason == DockerContainerLifecycleReasonCancelled ||
			reason == DockerContainerLifecycleReasonRestartRecovery
	case DockerContainerLifecycleTransitionCleaning:
		return reason == DockerContainerLifecycleReasonNaturalExit ||
			reason == DockerContainerLifecycleReasonTimeout ||
			reason == DockerContainerLifecycleReasonCancelled ||
			reason == DockerContainerLifecycleReasonRestartRecovery ||
			reason == DockerContainerLifecycleReasonCleanupStarted
	case DockerContainerLifecycleTransitionCleaned:
		return reason == DockerContainerLifecycleReasonCleanupCompleted ||
			reason == DockerContainerLifecycleReasonRestartRecovery
	case DockerContainerLifecycleTransitionFailed:
		switch reason {
		case DockerContainerLifecycleReasonCreateFailed,
			DockerContainerLifecycleReasonStartFailed,
			DockerContainerLifecycleReasonWaitFailed,
			DockerContainerLifecycleReasonTerminateFailed,
			DockerContainerLifecycleReasonCleanupFailed,
			DockerContainerLifecycleFailureDisabled,
			DockerContainerLifecycleFailureUnsupported,
			DockerContainerLifecycleFailureConnection,
			DockerContainerLifecycleFailureInvalidResponse,
			DockerContainerLifecycleFailureConfigMismatch,
			DockerContainerLifecycleFailureUnsafeExisting:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func dockerContainerLifecycleTransitionFingerprint(transition DockerContainerLifecycleTransition) string {
	exitCode := ""
	if transition.ExitCode != nil {
		exitCode = strconv.Itoa(*transition.ExitCode)
	}
	return fingerprint(DockerContainerLifecycleTransitionProtocolVersion, transition.IntentID,
		strconv.Itoa(transition.Ordinal), transition.LeaseID, transition.OwnerID,
		strconv.FormatInt(transition.LeaseGeneration, 10),
		strconv.FormatInt(transition.ResourceGeneration, 10), transition.State,
		transition.ReasonCode, exitCode, transition.ContainerIDFingerprint,
		transition.PreviousFingerprint, transition.RecordedAt.Format(time.RFC3339Nano))
}

type DockerContainerLifecycleReceipt struct {
	IntentID                   string
	LeaseID                    string
	OwnerID                    string
	LeaseGeneration            int64
	ResourceGeneration         int64
	FinalTransitionFingerprint string
	ContainerIDFingerprint     string
	Outcome                    string
	ExitCode                   *int
	ContainerRemovedNow        bool
	ContainerAlreadyAbsent     bool
	CleanupFingerprint         string
	CompletedAt                time.Time
}

func NewDockerContainerLifecycleReceipt(intentID string,
	lease DockerContainerLifecycleLease, final DockerContainerLifecycleTransition,
	containerIDFingerprint, outcome string, exitCode *int, removedNow, alreadyAbsent bool,
	completedAt time.Time,
) (DockerContainerLifecycleReceipt, error) {
	receipt := DockerContainerLifecycleReceipt{IntentID: intentID, LeaseID: lease.LeaseID,
		OwnerID: lease.OwnerID, LeaseGeneration: lease.Generation,
		ResourceGeneration:         lease.ResourceGeneration,
		FinalTransitionFingerprint: final.TransitionFingerprint,
		ContainerIDFingerprint:     containerIDFingerprint, Outcome: outcome,
		ExitCode: cloneInt(exitCode), ContainerRemovedNow: removedNow,
		ContainerAlreadyAbsent: alreadyAbsent, CompletedAt: completedAt.UTC()}
	receipt.CleanupFingerprint = dockerContainerLifecycleReceiptFingerprint(receipt)
	if lease.Validate() != nil || lease.IntentID != intentID ||
		final.Validate() != nil || final.IntentID != intentID ||
		final.State != DockerContainerLifecycleTransitionCleaned || receipt.Validate() != nil {
		return DockerContainerLifecycleReceipt{}, errors.New("docker lifecycle receipt is invalid")
	}
	return receipt, nil
}

func (receipt DockerContainerLifecycleReceipt) Validate() error {
	if validateStoredIdentity("Docker lifecycle receipt intent id", receipt.IntentID) != nil ||
		validateStoredIdentity("Docker lifecycle receipt lease id", receipt.LeaseID) != nil ||
		validateStoredIdentity("Docker lifecycle receipt owner", receipt.OwnerID) != nil ||
		receipt.LeaseGeneration < 1 || receipt.ResourceGeneration != 1 ||
		!validDigest(receipt.FinalTransitionFingerprint) ||
		(receipt.ContainerIDFingerprint != "" &&
			!validDigest(receipt.ContainerIDFingerprint)) ||
		!validDockerContainerLifecycleOutcome(receipt.Outcome) ||
		receipt.ContainerRemovedNow == receipt.ContainerAlreadyAbsent ||
		(receipt.ContainerRemovedNow && !validDigest(receipt.ContainerIDFingerprint)) ||
		receipt.CompletedAt.IsZero() || !validDigest(receipt.CleanupFingerprint) ||
		receipt.CleanupFingerprint != dockerContainerLifecycleReceiptFingerprint(receipt) {
		return errors.New("docker lifecycle receipt is invalid")
	}
	if receipt.ExitCode != nil && (*receipt.ExitCode < 0 || *receipt.ExitCode > 255) {
		return errors.New("docker lifecycle receipt exit code is invalid")
	}
	return nil
}

func validDockerContainerLifecycleOutcome(outcome string) bool {
	return outcome == DockerContainerLifecycleOutcomeNaturalExit ||
		outcome == DockerContainerLifecycleOutcomeTimedOut ||
		outcome == DockerContainerLifecycleOutcomeCancelled ||
		outcome == DockerContainerLifecycleOutcomeFailed
}

func dockerContainerLifecycleReceiptFingerprint(receipt DockerContainerLifecycleReceipt) string {
	exitCode := ""
	if receipt.ExitCode != nil {
		exitCode = strconv.Itoa(*receipt.ExitCode)
	}
	return fingerprint(DockerContainerLifecycleReceiptProtocolVersion, receipt.IntentID,
		receipt.LeaseID, receipt.OwnerID, strconv.FormatInt(receipt.LeaseGeneration, 10),
		strconv.FormatInt(receipt.ResourceGeneration, 10), receipt.FinalTransitionFingerprint,
		receipt.ContainerIDFingerprint, receipt.Outcome, exitCode,
		strconv.FormatBool(receipt.ContainerRemovedNow),
		strconv.FormatBool(receipt.ContainerAlreadyAbsent),
		receipt.CompletedAt.Format(time.RFC3339Nano))
}

type DockerContainerLifecycleRecord struct {
	Intent      DockerContainerLaunchIntent
	Lease       DockerContainerLifecycleLease
	Actions     []DockerContainerLifecyclePreparedAction
	Transitions []DockerContainerLifecycleTransition
	Receipt     *DockerContainerLifecycleReceipt
	Replayed    bool
	TookOver    bool
}

func (record DockerContainerLifecycleRecord) Validate() error {
	if record.Intent.Validate() != nil || record.Lease.Validate() != nil ||
		record.Lease.IntentID != record.Intent.ID ||
		record.Lease.ResourceGeneration != record.Intent.ResourceGeneration ||
		len(record.Actions) > MaxDockerContainerLifecycleActions ||
		len(record.Transitions) > MaxDockerContainerLifecycleTransitions {
		return errors.New("docker lifecycle record binding is invalid")
	}
	for index, action := range record.Actions {
		if action.Validate() != nil || action.IntentID != record.Intent.ID ||
			action.Ordinal != index+1 || action.ResourceGeneration != record.Intent.ResourceGeneration {
			return errors.New("docker lifecycle action ledger is invalid")
		}
	}
	seen := map[string]bool{}
	cleaned := false
	for index, transition := range record.Transitions {
		if transition.Validate() != nil || transition.IntentID != record.Intent.ID ||
			transition.Ordinal != index+1 ||
			transition.ResourceGeneration != record.Intent.ResourceGeneration {
			return errors.New("docker lifecycle transition ledger is invalid")
		}
		if index > 0 && transition.PreviousFingerprint !=
			record.Transitions[index-1].TransitionFingerprint {
			return errors.New("docker lifecycle transition chain is invalid")
		}
		if cleaned {
			return errors.New("docker lifecycle transition follows cleaned")
		}
		if transition.State != DockerContainerLifecycleTransitionFailed {
			if seen[transition.State] {
				return errors.New("docker lifecycle transition state is duplicated")
			}
		}
		if transition.State == DockerContainerLifecycleTransitionStarted &&
			!seen[DockerContainerLifecycleTransitionCreated] {
			return errors.New("docker lifecycle started before created")
		}
		if transition.State == DockerContainerLifecycleTransitionExited &&
			!seen[DockerContainerLifecycleTransitionStarted] {
			return errors.New("docker lifecycle exited before started")
		}
		if transition.State == DockerContainerLifecycleTransitionCleaned &&
			!seen[DockerContainerLifecycleTransitionCleaning] {
			return errors.New("docker lifecycle cleaned before cleaning")
		}
		if transition.State != DockerContainerLifecycleTransitionFailed {
			seen[transition.State] = true
		}
		cleaned = transition.State == DockerContainerLifecycleTransitionCleaned
	}
	if record.Receipt != nil {
		if record.Receipt.Validate() != nil || record.Receipt.IntentID != record.Intent.ID ||
			len(record.Transitions) == 0 ||
			record.Transitions[len(record.Transitions)-1].State !=
				DockerContainerLifecycleTransitionCleaned ||
			record.Receipt.FinalTransitionFingerprint !=
				record.Transitions[len(record.Transitions)-1].TransitionFingerprint {
			return errors.New("docker lifecycle receipt does not close the transition chain")
		}
	}
	return nil
}

func (record DockerContainerLifecycleRecord) RecoverableAt(now time.Time) bool {
	return record.Validate() == nil && record.Receipt == nil && !record.Lease.ActiveAt(now)
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
