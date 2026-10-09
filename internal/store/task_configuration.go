package store

import (
	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/projectconfig"
	"encoding/json"
)

func validateControlledCreationBudget(run domain.Run) error {
	invalid := func() error {
		return apperror.New(apperror.CodeInvalidArgument, "controlled creation budget or project snapshot is inconsistent")
	}
	if run.Config.RequestedBudget == nil {
		if run.Budget != domain.DefaultBudget() {
			return invalid()
		}
		return nil
	}
	requested := *run.Config.RequestedBudget
	if err := domain.ValidateTaskBudget(requested); err != nil {
		return invalid()
	}
	effective := requested
	if len(run.Config.ProjectConfig) != 0 {
		var project projectconfig.Effective
		if err := json.Unmarshal(run.Config.ProjectConfig, &project); err != nil ||
			project.Protocol != projectconfig.ProtocolVersion || project.Fingerprint() != run.Config.ProjectConfigFingerprint ||
			project.MaxTurns > requested.MaxTurns || int64(project.MaxToolCalls) > requested.MaxToolCalls {
			return invalid()
		}
		if project.MaxTurns > 0 {
			effective.MaxTurns = project.MaxTurns
		}
		if project.MaxToolCalls > 0 {
			effective.MaxToolCalls = int64(project.MaxToolCalls)
		}
	} else if run.Config.ProjectConfigFingerprint != "" {
		return invalid()
	}
	if effective != run.Budget {
		return invalid()
	}
	return nil
}
