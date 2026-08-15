package sandbox

import (
	"context"
	"errors"
	"sort"
	"strconv"
)

const (
	DockerContainerLifecycleOwnershipProtocolVersion   = "sandbox_docker_container_lifecycle_ownership.v1"
	DockerContainerLifecycleObservationProtocolVersion = "sandbox_docker_container_lifecycle_observation.v1"

	DockerContainerLifecycleLabelAttempt            = "io.cyberagent.lifecycle.attempt"
	DockerContainerLifecycleLabelResourceGeneration = "io.cyberagent.lifecycle.resource-generation"
	DockerContainerLifecycleLabelIntent             = "io.cyberagent.lifecycle.intent"

	DockerContainerLifecycleStateAbsent  = "absent"
	DockerContainerLifecycleStateCreated = "created"
	DockerContainerLifecycleStateRunning = "running"
	DockerContainerLifecycleStateExited  = "exited"

	DockerContainerLifecycleActionCreate = "create"
	DockerContainerLifecycleActionStart  = "start"
	DockerContainerLifecycleActionWait   = "wait"
	DockerContainerLifecycleActionTERM   = "term"
	DockerContainerLifecycleActionKILL   = "kill"
	DockerContainerLifecycleActionDelete = "delete"
)

type DockerContainerLifecycleActionKind string

func (action DockerContainerLifecycleActionKind) String() string {
	return string(action)
}

func (action DockerContainerLifecycleActionKind) Valid() bool {
	switch action {
	case DockerContainerLifecycleActionCreate, DockerContainerLifecycleActionStart,
		DockerContainerLifecycleActionWait, DockerContainerLifecycleActionTERM,
		DockerContainerLifecycleActionKILL, DockerContainerLifecycleActionDelete:
		return true
	default:
		return false
	}
}

// DockerContainerLifecycleFence is invoked immediately before each Docker POST or DELETE.
// A coordinator uses it to re-check durable lease ownership at the last safe point.
type DockerContainerLifecycleFence func(context.Context, DockerContainerLifecycleActionKind) error

type DockerContainerLifecycleOwnership struct {
	ProtocolVersion           string
	AttemptID                 string
	ResourceGeneration        int64
	IntentFingerprint         string
	BaseLabelPlanFingerprint  string
	OwnershipLabelFingerprint string
}

func NewDockerContainerLifecycleOwnership(attemptID string, resourceGeneration int64,
	intentFingerprint, baseLabelPlanFingerprint string,
) (DockerContainerLifecycleOwnership, error) {
	ownership := DockerContainerLifecycleOwnership{
		ProtocolVersion: DockerContainerLifecycleOwnershipProtocolVersion,
		AttemptID:       attemptID, ResourceGeneration: resourceGeneration,
		IntentFingerprint:        intentFingerprint,
		BaseLabelPlanFingerprint: baseLabelPlanFingerprint,
	}
	ownership.OwnershipLabelFingerprint = dockerContainerLifecycleOwnershipFingerprint(ownership)
	if ownership.Validate() != nil {
		return DockerContainerLifecycleOwnership{}, errors.New("docker lifecycle ownership is invalid")
	}
	return ownership, nil
}

func (ownership DockerContainerLifecycleOwnership) isZero() bool {
	return ownership == (DockerContainerLifecycleOwnership{})
}

func (ownership DockerContainerLifecycleOwnership) Validate() error {
	if ownership.ProtocolVersion != DockerContainerLifecycleOwnershipProtocolVersion ||
		validateStoredIdentity("Docker lifecycle ownership attempt id", ownership.AttemptID) != nil ||
		ownership.ResourceGeneration < 1 || !validDigest(ownership.IntentFingerprint) ||
		!validDigest(ownership.BaseLabelPlanFingerprint) ||
		!validDigest(ownership.OwnershipLabelFingerprint) ||
		ownership.OwnershipLabelFingerprint != dockerContainerLifecycleOwnershipFingerprint(ownership) {
		return errors.New("docker lifecycle ownership is invalid")
	}
	for _, label := range ownership.overlayLabels() {
		if label.Validate() != nil {
			return errors.New("docker lifecycle ownership label is invalid")
		}
	}
	return nil
}

