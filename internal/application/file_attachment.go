package application

import (
	"context"
	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
)

type FileAttachmentStore interface {
	SaveWorkspaceFileAttachment(context.Context, string, string, string, string, []byte) (domain.WorkspaceFileAttachment, error)
}

type FileAttachmentImportRequest struct {
	WorkspaceID  string
	OperationKey string
	Name         string
	MIMEType     string
	Data         []byte
}

type FileAttachmentService struct{ store FileAttachmentStore }

func NewFileAttachmentService(store FileAttachmentStore) *FileAttachmentService {
	return &FileAttachmentService{store: store}
}
func (s *FileAttachmentService) Import(ctx context.Context, request FileAttachmentImportRequest) (domain.WorkspaceFileAttachment, error) {
	if s == nil || s.store == nil {
		return domain.WorkspaceFileAttachment{}, apperror.New(apperror.CodeFailedPrecondition, "File attachment import is unavailable")
	}
	return s.store.SaveWorkspaceFileAttachment(ctx, request.WorkspaceID, request.OperationKey, request.MIMEType, request.Name, request.Data)
}

func (s *FileAttachmentService) Inspect(ctx context.Context, workspaceID, key string) (domain.FileAttachmentObservation, error) {
	if s == nil || s.store == nil {
		return domain.FileAttachmentObservation{}, apperror.New(apperror.CodeFailedPrecondition, "File attachment lookup is unavailable")
	}
	reader, ok := s.store.(interface {
		InspectWorkspaceFileAttachmentRequest(context.Context, string, string) (domain.FileAttachmentObservation, error)
	})
	if !ok {
		return domain.FileAttachmentObservation{}, apperror.New(apperror.CodeFailedPrecondition, "File attachment lookup is unavailable")
	}
	return reader.InspectWorkspaceFileAttachmentRequest(ctx, workspaceID, key)
}
