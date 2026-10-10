package projectconfig

import (
	"context"
	"cyberagent-workbench/internal/domain"
)

// ResolveWorkspace is shared by operator entries. It never executes command
// IDs or installs suggested Skills. A rejection must block the whole creation.
func ResolveWorkspace(ctx context.Context, root string, profile domain.Profile,
	budget domain.Budget, registeredCommands map[string]struct{},
) (*Effective, []Rejection, error) {
	config, found, err := LoadWorkspace(ctx, root)
	if err != nil || !found {
		return nil, nil, err
	}
	effective, rejected, err := config.Narrow(Ceiling{
		AllowedProfiles: []string{"code", "learn", "review", "script"},
		MaxTurns:        budget.MaxTurns, MaxToolCalls: int(budget.MaxToolCalls),
		RegisteredCommands: registeredCommands,
	})
	if err != nil {
		return nil, nil, err
	}
	if len(effective.AllowedProfiles) > 0 {
		allowed := false
		for _, value := range effective.AllowedProfiles {
			if value == string(profile) {
				allowed = true
			}
		}
		if !allowed {
			rejected = append(rejected, Rejection{Field: "allowed_profiles", Reason: "selected profile is excluded by project configuration"})
		}
	}
	if effective.ReadOnly && profile != domain.ProfileReview && profile != domain.ProfileLearn {
		rejected = append(rejected, Rejection{Field: "read_only", Reason: "read-only project requires review or learn profile"})
	}
	return &effective, rejected, nil
}
