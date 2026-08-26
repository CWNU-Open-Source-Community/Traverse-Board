package domain

import (
	"strings"
	"testing"
)

func TestDurableOperationPilotReplayDomainsRemainExplicitAndDistinct(t *testing.T) {
	runIdentity, err := (RunCreationOperation{
		ProtocolVersion: RunCreationProtocolVersion,
		KeyDigest:       strings.Repeat("a", 64), RequestFingerprint: strings.Repeat("b", 64),
	}).ReplayIdentity()
	if err != nil || runIdentity.DomainSeparator() != "run_creation_operation.v1" {
		t.Fatalf("Run creation replay domain=%q err=%v",
			runIdentity.DomainSeparator(), err)
	}
	scheduledIdentity, err := (ScheduledJobOperation{
		ProtocolVersion: ScheduledJobControlProtocolVersion,
		KeyDigest:       strings.Repeat("a", 64), RequestFingerprint: strings.Repeat("b", 64),
	}).ReplayIdentity()
	if err != nil || scheduledIdentity.DomainSeparator() != "scheduled_job_operation.v1" {
		t.Fatalf("scheduled job replay domain=%q err=%v",
			scheduledIdentity.DomainSeparator(), err)
	}
	if runIdentity.DomainSeparator() == scheduledIdentity.DomainSeparator() {
		t.Fatal("pilot domains collapsed")
	}
	if _, err := (RunCreationOperation{
		ProtocolVersion: "", KeyDigest: strings.Repeat("a", 64),
		RequestFingerprint: strings.Repeat("b", 64),
	}).ReplayIdentity(); err == nil {
		t.Fatal("missing Run creation protocol was accepted")
	}
	if _, err := (ScheduledJobOperation{
		ProtocolVersion: "", KeyDigest: strings.Repeat("a", 64),
		RequestFingerprint: strings.Repeat("b", 64),
	}).ReplayIdentity(); err == nil {
		t.Fatal("missing scheduled job protocol was accepted")
	}
}
