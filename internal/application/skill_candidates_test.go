package application

import (
	"context"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/skills"
	"cyberagent-workbench/internal/toolgateway"
)

type skillCandidateMutationStoreStub struct {
	run       domain.Run
	mode      domain.RunModeSnapshot
	selection skills.Selection
	found     bool
	created   *skills.SkillCandidate
}

func (s *skillCandidateMutationStoreStub) GetRun(context.Context, string) (domain.Run, error) {
	return s.run, nil
}

func (s *skillCandidateMutationStoreStub) GetRunMode(context.Context, string) (
	domain.RunModeSnapshot, error,
) {
	return s.mode, nil
}

func (s *skillCandidateMutationStoreStub) GetSkillSelectionByRun(context.Context,
	string,
) (skills.Selection, bool, error) {
	return s.selection, s.found, nil
}

func (s *skillCandidateMutationStoreStub) CreateSkillCandidate(_ context.Context,
	value skills.SkillCandidate,
) (skills.SkillCandidate, bool, error) {
	s.created = &value
	return value, false, nil
}

func TestSkillCandidateToolRequiresExplicitGeneratorSelection(t *testing.T) {
	now := time.Now().UTC()
	store := &skillCandidateMutationStoreStub{
		run: domain.Run{ID: "run-candidate", MissionID: "mission-candidate",
			SessionID: "session-candidate", Status: domain.RunRunning,
			Config: domain.RunConfig{ModelRoute: "test"},
			Budget: domain.Budget{MaxTurns: 4}, StartedAt: &now,
			CreatedAt: now, UpdatedAt: now},
		mode: domain.RunModeSnapshot{Surface: domain.ExecutionSurfaceCode,
			Phase: domain.ExecutionPhaseDeliver},
	}
	executor := NewSkillCandidateToolExecutor(store)
	scope := toolgateway.SkillCandidateContext{
		InvocationID: "candidate-invocation", OperationKey: "candidate-operation-0001",
		RunID: store.run.ID, RootAgentID: "agent-root", SessionID: store.run.SessionID,
		WorkspaceID: "workspace-candidate", LeaseID: "lease-candidate",
		LeaseGeneration: 1, RequestedBy: "run_supervisor",
		PolicyDecision: toolgateway.Decision{Allowed: true,
			Approval: toolgateway.ApprovalAutomatic, Risk: "low", Reason: "allowed"},
	}
	spec := toolgateway.SkillCandidateSpec{
		Version: toolgateway.SkillCandidateProposalVersion,
		Name:    "generated-helper", SkillVersion: "1.0.0",
		Description:   "A reusable bounded generated workflow.",
		Profiles:      []domain.Profile{domain.ProfileCode},
		Surfaces:      []domain.ExecutionSurface{domain.ExecutionSurfaceCode},
		Phases:        []domain.ExecutionPhase{domain.ExecutionPhaseDeliver},
		Roles:         []domain.AgentRole{domain.AgentRoleRoot},
		UserInvocable: true, ExplicitOnly: true,
		ToolDependencies: []toolgateway.ToolName{toolgateway.ReadFileTool},
		Content:          "# Generated helper\n\nRead the target and report verified facts.\n",
	}
	if _, err := executor.ProposeSkillCandidate(t.Context(), scope, spec); err == nil {
		t.Fatal("candidate proposal succeeded without explicit generator selection")
	}
	store.found = true
	store.selection.Items = []skills.SelectionItem{{Name: runSkillGeneratorName}}
	result, err := executor.ProposeSkillCandidate(t.Context(), scope, spec)
	if err != nil || store.created == nil || result.Status != "proposed" ||
		result.CandidateFingerprint == "" || store.created.Manifest.Name != spec.Name {
		t.Fatalf("candidate result=%#v stored=%#v err=%v", result, store.created, err)
	}
}
