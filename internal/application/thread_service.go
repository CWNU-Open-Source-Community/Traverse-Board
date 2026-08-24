package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/session"
)

type ThreadStore interface {
	GetThread(context.Context, string) (domain.Thread, error)
	GetThreadByRun(context.Context, string) (domain.Thread, error)
	GetMission(context.Context, string) (domain.Mission, error)
	GetRun(context.Context, string) (domain.Run, error)
	GetRunMode(context.Context, string) (domain.RunModeSnapshot, error)
	GetSession(context.Context, string) (session.Session, error)
	ListThreadsByCreationPage(context.Context, domain.ThreadFilter, time.Time, string) ([]domain.Thread, error)
	ListThreadRuns(context.Context, string) ([]domain.ThreadRun, error)
	ListThreadMessagesPage(context.Context, string, bool, int, int) ([]domain.ThreadMessage, error)
	EnsureThreadSuccessor(context.Context, string, string, domain.Mission, domain.Run,
		domain.RunModeSnapshot, session.Session, []events.Event) (domain.Thread, domain.Run, bool, error)
	EnqueueOperatorSteering(context.Context,
		domain.EnqueueOperatorSteeringRequest) (domain.OperatorSteeringEnqueueResult, error)
	TransitionThreadWithOperationKey(context.Context, string, domain.ThreadLifecycleAction,
		int64, string, string, time.Time) (domain.Thread, error)
	ExportThread(context.Context, string) (domain.ThreadExport, error)
}

type threadContinuityReader interface {
	LatestContextSummary(context.Context, string) (contextmgr.Summary, bool, error)
	ListRecentSessionMessages(context.Context, string, bool, int) ([]session.Message, error)
}

type ThreadService struct {
	store ThreadStore
}

func NewThreadService(store ThreadStore) *ThreadService {
	return &ThreadService{store: store}
}

type SubmitThreadMessageRequest struct {
	Version      string
	ThreadID     string
	Content      string
	OperationKey string
	RequestedBy  string
}

type SubmitThreadMessageResult struct {
	Thread           domain.Thread
	Run              domain.Run
	Session          session.Session
	Message          domain.OperatorSteeringMessage
	PredecessorRunID string
	SuccessorCreated bool
	Replayed         bool
}

func (s *ThreadService) Get(ctx context.Context, id string) (domain.Thread, error) {
	if s == nil || s.store == nil {
		return domain.Thread{}, apperror.New(apperror.CodeFailedPrecondition,
			"Thread store is required")
	}
	return s.store.GetThread(ctx, strings.TrimSpace(id))
}

func (s *ThreadService) List(ctx context.Context, filter domain.ThreadFilter,
	beforeCreatedAt time.Time, beforeID string,
) ([]domain.Thread, error) {
	if s == nil || s.store == nil {
		return nil, apperror.New(apperror.CodeFailedPrecondition, "Thread store is required")
	}
	return s.store.ListThreadsByCreationPage(ctx, filter, beforeCreatedAt, beforeID)
}

func (s *ThreadService) Runs(ctx context.Context, id string) ([]domain.ThreadRun, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	return s.store.ListThreadRuns(ctx, strings.TrimSpace(id))
}

func (s *ThreadService) Messages(ctx context.Context, id string, includeCompacted bool,
	offset, limit int,
) ([]domain.ThreadMessage, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	return s.store.ListThreadMessagesPage(ctx, strings.TrimSpace(id), includeCompacted,
		offset, limit)
}

