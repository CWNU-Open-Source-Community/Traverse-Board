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

type threadModelRoutePreferenceReader interface {
	GetThreadModelRoutePreference(context.Context, string) (
		domain.ThreadModelRoutePreference, bool, error)
}

type ThreadService struct {
	store        ThreadStore
	capabilities domain.ExecutionPermissionRuntimeCapabilities
	modelRoutes  ThreadModelRouteRegistry
}

// WithModelRouteRegistry enables fail-closed validation when a durable Thread
// preference is materialized into a successor Run.
func (s *ThreadService) WithModelRouteRegistry(registry ThreadModelRouteRegistry) *ThreadService {
	if s != nil {
		s.modelRoutes = registry
	}
	return s
}

func NewThreadService(store ThreadStore) *ThreadService {
	return &ThreadService{store: store}
}

func NewThreadServiceWithExecutionCapabilities(store ThreadStore,
	capabilities domain.ExecutionPermissionRuntimeCapabilities,
) *ThreadService {
	return &ThreadService{store: store, capabilities: capabilities}
}

type threadRunExecutionPermissionReader interface {
	GetRunExecutionPermission(context.Context,
		string) (domain.RunExecutionPermissionSnapshot, error)
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
	request, err := normalizeSubmitThreadMessageRequest(request)
	if err != nil {
		return SubmitThreadMessageResult{}, err
	}
	content, operationKey, requestedBy := request.Content, request.OperationKey,
		request.RequestedBy

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
		successorScope, legacyNetworkReset := successorRunNetworkScope(previousMode.Scope)
		successorMission := mission
		successorMission.Scope = successorScope
		candidate, linkedSession, mode, initialEvents, err := s.prepareSuccessor(
			ctx, threadRecord, successorMission, predecessor, previousMode,
			legacyNetworkReset, requestedBy)
		if err != nil {
			return SubmitThreadMessageResult{}, err
		}
		threadRecord, run, successorCreated, err = s.store.EnsureThreadSuccessor(ctx,
			threadRecord.ID, predecessor.ID, successorMission, candidate, mode, linkedSession,
			initialEvents)
		if err != nil {
			return SubmitThreadMessageResult{}, apperror.Normalize(err)
		}
		if successorCreated {
			predecessorID = predecessor.ID
		}
		if err := s.bindSuccessorFullAccess(ctx, threadRecord.ID, run); err != nil {
			return SubmitThreadMessageResult{}, err
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

func (s *ThreadService) bindSuccessorFullAccess(ctx context.Context, threadID string,
	run domain.Run,
) error {
	authority := s.capabilities.RuntimeAuthority
	if authority == nil || !s.capabilities.FullAccessRequiresRuntimeGrant {
		return nil
	}
	reader, ok := s.store.(threadRunExecutionPermissionReader)
	if !ok {
		return apperror.New(apperror.CodeFailedPrecondition,
			"Thread successor execution permission reader is required")
	}
	permission, err := reader.GetRunExecutionPermission(ctx, run.ID)
	if err != nil {
		return apperror.Normalize(err)
	}
	if permission.Mode != domain.RunExecutionPermissionFullAccess {
		return nil
	}
	if _, _, err := authority.BindThreadRun(threadID, permission); err != nil {
		return apperror.Wrap(apperror.CodeFailedPrecondition,
			"Thread successor Full Access binding failed", err)
	}
	return nil
}

func normalizeSubmitThreadMessageRequest(request SubmitThreadMessageRequest) (
	SubmitThreadMessageRequest, error,
) {
	if request.Version != domain.ThreadMessageProtocolVersion {
		return SubmitThreadMessageRequest{}, apperror.New(apperror.CodeInvalidArgument,
			"unsupported Thread message submission version")
	}
	if request.ThreadID != strings.TrimSpace(request.ThreadID) ||
		!domain.ValidAgentID(request.ThreadID) {
		return SubmitThreadMessageRequest{}, apperror.New(apperror.CodeInvalidArgument,
			"Thread message submission Thread id is invalid")
	}
	content, err := domain.NormalizeOperatorSteeringContent(request.Content)
	if err != nil {
		return SubmitThreadMessageRequest{}, apperror.Wrap(apperror.CodeInvalidArgument,
			err.Error(), err)
	}
	operationKey, err := domain.NormalizeAgentOperationKey(request.OperationKey)
	if err != nil || containsSpaceOrControl(operationKey) {
		return SubmitThreadMessageRequest{}, apperror.New(apperror.CodeInvalidArgument,
			"Thread message idempotency key is invalid")
	}
	requestedBy := strings.TrimSpace(request.RequestedBy)
	if requestedBy != request.RequestedBy || !domain.ValidAgentID(requestedBy) {
		return SubmitThreadMessageRequest{}, apperror.New(apperror.CodeInvalidArgument,
			"Thread message requester is invalid")
	}
	request.Content = content
	request.OperationKey = operationKey
	request.RequestedBy = requestedBy
	return request, nil
}

func (s *ThreadService) prepareSuccessor(ctx context.Context, threadRecord domain.Thread,
	mission domain.Mission, predecessor domain.Run, previousMode domain.RunModeSnapshot,
	legacyNetworkReset bool, requestedBy string,
) (domain.Run, session.Session, domain.RunModeSnapshot, []events.Event, error) {
	now := time.Now().UTC()
	linkedSession := session.New(mission.WorkspaceID, threadRecord.Title,
		predecessor.Config.ModelRoute)
	linkedSession.CreatedAt, linkedSession.UpdatedAt = now, now
	config := predecessor.Config
	config.ContinuityContext, config.ContinuityContextFingerprint = nil, ""
	if reader, ok := s.store.(threadModelRoutePreferenceReader); ok {
		preference, found, preferenceErr := reader.GetThreadModelRoutePreference(
			ctx, threadRecord.ID)
		if preferenceErr != nil {
			return domain.Run{}, session.Session{}, domain.RunModeSnapshot{}, nil,
				apperror.Normalize(preferenceErr)
		}
		if found && preference.Selected {
			if s.modelRoutes == nil {
				return domain.Run{}, session.Session{}, domain.RunModeSnapshot{}, nil,
					apperror.New(apperror.CodeFailedPrecondition,
						"Thread model route Registry is required")
			}
			catalog, catalogErr := NewThreadModelRouteService(nil, s.modelRoutes).Catalog(ctx)
			if catalogErr != nil {
				return domain.Run{}, session.Session{}, domain.RunModeSnapshot{}, nil, catalogErr
			}
			selectable := false
			for _, route := range catalog.Routes {
				if route.ProviderID == preference.Provider && route.Model == preference.Model &&
					route.Selectable {
					selectable = true
					break
				}
			}
			if !selectable {
				return domain.Run{}, session.Session{}, domain.RunModeSnapshot{}, nil,
					apperror.New(apperror.CodeFailedPrecondition,
						"selected Thread model route is no longer eligible")
			}
			// The preference is materialized only into the successor. The
			// predecessor Run and its Session remain immutable even if they are
			// still executing while the operator changes the next model.
			config.ModelRoute = preference.Provider + "/" + preference.Model
			linkedSession.Route = config.ModelRoute
		} else if found {
			// A reset tombstone is an explicit instruction to return to the
			// Mission profile's named Registry route. Copying the predecessor
			// would incorrectly preserve the old direct Provider/model ref.
			config.ModelRoute = string(mission.Profile)
			linkedSession.Route = config.ModelRoute
		}
	}
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
	networkInherited := mission.Scope.NetworkMode == "allowlist" &&
		len(mission.Scope.AllowedTargets) > 0
	reason := "Thread successor Run; runtime authority reset"
	if networkInherited {
		reason = "Thread successor Run; exact network preference inherited; runtime authority reset"
	} else if legacyNetworkReset {
		reason = "Thread successor Run; legacy broad network preference reset; runtime authority reset"
	}
	mode, err := domain.NewInitialRunModeSnapshot(idgen.New("run-mode"), candidate,
		mission, previousMode.Surface, previousMode.Phase, requestedBy, reason, now)
	if err != nil {
		return domain.Run{}, session.Session{}, domain.RunModeSnapshot{}, nil, err
	}
	created, err := events.New(candidate.ID, mission.ID, events.RunCreatedEvent,
		"thread_service", candidate.ID, map[string]any{
			"status": candidate.Status, "profile": mission.Profile,
			"surface": mode.Surface, "phase": mode.Phase,
			"network_mode":                      mode.Scope.NetworkMode,
			"allowed_target_count":              len(mode.Scope.AllowedTargets),
			"network_authority_inherited":       networkInherited,
			"network_authority_source_revision": previousMode.Revision,
			"legacy_network_authority_reset":    legacyNetworkReset,
			"session_id":                        candidate.SessionID,
			"thread_id":                         threadRecord.ID, "predecessor_run_id": predecessor.ID,
			"authority_inherited":         networkInherited,
			"runtime_authority_inherited": false,
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
	if (action == domain.ThreadArchive || action == domain.ThreadDelete) &&
		s.capabilities.RuntimeAuthority != nil {
		// Lifecycle changes close process-local authority before persistence. A
		// failed transition therefore remains fail-closed until reconfirmed.
		if threadRecord, err := s.store.GetThread(ctx, id); err == nil &&
			threadRecord.ActiveRunID != "" {
			s.capabilities.RuntimeAuthority.RevokeRun(threadRecord.ActiveRunID)
		}
		s.capabilities.RuntimeAuthority.RevokeThread(id)
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
