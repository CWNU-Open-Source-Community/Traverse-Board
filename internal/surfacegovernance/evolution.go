package surfacegovernance

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// ValidateEvolution compares a reviewed base registry with a proposed registry.
// Existing entries are append-only governance records: an entry can transition to
// removed, but it cannot disappear, rewrite its registration decision, or rewrite
// already reviewed transition history.
func ValidateEvolution(previous, current Registry) error {
	if err := Validate(previous); err != nil {
		return fmt.Errorf("base %w", err)
	}
	if err := Validate(current); err != nil {
		return err
	}

	currentEntries := make(map[string]SurfaceEntry, len(current.Entries))
	for _, entry := range current.Entries {
		currentEntries[entry.ID] = entry
	}

	var problems []string
	for _, oldEntry := range previous.Entries {
		newEntry, exists := currentEntries[oldEntry.ID]
		if !exists {
			problems = append(problems, fmt.Sprintf(
				"entry %q disappeared; retain it with a reviewed removal transition and removed tombstone",
				oldEntry.ID))
			continue
		}
		if newEntry.Lifecycle.RegisteredTier != oldEntry.Lifecycle.RegisteredTier {
			problems = append(problems, fmt.Sprintf(
				"entry %q rewrote registered_tier from %q to %q",
				oldEntry.ID, oldEntry.Lifecycle.RegisteredTier, newEntry.Lifecycle.RegisteredTier))
		}
		if newEntry.Lifecycle.RegisteredBy != oldEntry.Lifecycle.RegisteredBy {
			problems = append(problems, fmt.Sprintf(
				"entry %q rewrote registered_by from %q to %q",
				oldEntry.ID, oldEntry.Lifecycle.RegisteredBy, newEntry.Lifecycle.RegisteredBy))
		}
		if oldEntry.Lifecycle.Status == "removed" && newEntry.Lifecycle.Status != "removed" {
			problems = append(problems, fmt.Sprintf(
				"entry %q resurrected a removed Surface without a new registry identity and entry review",
				oldEntry.ID))
		}
		oldTransitions := oldEntry.Lifecycle.Transitions
		newTransitions := newEntry.Lifecycle.Transitions
		if len(newTransitions) < len(oldTransitions) {
			problems = append(problems, fmt.Sprintf(
				"entry %q deleted %d reviewed lifecycle transition(s)",
				oldEntry.ID, len(oldTransitions)-len(newTransitions)))
			continue
		}
		for i := range oldTransitions {
			if !reflect.DeepEqual(oldTransitions[i], newTransitions[i]) {
				problems = append(problems, fmt.Sprintf(
					"entry %q rewrote reviewed lifecycle transition %d; transitions are append-only",
					oldEntry.ID, i))
			}
		}
	}
	if len(problems) != 0 {
		sort.Strings(problems)
		return fmt.Errorf("Surface registry evolution is invalid:\n- %s",
			strings.Join(problems, "\n- "))
	}
	return nil
}