func (ownership DockerContainerLifecycleOwnership) overlayLabels() []DockerContainerLabel {
	return []DockerContainerLabel{
		{Name: DockerContainerLifecycleLabelAttempt, Value: ownership.AttemptID},
		{Name: DockerContainerLifecycleLabelResourceGeneration,
			Value: strconv.FormatInt(ownership.ResourceGeneration, 10)},
		{Name: DockerContainerLifecycleLabelIntent, Value: ownership.IntentFingerprint},
	}
}

func dockerContainerLifecycleOwnershipFingerprint(ownership DockerContainerLifecycleOwnership) string {
	return fingerprint(DockerContainerLifecycleOwnershipProtocolVersion,
		ownership.BaseLabelPlanFingerprint, DockerContainerLifecycleLabelAttempt, ownership.AttemptID,
		DockerContainerLifecycleLabelResourceGeneration,
		strconv.FormatInt(ownership.ResourceGeneration, 10),
		DockerContainerLifecycleLabelIntent, ownership.IntentFingerprint)
}

func dockerContainerLifecycleOwnedLabels(base []DockerContainerLabel,
	ownership DockerContainerLifecycleOwnership,
) ([]DockerContainerLabel, error) {
	if ownership.Validate() != nil {
		return nil, errors.New("docker lifecycle ownership is invalid")
	}
	labels := append([]DockerContainerLabel(nil), base...)
	labels = append(labels, ownership.overlayLabels()...)
	sort.Slice(labels, func(i, j int) bool { return labels[i].Name < labels[j].Name })
	for index, label := range labels {
		if label.Validate() != nil || (index > 0 && labels[index-1].Name == label.Name) {
			return nil, errors.New("docker lifecycle ownership labels are invalid")
		}
	}
	return labels, nil
}

func NewOwnedDockerContainerLifecycleRequest(attemptID string, generation int64,
	writeRequest DockerContainerWriteRequest, stage DockerContainerStageResult,
	ownership DockerContainerLifecycleOwnership, confirmation string,
) (DockerContainerLifecycleRequest, error) {
	request, err := NewDockerContainerLifecycleRequest(attemptID, generation, writeRequest,
		stage, confirmation)
	if err != nil {
		return DockerContainerLifecycleRequest{}, err
	}
	request.Ownership = ownership
	request.RequestFingerprint = dockerContainerLifecycleRequestFingerprint(request)
	if request.Validate() != nil || ownership.BaseLabelPlanFingerprint !=
		writeRequest.Spec.LabelPlanFingerprint {
		return DockerContainerLifecycleRequest{}, errors.New("owned docker lifecycle request is invalid")
	}
	return request, nil
}

// NewRecoveredDockerContainerLifecycleRequest reconstructs an owned request
// from durable identity after the original stopped-container Stage fact is no
// longer semantically valid (for example, the container is already running or
// exited). It never fabricates the v56 "never started" claim.
func NewRecoveredDockerContainerLifecycleRequest(attemptID string, generation int64,
	writeRequest DockerContainerWriteRequest, resourceIDFingerprint string,
	ownership DockerContainerLifecycleOwnership, confirmation string,
) (DockerContainerLifecycleRequest, error) {
	request := DockerContainerLifecycleRequest{
		ProtocolVersion: DockerContainerLifecycleRequestProtocolVersion,
		AttemptID:       attemptID, LeaseGeneration: generation, WriteRequest: writeRequest,
		Ownership: ownership, ResourceIDFingerprint: resourceIDFingerprint,
		ProbeAuthorized: confirmation == DockerContainerLifecycleConfirmation,
	}
	request.RequestFingerprint = dockerContainerLifecycleRequestFingerprint(request)
	if request.Validate() != nil || ownership.BaseLabelPlanFingerprint !=
		writeRequest.Spec.LabelPlanFingerprint || resourceIDFingerprint == "" {
		return DockerContainerLifecycleRequest{}, errors.New("recovered Docker lifecycle request is invalid")
	}
	return request, nil
}

type DockerContainerLifecycleObservation struct {
	ProtocolVersion           string
	State                     string
	EndpointClass             string
	EndpointFingerprint       string
	RequestFingerprint        string
	OwnershipLabelFingerprint string
	ContainerIDFingerprint    string
	ObservationFingerprint    string
	ExitCode                  int
	DaemonReadCount           int
	ContainerPresent          bool
	ConfigurationMatched      bool
	Running                   bool
	OOMKilled                 bool
}

