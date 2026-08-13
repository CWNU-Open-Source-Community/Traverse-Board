package desktop

import (
	"context"
	"time"

	"cyberagent-workbench/internal/apperror"
)

const WorkspaceImportProtocolVersion = "desktop_workspace_import.v1"

type WorkspaceImportStatus string

const (
	WorkspaceImportRegistered WorkspaceImportStatus = "registered"
	WorkspaceImportCancelled  WorkspaceImportStatus = "cancelled"
)

// WorkspaceImportSummary is the only Workspace projection returned to the
// renderer. The selected host path remains inside the Go control plane.
type WorkspaceImportSummary struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type WorkspaceImportResult struct {
	ProtocolVersion            string                  `json:"protocol_version"`
	Status                     WorkspaceImportStatus   `json:"status"`
	Workspace                  *WorkspaceImportSummary `json:"workspace"`
	RootPathExposed            bool                    `json:"root_path_exposed"`
	RendererPathInputSupported bool                    `json:"renderer_path_input_supported"`
	DirectoryContentModified   bool                    `json:"directory_content_modified"`
	AgentAuthorityGranted      bool                    `json:"agent_authority_granted"`
}

type WorkspaceDirectoryPicker interface {
	OpenWorkspaceDirectory(context.Context) (string, error)
}

type WorkspaceDirectoryRegistrar interface {
	RegisterWorkspaceDirectory(context.Context, string) (WorkspaceImportSummary, error)
}

// ImportWorkspace opens one native directory picker and registers the chosen
// existing directory. It accepts no renderer-controlled path.
func (b *DesktopBridge) ImportWorkspace() (WorkspaceImportResult, error) {
	if b == nil || !b.bootstrap.WorkspaceImportEnabled ||
		b.workspaceDirectoryPicker == nil || b.workspaceRegistrar == nil {
		return WorkspaceImportResult{}, apperror.New(apperror.CodeNotFound,
			"desktop workspace import is disabled")
	}
	if !b.dialogActive.CompareAndSwap(false, true) {
		return WorkspaceImportResult{}, apperror.New(apperror.CodeResourceExhausted,
			"a desktop file dialog is already active")
	}
	defer b.dialogActive.Store(false)

	ctx, err := b.lifecycleContext()
	if err != nil {
		return WorkspaceImportResult{}, err
	}
	selectedPath, err := b.workspaceDirectoryPicker.OpenWorkspaceDirectory(ctx)
	if err != nil {
		return WorkspaceImportResult{}, apperror.New(apperror.CodeUnavailable,
			"native workspace directory dialog failed")
	}
	if err := ctx.Err(); err != nil {
		return WorkspaceImportResult{}, apperror.Normalize(err)
	}
	if selectedPath == "" {
		return workspaceImportResult(WorkspaceImportCancelled, nil), nil
	}
	registered, err := b.workspaceRegistrar.RegisterWorkspaceDirectory(ctx, selectedPath)
	if err != nil {
		code := apperror.CodeOf(apperror.Normalize(err))
		if code == apperror.CodeInvalidArgument {
			return WorkspaceImportResult{}, apperror.New(code,
				"selected workspace directory was rejected")
		}
		return WorkspaceImportResult{}, apperror.New(apperror.CodeUnavailable,
			"workspace directory registration failed")
	}
	if !validWorkspaceIdentity(registered.ID) ||
		!validNormalizedText(registered.Name, 128) || registered.CreatedAt.IsZero() {
		return WorkspaceImportResult{}, apperror.New(apperror.CodeInternal,
			"registered workspace projection is invalid")
	}
	return workspaceImportResult(WorkspaceImportRegistered, &registered), nil
}

func workspaceImportResult(status WorkspaceImportStatus,
	workspace *WorkspaceImportSummary) WorkspaceImportResult {
	return WorkspaceImportResult{
		ProtocolVersion: WorkspaceImportProtocolVersion,
		Status:          status, Workspace: workspace,
		RootPathExposed: false, RendererPathInputSupported: false,
		DirectoryContentModified: false, AgentAuthorityGranted: false,
	}
}
