package application

import (
	"context"
	"errors"
	"time"

	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/session"
)

type ContextMemoryStore interface {
	CreateContextMemory(context.Context, contextmgr.Memory) error
	GetContextMemory(context.Context, string) (contextmgr.Memory, error)
	ListContextMemories(context.Context, contextmgr.MemoryFilter, time.Time) ([]contextmgr.Memory, error)
	UpdateContextMemory(context.Context, contextmgr.Memory, int64) error
	DeleteContextMemory(context.Context, string, int64) (bool, error)
	GetWorkspaceInfo(context.Context, string) (session.WorkspaceInfo, error)
}

type ContextMemoryService struct {
	store ContextMemoryStore
}

type ContextMemoryExport struct {
	ProtocolVersion string                 `json:"protocol_version"`
	ExportedAt      time.Time              `json:"exported_at"`
	Scope           contextmgr.MemoryScope `json:"scope"`
	ScopeID         string                 `json:"scope_id"`
	Items           []contextmgr.Memory    `json:"items"`
	CapabilityGrant bool                   `json:"capability_grant"`
}

func NewContextMemoryService(store ContextMemoryStore) *ContextMemoryService {
	return &ContextMemoryService{store: store}
}

func (s *ContextMemoryService) Create(ctx context.Context,
	request contextmgr.CreateMemoryRequest,
) (contextmgr.Memory, error) {
	if s == nil || s.store == nil {
		return contextmgr.Memory{}, errors.New("long-term memory store is required")
	}
	if request.ID == "" {
		request.ID = idgen.New("memory")
	}
	request.ExplicitOperator = true
	if err := s.validateScope(ctx, request.Scope, request.ScopeID); err != nil {
		return contextmgr.Memory{}, err
	}
	memory, err := contextmgr.PrepareMemory(request, time.Now().UTC())
	if err != nil {
		return contextmgr.Memory{}, err
	}
	if err := s.store.CreateContextMemory(ctx, memory); err != nil {
		return contextmgr.Memory{}, err
	}
	return memory, nil
}

func (s *ContextMemoryService) Get(ctx context.Context, id string) (contextmgr.Memory, error) {
	if s == nil || s.store == nil {
		return contextmgr.Memory{}, errors.New("long-term memory store is required")
	}
	return s.store.GetContextMemory(ctx, id)
}

func (s *ContextMemoryService) List(ctx context.Context,
	filter contextmgr.MemoryFilter,
) ([]contextmgr.Memory, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("long-term memory store is required")
	}
	if err := s.validateScope(ctx, filter.Scope, filter.ScopeID); err != nil {
		return nil, err
	}
	return s.store.ListContextMemories(ctx, filter, time.Now().UTC())
}

func (s *ContextMemoryService) Update(ctx context.Context, id string,
	request contextmgr.UpdateMemoryRequest,
) (contextmgr.Memory, error) {
	if s == nil || s.store == nil {
		return contextmgr.Memory{}, errors.New("long-term memory store is required")
	}
	existing, err := s.store.GetContextMemory(ctx, id)
	if err != nil {
		return contextmgr.Memory{}, err
	}
	updated, err := contextmgr.UpdateMemory(existing, request, time.Now().UTC())
	if err != nil {
		return contextmgr.Memory{}, err
	}
	if err := s.store.UpdateContextMemory(ctx, updated, existing.Version); err != nil {
		return contextmgr.Memory{}, err
	}
	return updated, nil
}

func (s *ContextMemoryService) Delete(ctx context.Context, id string,
	expectedVersion int64, requestedBy string,
) (bool, error) {
	if s == nil || s.store == nil {
		return false, errors.New("long-term memory store is required")
	}
	if err := contextmgr.ValidateMemoryActor(requestedBy); err != nil {
		return false, err
	}
	return s.store.DeleteContextMemory(ctx, id, expectedVersion)
}

func (s *ContextMemoryService) Export(ctx context.Context,
	filter contextmgr.MemoryFilter,
) (ContextMemoryExport, error) {
	filter.IncludeDisabled = true
	filter.IncludeExpired = true
	if filter.Limit == 0 {
		filter.Limit = contextmgr.MaxMemoryListItems
	}
	items, err := s.List(ctx, filter)
	if err != nil {
		return ContextMemoryExport{}, err
	}
	return ContextMemoryExport{ProtocolVersion: "context_memory_export.v1",
		ExportedAt: time.Now().UTC(), Scope: filter.Scope, ScopeID: filter.ScopeID,
		Items: items, CapabilityGrant: false}, nil
}

func (s *ContextMemoryService) validateScope(ctx context.Context,
	scope contextmgr.MemoryScope, scopeID string,
) error {
	filter := contextmgr.MemoryFilter{Scope: scope, ScopeID: scopeID}
	if err := filter.Validate(); err != nil {
		return err
	}
	if scope == contextmgr.MemoryScopeProject {
		workspace, err := s.store.GetWorkspaceInfo(ctx, scopeID)
		if err != nil {
			return err
		}
		if workspace.ID != scopeID || workspace.RootPath == "" {
			return errors.New("project long-term memory Workspace binding is invalid")
		}
	}
	return nil
}
