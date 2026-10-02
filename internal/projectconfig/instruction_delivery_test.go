package projectconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInstructionDeliveryPinsClassificationsAndRejectsStaleSources(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "module"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"AGENTS.md": "old root rule\n", "module/AGENTS.md": "current module rule\n"} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	legacy, err := DiscoverInstructions(t.Context(), root, "module/file.go")
	if err != nil {
		t.Fatal(err)
	}
	classes := []InstructionSourceDelivery{
		{Path: legacy.Sources[0].Path, ContentSHA256: legacy.Sources[0].ContentSHA256, Requirement: InstructionExcluded, ExclusionReason: "superseded"},
		{Path: legacy.Sources[1].Path, ContentSHA256: legacy.Sources[1].ContentSHA256, Requirement: InstructionMandatory},
	}
	pinned, err := ClassifyInstructionSnapshot(legacy, classes)
	if err != nil {
		t.Fatal(err)
	}
	if pinned.Fingerprint == legacy.Fingerprint || pinned.Sources[1].Precedence <= pinned.Sources[0].Precedence {
		t.Fatal("classification lost its content/precedence binding")
	}
	if diff := DiffInstructionSnapshots(legacy, pinned); !diff.RequiresConfirmation || len(diff.Changed) != 2 {
		t.Fatal("classification changes were absent from the confirmation diff")
	}
	for _, source := range pinned.Sources {
		if source.Authority != workflowOnlyInstructionAuthority() {
			t.Fatal("classification granted authority")
		}
	}
	raw, _ := json.Marshal(pinned)
	var restored InstructionSnapshot
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if err := restored.Validate(); err != nil {
		t.Fatal(err)
	}
	// A legacy reader ignores the new field; its unchanged fingerprint algorithm
	// cannot validate a record whose effective classification it cannot enforce.
	restored.Delivery = nil
	if restored.Validate() == nil {
		t.Fatal("classified record was readable after stripping its contract")
	}
	live, _ := DiscoverInstructions(t.Context(), root, "module/file.go")
	if matched := MatchLiveInstructionDelivery(pinned, live); matched.Fingerprint != pinned.Fingerprint {
		t.Fatal("unchanged disk content lost its confirmed classifications")
	}
	if err := os.WriteFile(filepath.Join(root, "module", "AGENTS.md"), []byte("unconfirmed replacement\n"), 0600); err != nil {
		t.Fatal(err)
	}
	live, err = DiscoverInstructions(t.Context(), root, "module/file.go")
	if err != nil {
		t.Fatal(err)
	}
	if MatchLiveInstructionDelivery(pinned, live).Delivery != nil {
		t.Fatal("disk change acquired old classification")
	}
	if _, err := ClassifyInstructionSnapshot(live, classes); err == nil {
		t.Fatal("stale source hash was accepted")
	}
	if err := pinned.Validate(); err != nil {
		t.Fatalf("disk edit mutated pinned snapshot: %v", err)
	}
}

func TestInstructionDeliveryRequiresCompleteExplicitClassification(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("rule\n"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := DiscoverInstructions(t.Context(), root, ".")
	if err != nil {
		t.Fatal(err)
	}
	source := snapshot.Sources[0]
	valid := InstructionSourceDelivery{Path: source.Path, ContentSHA256: source.ContentSHA256, Requirement: InstructionOptional}
	for _, tc := range []struct {
		name  string
		items []InstructionSourceDelivery
	}{
		{"missing", nil},
		{"duplicate", []InstructionSourceDelivery{valid, valid}},
		{"unknown", []InstructionSourceDelivery{{Path: "other.md", ContentSHA256: source.ContentSHA256, Requirement: InstructionMandatory}}},
		{"inferred", []InstructionSourceDelivery{{Path: source.Path, ContentSHA256: source.ContentSHA256, Requirement: "auto"}}},
		{"unexplained exclusion", []InstructionSourceDelivery{{Path: source.Path, ContentSHA256: source.ContentSHA256, Requirement: InstructionExcluded}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ClassifyInstructionSnapshot(snapshot, tc.items); err == nil {
				t.Fatal("invalid classification accepted")
			}
		})
	}
	if snapshot.Delivery != nil || snapshot.Validate() != nil {
		t.Fatal("classification changed legacy input")
	}
}

func TestInstructionDeliveryEmptySourceSetRetainsPinnedFingerprint(t *testing.T) {
	root := t.TempDir()
	legacy, err := DiscoverInstructions(t.Context(), root, ".")
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := ClassifyInstructionSnapshot(legacy, []InstructionSourceDelivery{})
	if err != nil {
		t.Fatal(err)
	}
	live, err := DiscoverInstructions(t.Context(), root, ".")
	if err != nil {
		t.Fatal(err)
	}
	matched := MatchLiveInstructionDelivery(pinned, live)
	if err := matched.Validate(); err != nil || matched.Fingerprint != pinned.Fingerprint {
		t.Fatalf("empty classification changed its pinned fingerprint: %v", err)
	}
}