func (s *ThreadService) Submit(ctx context.Context,
	request SubmitThreadMessageRequest,
) (SubmitThreadMessageResult, error) {
	if s == nil || s.store == nil {
		return SubmitThreadMessageResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"Thread store is required")
	}
	if request.Version != domain.ThreadMessageProtocolVersion {
		return SubmitThreadMessageResult{}, apperror.New(apperror.CodeInvalidArgument,
			"unsupported Thread message submission version")
	}
	if request.ThreadID != strings.TrimSpace(request.ThreadID) ||
		!domain.ValidAgentID(request.ThreadID) {
		return SubmitThreadMessageResult{}, apperror.New(apperror.CodeInvalidArgument,
			"Thread message submission Thread id is invalid")
	}
	content, err := domain.NormalizeOperatorSteeringContent(request.Content)
	if err != nil {
		return SubmitThreadMessageResult{}, apperror.Wrap(apperror.CodeInvalidArgument,
			err.Error(), err)
	}
	operationKey, err := domain.NormalizeAgentOperationKey(request.OperationKey)
	if err != nil || containsSpaceOrControl(operationKey) {
		return SubmitThreadMessageResult{}, apperror.New(apperror.CodeInvalidArgument,
			"Thread message idempotency key is invalid")
	}
	requestedBy := strings.TrimSpace(request.RequestedBy)
	if requestedBy != request.RequestedBy || !domain.ValidAgentID(requestedBy) {
		return SubmitThreadMessageResult{}, apperror.New(apperror.CodeInvalidArgument,
			"Thread message requester is invalid")
	}

	threadRecord, err := s.store.GetThread(ctx, request.ThreadID)
	if err != nil {
		return SubmitThreadMessageResult{}, apperror.Normalize(err)
	}
	if !threadRecord.CanAcceptMessages() {
		return SubmitThreadMessageResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"Thread is not active")
	}
	var run domain.Run
	var predecessorID string
	var successorCreated bool
	if threadRecord.ActiveRunID != "" {
		run, err = s.store.GetRun(ctx, threadRecord.ActiveRunID)
		if err != nil {
			return SubmitThreadMessageResult{}, apperror.Normalize(err)
		}
		if run.Terminal() {
			return SubmitThreadMessageResult{}, apperror.New(apperror.CodeConflict,
				"Thread active Run projection is terminal")
		}
	} else {
		predecessor, err := s.store.GetRun(ctx, threadRecord.LastRunID)
		if err != nil {
			return SubmitThreadMessageResult{}, apperror.Normalize(err)
		}
		if !predecessor.Terminal() {
			return SubmitThreadMessageResult{}, apperror.New(apperror.CodeConflict,
				"Thread has no active or terminal Run")
		}
		mission, err := s.store.GetMission(ctx, threadRecord.MissionID)
		if err != nil {
			return SubmitThreadMessageResult{}, apperror.Normalize(err)
		}
		previousMode, err := s.store.GetRunMode(ctx, predecessor.ID)
		if err != nil {
			return SubmitThreadMessageResult{}, apperror.Normalize(err)
		}
		candidate, linkedSession, mode, initialEvents, err := s.prepareSuccessor(
			ctx, threadRecord, mission, predecessor, previousMode, requestedBy)
		if err != nil {
			return SubmitThreadMessageResult{}, err
		}
		threadRecord, run, successorCreated, err = s.store.EnsureThreadSuccessor(ctx,
			threadRecord.ID, predecessor.ID, mission, candidate, mode, linkedSession,
			initialEvents)
		if err != nil {
			return SubmitThreadMessageResult{}, apperror.Normalize(err)
		}
		if successorCreated {
			predecessorID = predecessor.ID
		}
	}
	linkedSession, err := s.store.GetSession(ctx, run.SessionID)
	if err != nil {
		return SubmitThreadMessageResult{}, apperror.Normalize(err)
	}
	if linkedSession.Status != session.StatusActive {
		return SubmitThreadMessageResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"Thread Run Session is not active")
	}
	queued, err := s.store.EnqueueOperatorSteering(ctx,
		domain.EnqueueOperatorSteeringRequest{RunID: run.ID, SessionID: run.SessionID,
			Content: content, OperationKey: operationKey, RequestedBy: requestedBy})
	if err != nil {
		return SubmitThreadMessageResult{}, apperror.Normalize(err)
	}
	return SubmitThreadMessageResult{Thread: threadRecord, Run: run,
		Session: linkedSession, Message: queued.Message, PredecessorRunID: predecessorID,
		SuccessorCreated: successorCreated, Replayed: queued.Replayed}, nil
}

