package application

import (
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/llm"
)

func TestModelStreamPublicPreviewForcesFinalScanBelowCadence(t *testing.T) {
	registry := newActiveCallRegistry(1)
	key := activeCallKey{runID: "run-preview", attemptID: "attempt-preview", modelAttempt: 1}
	now := time.Now().UTC()
	entry := &activeCallEntry{
		key:     key,
		started: true,
		info: ActiveCallInfo{
			RunID: "run-preview", SessionID: "session-preview", AttemptID: "attempt-preview",
			ModelAttempt: 1, TransportAttempt: 1, MaxAttempts: 1,
			Provider: "provider", Model: "model", StartedAt: now,
		},
		publicRevision:  1,
		publicUpdatedAt: now,
		subscribers:     map[uint64]*activeCallSubscriber{},
	}
	registry.calls[key.runID] = entry
	aggregator := &modelStreamAggregator{
		ref:     llm.ModelRef{Provider: "provider", Model: "model"},
		live:    &activeCallLease{registry: registry, key: key, entry: entry},
		preview: newRootMessagePreviewer(nil),
	}

	message := strings.Repeat("safe-", 24)
	first := `{"version":"root_lifecycle.v1","action":"continue","message":"` + message
	if err := aggregator.appendText(first); err != nil {
		t.Fatal(err)
	}
	initial, found := registry.LookupPublic(key.runID)
	if !found || initial.Text == "" || initial.MessageComplete {
		t.Fatalf("expected a safe incomplete preview after the cadence boundary: %#v", initial)
	}

	tail := `-final"}`
	if len(tail) >= publicPreviewScanIntervalBytes {
		t.Fatalf("test tail unexpectedly reached the scan cadence: %d", len(tail))
	}
	if err := aggregator.appendText(tail); err != nil {
		t.Fatal(err)
	}
	deferred, found := registry.LookupPublic(key.runID)
	if !found || deferred.Revision != initial.Revision || deferred.Text != initial.Text || deferred.MessageComplete {
		t.Fatalf("sub-cadence tail should remain deferred until the final scan: %#v", deferred)
	}

	if err := aggregator.publishPublicPreview(true); err != nil {
		t.Fatal(err)
	}
	final, found := registry.LookupPublic(key.runID)
	if !found || final.Text != message+"-final" || !final.MessageComplete || final.Revision <= deferred.Revision {
		t.Fatalf("forced final scan did not publish the complete safe message: %#v", final)
	}
}
