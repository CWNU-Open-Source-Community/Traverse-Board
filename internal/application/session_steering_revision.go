package application

import (
	"context"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/session"
)

type SessionSteeringRevisionStore interface {
	GetSession(context.Context, string) (session.Session, error)
	GetRunBySession(context.Context, string) (domain.Run, bool, error)
	GetOperatorSteering(context.Context, string) (domain.OperatorSteeringMessage, error)
	ReviseOperatorSteering(context.Context, domain.ReviseOperatorSteeringRequest) (domain.ReviseOperatorSteeringResult, error)
	InspectOperatorSteeringRevision(context.Context, string, string, string, string) (domain.OperatorSteeringRevisionInspection, error)
}

type SessionSteeringRevisionService struct {
	store SessionSteeringRevisionStore
}

type ReviseSessionSteeringRequest struct {
	Version          string
	SessionID        string
	MessageID        string
	ExpectedRevision int64
	Content          string
	OperationKey     string
	RequestedBy      string
}

type ReviseSessionSteeringResult struct {
	Version  string
	Message  domain.OperatorSteeringMessage
	Receipt  domain.OperatorSteeringRevisionReceipt
	Replayed bool
}

func NewSessionSteeringRevisionService(store SessionSteeringRevisionStore) *SessionSteeringRevisionService {
	return &SessionSteeringRevisionService{store: store}
}

func (s *SessionSteeringRevisionService) Revise(ctx context.Context,
	request ReviseSessionSteeringRequest,
) (ReviseSessionSteeringResult, error) {
	if s == nil || s.store == nil {
		return ReviseSessionSteeringResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"Session steering revision store is required")
	}
	if request.Version != domain.SessionSteeringRevisionProtocolVersion {
		return ReviseSessionSteeringResult{}, apperror.New(apperror.CodeInvalidArgument,
			"Session steering revision protocol version is unsupported")
	}
	request.SessionID, request.MessageID = strings.TrimSpace(request.SessionID), strings.TrimSpace(request.MessageID)
	linked, err := s.store.GetSession(ctx, request.SessionID)
	if err != nil {
		return ReviseSessionSteeringResult{}, apperror.Normalize(err)
	}
	if linked.Status != session.StatusActive {
		return ReviseSessionSteeringResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"Session is not active")
	}
	run, found, err := s.store.GetRunBySession(ctx, request.SessionID)
	if err != nil {
		return ReviseSessionSteeringResult{}, apperror.Normalize(err)
	}
	if !found || (run.Status != domain.RunRunning && run.Status != domain.RunPaused) {
		return ReviseSessionSteeringResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"Session has no running or paused Run")
	}
	message, err := s.store.GetOperatorSteering(ctx, request.MessageID)
	if err != nil {
		return ReviseSessionSteeringResult{}, apperror.Normalize(err)
	}
	if message.SessionID != request.SessionID || message.RunID != run.ID {
		return ReviseSessionSteeringResult{}, apperror.New(apperror.CodeConflict,
			"operator steering message does not belong to this Session Run")
	}
	result, err := s.store.ReviseOperatorSteering(ctx, domain.ReviseOperatorSteeringRequest{
		SessionID: request.SessionID, MessageID: request.MessageID,
		ExpectedRevision: request.ExpectedRevision, Content: request.Content,
		OperationKey: request.OperationKey, RequestedBy: request.RequestedBy,
	})
	if err != nil {
		return ReviseSessionSteeringResult{}, apperror.Normalize(err)
	}
	return ReviseSessionSteeringResult{Version: domain.SessionSteeringRevisionProtocolVersion,
		Message: result.Message, Receipt: result.Receipt, Replayed: result.Replayed}, nil
}

func (s *SessionSteeringRevisionService) Inspect(ctx context.Context, sessionID,
	messageID, operationKey, requestedBy string,
) (domain.OperatorSteeringRevisionInspection, error) {
	if s == nil || s.store == nil {
		return domain.OperatorSteeringRevisionInspection{}, apperror.New(
			apperror.CodeFailedPrecondition, "Session steering revision store is required")
	}
	result, err := s.store.InspectOperatorSteeringRevision(ctx, sessionID, messageID,
		operationKey, requestedBy)
	return result, apperror.Normalize(err)
}