func (s *ThreadService) prepareSuccessor(ctx context.Context, threadRecord domain.Thread,
	mission domain.Mission, predecessor domain.Run, previousMode domain.RunModeSnapshot,
	requestedBy string,
) (domain.Run, session.Session, domain.RunModeSnapshot, []events.Event, error) {
	now := time.Now().UTC()
	linkedSession := session.New(mission.WorkspaceID, threadRecord.Title,
		predecessor.Config.ModelRoute)
	linkedSession.CreatedAt, linkedSession.UpdatedAt = now, now
	config := predecessor.Config
	config.ContinuityContext, config.ContinuityContextFingerprint = nil, ""
	if mission.WorkspaceID != "" {
		snapshot, err := s.captureThreadContinuity(ctx, predecessor, mission, now)
		if err != nil {
			return domain.Run{}, session.Session{}, domain.RunModeSnapshot{}, nil, err
		}
		raw, err := json.Marshal(snapshot)
		if err != nil {
			return domain.Run{}, session.Session{}, domain.RunModeSnapshot{}, nil, err
		}
		config.ContinuityContext = raw
		config.ContinuityContextFingerprint = snapshot.Fingerprint
	}
	candidate := domain.Run{ID: idgen.New("run"), MissionID: mission.ID,
		SessionID: linkedSession.ID, Status: domain.RunCreated, Config: config,
		Budget: predecessor.Budget, CreatedAt: now, UpdatedAt: now}
	if err := candidate.Validate(); err != nil {
		return domain.Run{}, session.Session{}, domain.RunModeSnapshot{}, nil, err
	}
	mode, err := domain.NewInitialRunModeSnapshot(idgen.New("run-mode"), candidate,
		mission, previousMode.Surface, previousMode.Phase, requestedBy,
		"Thread successor Run; all authority reset", now)
	if err != nil {
		return domain.Run{}, session.Session{}, domain.RunModeSnapshot{}, nil, err
	}
	created, err := events.New(candidate.ID, mission.ID, events.RunCreatedEvent,
		"thread_service", candidate.ID, map[string]any{
			"status": candidate.Status, "profile": mission.Profile,
			"surface": mode.Surface, "phase": mode.Phase,
			"network_mode": mission.Scope.NetworkMode, "session_id": candidate.SessionID,
			"thread_id": threadRecord.ID, "predecessor_run_id": predecessor.ID,
			"authority_inherited": false,
		})
	if err != nil {
		return domain.Run{}, session.Session{}, domain.RunModeSnapshot{}, nil, err
	}
	attached, err := events.New(candidate.ID, mission.ID, events.SessionAttachedEvent,
		"thread_service", linkedSession.ID, map[string]any{
			"created": true, "route": linkedSession.Route,
			"workspace_id": linkedSession.WorkspaceID, "thread_id": threadRecord.ID,
		})
	if err != nil {
		return domain.Run{}, session.Session{}, domain.RunModeSnapshot{}, nil, err
	}
	return candidate, linkedSession, mode, []events.Event{created, attached}, nil
}

