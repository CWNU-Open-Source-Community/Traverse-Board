package sandbox

import (
	"testing"
	"time"
)

func TestDockerContainerLifecycleLeaseFencesFullIdentityAndRenewal(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	intent := DockerContainerLaunchIntent{ID: "lifecycle-intent", AttemptID: "lifecycle-attempt",
		PlanID: "plan", RunID: "run", MissionID: "mission", WorkspaceID: "workspace",
		ProtocolVersion: DockerContainerLaunchIntentProtocolVersion, ResourceGeneration: 1,
		OperationKeyDigest: lifecycleTestDigest("operation"), RequestFingerprint: lifecycleTestDigest("request"),
		SpecFingerprint: lifecycleTestDigest("spec"), PlanFingerprint: lifecycleTestDigest("plan"),
		AuthorityFingerprint:     lifecycleTestDigest("authority"),
		BaseLabelPlanFingerprint: lifecycleTestDigest("labels"),
		ContainerNameFingerprint: lifecycleTestDigest("name"), EndpointClass: DockerObservationEndpointLocalUnix,
		EndpointFingerprint: lifecycleTestDigest("wrong"), RequestedBy: "requester", CreatedAt: now}
	endpoint, err := NewDockerObservationEndpoint(DockerObservationEndpointLocalUnix)
	if err != nil {
		t.Fatal(err)
	}
	intent.EndpointFingerprint = endpoint.Fingerprint
	intent.IntentFingerprint = dockerContainerLaunchIntentFingerprint(intent)
	ownership, err := intent.LifecycleOwnership()
	if err != nil {
		t.Fatal(err)
	}
	intent.OwnershipLabelFingerprint = ownership.OwnershipLabelFingerprint
	if err := intent.Validate(); err != nil {
		t.Fatal(err)
	}
	lease, err := NewDockerContainerLifecycleLease(intent, "lease", "owner", 1, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !lease.Fences(lease, now.Add(time.Second)) {
		t.Fatal("exact active lifecycle lease did not fence")
	}
	stale := lease
	stale.Generation++
	if lease.Fences(stale, now.Add(time.Second)) {
		t.Fatal("changed generation passed lifecycle fence")
	}
	renewed, err := lease.Renew(now.Add(10*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.Fences(lease, now.Add(11*time.Second)) || !renewed.Fences(renewed, now.Add(11*time.Second)) {
		t.Fatal("renewal did not replace the previous fencing token")
	}
}

func TestDockerContainerLifecycleRecordRejectsSkippedAndPostCleanedTransitions(t *testing.T) {
	intent, lease := testDockerContainerLifecycleIntentAndLease(t)
	now := lease.AcquiredAt.Add(time.Second)
	started, err := NewDockerContainerLifecycleTransition(intent.ID, 1, lease,
		DockerContainerLifecycleTransitionStarted, DockerContainerLifecycleReasonStarted,
		nil, lifecycleTestDigest("container"), "", now)
	if err != nil {
		t.Fatal(err)
	}
	record := DockerContainerLifecycleRecord{Intent: intent, Lease: lease,
		Transitions: []DockerContainerLifecycleTransition{started}}
	if record.Validate() == nil {
		t.Fatal("started transition without created was accepted")
	}

	cleaning, err := NewDockerContainerLifecycleTransition(intent.ID, 1, lease,
		DockerContainerLifecycleTransitionCleaning, DockerContainerLifecycleReasonRestartRecovery,
		nil, "", "", now)
	if err != nil {
		t.Fatal(err)
	}
	cleaned, err := NewDockerContainerLifecycleTransition(intent.ID, 2, lease,
		DockerContainerLifecycleTransitionCleaned, DockerContainerLifecycleReasonCleanupCompleted,
		nil, "", cleaning.TransitionFingerprint, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	failed, err := NewDockerContainerLifecycleTransition(intent.ID, 3, lease,
		DockerContainerLifecycleTransitionFailed, DockerContainerLifecycleReasonCleanupFailed,
		nil, "", cleaned.TransitionFingerprint, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	record.Transitions = []DockerContainerLifecycleTransition{cleaning, cleaned, failed}
	if record.Validate() == nil {
		t.Fatal("transition after cleaned was accepted")
	}
}

func TestDockerContainerLifecycleTransitionRejectsReasonFromAnotherState(t *testing.T) {
	intent, lease := testDockerContainerLifecycleIntentAndLease(t)
	_, err := NewDockerContainerLifecycleTransition(intent.ID, 1, lease,
		DockerContainerLifecycleTransitionCreated, DockerContainerLifecycleReasonStarted,
		nil, lifecycleTestDigest("container"), "", lease.AcquiredAt.Add(time.Second))
	if err == nil {
		t.Fatal("created transition accepted a started reason")
	}
}

func testDockerContainerLifecycleIntentAndLease(t *testing.T) (DockerContainerLaunchIntent,
	DockerContainerLifecycleLease,
) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	endpoint, err := NewDockerObservationEndpoint(DockerObservationEndpointLocalUnix)
	if err != nil {
		t.Fatal(err)
	}
	intent := DockerContainerLaunchIntent{ID: "lifecycle-intent", AttemptID: "lifecycle-attempt",
		PlanID: "plan", RunID: "run", MissionID: "mission", WorkspaceID: "workspace",
		ProtocolVersion: DockerContainerLaunchIntentProtocolVersion, ResourceGeneration: 1,
		OperationKeyDigest: lifecycleTestDigest("operation"), RequestFingerprint: lifecycleTestDigest("request"),
		SpecFingerprint: lifecycleTestDigest("spec"), PlanFingerprint: lifecycleTestDigest("plan"),
		AuthorityFingerprint:     lifecycleTestDigest("authority"),
		BaseLabelPlanFingerprint: lifecycleTestDigest("labels"),
		ContainerNameFingerprint: lifecycleTestDigest("name"), EndpointClass: endpoint.Class,
		EndpointFingerprint: endpoint.Fingerprint, RequestedBy: "requester", CreatedAt: now}
	intent.IntentFingerprint = dockerContainerLaunchIntentFingerprint(intent)
	ownership, err := intent.LifecycleOwnership()
	if err != nil {
		t.Fatal(err)
	}
	intent.OwnershipLabelFingerprint = ownership.OwnershipLabelFingerprint
	if err := intent.Validate(); err != nil {
		t.Fatal(err)
	}
	lease, err := NewDockerContainerLifecycleLease(intent, "lease", "owner", 1, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return intent, lease
}

func lifecycleTestDigest(value string) string {
	return fingerprint("docker_lifecycle_test.v1", value)
}
