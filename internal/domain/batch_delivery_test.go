package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBatchDeliverySpecRejectsCyclesOverlapAndUnknownFields(t *testing.T) {
	valid := batchDeliveryDomainSpecFixture()
	if normalized, err := NormalizeBatchDeliverySpec(valid); err != nil || normalized.Validate() != nil {
		t.Fatalf("valid spec=%#v err=%v", normalized, err)
	}

	cycle := batchDeliveryDomainSpecFixture()
	cycle.Tasks[0].DependencyOrdinals = []int{2}
	if _, err := NormalizeBatchDeliverySpec(cycle); err == nil ||
		!strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle error=%v", err)
	}

	overlap := batchDeliveryDomainSpecFixture()
	overlap.Tasks[1].OwnershipHints[0].Path = "internal/one/nested.go"
	overlap.Tasks[1].OwnershipHints[0].Kind = BatchDeliveryOwnershipFile
	if _, err := NormalizeBatchDeliverySpec(overlap); err == nil ||
		!strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("overlap error=%v", err)
	}

	encoded, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	encoded = []byte(strings.Replace(string(encoded), `"version":"batch-delivery.v1"`,
		`"version":"batch-delivery.v1","unexpected":true`, 1))
	if _, err := DecodeBatchDeliverySpec(encoded); err == nil {
		t.Fatal("unknown batch delivery field was accepted")
	}
}

func TestBatchDeliveryToolProfileIsClosedAndOwnershipIsExact(t *testing.T) {
	profile := DefaultBatchDeliveryToolProfile()
	if err := profile.Validate(); err != nil || profile.Network || profile.Credentials ||
		profile.DebugTerminal || profile.WorkspaceDelete || profile.SpawnChildren ||
		profile.Shell || profile.Process {
		t.Fatalf("default profile widened authority: %#v err=%v", profile, err)
	}
	widened := profile
	widened.Network = true
	if widened.Validate() == nil || widened.Fingerprint() == profile.Fingerprint() {
		t.Fatal("widened child tool profile was accepted")
	}
	hints := []BatchDeliveryOwnershipHint{
		{Path: "internal/owned", Kind: BatchDeliveryOwnershipDirectory},
		{Path: "README.md", Kind: BatchDeliveryOwnershipFile},
	}
	for path, want := range map[string]bool{
		"internal/owned/file.go":     true,
		"internal/ownedness/file.go": false,
		"README.md":                  true,
		"README.md/child":            false,
		"../README.md":               false,
	} {
		if got := BatchOwnershipAllows(hints, path); got != want {
			t.Fatalf("ownership %q=%t want=%t", path, got, want)
		}
	}
}

func batchDeliveryDomainSpecFixture() BatchDeliverySpec {
	return BatchDeliverySpec{Version: BatchDeliveryProtocolVersion,
		Tasks: []BatchDeliveryTaskSpec{
			{Ordinal: 1, OwnershipHints: []BatchDeliveryOwnershipHint{{
				Path: "internal/one", Kind: BatchDeliveryOwnershipDirectory}},
				Budget: BatchDeliveryBudget{TurnLimit: 2, TokenLimit: 128, TimeoutMillis: 60_000},
				Validations: []BatchDeliveryValidationRequirement{{
					ID: "diff-one", Kind: BatchValidationGitDiffCheck, Scope: "."}}},
			{Ordinal: 2, OwnershipHints: []BatchDeliveryOwnershipHint{{
				Path: "internal/two", Kind: BatchDeliveryOwnershipDirectory}},
				DependencyOrdinals: []int{1},
				Budget:             BatchDeliveryBudget{TurnLimit: 2, TokenLimit: 128, TimeoutMillis: 60_000},
				Validations: []BatchDeliveryValidationRequirement{{
					ID: "diff-two", Kind: BatchValidationGitDiffCheck, Scope: "."}}},
		}, Contract: BatchDeliveryContract{RequireClean: true,
			RequireIndependentReview: true, RequireAllValidations: true,
			MaxChangedFiles: 32, MaxDiffBytes: 1024 * 1024}}
}