// NewDockerContainerLifecycleObservation builds a validated, content-free
// observation for checkpointing transports and deterministic test doubles.
// Raw daemon container IDs are never accepted; callers provide only the
// already-derived identity fingerprint.
func NewDockerContainerLifecycleObservation(endpoint DockerObservationEndpoint,
	request DockerContainerLifecycleRequest, state, containerIDFingerprint string,
	exitCode, daemonReadCount int,
) (DockerContainerLifecycleObservation, error) {
	if endpoint.Validate() != nil || request.Validate() != nil {
		return DockerContainerLifecycleObservation{},
			errors.New("Docker lifecycle observation authority is invalid")
	}
	present := state != DockerContainerLifecycleStateAbsent
	observation := DockerContainerLifecycleObservation{
		ProtocolVersion: DockerContainerLifecycleObservationProtocolVersion,
		State:           state, EndpointClass: endpoint.Class,
		EndpointFingerprint:       endpoint.Fingerprint,
		RequestFingerprint:        request.RequestFingerprint,
		OwnershipLabelFingerprint: request.Ownership.OwnershipLabelFingerprint,
		ContainerIDFingerprint:    containerIDFingerprint, ExitCode: exitCode,
		DaemonReadCount: daemonReadCount, ContainerPresent: present,
		ConfigurationMatched: present,
		Running:              state == DockerContainerLifecycleStateRunning,
	}
	observation.ObservationFingerprint =
		dockerContainerLifecycleObservationFingerprint(observation)
	if observation.Validate() != nil {
		return DockerContainerLifecycleObservation{},
			errors.New("Docker lifecycle observation is invalid")
	}
	return observation, nil
}

func (observation DockerContainerLifecycleObservation) Validate() error {
	endpoint, endpointErr := NewDockerObservationEndpoint(observation.EndpointClass)
	present := observation.State != DockerContainerLifecycleStateAbsent
	validState := observation.State == DockerContainerLifecycleStateAbsent ||
		observation.State == DockerContainerLifecycleStateCreated ||
		observation.State == DockerContainerLifecycleStateRunning ||
		observation.State == DockerContainerLifecycleStateExited
	if endpointErr != nil || !validDockerContainerLocalEndpoint(endpoint) || !validState ||
		observation.ProtocolVersion != DockerContainerLifecycleObservationProtocolVersion ||
		observation.EndpointFingerprint != endpoint.Fingerprint ||
		!validDigest(observation.RequestFingerprint) ||
		!validDigest(observation.OwnershipLabelFingerprint) ||
		observation.DaemonReadCount < 1 || observation.DaemonReadCount > 4 ||
		observation.ContainerPresent != present || observation.OOMKilled ||
		observation.ConfigurationMatched != present ||
		observation.Running != (observation.State == DockerContainerLifecycleStateRunning) ||
		(present && !validDigest(observation.ContainerIDFingerprint)) ||
		(!present && observation.ContainerIDFingerprint != "") ||
		observation.ExitCode < 0 || observation.ExitCode > 255 ||
		(observation.State != DockerContainerLifecycleStateExited && observation.ExitCode != 0) ||
		!validDigest(observation.ObservationFingerprint) ||
		observation.ObservationFingerprint != dockerContainerLifecycleObservationFingerprint(observation) {
		return errors.New("docker lifecycle observation is invalid")
	}
	return nil
}

func dockerContainerLifecycleObservationFingerprint(observation DockerContainerLifecycleObservation) string {
	return fingerprint(DockerContainerLifecycleObservationProtocolVersion, observation.State,
		observation.EndpointClass, observation.EndpointFingerprint, observation.RequestFingerprint,
		observation.OwnershipLabelFingerprint, observation.ContainerIDFingerprint,
		strconv.Itoa(observation.ExitCode), strconv.Itoa(observation.DaemonReadCount),
		strconv.FormatBool(observation.ContainerPresent),
		strconv.FormatBool(observation.ConfigurationMatched),
		strconv.FormatBool(observation.Running), strconv.FormatBool(observation.OOMKilled))
}

type DockerContainerLifecycleTerminationResult struct {
	Observation        DockerContainerLifecycleObservation
	ExitCode           int
	DaemonReadCount    int
	DaemonWriteCount   int
	AlreadyStopped     bool
	GracefulSignalSent bool
	ForcedSignalSent   bool
}

type DockerContainerLifecycleCleanupResult struct {
	Observation      DockerContainerLifecycleObservation
	DaemonReadCount  int
	DaemonWriteCount int
	AlreadyAbsent    bool
	ContainerRemoved bool
	AbsenceConfirmed bool
}
