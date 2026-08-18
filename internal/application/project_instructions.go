package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/projectconfig"
	"cyberagent-workbench/internal/session"
)

type ProjectInstructionStore interface {
	GetRun(context.Context, string) (domain.Run, error)
	GetMission(context.Context, string) (domain.Mission, error)
	GetWorkspaceInfo(context.Context, string) (session.WorkspaceInfo, error)
	GetLatestRunInstructionSnapshot(context.Context, string) (
		projectconfig.RunInstructionSnapshot, bool, error)
	ListRunInstructionSnapshots(context.Context, string, int) (
		[]projectconfig.RunInstructionSnapshot, error)
	ConfirmRunInstructionSnapshot(context.Context, string, string,
		projectconfig.InstructionSnapshot, projectconfig.InstructionSnapshotDiff,
		string, time.Time) (projectconfig.RunInstructionSnapshot, bool, error)
}

type ProjectInstructionService struct {
	store ProjectInstructionStore
}

type ProjectInstructionState struct {
	RunID            string                                 `json:"run_id"`
	WorkspaceID      string                                 `json:"workspace_id"`
	Pinned           projectconfig.RunInstructionSnapshot   `json:"pinned"`
	Live             projectconfig.InstructionSnapshot      `json:"live"`
	Diff             projectconfig.InstructionSnapshotDiff  `json:"diff"`
	History          []projectconfig.RunInstructionSnapshot `json:"history"`
	PinnedPresent    bool                                   `json:"pinned_present"`
	Stale            bool                                   `json:"stale"`
	RefreshConfirmed bool                                   `json:"refresh_confirmed"`
	CapabilityGrant  bool                                   `json:"capability_grant"`
}

func NewProjectInstructionService(store ProjectInstructionStore) *ProjectInstructionService {
	return &ProjectInstructionService{store: store}
}

func (s *ProjectInstructionService) Inspect(ctx context.Context, runID,
	targetPath string,
) (ProjectInstructionState, error) {
	if s == nil || s.store == nil {
		return ProjectInstructionState{}, errors.New("project instruction store is required")
	}
	run, err := s.store.GetRun(ctx, strings.TrimSpace(runID))
	if err != nil {
		return ProjectInstructionState{}, err
	}
	mission, err := s.store.GetMission(ctx, run.MissionID)
	if err != nil {
		return ProjectInstructionState{}, err
	}
	if mission.WorkspaceID == "" {
		return ProjectInstructionState{}, errors.New("Run has no Workspace for project instructions")
	}
	workspace, err := s.store.GetWorkspaceInfo(ctx, mission.WorkspaceID)
	if err != nil {
		return ProjectInstructionState{}, err
	}
	pinned, found, err := s.store.GetLatestRunInstructionSnapshot(ctx, run.ID)
	if err != nil {
		return ProjectInstructionState{}, err
	}
	if !found {
		if len(run.Config.ProjectInstructions) == 0 {
			pinned = projectconfig.RunInstructionSnapshot{RunID: run.ID}
		} else {
			var snapshot projectconfig.InstructionSnapshot
			if err := json.Unmarshal(run.Config.ProjectInstructions, &snapshot); err != nil {
				return ProjectInstructionState{}, err
			}
			pinned = projectconfig.RunInstructionSnapshot{RunID: run.ID, Revision: 1,
				Snapshot: snapshot, ConfirmedBy: "legacy_run_config", CreatedAt: run.CreatedAt,
				Diff: projectconfig.InstructionSnapshotDiff{ToFingerprint: snapshot.Fingerprint}}
		}
	}
	if targetPath == "" {
		targetPath = pinned.Snapshot.TargetPath
	}
	live, err := projectconfig.DiscoverInstructions(ctx, workspace.RootPath, targetPath)
	if err != nil {
		return ProjectInstructionState{}, err
	}
	diff := projectconfig.DiffInstructionSnapshots(pinned.Snapshot, live)
	history, err := s.store.ListRunInstructionSnapshots(ctx, run.ID, 100)
	if err != nil {
		return ProjectInstructionState{}, err
	}
	return ProjectInstructionState{RunID: run.ID, WorkspaceID: mission.WorkspaceID,
		Pinned: pinned, Live: live, Diff: diff, History: history,
		PinnedPresent: found || len(run.Config.ProjectInstructions) > 0,
		Stale:         diff.RequiresConfirmation, CapabilityGrant: false}, nil
}

func (s *ProjectInstructionService) Refresh(ctx context.Context, runID,
	targetPath, expectedFingerprint, expectedLiveFingerprint, requestedBy string, confirm bool,
) (ProjectInstructionState, error) {
	state, err := s.Inspect(ctx, runID, targetPath)
	if err != nil {
		return ProjectInstructionState{}, err
	}
	if expectedFingerprint != state.Pinned.Snapshot.Fingerprint {
		return ProjectInstructionState{}, apperror.New(apperror.CodeFailedPrecondition,
			"project instruction refresh pinned fingerprint is stale")
	}
	if !state.Stale || !confirm {
		return state, nil
	}
	if strings.TrimSpace(expectedLiveFingerprint) != state.Live.Fingerprint {
		return ProjectInstructionState{}, apperror.New(apperror.CodeFailedPrecondition,
			"project instruction refresh live fingerprint changed after review")
	}
	if err := contextmgr.ValidateMemoryActor(requestedBy); err != nil {
		return ProjectInstructionState{}, err
	}
	record, changed, err := s.store.ConfirmRunInstructionSnapshot(ctx, state.RunID,
		expectedFingerprint, state.Live, state.Diff, requestedBy, time.Now().UTC())
	if err != nil {
		return ProjectInstructionState{}, err
	}
	if !changed {
		return state, nil
	}
	state.Pinned = record
	state.PinnedPresent = true
	state.Diff = projectconfig.DiffInstructionSnapshots(record.Snapshot, state.Live)
	state.Stale = state.Diff.RequiresConfirmation
	state.RefreshConfirmed = true
	state.History, err = s.store.ListRunInstructionSnapshots(ctx, state.RunID, 100)
	return state, err
}