func (s *ThreadService) captureThreadContinuity(ctx context.Context, run domain.Run,
	mission domain.Mission, at time.Time,
) (contextmgr.ContinuitySnapshot, error) {
	reader, ok := s.store.(threadContinuityReader)
	if !ok {
		return contextmgr.ContinuitySnapshot{}, errors.New(
			"Thread continuation history reader is required")
	}
	snapshot := contextmgr.ContinuitySnapshot{SourceRunID: run.ID,
		SourceSessionID: run.SessionID, WorkspaceID: mission.WorkspaceID,
		RecentMessages:                 []contextmgr.ContinuityMessage{},
		Memories:                       []contextmgr.ContinuityMemoryReference{},
		ProjectConfigFingerprint:       run.Config.ProjectConfigFingerprint,
		ProjectInstructionsFingerprint: run.Config.ProjectInstructionsFingerprint,
		InheritedContext:               []string{}, CreatedAt: at}
	inherited := make(map[string]struct{})
	if len(run.Config.ContinuityContext) > 0 {
		var previous contextmgr.ContinuitySnapshot
		if err := json.Unmarshal(run.Config.ContinuityContext, &previous); err != nil {
			return contextmgr.ContinuitySnapshot{}, fmt.Errorf(
				"decode predecessor continuity context: %w", err)
		}
		if err := previous.Validate(); err != nil ||
			previous.Fingerprint != run.Config.ContinuityContextFingerprint ||
			previous.WorkspaceID != mission.WorkspaceID {
			return contextmgr.ContinuitySnapshot{}, errors.New(
				"predecessor continuity context binding is invalid")
		}
		snapshot.SummaryID = previous.SummaryID
		snapshot.SummaryContent = previous.SummaryContent
		snapshot.SummaryContentSHA256 = previous.SummaryContentSHA256
		snapshot.ThroughMessageID = previous.ThroughMessageID
		snapshot.RecentMessages = append(snapshot.RecentMessages, previous.RecentMessages...)
		snapshot.Memories = append(snapshot.Memories, previous.Memories...)
		snapshot.GitBranch, snapshot.GitHead = previous.GitBranch, previous.GitHead
		inherited[fmt.Sprintf("continuity:%s:%s", previous.SourceRunID,
			previous.Fingerprint)] = struct{}{}
	}
	if summary, found, err := reader.LatestContextSummary(ctx, run.SessionID); err != nil {
		return contextmgr.ContinuitySnapshot{}, err
	} else if found {
		snapshot.SummaryID = summary.ID
		snapshot.SummaryContent = boundedContinuityText(summary.Content,
			contextmgr.MaxContinuitySummaryBytes)
		snapshot.SummaryContentSHA256 = session.ContentSHA256(snapshot.SummaryContent)
		inherited[fmt.Sprintf("compaction:%d:%s", summary.ID,
			snapshot.SummaryContentSHA256)] = struct{}{}
	}
	messages, err := reader.ListRecentSessionMessages(ctx, run.SessionID, true,
		contextmgr.MaxContinuityRecentMessages)
	if err != nil {
		return contextmgr.ContinuitySnapshot{}, err
	}
	for _, message := range messages {
		content := boundedContinuityText(message.Content, contextmgr.MaxContinuityMessageBytes)
		item := contextmgr.ContinuityMessage{ID: message.ID, Role: message.Role,
			SourceKind: boundedContinuityText(message.Provenance.SourceKind,
				contextmgr.MaxMemoryReferenceBytes),
			SourceRef: boundedContinuityText(message.Provenance.SourceRef,
				contextmgr.MaxMemoryReferenceBytes),
			ContentSHA256: session.ContentSHA256(content), Content: content,
			InstructionAuthorized: message.Role == "user" &&
				message.Provenance.SourceKind == session.SourceOperatorMessage &&
				message.Provenance.InstructionAuthorized}
		snapshot.RecentMessages = append(snapshot.RecentMessages, item)
		if message.ID > snapshot.ThroughMessageID {
			snapshot.ThroughMessageID = message.ID
		}
		inherited[fmt.Sprintf("message:%d:%s:%s:instruction_authorized=%t",
			item.ID, item.ContentSHA256, item.SourceKind, item.InstructionAuthorized)] = struct{}{}
	}
	sort.Slice(snapshot.RecentMessages, func(left, right int) bool {
		return snapshot.RecentMessages[left].ID < snapshot.RecentMessages[right].ID
	})
	if len(snapshot.RecentMessages) > contextmgr.MaxContinuityRecentMessages {
		snapshot.RecentMessages = snapshot.RecentMessages[len(snapshot.RecentMessages)-contextmgr.MaxContinuityRecentMessages:]
	}
	if snapshot.ProjectConfigFingerprint != "" {
		inherited["project_config:"+snapshot.ProjectConfigFingerprint] = struct{}{}
	}
	if snapshot.ProjectInstructionsFingerprint != "" {
		inherited["project_instructions:"+snapshot.ProjectInstructionsFingerprint] = struct{}{}
	}
	for value := range inherited {
		snapshot.InheritedContext = append(snapshot.InheritedContext, value)
	}
	sort.Strings(snapshot.InheritedContext)
	return contextmgr.SealContinuitySnapshot(snapshot)
}

func (s *ThreadService) Transition(ctx context.Context, id string,
	action domain.ThreadLifecycleAction, expectedVersion int64, requestedBy, operationKey string,
) (domain.Thread, error) {
	if s == nil || s.store == nil {
		return domain.Thread{}, apperror.New(apperror.CodeFailedPrecondition,
			"Thread store is required")
	}
	id = strings.TrimSpace(id)
	requestedBy = strings.TrimSpace(requestedBy)
	if !domain.ValidAgentID(id) || !domain.ValidAgentID(requestedBy) || expectedVersion <= 0 {
		return domain.Thread{}, apperror.New(apperror.CodeInvalidArgument,
			"Thread lifecycle identity and positive expected version are required")
	}
	if action != domain.ThreadArchive && action != domain.ThreadRestore &&
		action != domain.ThreadDelete {
		return domain.Thread{}, apperror.New(apperror.CodeInvalidArgument,
			"unsupported Thread lifecycle action")
	}
	return s.store.TransitionThreadWithOperationKey(ctx, id, action,
		expectedVersion, requestedBy, operationKey, time.Now().UTC())
}

func (s *ThreadService) Export(ctx context.Context, id string) (domain.ThreadExport, error) {
	if s == nil || s.store == nil {
		return domain.ThreadExport{}, apperror.New(apperror.CodeFailedPrecondition,
			"Thread store is required")
	}
	return s.store.ExportThread(ctx, strings.TrimSpace(id))
}
