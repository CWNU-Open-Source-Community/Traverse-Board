package surfacegovernance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRepositoryRegistryAndGeneratedInventoryAreCurrent(t *testing.T) {
	root := repositoryRoot(t)
	registry, err := LoadFile(filepath.Join(root, "docs", "convergence", "surface-registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	generated, err := Render(registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckDocument(filepath.Join(root, "docs", "convergence", "surface-inventory.md"), generated); err != nil {
		t.Fatal(err)
	}
	template, err := os.ReadFile(filepath.Join(root, ".github", "PULL_REQUEST_TEMPLATE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePullRequestTemplate(template); err != nil {
		t.Fatal(err)
	}
}

func TestCheckDocumentRejectsRegistryDocumentationDrift(t *testing.T) {
	path := filepath.Join(t.TempDir(), "surface-inventory.md")
	if err := os.WriteFile(path, []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := CheckDocument(path, []byte("generated\n"))
	if err == nil || !strings.Contains(err.Error(), "surfacecheck -write") {
		t.Fatalf("drift error = %v", err)
	}
}

func TestDowngradeRemovalFixtureMeetsExitGates(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("testdata", "downgrade-removal.json"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var lifecycle Lifecycle
	if err := decoder.Decode(&lifecycle); err != nil {
		t.Fatal(err)
	}
	entry := SurfaceEntry{ID: "fixture", Tier: "maintenance-only", Lifecycle: lifecycle}
	governance := Governance{DefaultDeprecationDays: 90, MinimumTaggedPrereleases: 2}
	var problems []string
	validateLifecycle(entry, governance, "fixture", func(format string, args ...any) {
		problems = append(problems, formatMessage(format, args...))
	})
	if len(problems) != 0 {
		t.Fatalf("valid downgrade/removal fixture rejected: %s", strings.Join(problems, "; "))
	}

	entry.Lifecycle.Transitions[1].RemovalEligibleOn = "2026-03-31"
	problems = nil
	validateLifecycle(entry, governance, "fixture", func(format string, args ...any) {
		problems = append(problems, formatMessage(format, args...))
	})
	if !containsProblem(problems, "must be at least 90") {
		t.Fatalf("short removal window was accepted: %v", problems)
	}
}

func TestRegistryEvolutionRetainsTombstonesAndReviewedHistory(t *testing.T) {
	root := repositoryRoot(t)
	base, err := LoadFile(filepath.Join(root, "docs", "convergence", "surface-registry.json"))
	if err != nil {
		t.Fatal(err)
	}

	current := cloneRegistry(t, base)
	current.Entries = current.Entries[1:]
	if err := ValidateEvolution(base, current); err == nil || !strings.Contains(err.Error(), "removal transition") {
		t.Fatalf("deleted entry evolution error = %v", err)
	}

	current = cloneRegistry(t, base)
	desktop := findEntry(t, current, "desktop")
	current.Entries[desktop].Lifecycle.RegisteredTier = "maintenance-only"
	current.Entries[desktop].Tier = "maintenance-only"
	current.Entries[desktop].CoreReleaseBlocker = false
	current.Entries[desktop].ReleaseBlockingChecks = nil
	if err := ValidateEvolution(base, current); err == nil || !strings.Contains(err.Error(), "rewrote registered_tier") {
		t.Fatalf("rewritten registration evolution error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join("testdata", "downgrade-removal.json"))
	if err != nil {
		t.Fatal(err)
	}
	var lifecycle Lifecycle
	if err := json.Unmarshal(content, &lifecycle); err != nil {
		t.Fatal(err)
	}
	proposed := cloneRegistry(t, base)
	desktop = findEntry(t, proposed, "desktop")
	proposed.Entries[desktop].Tier = "maintenance-only"
	proposed.Entries[desktop].CoreReleaseBlocker = false
	proposed.Entries[desktop].ReleaseBlockingChecks = nil
	proposed.Entries[desktop].Lifecycle = lifecycle
	if err := ValidateEvolution(base, proposed); err != nil {
		t.Fatalf("append-only removal history rejected: %v", err)
	}

	rewritten := cloneRegistry(t, proposed)
	desktop = findEntry(t, rewritten, "desktop")
	rewritten.Entries[desktop].Lifecycle.Transitions[0].Decision = "docs/adr/0999-rewritten-decision.md"
	if err := ValidateEvolution(proposed, rewritten); err == nil ||
		!strings.Contains(err.Error(), "transitions are append-only") {
		t.Fatalf("rewritten transition history error = %v", err)
	}
}

func TestTierRulesFailClosed(t *testing.T) {
	root := repositoryRoot(t)
	registry, err := LoadFile(filepath.Join(root, "docs", "convergence", "surface-registry.json"))
	if err != nil {
		t.Fatal(err)
	}

	active := findEntry(t, registry, "desktop")
	registry.Entries[active].ReleaseBlockingChecks = nil
	if err := Validate(registry); err == nil || !strings.Contains(err.Error(), "release_blocking_checks") {
		t.Fatalf("active entry without release evidence error = %v", err)
	}

	registry, err = LoadFile(filepath.Join(root, "docs", "convergence", "surface-registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	extension := findEntry(t, registry, "mcp")
	registry.Entries[extension].CoreReleaseBlocker = true
	registry.Entries[extension].GoOwnedControls = []string{"policy", "scope"}
	if err := Validate(registry); err == nil ||
		!strings.Contains(err.Error(), "cannot be a core release blocker") ||
		!strings.Contains(err.Error(), "Go-owned approval control") {
		t.Fatalf("unsafe extension error = %v", err)
	}

	registry, err = LoadFile(filepath.Join(root, "docs", "convergence", "surface-registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range registry.Tiers {
		if registry.Tiers[i].ID == "maintenance-only" {
			registry.Tiers[i].AllowedChanges = append(registry.Tiers[i].AllowedChanges, "features")
		}
	}
	if err := Validate(registry); err == nil || !strings.Contains(err.Error(), "want exactly") {
		t.Fatalf("maintenance feature widening error = %v", err)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
}

func findEntry(t *testing.T, registry Registry, id string) int {
	t.Helper()
	for i, entry := range registry.Entries {
		if entry.ID == id {
			return i
		}
	}
	t.Fatalf("entry %q not found", id)
	return -1
}

func cloneRegistry(t *testing.T, registry Registry) Registry {
	t.Helper()
	content, err := json.Marshal(registry)
	if err != nil {
		t.Fatal(err)
	}
	var clone Registry
	if err := json.Unmarshal(content, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func formatMessage(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

func containsProblem(problems []string, fragment string) bool {
	for _, problem := range problems {
		if strings.Contains(problem, fragment) {
			return true
		}
	}
	return false
}
