package application

import (
	"context"
	"strings"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/fileedit"
	"cyberagent-workbench/internal/redact"
)

// threadActivityFileEditPreviewStore is deliberately optional so legacy and
// diagnostic stores can still project old activity rows. Production SQLite
// implements it and therefore advertises a diff only after the durable edit is
// found and rebound to the exact Run Session and Mission Workspace.
type threadActivityFileEditPreviewStore interface {
	GetFileEditPreview(context.Context, string) (fileedit.Preview, error)
}

func (s *ThreadActivityDetailService) enrichThreadActivityFileEdit(ctx context.Context,
	run domain.Run, detail *ThreadActivityFileEditDetail,
) error {
	if detail == nil || detail.EditID == "" {
		return nil
	}
	previews, ok := s.store.(threadActivityFileEditPreviewStore)
	if !ok {
		return nil
	}
	preview, err := previews.GetFileEditPreview(ctx, detail.EditID)
	if err != nil {
		normalized := apperror.Normalize(err)
		if apperror.CodeOf(normalized) == apperror.CodeNotFound {
			return nil
		}
		return normalized
	}
	mission, err := s.store.GetMission(ctx, run.MissionID)
	if err != nil {
		return apperror.Normalize(err)
	}
	if preview.ID != detail.EditID || preview.SessionID != run.SessionID ||
		preview.WorkspaceID != mission.WorkspaceID {
		return apperror.New(apperror.CodeFailedPrecondition,
			"durable Thread file-edit diff has an inconsistent Run binding")
	}
	if preview.Diff == "" {
		return nil
	}
	if !utf8.ValidString(preview.Diff) || len([]byte(preview.Diff)) > fileedit.MaxDiffBytes ||
		redact.String(preview.Diff) != preview.Diff {
		return apperror.New(apperror.CodeFailedPrecondition,
			"durable Thread file-edit diff is not a safe bounded projection")
	}

	detail.DiffAvailable = true
	if action := safeThreadActivityFactIdentity(preview.Operation); action != "" {
		detail.Action = action
	}
	if path := safeThreadActivityPath(preview.Path); path != "" {
		detail.Path = path
	}
	detail.DestinationPath = safeThreadActivityPath(preview.DestinationPath)
	if status := safeThreadActivityFactIdentity(preview.Status); status != "" {
		detail.ApplyStatus = status
	}
	detail.Applied = detail.Applied || preview.Status == fileedit.StatusApplied
	detail.Diff = safeThreadActivityDiffSummary(preview.Diff, detail.Action,
		detail.Path, detail.DestinationPath)
	if strings.TrimSpace(detail.Diff.Summary) == "" {
		detail.Diff.Summary = threadActivityEditSummary(detail.Action, detail.Path,
			detail.DestinationPath, detail.ApplyStatus)
	}
	return nil
}
