package application

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/drydock"
	"cyberagent-workbench/internal/gitadvanced"
	"cyberagent-workbench/internal/repository"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/workspacecheckpoint"
)

const DrydockAPIProtocolVersion = "drydock-api.v1"

type DrydockStore interface {
	GetRun(context.Context, string) (domain.Run, error)
	GetMission(context.Context, string) (domain.Mission, error)
	GetSession(context.Context, string) (session.Session, error)
	GetWorkspaceByID(context.Context, string) (session.WorkspaceRecord, error)

	CreateDrydockTrust(context.Context, drydock.Trust) (drydock.Trust, bool, error)
	GetDrydockTrustByRun(context.Context, string) (drydock.Trust, bool, error)
	PrepareDrydock(context.Context, drydock.Workspace) (drydock.Workspace, bool, error)
	AdvanceDrydock(context.Context, drydock.Workspace, int64, drydock.Receipt) (
		drydock.Workspace, bool, error)
	CreateDrydockDelivery(context.Context, drydock.DeliveryProposal, drydock.Workspace,
		int64, drydock.Receipt) (drydock.DeliveryProposal, drydock.Workspace, bool, error)
	GetDrydockByRun(context.Context, string) (drydock.Workspace, bool, error)
	GetDrydock(context.Context, string) (drydock.Workspace, bool, error)
	ListDrydocks(context.Context, drydock.ListFilter) ([]drydock.Workspace, error)
	GetDrydockReceiptByOperation(context.Context, string) (drydock.Receipt, bool, error)
	ListDrydockReceipts(context.Context, string, int) ([]drydock.Receipt, error)
	GetDrydockDelivery(context.Context, string) (drydock.DeliveryProposal, bool, error)

	CreateWorkspaceCheckpoint(context.Context, workspacecheckpoint.Snapshot) (
		workspacecheckpoint.Checkpoint, bool, error)
	GetWorkspaceCheckpointSnapshot(context.Context, string) (workspacecheckpoint.Snapshot, error)
}

type DrydockService struct {
	store       DrydockStore
	executor    *repository.DrydockExecutor
	checkpoints *WorkspaceCheckpointService
	now         func() time.Time
}

func NewDrydockService(store DrydockStore,
	executor *repository.DrydockExecutor,
) (*DrydockService, error) {
	if store == nil || executor == nil || !executor.Available() {
		return nil, apperror.New(apperror.CodeFailedPrecondition,
			"Drydock requires durable storage and an available product-managed Git root")
	}
	return &DrydockService{store: store, executor: executor,
		now: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *DrydockService) WithCheckpointService(
	checkpoints *WorkspaceCheckpointService,
) *DrydockService {
	if s == nil {
		return s
	}
	s.checkpoints = nil
	if checkpoints != nil {
		// Keep the caller's ordinary source-Workspace checkpoint service intact.
		// Drydock receives a private projection with the owned Workspace resolver.
		owned := *checkpoints
		s.checkpoints = &owned
		s.checkpoints.withRunWorkspaceResolver(func(ctx context.Context,
			runID string,
		) (session.WorkspaceInfo, bool, error) {
			workspace, found, err := s.store.GetDrydockByRun(ctx, runID)
			if err != nil || !found {
				return session.WorkspaceInfo{}, false, err
			}
			if workspace.State != drydock.StateReady &&
				workspace.State != drydock.StateDelivered {
				return session.WorkspaceInfo{}, false, nil
			}
			return session.WorkspaceInfo{ID: workspace.WorkspaceID,
				Name: workspace.Name, RootPath: workspace.Path}, true, nil
		})
	}
	return s
}

type DrydockCreateRequest struct {
	RunID                 string `json:"run_id"`
	OperationKey          string `json:"operation_key"`
	RequestedBy           string `json:"requested_by"`
	ConfirmWorkspaceTrust bool   `json:"confirm_workspace_trust"`
	ExpectedTrustDigest   string `json:"expected_trust_digest,omitempty"`
}

type DrydockCreateResult struct {
	ProtocolVersion string                          `json:"protocol_version"`
	Source          drydock.SourceIdentity          `json:"source"`
	SourceRoot      string                          `json:"-"`
	SourceState     drydock.SourceState             `json:"source_state"`
	TrustDigest     string                          `json:"trust_digest"`
	TrustRequired   bool                            `json:"trust_required"`
	Trust           *drydock.Trust                  `json:"trust,omitempty"`
	Workspace       *drydock.Workspace              `json:"workspace,omitempty"`
	Receipt         *drydock.Receipt                `json:"receipt,omitempty"`
	Checkpoint      *workspacecheckpoint.Checkpoint `json:"checkpoint,omitempty"`
	Replayed        bool                            `json:"replayed"`
}

type DrydockUseRequest struct {
	RunID              string `json:"run_id"`
	ExpectedGeneration int64  `json:"expected_generation"`
	OperationKey       string `json:"operation_key"`
	RequestedBy        string `json:"requested_by"`
}

type DrydockUseResult struct {
	ProtocolVersion        string            `json:"protocol_version"`
	Workspace              drydock.Workspace `json:"workspace"`
	Receipt                drydock.Receipt   `json:"receipt"`
	RootPath               string            `json:"-"`
	BindingFingerprint     string            `json:"binding_fingerprint"`
	GrantsProcessAuthority bool              `json:"grants_process_authority"`
	Replayed               bool              `json:"replayed"`
}

type DrydockCheckpointRequest struct {
	RunID                  string `json:"run_id"`
	ExpectedGeneration     int64  `json:"expected_generation"`
	OperationKey           string `json:"operation_key"`
	RequestedBy            string `json:"requested_by"`
	Title                  string `json:"title,omitempty"`
	ConfirmObservedChanges bool   `json:"confirm_observed_changes"`
}

type DrydockCheckpointResult struct {
	ProtocolVersion string                         `json:"protocol_version"`
	Workspace       drydock.Workspace              `json:"workspace"`
	Checkpoint      workspacecheckpoint.Checkpoint `json:"checkpoint"`
	Receipt         drydock.Receipt                `json:"receipt"`
	Replayed        bool                           `json:"replayed"`
}

type DrydockRewindRequest struct {
	RunID                  string `json:"run_id"`
	TargetCheckpointID     string `json:"target_checkpoint_id"`
	ExpectedGeneration     int64  `json:"expected_generation"`
	OperationKey           string `json:"operation_key"`
	RequestedBy            string `json:"requested_by"`
	Confirm                bool   `json:"confirm"`
	ConfirmObservedChanges bool   `json:"confirm_observed_changes"`
}

type DrydockUndoRequest struct {
	RunID                  string `json:"run_id"`
	ExpectedGeneration     int64  `json:"expected_generation"`
	OperationKey           string `json:"operation_key"`
	RequestedBy            string `json:"requested_by"`
	Confirm                bool   `json:"confirm"`
	ConfirmObservedChanges bool   `json:"confirm_observed_changes"`
}

type DrydockRewindResult struct {
	ProtocolVersion string                          `json:"protocol_version"`
	Workspace       drydock.Workspace               `json:"workspace"`
	Target          workspacecheckpoint.Checkpoint  `json:"target"`
	Before          workspacecheckpoint.Checkpoint  `json:"before"`
	Preview         workspacecheckpoint.Preview     `json:"preview"`
	After           *workspacecheckpoint.Checkpoint `json:"after,omitempty"`
	Receipt         *drydock.Receipt                `json:"receipt,omitempty"`
	Confirmed       bool                            `json:"confirmed"`
	Replayed        bool                            `json:"replayed"`
}

type DrydockForkRequest struct {
	RunID                       string `json:"run_id"`
	TargetCheckpointID          string `json:"target_checkpoint_id"`
	ExpectedCurrentCheckpointID string `json:"expected_current_checkpoint_id"`
	ExpectedGeneration          int64  `json:"expected_generation"`
	OperationKey                string `json:"operation_key"`
	RequestedBy                 string `json:"requested_by"`
	WorkspaceName               string `json:"workspace_name"`
	WorkspaceRoot               string `json:"workspace_root"`
	Branch                      string `json:"branch"`
	Goal                        string `json:"goal"`
	Confirm                     bool   `json:"confirm"`
}

type DrydockForkResult struct {
	ProtocolVersion string              `json:"protocol_version"`
	Workspace       drydock.Workspace   `json:"workspace"`
	Fork            WorkspaceForkResult `json:"fork"`
	Receipt         drydock.Receipt     `json:"receipt"`
	Replayed        bool                `json:"replayed"`
}

type DrydockDeliveryRequest struct {
	RunID              string `json:"run_id"`
	ExpectedGeneration int64  `json:"expected_generation"`
	OperationKey       string `json:"operation_key"`
	RequestedBy        string `json:"requested_by"`
	Confirm            bool   `json:"confirm"`
}

type DrydockDeliveryResult struct {
	ProtocolVersion string            `json:"protocol_version"`
	Workspace       drydock.Workspace `json:"workspace"`
	Review          drydock.Review    `json:"review"`
	Receipt         drydock.Receipt   `json:"receipt"`
	Replayed        bool              `json:"replayed"`
}

type DrydockCleanupRequest struct {
	RunID              string `json:"run_id"`
	ExpectedGeneration int64  `json:"expected_generation"`
	OperationKey       string `json:"operation_key"`
	RequestedBy        string `json:"requested_by"`
	Confirm            bool   `json:"confirm"`
}

type DrydockCleanupResult struct {
	ProtocolVersion string            `json:"protocol_version"`
	Workspace       drydock.Workspace `json:"workspace"`
	Receipt         drydock.Receipt   `json:"receipt"`
	Preserved       bool              `json:"preserved"`
	Replayed        bool              `json:"replayed"`
}

type DrydockProjection struct {
	ProtocolVersion string                    `json:"protocol_version"`
	RunID           string                    `json:"run_id"`
	Trust           *drydock.Trust            `json:"trust,omitempty"`
	Workspace       *drydock.Workspace        `json:"workspace,omitempty"`
	Delivery        *drydock.DeliveryProposal `json:"delivery,omitempty"`
	Receipts        []drydock.Receipt         `json:"receipts"`
}

type DrydockReconcileResult struct {
	ProtocolVersion  string   `json:"protocol_version"`
	Examined         int      `json:"examined"`
	Recovered        int      `json:"recovered"`
	RecoveryRequired int      `json:"recovery_required"`
	Unchanged        int      `json:"unchanged"`
	DrydockIDs       []string `json:"drydock_ids"`
}

type DrydockGCResult struct {
	ProtocolVersion string   `json:"protocol_version"`
	Examined        int      `json:"examined"`
	Cleaned         int      `json:"cleaned"`
	Preserved       int      `json:"preserved"`
	DrydockIDs      []string `json:"drydock_ids"`
}

type drydockRunBinding struct {
	run       domain.Run
	mission   domain.Mission
	session   session.Session
	workspace session.WorkspaceRecord
}

func (s *DrydockService) Create(ctx context.Context,
	request DrydockCreateRequest,
) (DrydockCreateResult, error) {
	request.RunID = strings.TrimSpace(request.RunID)
	request.OperationKey = strings.TrimSpace(request.OperationKey)
	request.RequestedBy = normalizeDrydockActor(request.RequestedBy)
	request.ExpectedTrustDigest = strings.TrimSpace(request.ExpectedTrustDigest)
	if s == nil || s.store == nil || s.executor == nil || request.RunID == "" ||
		request.OperationKey == "" || request.RequestedBy == "" {
		return DrydockCreateResult{}, apperror.New(apperror.CodeInvalidArgument,
			"Drydock create request is invalid")
	}
	binding, err := s.loadRunBinding(ctx, request.RunID, false)
	if err != nil {
		return DrydockCreateResult{}, err
	}
	source, err := s.executor.InspectSource(ctx, binding.workspace.ID,
		binding.workspace.RootPath)
	if err != nil {
		return DrydockCreateResult{}, apperror.Normalize(err)
	}
	result := DrydockCreateResult{ProtocolVersion: DrydockAPIProtocolVersion,
		Source: source.Identity, SourceRoot: source.Identity.RootPath,
		SourceState: source.State,
		TrustDigest: drydock.TrustConfirmationDigest(source.Identity, source.State)}
	if source.State.SymlinkEntries != 0 || source.State.SubmoduleEntries != 0 {
		return result, apperror.New(apperror.CodeFailedPrecondition,
			"Drydock v1 rejects source symlink/reparse and submodule entries instead of following them implicitly")
	}
	if existing, found, getErr := s.store.GetDrydockByRun(ctx, binding.run.ID); getErr != nil {
		return result, apperror.Normalize(getErr)
	} else if found {
		if existing.Source.Fingerprint() != source.Identity.Fingerprint() {
			return result, apperror.New(apperror.CodeConflict,
				"Drydock source root, branch, or base commit drifted")
		}
		if existing.State == drydock.StatePreparing {
			reconciled, _, reconcileErr := s.reconcileOne(ctx, existing)
			if reconcileErr != nil {
				return result, apperror.Normalize(reconcileErr)
			}
			existing = reconciled
		}
		trust, trustFound, trustErr := s.store.GetDrydockTrustByRun(ctx, binding.run.ID)
		if trustErr != nil {
			return result, apperror.Normalize(trustErr)
		}
		if trustFound {
			result.Trust = &trust
		}
		createDigest := drydockOperationDigest(drydock.OperationCreate,
			binding.run.ID, request.OperationKey)
		if receipt, receiptFound, receiptErr := s.store.GetDrydockReceiptByOperation(ctx,
			createDigest); receiptErr != nil {
			return result, apperror.Normalize(receiptErr)
		} else if receiptFound {
			expectedFingerprint := runmutation.Fingerprint("drydock-create-request.v1",
				binding.run.ID, source.Identity.Fingerprint(), request.RequestedBy)
			if receipt.Operation != drydock.OperationCreate ||
				receipt.RequestFingerprint != expectedFingerprint {
				return result, apperror.New(apperror.CodeConflict,
					"Drydock create operation key was reused for different intent")
			}
			result.Receipt = &receipt
			if receipt.CheckpointID != "" {
				snapshot, snapshotErr := s.store.GetWorkspaceCheckpointSnapshot(ctx,
					receipt.CheckpointID)
				if snapshotErr != nil {
					return result, apperror.Normalize(snapshotErr)
				}
				result.Checkpoint = &snapshot.Checkpoint
			}
		}
		result.Workspace, result.Replayed = &existing, true
		return result, nil
	}

	trust, trustFound, err := s.store.GetDrydockTrustByRun(ctx, binding.run.ID)
	if err != nil {
		return result, apperror.Normalize(err)
	}
	if trustFound && (trust.Source.Fingerprint() != source.Identity.Fingerprint() ||
		trust.SourceState.Fingerprint() != source.State.Fingerprint()) {
		return result, apperror.New(apperror.CodeConflict,
			"stored Workspace Trust does not match the current source identity and state")
	}
	if !trustFound {
		if !request.ConfirmWorkspaceTrust {
			result.TrustRequired = true
			return result, nil
		}
		if request.ExpectedTrustDigest == "" ||
			request.ExpectedTrustDigest != result.TrustDigest {
			return result, apperror.New(apperror.CodeConflict,
				"Workspace Trust confirmation does not match the exact reviewed source state")
		}
		now := s.now().UTC()
		trust = drydock.Trust{ID: drydockTrustID(binding.run.ID, source.Identity),
			ProtocolVersion: drydock.TrustProtocolVersion, RunID: binding.run.ID,
			WorkspaceID: binding.workspace.ID, Source: source.Identity,
			SourceState: source.State, ConfirmedBy: request.RequestedBy,
			GrantsProcessAuthority: false, ConfirmedAt: now}
		trust, _, err = s.store.CreateDrydockTrust(ctx, trust)
		if err != nil {
			return result, apperror.Normalize(err)
		}
	}
	result.Trust = &trust

	identityDigest := drydock.Fingerprint("drydock", binding.run.ID,
		source.Identity.Fingerprint())
	name := "drydock-" + identityDigest[:24]
	branch := "codex/drydock/" + identityDigest[:24]
	plan, err := s.executor.PlanCreate(ctx, source.Identity.RootPath, name, branch,
		source.Identity.BaseCommit)
	if err != nil {
		return result, apperror.Normalize(err)
	}
	if !plan.Preview.Executable() {
		return result, apperror.New(apperror.CodeFailedPrecondition,
			"Drydock create preview is blocked: "+strings.Join(plan.Preview.BlockedReasons, "; "))
	}
	preCreateSource, err := s.executor.InspectSource(ctx, binding.workspace.ID,
		binding.workspace.RootPath)
	if err != nil || preCreateSource.Identity.Fingerprint() != trust.Source.Fingerprint() ||
		preCreateSource.State.Fingerprint() != trust.SourceState.Fingerprint() {
		return result, apperror.Wrap(apperror.CodeConflict,
			"source identity or dirty/index state changed after Workspace Trust confirmation", err)
	}
	now := s.now().UTC()
	workspace := drydock.Workspace{ID: "drydock-" + identityDigest[:32],
		ProtocolVersion: drydock.WorkspaceProtocolVersion, RunID: binding.run.ID,
		MissionID: binding.mission.ID, SessionID: binding.session.ID,
		SourceWorkspaceID: binding.workspace.ID,
		WorkspaceID:       "drydock-ws-" + identityDigest[:32], TrustID: trust.ID,
		Source: source.Identity, Name: name, Path: filepath.Clean(plan.Path),
		PathSHA256: drydock.FingerprintBytes([]byte(filepath.ToSlash(filepath.Clean(plan.Path)))),
		Branch:     branch, BaseCommit: source.Identity.BaseCommit,
		CreatePreviewID: plan.Preview.ID, State: drydock.StatePreparing,
		Generation: 1, ExpiresAt: now.Add(drydock.DefaultLifetime),
		CreatedAt: now, UpdatedAt: now}
	workspace, replayed, err := s.store.PrepareDrydock(ctx, workspace)
	if err != nil {
		return result, apperror.Normalize(err)
	}
	if replayed {
		result.Workspace, result.Replayed = &workspace, true
		return result, nil
	}
	gitReceipt, executionErr := s.executor.ExecuteCreate(ctx, source.Identity.RootPath, plan)
	receiptErr := gitReceipt.Validate()
	if executionErr != nil || receiptErr != nil ||
		gitReceipt.Status != gitadvanced.ReceiptSucceeded {
		cause := errors.Join(executionErr, receiptErr)
		if cause == nil {
			cause = errors.New("Git advanced create receipt did not succeed")
		}
		failed, receipt, transitionErr := s.failCreate(ctx, workspace, source, request,
			gitReceipt, cause)
		result.Workspace, result.Receipt = &failed, &receipt
		return result, errors.Join(apperror.Normalize(cause), transitionErr)
	}
	postCreateSource, postSourceErr := s.executor.InspectSource(ctx, binding.workspace.ID,
		binding.workspace.RootPath)
	if postSourceErr != nil ||
		postCreateSource.Identity.Fingerprint() != trust.Source.Fingerprint() ||
		postCreateSource.State.Fingerprint() != trust.SourceState.Fingerprint() {
		cause := errors.Join(postSourceErr,
			errors.New("source identity or dirty/index state changed while the Drydock was created"))
		failed, receipt, transitionErr := s.failCreate(ctx, workspace, source, request,
			gitReceipt, cause)
		result.Workspace, result.Receipt = &failed, &receipt
		return result, errors.Join(apperror.Normalize(cause), transitionErr)
	}
	observed, err := s.executor.Inspect(ctx, source.Identity.RootPath, source.Binding,
		workspace.Name)
	if err != nil || !observed.Present || !observed.Clean ||
		observed.Path != workspace.Path || observed.Branch != workspace.Branch ||
		observed.Head != workspace.BaseCommit {
		failed, receipt, transitionErr := s.failCreate(ctx, workspace, source, request,
			gitReceipt, errors.Join(err, errors.New("created Drydock identity was not exact and clean")))
		result.Workspace, result.Receipt = &failed, &receipt
		return result, errors.Join(err, transitionErr,
			apperror.New(apperror.CodeConflict, "created Drydock requires recovery"))
	}
	if err := s.executor.VerifyBaseAncestry(ctx, observed.Path, workspace.BaseCommit,
		observed.Binding.Head); err != nil {
		failed, receipt, transitionErr := s.failCreate(ctx, workspace, source, request,
			gitReceipt, err)
		result.Workspace, result.Receipt = &failed, &receipt
		return result, errors.Join(err, transitionErr)
	}
	createDigest := drydockOperationDigest(drydock.OperationCreate, binding.run.ID,
		request.OperationKey)
	receiptID := drydockReceiptID(createDigest)
	checkpoint, err := s.captureCheckpoint(ctx, workspace, receiptID,
		"Drydock baseline", request.RequestedBy, "create", "")
	if err != nil {
		failed, receipt, transitionErr := s.failCreate(ctx, workspace, source, request,
			gitReceipt, err)
		result.Workspace, result.Receipt = &failed, &receipt
		return result, errors.Join(err, transitionErr)
	}
	beforeGeneration := workspace.Generation
	workspace.RootFingerprint = observed.RootFingerprint
	workspace.ExpectedHead = observed.Binding.Head
	workspace.ExpectedBindingFingerprint = observed.Binding.Fingerprint()
	workspace.CreateGitReceiptID = gitReceipt.ID
	workspace.ManagedWorktreeID = gitReceipt.WorktreeID
	workspace.State = drydock.StateReady
	workspace.Generation++
	workspace.LastCheckpointID = checkpoint.ID
	workspace.UpdatedAt = s.now().UTC()
	receipt := s.transitionReceipt(workspace, beforeGeneration, drydock.OperationCreate,
		createDigest, runmutation.Fingerprint("drydock-create-request.v1", binding.run.ID,
			source.Identity.Fingerprint(), request.RequestedBy), drydock.OutcomeSucceeded,
		"", "created an exact Run-owned Drydock without granting process authority",
		"", observed.Binding.Fingerprint(), gitReceipt.ID, checkpoint.ID, "")
	workspace, replayed, err = s.store.AdvanceDrydock(ctx, workspace, beforeGeneration, receipt)
	if err != nil {
		return result, apperror.Normalize(err)
	}
	result.Workspace, result.Receipt, result.Checkpoint = &workspace, &receipt, &checkpoint
	result.Replayed = replayed
	return result, nil
}

func (s *DrydockService) Use(ctx context.Context,
	request DrydockUseRequest,
) (DrydockUseResult, error) {
	request.RunID = strings.TrimSpace(request.RunID)
	request.OperationKey = strings.TrimSpace(request.OperationKey)
	request.RequestedBy = normalizeDrydockActor(request.RequestedBy)
	if request.RunID == "" || request.OperationKey == "" || request.RequestedBy == "" ||
		request.ExpectedGeneration < 1 {
		return DrydockUseResult{}, apperror.New(apperror.CodeInvalidArgument,
			"Drydock use request is invalid")
	}
	digest := drydockOperationDigest(drydock.OperationUse, request.RunID,
		request.OperationKey)
	if replay, found, err := s.replayUse(ctx, request, digest); found || err != nil {
		return replay, err
	}
	workspace, source, observed, err := s.loadExactDrydock(ctx, request.RunID,
		request.ExpectedGeneration, false)
	if err != nil {
		return DrydockUseResult{}, err
	}
	if workspace.State != drydock.StateReady && workspace.State != drydock.StateDelivered {
		return DrydockUseResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"Drydock is not ready for use")
	}
	if observed.Binding.Fingerprint() != workspace.ExpectedBindingFingerprint {
		preserved, receipt, preserveErr := s.markRecovery(ctx, workspace,
			drydock.OperationUse, digest, request.RequestedBy,
			drydockUseRequestFingerprint(workspace.ID, request), "unattributed_workspace_change",
			"Drydock content changed outside its last attributed generation", &observed)
		return DrydockUseResult{ProtocolVersion: DrydockAPIProtocolVersion,
				Workspace: preserved, Receipt: receipt}, errors.Join(preserveErr,
				apperror.New(apperror.CodeConflict, "Drydock content requires recovery review"))
	}
	beforeGeneration := workspace.Generation
	workspace.Generation++
	workspace.UpdatedAt = s.now().UTC()
	receipt := s.transitionReceipt(workspace, beforeGeneration, drydock.OperationUse,
		digest, drydockUseRequestFingerprint(workspace.ID, request),
		drydock.OutcomeSucceeded, "",
		"attested the exact Drydock binding; no process authority was granted",
		observed.Binding.Fingerprint(), observed.Binding.Fingerprint(), "", "", "")
	workspace, replayed, err := s.store.AdvanceDrydock(ctx, workspace,
		beforeGeneration, receipt)
	if err != nil {
		return DrydockUseResult{}, apperror.Normalize(err)
	}
	_ = source
	return DrydockUseResult{ProtocolVersion: DrydockAPIProtocolVersion,
		Workspace: workspace, Receipt: receipt, RootPath: workspace.Path,
		BindingFingerprint:     observed.Binding.Fingerprint(),
		GrantsProcessAuthority: false, Replayed: replayed}, nil
}

func drydockUseRequestFingerprint(workspaceID string, request DrydockUseRequest) string {
	return runmutation.Fingerprint("drydock-use-request.v1", workspaceID,
		strconv.FormatInt(request.ExpectedGeneration, 10), request.RequestedBy)
}

func (s *DrydockService) Checkpoint(ctx context.Context,
	request DrydockCheckpointRequest,
) (DrydockCheckpointResult, error) {
	request.RunID = strings.TrimSpace(request.RunID)
	request.OperationKey = strings.TrimSpace(request.OperationKey)
	request.RequestedBy = normalizeDrydockActor(request.RequestedBy)
	request.Title = strings.TrimSpace(request.Title)
	if request.RunID == "" || request.OperationKey == "" || request.RequestedBy == "" ||
		request.ExpectedGeneration < 1 {
		return DrydockCheckpointResult{}, apperror.New(apperror.CodeInvalidArgument,
			"Drydock checkpoint request is invalid")
	}
	checkpointDigest := drydockOperationDigest(drydock.OperationCheckpoint, request.RunID,
		request.OperationKey)
	recoverDigest := drydockOperationDigest(drydock.OperationRecover, request.RunID,
		request.OperationKey)
	for _, replayDigest := range []string{checkpointDigest, recoverDigest} {
		receipt, found, replayErr := s.store.GetDrydockReceiptByOperation(ctx, replayDigest)
		if replayErr != nil {
			return DrydockCheckpointResult{}, apperror.Normalize(replayErr)
		}
		if !found {
			continue
		}
		workspace, workspaceFound, getErr := s.store.GetDrydockByRun(ctx, request.RunID)
		if getErr != nil || !workspaceFound {
			return DrydockCheckpointResult{}, apperror.Normalize(getErr)
		}
		expectedFingerprint := drydockCheckpointRequestFingerprint(workspace.ID, request)
		if !drydockReceiptMatchesRequest(receipt, workspace.ID, request.ExpectedGeneration,
			expectedFingerprint) {
			return DrydockCheckpointResult{}, apperror.New(apperror.CodeConflict,
				"Drydock checkpoint operation key was reused for different intent")
		}
		result := DrydockCheckpointResult{ProtocolVersion: DrydockAPIProtocolVersion,
			Workspace: workspace, Receipt: receipt, Replayed: true}
		if receipt.Outcome != drydock.OutcomeSucceeded || receipt.CheckpointID == "" {
			return result, apperror.New(apperror.CodeConflict,
				"the previous checkpoint request preserved the Drydock for recovery")
		}
		snapshot, getErr := s.store.GetWorkspaceCheckpointSnapshot(ctx, receipt.CheckpointID)
		result.Checkpoint = snapshot.Checkpoint
		return result, apperror.Normalize(getErr)
	}
	workspace, _, observed, err := s.loadExactDrydock(ctx, request.RunID,
		request.ExpectedGeneration, true)
	if err != nil {
		return DrydockCheckpointResult{}, err
	}
	if workspace.State == drydock.StateCleaned || workspace.State == drydock.StatePreparing {
		return DrydockCheckpointResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"Drydock cannot be checkpointed in its current state")
	}
	operation := drydock.OperationCheckpoint
	digest := checkpointDigest
	if workspace.State == drydock.StateRecoveryRequired {
		operation = drydock.OperationRecover
		digest = recoverDigest
	}
	changed := observed.Binding.Fingerprint() != workspace.ExpectedBindingFingerprint
	if (changed || workspace.State == drydock.StateRecoveryRequired) &&
		!request.ConfirmObservedChanges {
		preserved, receipt, preserveErr := s.markRecovery(ctx, workspace,
			operation, digest, request.RequestedBy,
			drydockCheckpointRequestFingerprint(workspace.ID, request),
			"unconfirmed_workspace_change",
			"Drydock changed after its last receipt and was preserved for recovery", &observed)
		return DrydockCheckpointResult{ProtocolVersion: DrydockAPIProtocolVersion,
				Workspace: preserved, Receipt: receipt}, errors.Join(preserveErr,
				apperror.New(apperror.CodeConflict, "observed changes require explicit confirmation"))
	}
	receiptID := drydockReceiptID(digest)
	title := request.Title
	if title == "" {
		title = "Drydock checkpoint"
	}
	checkpoint, err := s.captureCheckpoint(ctx, workspace, receiptID, title,
		request.RequestedBy, string(operation), workspace.LastCheckpointID)
	if err != nil {
		return DrydockCheckpointResult{}, apperror.Normalize(err)
	}
	beforeGeneration := workspace.Generation
	previousBinding := workspace.ExpectedBindingFingerprint
	workspace.RootFingerprint = observed.RootFingerprint
	workspace.ExpectedHead = observed.Binding.Head
	workspace.ExpectedBindingFingerprint = observed.Binding.Fingerprint()
	workspace.State = drydock.StateReady
	workspace.RecoveryReason = ""
	workspace.Generation++
	workspace.LastCheckpointID = checkpoint.ID
	workspace.UpdatedAt = s.now().UTC()
	receipt := s.transitionReceipt(workspace, beforeGeneration, operation, digest,
		drydockCheckpointRequestFingerprint(workspace.ID, request),
		drydock.OutcomeSucceeded, "", "captured tracked, untracked, and raw Git index state",
		previousBinding, observed.Binding.Fingerprint(), "", checkpoint.ID, "")
	workspace, replayed, err := s.store.AdvanceDrydock(ctx, workspace,
		beforeGeneration, receipt)
	if err != nil {
		return DrydockCheckpointResult{}, apperror.Normalize(err)
	}
	return DrydockCheckpointResult{ProtocolVersion: DrydockAPIProtocolVersion,
		Workspace: workspace, Checkpoint: checkpoint, Receipt: receipt,
		Replayed: replayed}, nil
}

func drydockCheckpointRequestFingerprint(workspaceID string,
	request DrydockCheckpointRequest,
) string {
	title := strings.TrimSpace(request.Title)
	if title == "" {
		title = "Drydock checkpoint"
	}
	return runmutation.Fingerprint("drydock-checkpoint-request.v1", workspaceID,
		strconv.FormatInt(request.ExpectedGeneration, 10), request.RequestedBy,
		fmt.Sprintf("%t", request.ConfirmObservedChanges), title)
}

func drydockReceiptMatchesRequest(receipt drydock.Receipt, workspaceID string,
	expectedGeneration int64, expectedFingerprint string,
) bool {
	if receipt.DrydockID != workspaceID || receipt.GenerationBefore != expectedGeneration {
		return false
	}
	return receipt.RequestFingerprint == expectedFingerprint
}

func (s *DrydockService) Rewind(ctx context.Context,
	request DrydockRewindRequest,
) (DrydockRewindResult, error) {
	return s.rewind(ctx, request, drydock.OperationRewind)
}

func (s *DrydockService) Undo(ctx context.Context,
	request DrydockUndoRequest,
) (DrydockRewindResult, error) {
	request.RunID = strings.TrimSpace(request.RunID)
	request.OperationKey = strings.TrimSpace(request.OperationKey)
	request.RequestedBy = normalizeDrydockActor(request.RequestedBy)
	if request.RunID == "" || request.OperationKey == "" || request.RequestedBy == "" ||
		request.ExpectedGeneration < 1 {
		return DrydockRewindResult{}, apperror.New(apperror.CodeInvalidArgument,
			"Drydock undo request is invalid")
	}
	digest := drydockOperationDigest(drydock.OperationUndo, request.RunID,
		request.OperationKey)
	if receipt, receiptFound, receiptErr := s.store.GetDrydockReceiptByOperation(ctx,
		digest); receiptErr != nil {
		return DrydockRewindResult{}, apperror.Normalize(receiptErr)
	} else if receiptFound && receipt.Outcome == drydock.OutcomeSucceeded {
		if receipt.Operation != drydock.OperationUndo || receipt.CheckpointID == "" {
			return DrydockRewindResult{}, apperror.New(apperror.CodeConflict,
				"stored Drydock undo receipt is invalid")
		}
		after, getErr := s.store.GetWorkspaceCheckpointSnapshot(ctx, receipt.CheckpointID)
		if getErr != nil || after.Checkpoint.ParentCheckpointID == "" {
			return DrydockRewindResult{}, apperror.Normalize(errors.Join(getErr,
				errors.New("stored Drydock undo after-checkpoint is incomplete")))
		}
		before, getErr := s.store.GetWorkspaceCheckpointSnapshot(ctx,
			after.Checkpoint.ParentCheckpointID)
		if getErr != nil || before.Checkpoint.ParentCheckpointID == "" {
			return DrydockRewindResult{}, apperror.Normalize(errors.Join(getErr,
				errors.New("stored Drydock undo before-checkpoint is incomplete")))
		}
		return s.rewind(ctx, DrydockRewindRequest{RunID: request.RunID,
			TargetCheckpointID: before.Checkpoint.ParentCheckpointID,
			ExpectedGeneration: request.ExpectedGeneration,
			OperationKey:       request.OperationKey, RequestedBy: request.RequestedBy,
			Confirm:                request.Confirm,
			ConfirmObservedChanges: request.ConfirmObservedChanges}, drydock.OperationUndo)
	}
	workspace, found, err := s.store.GetDrydockByRun(ctx, request.RunID)
	if err != nil || !found {
		if err == nil {
			err = apperror.New(apperror.CodeNotFound, "Drydock was not found for this Run")
		}
		return DrydockRewindResult{}, apperror.Normalize(err)
	}
	if workspace.LastCheckpointID == "" {
		return DrydockRewindResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"Drydock has no checkpoint to undo")
	}
	current, err := s.store.GetWorkspaceCheckpointSnapshot(ctx, workspace.LastCheckpointID)
	if err != nil {
		return DrydockRewindResult{}, apperror.Normalize(err)
	}
	if current.Checkpoint.ParentCheckpointID == "" {
		return DrydockRewindResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"Drydock checkpoint has no previous state to undo to")
	}
	return s.rewind(ctx, DrydockRewindRequest{RunID: request.RunID,
		TargetCheckpointID: current.Checkpoint.ParentCheckpointID,
		ExpectedGeneration: request.ExpectedGeneration, OperationKey: request.OperationKey,
		RequestedBy: request.RequestedBy, Confirm: request.Confirm,
		ConfirmObservedChanges: request.ConfirmObservedChanges}, drydock.OperationUndo)
}

func (s *DrydockService) rewind(ctx context.Context, request DrydockRewindRequest,
	operation drydock.Operation,
) (DrydockRewindResult, error) {
	request.RunID = strings.TrimSpace(request.RunID)
	request.TargetCheckpointID = strings.TrimSpace(request.TargetCheckpointID)
	request.OperationKey = strings.TrimSpace(request.OperationKey)
	request.RequestedBy = normalizeDrydockActor(request.RequestedBy)
	if (operation != drydock.OperationRewind && operation != drydock.OperationUndo) ||
		request.RunID == "" || request.TargetCheckpointID == "" ||
		request.OperationKey == "" || request.RequestedBy == "" ||
		request.ExpectedGeneration < 1 {
		return DrydockRewindResult{}, apperror.New(apperror.CodeInvalidArgument,
			"Drydock rewind request is invalid")
	}
	digest := drydockOperationDigest(operation, request.RunID, request.OperationKey)
	if request.Confirm {
		if stored, found, err := s.store.GetDrydockReceiptByOperation(ctx, digest); err != nil {
			return DrydockRewindResult{}, apperror.Normalize(err)
		} else if found {
			workspace, workspaceFound, getErr := s.store.GetDrydockByRun(ctx, request.RunID)
			if getErr != nil || !workspaceFound {
				return DrydockRewindResult{}, apperror.Normalize(getErr)
			}
			fingerprint := drydockRewindRequestFingerprint(workspace.ID, operation, request)
			if !drydockReceiptMatchesRequest(stored, workspace.ID,
				request.ExpectedGeneration, fingerprint) {
				return DrydockRewindResult{}, apperror.New(apperror.CodeConflict,
					"Drydock rewind operation key was reused for different intent")
			}
			if stored.Outcome != drydock.OutcomeSucceeded || stored.CheckpointID == "" {
				return DrydockRewindResult{ProtocolVersion: DrydockAPIProtocolVersion,
						Workspace: workspace, Receipt: &stored, Confirmed: true, Replayed: true},
					apperror.New(apperror.CodeConflict,
						"the previous rewind request preserved the Drydock for recovery")
			}
			after, getErr := s.store.GetWorkspaceCheckpointSnapshot(ctx, stored.CheckpointID)
			if getErr != nil {
				return DrydockRewindResult{}, apperror.Normalize(getErr)
			}
			before, getErr := s.store.GetWorkspaceCheckpointSnapshot(ctx,
				after.Checkpoint.ParentCheckpointID)
			if getErr != nil {
				return DrydockRewindResult{}, apperror.Normalize(getErr)
			}
			target, getErr := s.store.GetWorkspaceCheckpointSnapshot(ctx,
				request.TargetCheckpointID)
			return DrydockRewindResult{ProtocolVersion: DrydockAPIProtocolVersion,
				Workspace: workspace, Target: target.Checkpoint, Before: before.Checkpoint,
				After: &after.Checkpoint, Receipt: &stored, Confirmed: true,
				Replayed: true}, apperror.Normalize(getErr)
		}
	}
	workspace, source, observed, err := s.loadExactDrydock(ctx, request.RunID,
		request.ExpectedGeneration, false)
	if err != nil {
		return DrydockRewindResult{}, err
	}
	if workspace.State != drydock.StateReady && workspace.State != drydock.StateDelivered {
		return DrydockRewindResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"Drydock is not ready for rewind")
	}
	current, err := s.store.GetWorkspaceCheckpointSnapshot(ctx, workspace.LastCheckpointID)
	if err != nil {
		return DrydockRewindResult{}, apperror.Normalize(err)
	}
	target, err := s.store.GetWorkspaceCheckpointSnapshot(ctx, request.TargetCheckpointID)
	if err != nil {
		return DrydockRewindResult{}, apperror.Normalize(err)
	}
	if current.Checkpoint.RunID != workspace.RunID ||
		current.Checkpoint.WorkspaceID != workspace.WorkspaceID ||
		target.Checkpoint.RunID != workspace.RunID ||
		target.Checkpoint.WorkspaceID != workspace.WorkspaceID ||
		current.Checkpoint.BaseCommit != observed.Binding.Head ||
		target.Checkpoint.BaseCommit != current.Checkpoint.BaseCommit ||
		current.Checkpoint.Branch != workspace.Branch ||
		target.Checkpoint.Branch != workspace.Branch ||
		target.Checkpoint.RecoveryLevel == workspacecheckpoint.RecoveryUnavailable {
		return DrydockRewindResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"target checkpoint is not materializable in this exact Drydock")
	}
	if observed.Binding.Fingerprint() != workspace.ExpectedBindingFingerprint &&
		!request.ConfirmObservedChanges {
		if !request.Confirm {
			return DrydockRewindResult{ProtocolVersion: DrydockAPIProtocolVersion,
					Workspace: workspace, Target: target.Checkpoint,
					Before: current.Checkpoint}, apperror.New(apperror.CodeConflict,
					"Drydock changed after its last attributed checkpoint")
		}
		preserved, receipt, preserveErr := s.markRecovery(ctx, workspace, operation,
			digest, request.RequestedBy,
			drydockRewindRequestFingerprint(workspace.ID, operation, request),
			"unattributed_workspace_change",
			"rewind refused content not attributed to the last Drydock checkpoint", &observed)
		return DrydockRewindResult{ProtocolVersion: DrydockAPIProtocolVersion,
				Workspace: preserved, Target: target.Checkpoint, Before: current.Checkpoint,
				Receipt: &receipt, Confirmed: true}, errors.Join(preserveErr,
				apperror.New(apperror.CodeConflict,
					"Drydock rewind requires explicit attribution of observed changes"))
	}
	observedSnapshot, err := workspacecheckpoint.Capture(ctx,
		workspacecheckpoint.CaptureRequest{ID: "drydock-rewind-observed-" + digest[:24],
			RunID: workspace.RunID, MissionID: workspace.MissionID,
			SessionID: workspace.SessionID, WorkspaceID: workspace.WorkspaceID,
			WorkspaceRoot: workspace.Path, Trigger: workspacecheckpoint.TriggerRewindPreflight,
			Phase:              workspacecheckpoint.PhasePreflight,
			TriggerReceiptID:   request.TargetCheckpointID,
			ParentCheckpointID: current.Checkpoint.ID, RequestedBy: request.RequestedBy,
			CreatedAt: s.now().UTC()})
	if err != nil {
		return DrydockRewindResult{}, apperror.Normalize(err)
	}
	expectedSnapshot := current
	if current.Checkpoint.IndexSHA256 != observedSnapshot.Checkpoint.IndexSHA256 &&
		drydockIndexProjectionEqual(current, observedSnapshot) {
		// Git may rewrite stat-cache-only bytes in an otherwise identical index.
		// Use the freshly observed raw index as the CAS input only when every
		// projected path/OID/mode/staged value is still exact.
		expectedSnapshot.Checkpoint.IndexSHA256 = observedSnapshot.Checkpoint.IndexSHA256
		expectedSnapshot.Checkpoint.IndexBlobSHA256 = observedSnapshot.Checkpoint.IndexBlobSHA256
		expectedSnapshot.Blobs = observedSnapshot.Blobs
	}
	preview, previewErr := workspacecheckpoint.PreviewRestore(expectedSnapshot, target,
		observedSnapshot)
	result := DrydockRewindResult{ProtocolVersion: DrydockAPIProtocolVersion,
		Workspace: workspace, Target: target.Checkpoint, Before: current.Checkpoint,
		Preview: preview}
	if !request.Confirm {
		// Preview conflicts are data for the operator; no write has occurred.
		return result, nil
	}
	if previewErr != nil || len(preview.Conflicts) != 0 {
		if previewErr == nil {
			previewErr = &workspacecheckpoint.ConflictError{Conflicts: preview.Conflicts}
		}
		return result, apperror.Normalize(previewErr)
	}
	_, applyErr := workspacecheckpoint.ApplyRestore(ctx, workspace.Path, expectedSnapshot,
		target, observedSnapshot)
	if applyErr != nil {
		post, inspectErr := s.executor.Inspect(ctx, source.Identity.RootPath,
			source.Binding, workspace.Name)
		preserved, receipt, preserveErr := s.markRecovery(ctx, workspace, operation,
			digest, request.RequestedBy,
			drydockRewindRequestFingerprint(workspace.ID, operation, request),
			"rewind_apply_failed",
			"a partial or conflicted rewind was preserved for operator recovery", &post)
		result.Workspace, result.Receipt, result.Confirmed = preserved, &receipt, true
		return result, errors.Join(apperror.Normalize(applyErr), apperror.Normalize(inspectErr),
			preserveErr)
	}
	receiptID := drydockReceiptID(digest)
	afterSnapshot, captureErr := workspacecheckpoint.Capture(ctx,
		workspacecheckpoint.CaptureRequest{ID: "drydock-checkpoint-" + digest[:32],
			RunID: workspace.RunID, MissionID: workspace.MissionID,
			SessionID: workspace.SessionID, WorkspaceID: workspace.WorkspaceID,
			WorkspaceRoot: workspace.Path, Trigger: workspacecheckpoint.TriggerRewindResult,
			Phase: workspacecheckpoint.PhaseAfter, TriggerReceiptID: receiptID,
			ParentCheckpointID: current.Checkpoint.ID, RequestedBy: request.RequestedBy,
			Title: "Drydock " + string(operation), CreatedAt: s.now().UTC()})
	if captureErr == nil && (afterSnapshot.Checkpoint.ManifestSHA256 !=
		target.Checkpoint.ManifestSHA256 || afterSnapshot.Checkpoint.IndexSHA256 !=
		target.Checkpoint.IndexSHA256) {
		captureErr = errors.New("Drydock rewind final checkpoint does not match its target")
	}
	if captureErr != nil {
		post, inspectErr := s.executor.Inspect(ctx, source.Identity.RootPath,
			source.Binding, workspace.Name)
		preserved, receipt, preserveErr := s.markRecovery(ctx, workspace, operation,
			digest, request.RequestedBy,
			drydockRewindRequestFingerprint(workspace.ID, operation, request),
			"rewind_verification_failed",
			"rewind output was preserved because exact checkpoint verification failed", &post)
		result.Workspace, result.Receipt, result.Confirmed = preserved, &receipt, true
		return result, errors.Join(apperror.Normalize(captureErr),
			apperror.Normalize(inspectErr), preserveErr)
	}
	afterCheckpoint, _, err := s.store.CreateWorkspaceCheckpoint(ctx, afterSnapshot)
	if err != nil {
		return result, apperror.Normalize(err)
	}
	post, err := s.executor.Inspect(ctx, source.Identity.RootPath, source.Binding,
		workspace.Name)
	validationErr := s.validateObservation(ctx, workspace, post)
	if err != nil || validationErr != nil {
		preserved, receipt, preserveErr := s.markRecovery(ctx, workspace, operation,
			digest, request.RequestedBy,
			drydockRewindRequestFingerprint(workspace.ID, operation, request),
			"rewind_identity_failed",
			"rewind output was preserved because its root identity could not be reverified", &post)
		result.Workspace, result.Receipt, result.Confirmed = preserved, &receipt, true
		return result, errors.Join(apperror.Normalize(err),
			apperror.Normalize(validationErr), preserveErr)
	}
	beforeGeneration := workspace.Generation
	previousBinding := workspace.ExpectedBindingFingerprint
	workspace.State = drydock.StateReady
	workspace.Generation++
	workspace.LastCheckpointID = afterCheckpoint.ID
	workspace.RootFingerprint = post.RootFingerprint
	workspace.ExpectedHead = post.Binding.Head
	workspace.ExpectedBindingFingerprint = post.Binding.Fingerprint()
	workspace.UpdatedAt = s.now().UTC()
	receipt := s.transitionReceipt(workspace, beforeGeneration, operation, digest,
		drydockRewindRequestFingerprint(workspace.ID, operation, request),
		drydock.OutcomeSucceeded, "", "restored a reviewed Drydock checkpoint with exact tracked, untracked, and index verification",
		previousBinding, post.Binding.Fingerprint(), "", afterCheckpoint.ID, "")
	workspace, replayed, err := s.store.AdvanceDrydock(ctx, workspace,
		beforeGeneration, receipt)
	if err != nil {
		return result, apperror.Normalize(err)
	}
	result.Workspace, result.After, result.Receipt = workspace, &afterCheckpoint, &receipt
	result.Confirmed, result.Replayed = true, replayed
	return result, nil
}

func drydockRewindRequestFingerprint(workspaceID string, operation drydock.Operation,
	request DrydockRewindRequest,
) string {
	return runmutation.Fingerprint("drydock-rewind-request.v1", workspaceID,
		string(operation), request.TargetCheckpointID,
		strconv.FormatInt(request.ExpectedGeneration, 10), request.RequestedBy,
		fmt.Sprintf("%t", request.ConfirmObservedChanges))
}

func drydockIndexProjectionEqual(left, right workspacecheckpoint.Snapshot) bool {
	type indexEntry struct {
		OID    string
		Mode   string
		Staged bool
	}
	project := func(snapshot workspacecheckpoint.Snapshot) map[string]indexEntry {
		values := make(map[string]indexEntry)
		for _, entry := range snapshot.Entries {
			if !entry.Tracked && entry.IndexOID == "" && entry.IndexMode == "" {
				continue
			}
			values[entry.Path] = indexEntry{OID: entry.IndexOID,
				Mode: entry.IndexMode, Staged: entry.Staged}
		}
		return values
	}
	first, second := project(left), project(right)
	if len(first) != len(second) {
		return false
	}
	for path, value := range first {
		if second[path] != value {
			return false
		}
	}
	return true
}

func (s *DrydockService) Fork(ctx context.Context,
	request DrydockForkRequest,
) (DrydockForkResult, error) {
	request.RunID = strings.TrimSpace(request.RunID)
	request.TargetCheckpointID = strings.TrimSpace(request.TargetCheckpointID)
	request.ExpectedCurrentCheckpointID = strings.TrimSpace(
		request.ExpectedCurrentCheckpointID)
	request.OperationKey = strings.TrimSpace(request.OperationKey)
	request.RequestedBy = normalizeDrydockActor(request.RequestedBy)
	request.WorkspaceName = strings.TrimSpace(request.WorkspaceName)
	request.WorkspaceRoot = strings.TrimSpace(request.WorkspaceRoot)
	request.Branch = strings.TrimSpace(request.Branch)
	request.Goal = strings.TrimSpace(request.Goal)
	if s == nil || s.checkpoints == nil || request.RunID == "" ||
		request.TargetCheckpointID == "" || request.ExpectedCurrentCheckpointID == "" ||
		request.ExpectedGeneration < 1 || request.OperationKey == "" ||
		request.RequestedBy == "" || request.WorkspaceName == "" ||
		request.WorkspaceRoot == "" || request.Branch == "" || !request.Confirm {
		return DrydockForkResult{}, apperror.New(apperror.CodeInvalidArgument,
			"Drydock fork request is invalid or its checkpoint integration is unavailable")
	}
	digest := drydockOperationDigest(drydock.OperationFork, request.RunID,
		request.OperationKey)
	checkpointRequest := WorkspaceForkRequest{RunID: request.RunID,
		TargetCheckpointID:          request.TargetCheckpointID,
		ExpectedCurrentCheckpointID: request.ExpectedCurrentCheckpointID,
		OperationKey:                request.OperationKey, RequestedBy: request.RequestedBy,
		WorkspaceName: request.WorkspaceName, WorkspaceRoot: request.WorkspaceRoot,
		Branch: request.Branch, Goal: request.Goal, Confirm: true}
	if receipt, found, err := s.store.GetDrydockReceiptByOperation(ctx, digest); err != nil {
		return DrydockForkResult{}, apperror.Normalize(err)
	} else if found {
		workspace, workspaceFound, getErr := s.store.GetDrydockByRun(ctx, request.RunID)
		if getErr != nil || !workspaceFound {
			return DrydockForkResult{}, apperror.Normalize(getErr)
		}
		if receipt.RequestFingerprint != drydockForkRequestFingerprint(workspace.ID,
			request) {
			return DrydockForkResult{}, apperror.New(apperror.CodeConflict,
				"Drydock fork operation key was reused for different intent")
		}
		forked, forkErr := s.forkOwnedCheckpoint(ctx, workspace, checkpointRequest)
		return DrydockForkResult{ProtocolVersion: DrydockAPIProtocolVersion,
			Workspace: workspace, Fork: forked, Receipt: receipt,
			Replayed: true}, forkErr
	}
	workspace, _, observed, err := s.loadExactDrydock(ctx, request.RunID,
		request.ExpectedGeneration, false)
	if err != nil {
		return DrydockForkResult{}, err
	}
	if workspace.LastCheckpointID != request.ExpectedCurrentCheckpointID ||
		observed.Binding.Fingerprint() != workspace.ExpectedBindingFingerprint {
		return DrydockForkResult{}, apperror.New(apperror.CodeConflict,
			"Drydock changed after the fork checkpoint was reviewed")
	}
	target, err := s.store.GetWorkspaceCheckpointSnapshot(ctx,
		request.TargetCheckpointID)
	if err != nil {
		return DrydockForkResult{}, apperror.Normalize(err)
	}
	if target.Checkpoint.RunID != workspace.RunID ||
		target.Checkpoint.WorkspaceID != workspace.WorkspaceID ||
		target.Checkpoint.Branch != workspace.Branch ||
		target.Checkpoint.RecoveryLevel == workspacecheckpoint.RecoveryUnavailable {
		return DrydockForkResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"fork checkpoint is not materializable in this exact Drydock")
	}
	if err := s.executor.VerifyBaseAncestry(ctx, workspace.Path, workspace.BaseCommit,
		target.Checkpoint.BaseCommit); err != nil {
		return DrydockForkResult{}, apperror.Wrap(apperror.CodeFailedPrecondition,
			"fork checkpoint no longer descends from the fixed Drydock base", err)
	}
	forked, err := s.forkOwnedCheckpoint(ctx, workspace, checkpointRequest)
	if err != nil {
		return DrydockForkResult{}, err
	}
	verified, _, after, err := s.loadExactDrydock(ctx, request.RunID,
		request.ExpectedGeneration, false)
	if err != nil || after.Binding.Fingerprint() != observed.Binding.Fingerprint() {
		return DrydockForkResult{ProtocolVersion: DrydockAPIProtocolVersion,
				Workspace: verified, Fork: forked}, errors.Join(err,
				apperror.New(apperror.CodeConflict,
					"Drydock changed while the independent fork was materialized"))
	}
	beforeGeneration := verified.Generation
	verified.Generation++
	verified.UpdatedAt = s.now().UTC()
	receipt := s.transitionReceipt(verified, beforeGeneration, drydock.OperationFork,
		digest, drydockForkRequestFingerprint(verified.ID, request),
		drydock.OutcomeSucceeded, "",
		"materialized a checkpoint into a distinct authority-reset Run and branch without changing the source Drydock",
		observed.Binding.Fingerprint(), after.Binding.Fingerprint(), "",
		request.TargetCheckpointID, "")
	verified, replayed, err := s.store.AdvanceDrydock(ctx, verified,
		beforeGeneration, receipt)
	if err != nil {
		return DrydockForkResult{ProtocolVersion: DrydockAPIProtocolVersion,
			Workspace: verified, Fork: forked}, apperror.Normalize(err)
	}
	return DrydockForkResult{ProtocolVersion: DrydockAPIProtocolVersion,
		Workspace: verified, Fork: forked, Receipt: receipt,
		Replayed: replayed || forked.Replayed}, nil
}

func (s *DrydockService) forkOwnedCheckpoint(ctx context.Context,
	workspace drydock.Workspace, request WorkspaceForkRequest,
) (WorkspaceForkResult, error) {
	return s.checkpoints.forkFromOwnedCheckpoint(ctx, request,
		workspacecheckpoint.RunState{RunID: workspace.RunID,
			WorkspaceID: workspace.WorkspaceID, CurrentCheckpointID: workspace.LastCheckpointID,
			LastTransactionID: "", UpdatedAt: s.now().UTC()})
}

func drydockForkRequestFingerprint(workspaceID string, request DrydockForkRequest) string {
	return runmutation.Fingerprint("drydock-fork-request.v1", workspaceID,
		request.TargetCheckpointID, request.ExpectedCurrentCheckpointID,
		strconv.FormatInt(request.ExpectedGeneration, 10), request.RequestedBy,
		request.WorkspaceName, filepath.Clean(request.WorkspaceRoot), request.Branch,
		request.Goal)
}

func (s *DrydockService) Deliver(ctx context.Context,
	request DrydockDeliveryRequest,
) (DrydockDeliveryResult, error) {
	request.RunID = strings.TrimSpace(request.RunID)
	request.OperationKey = strings.TrimSpace(request.OperationKey)
	request.RequestedBy = normalizeDrydockActor(request.RequestedBy)
	if request.RunID == "" || request.OperationKey == "" || request.RequestedBy == "" ||
		request.ExpectedGeneration < 1 || !request.Confirm {
		return DrydockDeliveryResult{}, apperror.New(apperror.CodeInvalidArgument,
			"Drydock delivery requires an exact generation and explicit review confirmation")
	}
	digest := drydockOperationDigest(drydock.OperationDeliver, request.RunID,
		request.OperationKey)
	if receipt, found, err := s.store.GetDrydockReceiptByOperation(ctx, digest); err != nil {
		return DrydockDeliveryResult{}, apperror.Normalize(err)
	} else if found {
		return s.replayDelivery(ctx, request, receipt)
	}
	workspace, _, observed, err := s.loadExactDrydock(ctx, request.RunID,
		request.ExpectedGeneration, false)
	if err != nil {
		return DrydockDeliveryResult{}, err
	}
	if workspace.State != drydock.StateReady && workspace.State != drydock.StateDelivered {
		return DrydockDeliveryResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"Drydock is not ready for delivery")
	}
	if observed.Binding.Fingerprint() != workspace.ExpectedBindingFingerprint {
		preserved, receipt, preserveErr := s.markRecovery(ctx, workspace,
			drydock.OperationDeliver, digest, request.RequestedBy,
			drydockDeliveryRequestFingerprint(workspace.ID, request),
			"unattributed_workspace_change",
			"delivery refused content not covered by the last Drydock checkpoint", &observed)
		return DrydockDeliveryResult{ProtocolVersion: DrydockAPIProtocolVersion,
				Workspace: preserved, Receipt: receipt}, errors.Join(preserveErr,
				apperror.New(apperror.CodeConflict, "delivery requires a fresh attributed checkpoint"))
	}
	receiptID := drydockReceiptID(digest)
	checkpoint, err := s.captureCheckpoint(ctx, workspace, receiptID,
		"Drydock delivery", request.RequestedBy, "deliver", workspace.LastCheckpointID)
	if err != nil {
		return DrydockDeliveryResult{}, apperror.Normalize(err)
	}
	evidence, err := s.executor.CaptureDelivery(ctx, workspace.Path, workspace.BaseCommit)
	if err != nil {
		return DrydockDeliveryResult{}, apperror.Normalize(err)
	}
	if evidence.Binding.Fingerprint() != observed.Binding.Fingerprint() {
		return DrydockDeliveryResult{}, apperror.New(apperror.CodeConflict,
			"Drydock changed after its delivery checkpoint")
	}
	proposalID := "drydock-delivery-" + digest[:32]
	proposal := drydock.DeliveryProposal{ID: proposalID,
		ProtocolVersion:    drydock.DeliveryProtocolVersion,
		OperationKeySHA256: digest,
		RequestFingerprint: drydockDeliveryRequestFingerprint(workspace.ID, request),
		DrydockID:          workspace.ID, RunID: workspace.RunID,
		Generation:           workspace.Generation + 1,
		SourceIdentitySHA256: workspace.Source.Fingerprint(),
		RootFingerprint:      workspace.RootFingerprint, BaseCommit: workspace.BaseCommit,
		HeadCommit: evidence.HeadCommit, MergeBaseCommit: evidence.MergeBaseCommit,
		BindingFingerprint: evidence.Binding.Fingerprint(),
		DiffSHA256:         drydock.FingerprintBytes([]byte(evidence.Patch)),
		DiffBytes:          len([]byte(evidence.Patch)), DiffStat: evidence.DiffStat,
		ChangedPaths: evidence.ChangedPaths, CheckpointID: checkpoint.ID,
		CreatedBy: request.RequestedBy, AutomaticMerge: false,
		PushAuthorized: false, ForceAuthorized: false,
		SourceOverwriteAllowed: false, CreatedAt: s.now().UTC()}
	review := drydock.Review{Proposal: proposal, Patch: evidence.Patch}
	if err := review.Validate(); err != nil {
		return DrydockDeliveryResult{}, apperror.Normalize(err)
	}
	beforeGeneration := workspace.Generation
	workspace.State = drydock.StateDelivered
	workspace.Generation++
	workspace.LastCheckpointID = checkpoint.ID
	workspace.LastDeliveryID = proposal.ID
	workspace.ExpectedHead = evidence.Binding.Head
	workspace.ExpectedBindingFingerprint = evidence.Binding.Fingerprint()
	workspace.UpdatedAt = s.now().UTC()
	receipt := s.transitionReceipt(workspace, beforeGeneration, drydock.OperationDeliver,
		digest, proposal.RequestFingerprint, drydock.OutcomeSucceeded, "",
		"recorded a review-only Diff proposal without push, force, merge, or source overwrite authority",
		observed.Binding.Fingerprint(), evidence.Binding.Fingerprint(), "",
		checkpoint.ID, proposal.ID)
	proposal, workspace, replayed, err := s.store.CreateDrydockDelivery(ctx,
		proposal, workspace, beforeGeneration, receipt)
	if err != nil {
		return DrydockDeliveryResult{}, apperror.Normalize(err)
	}
	review.Proposal = proposal
	return DrydockDeliveryResult{ProtocolVersion: DrydockAPIProtocolVersion,
		Workspace: workspace, Review: review, Receipt: receipt, Replayed: replayed}, nil
}

func drydockDeliveryRequestFingerprint(workspaceID string,
	request DrydockDeliveryRequest,
) string {
	return runmutation.Fingerprint("drydock-delivery-request.v1", workspaceID,
		strconv.FormatInt(request.ExpectedGeneration, 10), request.RequestedBy)
}

func (s *DrydockService) Cleanup(ctx context.Context,
	request DrydockCleanupRequest,
) (DrydockCleanupResult, error) {
	request.RunID = strings.TrimSpace(request.RunID)
	request.OperationKey = strings.TrimSpace(request.OperationKey)
	request.RequestedBy = normalizeDrydockActor(request.RequestedBy)
	if request.RunID == "" || request.OperationKey == "" || request.RequestedBy == "" ||
		request.ExpectedGeneration < 1 || !request.Confirm {
		return DrydockCleanupResult{}, apperror.New(apperror.CodeInvalidArgument,
			"Drydock cleanup requires an exact generation and explicit confirmation")
	}
	digest := drydockOperationDigest(drydock.OperationCleanup, request.RunID,
		request.OperationKey)
	if receipt, found, err := s.store.GetDrydockReceiptByOperation(ctx, digest); err != nil {
		return DrydockCleanupResult{}, apperror.Normalize(err)
	} else if found {
		workspace, workspaceFound, getErr := s.store.GetDrydockByRun(ctx, request.RunID)
		if getErr != nil || !workspaceFound {
			return DrydockCleanupResult{}, apperror.Normalize(getErr)
		}
		if receipt.Operation != drydock.OperationCleanup ||
			!drydockReceiptMatchesRequest(receipt, workspace.ID,
				request.ExpectedGeneration,
				drydockCleanupRequestFingerprint(workspace.ID, request)) {
			return DrydockCleanupResult{}, apperror.New(apperror.CodeConflict,
				"Drydock cleanup operation key was reused for different intent")
		}
		return DrydockCleanupResult{ProtocolVersion: DrydockAPIProtocolVersion,
			Workspace: workspace, Receipt: receipt,
			Preserved: receipt.Outcome == drydock.OutcomePreserved, Replayed: true}, nil
	}
	workspace, _, observed, err := s.loadDrydockForCleanup(ctx, request.RunID,
		request.ExpectedGeneration)
	if err != nil {
		return DrydockCleanupResult{}, err
	}
	if workspace.State == drydock.StateCleaned {
		return DrydockCleanupResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"Drydock is already cleaned")
	}
	if !observed.Found && !observed.Present {
		now := s.now().UTC()
		beforeGeneration := workspace.Generation
		workspace.State = drydock.StateCleaned
		workspace.RecoveryReason = ""
		workspace.Generation++
		workspace.UpdatedAt = now
		workspace.CleanedAt = &now
		receipt := s.transitionReceipt(workspace, beforeGeneration,
			drydock.OperationCleanup, digest,
			drydockCleanupRequestFingerprint(workspace.ID, request),
			drydock.OutcomeSucceeded, "",
			"closed an exactly absent Drydock registration after explicit confirmation; no filesystem entry was deleted",
			workspace.ExpectedBindingFingerprint, "", "", "", "")
		workspace, replayed, advanceErr := s.store.AdvanceDrydock(ctx, workspace,
			beforeGeneration, receipt)
		if advanceErr != nil {
			return DrydockCleanupResult{}, apperror.Normalize(advanceErr)
		}
		return DrydockCleanupResult{ProtocolVersion: DrydockAPIProtocolVersion,
			Workspace: workspace, Receipt: receipt, Replayed: replayed}, nil
	}
	if workspace.State == drydock.StatePreparing || workspace.State == drydock.StateRecoveryRequired ||
		!observed.Clean || observed.Binding.Fingerprint() != workspace.ExpectedBindingFingerprint {
		preserved, receipt, preserveErr := s.markRecovery(ctx, workspace,
			drydock.OperationCleanup, digest, request.RequestedBy,
			drydockCleanupRequestFingerprint(workspace.ID, request),
			"cleanup_identity_or_content_drift",
			"cleanup preserved a changed or uncertain Drydock for operator recovery", &observed)
		return DrydockCleanupResult{ProtocolVersion: DrydockAPIProtocolVersion,
			Workspace: preserved, Receipt: receipt, Preserved: true}, preserveErr
	}
	preview, err := s.executor.PlanRemove(ctx, workspace.Source.RootPath,
		workspace.Name, workspace.ManagedWorktreeID)
	if err != nil || !preview.Executable() {
		preserved, receipt, preserveErr := s.markRecovery(ctx, workspace,
			drydock.OperationCleanup, digest, request.RequestedBy,
			drydockCleanupRequestFingerprint(workspace.ID, request),
			"cleanup_preflight_blocked", "cleanup preflight could not prove exact ownership",
			&observed)
		return DrydockCleanupResult{ProtocolVersion: DrydockAPIProtocolVersion,
				Workspace: preserved, Receipt: receipt, Preserved: true},
			errors.Join(apperror.Normalize(err), preserveErr)
	}
	gitReceipt, err := s.executor.ExecuteRemove(ctx, workspace.Source.RootPath, preview)
	receiptErr := gitReceipt.Validate()
	if err != nil || receiptErr != nil || gitReceipt.Status != gitadvanced.ReceiptSucceeded {
		gitErr := errors.Join(err, receiptErr)
		if gitErr == nil {
			gitErr = errors.New("Git advanced cleanup receipt did not succeed")
		}
		preserved, receipt, preserveErr := s.markRecoveryWithGit(ctx, workspace,
			drydock.OperationCleanup, digest, request.RequestedBy,
			drydockCleanupRequestFingerprint(workspace.ID, request),
			"cleanup_git_failed", "Git refused exact non-force Drydock removal", &observed,
			gitReceipt.ID)
		return DrydockCleanupResult{ProtocolVersion: DrydockAPIProtocolVersion,
				Workspace: preserved, Receipt: receipt, Preserved: true},
			errors.Join(apperror.Normalize(gitErr), preserveErr)
	}
	now := s.now().UTC()
	beforeGeneration := workspace.Generation
	workspace.State = drydock.StateCleaned
	workspace.Generation++
	workspace.UpdatedAt = now
	workspace.CleanedAt = &now
	receipt := s.transitionReceipt(workspace, beforeGeneration, drydock.OperationCleanup,
		digest, drydockCleanupRequestFingerprint(workspace.ID, request),
		drydock.OutcomeSucceeded, "",
		"removed only the exact clean Git worktree; the local branch and source Workspace were retained",
		observed.Binding.Fingerprint(), observed.Binding.Fingerprint(), gitReceipt.ID, "", "")
	workspace, replayed, err := s.store.AdvanceDrydock(ctx, workspace,
		beforeGeneration, receipt)
	if err != nil {
		return DrydockCleanupResult{}, apperror.Normalize(err)
	}
	return DrydockCleanupResult{ProtocolVersion: DrydockAPIProtocolVersion,
		Workspace: workspace, Receipt: receipt, Replayed: replayed}, nil
}

func drydockCleanupRequestFingerprint(workspaceID string,
	request DrydockCleanupRequest,
) string {
	return runmutation.Fingerprint("drydock-cleanup-request.v1", workspaceID,
		strconv.FormatInt(request.ExpectedGeneration, 10), request.RequestedBy)
}

func (s *DrydockService) Projection(ctx context.Context, runID string,
	limit int,
) (DrydockProjection, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" || limit < 1 || limit > drydock.MaxList {
		return DrydockProjection{}, apperror.New(apperror.CodeInvalidArgument,
			"Drydock projection request is invalid")
	}
	if _, err := s.loadRunBinding(ctx, runID, true); err != nil {
		return DrydockProjection{}, err
	}
	value := DrydockProjection{ProtocolVersion: DrydockAPIProtocolVersion,
		RunID: runID, Receipts: []drydock.Receipt{}}
	if trust, found, err := s.store.GetDrydockTrustByRun(ctx, runID); err != nil {
		return value, apperror.Normalize(err)
	} else if found {
		value.Trust = &trust
	}
	workspace, found, err := s.store.GetDrydockByRun(ctx, runID)
	if err != nil || !found {
		return value, apperror.Normalize(err)
	}
	value.Workspace = &workspace
	value.Receipts, err = s.store.ListDrydockReceipts(ctx, workspace.ID, limit)
	if err != nil {
		return value, apperror.Normalize(err)
	}
	if workspace.LastDeliveryID != "" {
		if proposal, proposalFound, proposalErr := s.store.GetDrydockDelivery(ctx,
			workspace.LastDeliveryID); proposalErr != nil {
			return value, apperror.Normalize(proposalErr)
		} else if proposalFound {
			value.Delivery = &proposal
		}
	}
	return value, nil
}

func (s *DrydockService) Reconcile(ctx context.Context) (DrydockReconcileResult, error) {
	value := DrydockReconcileResult{ProtocolVersion: DrydockAPIProtocolVersion,
		DrydockIDs: []string{}}
	workspaces, err := s.store.ListDrydocks(ctx, drydock.ListFilter{
		IncludeCleaned: false, Limit: drydock.MaxList})
	if err != nil {
		return value, apperror.Normalize(err)
	}
	for _, workspace := range workspaces {
		if err := ctx.Err(); err != nil {
			return value, err
		}
		value.Examined++
		updated, changed, reconcileErr := s.reconcileOne(ctx, workspace)
		if reconcileErr != nil {
			return value, reconcileErr
		}
		if !changed {
			value.Unchanged++
			continue
		}
		value.DrydockIDs = append(value.DrydockIDs, updated.ID)
		if updated.State == drydock.StateReady {
			value.Recovered++
		} else if updated.State == drydock.StateRecoveryRequired {
			value.RecoveryRequired++
		}
	}
	return value, nil
}

func (s *DrydockService) GarbageCollect(ctx context.Context, limit int) (DrydockGCResult, error) {
	value := DrydockGCResult{ProtocolVersion: DrydockAPIProtocolVersion,
		DrydockIDs: []string{}}
	if limit < 1 || limit > 100 {
		return value, apperror.New(apperror.CodeInvalidArgument,
			"Drydock GC limit must be between 1 and 100")
	}
	now := s.now().UTC()
	workspaces, err := s.store.ListDrydocks(ctx, drydock.ListFilter{
		ExpiredBefore: &now, IncludeCleaned: false, Limit: limit})
	if err != nil {
		return value, apperror.Normalize(err)
	}
	for _, workspace := range workspaces {
		value.Examined++
		if workspace.State != drydock.StateReady && workspace.State != drydock.StateDelivered {
			value.Preserved++
			continue
		}
		result, cleanupErr := s.Cleanup(ctx, DrydockCleanupRequest{RunID: workspace.RunID,
			ExpectedGeneration: workspace.Generation,
			OperationKey:       "gc-" + workspace.ID + "-" + strconv.FormatInt(workspace.Generation, 10),
			RequestedBy:        "drydock_gc", Confirm: true})
		if cleanupErr != nil && result.Workspace.ID == "" {
			return value, cleanupErr
		}
		value.DrydockIDs = append(value.DrydockIDs, workspace.ID)
		if result.Workspace.State == drydock.StateCleaned {
			value.Cleaned++
		} else {
			value.Preserved++
		}
	}
	return value, nil
}

func (s *DrydockService) replayUse(ctx context.Context, request DrydockUseRequest,
	digest string,
) (DrydockUseResult, bool, error) {
	receipt, found, err := s.store.GetDrydockReceiptByOperation(ctx, digest)
	if err != nil || !found {
		return DrydockUseResult{}, false, apperror.Normalize(err)
	}
	workspace, workspaceFound, err := s.store.GetDrydockByRun(ctx, request.RunID)
	if err != nil || !workspaceFound {
		return DrydockUseResult{}, true, apperror.Normalize(err)
	}
	if receipt.Operation != drydock.OperationUse ||
		!drydockReceiptMatchesRequest(receipt, workspace.ID,
			request.ExpectedGeneration,
			drydockUseRequestFingerprint(workspace.ID, request)) {
		return DrydockUseResult{}, true, apperror.New(apperror.CodeConflict,
			"Drydock use operation key was reused for different intent")
	}
	result := DrydockUseResult{ProtocolVersion: DrydockAPIProtocolVersion,
		Workspace: workspace, Receipt: receipt, RootPath: workspace.Path,
		BindingFingerprint:     receipt.BindingAfterSHA256,
		GrantsProcessAuthority: false, Replayed: true}
	if receipt.Outcome != drydock.OutcomeSucceeded {
		return result, true, apperror.New(apperror.CodeConflict,
			"the previous use request preserved the Drydock for recovery")
	}
	return result, true, nil
}

func (s *DrydockService) replayDelivery(ctx context.Context,
	request DrydockDeliveryRequest,
	receipt drydock.Receipt,
) (DrydockDeliveryResult, error) {
	workspace, found, err := s.store.GetDrydockByRun(ctx, request.RunID)
	if err != nil || !found {
		return DrydockDeliveryResult{}, apperror.Normalize(err)
	}
	if receipt.Operation != drydock.OperationDeliver ||
		!drydockReceiptMatchesRequest(receipt, workspace.ID,
			request.ExpectedGeneration,
			drydockDeliveryRequestFingerprint(workspace.ID, request)) {
		return DrydockDeliveryResult{}, apperror.New(apperror.CodeConflict,
			"Drydock delivery operation key was reused for different intent")
	}
	if receipt.Outcome != drydock.OutcomeSucceeded || receipt.DeliveryID == "" {
		return DrydockDeliveryResult{ProtocolVersion: DrydockAPIProtocolVersion,
				Workspace: workspace, Receipt: receipt, Replayed: true},
			apperror.New(apperror.CodeConflict,
				"the previous delivery request preserved the Drydock for recovery")
	}
	proposal, found, err := s.store.GetDrydockDelivery(ctx, receipt.DeliveryID)
	if err != nil || !found {
		if err == nil {
			err = errors.New("stored Drydock delivery proposal is unavailable")
		}
		return DrydockDeliveryResult{}, apperror.Normalize(err)
	}
	evidence, err := s.executor.CaptureDelivery(ctx, workspace.Path, workspace.BaseCommit)
	if err != nil || drydock.FingerprintBytes([]byte(evidence.Patch)) != proposal.DiffSHA256 ||
		evidence.Binding.Fingerprint() != proposal.BindingFingerprint {
		return DrydockDeliveryResult{}, apperror.New(apperror.CodeConflict,
			"stored delivery proposal no longer matches the live Drydock")
	}
	return DrydockDeliveryResult{ProtocolVersion: DrydockAPIProtocolVersion,
		Workspace: workspace, Review: drydock.Review{Proposal: proposal, Patch: evidence.Patch},
		Receipt: receipt, Replayed: true}, nil
}

func (s *DrydockService) loadExactDrydock(ctx context.Context, runID string,
	expectedGeneration int64, allowRecovery bool,
) (drydock.Workspace, repository.DrydockSourceObservation,
	repository.DrydockObservation, error,
) {
	binding, err := s.loadRunBinding(ctx, runID, true)
	if err != nil {
		return drydock.Workspace{}, repository.DrydockSourceObservation{},
			repository.DrydockObservation{}, err
	}
	workspace, found, err := s.store.GetDrydockByRun(ctx, runID)
	if err != nil || !found {
		if err == nil {
			err = apperror.New(apperror.CodeNotFound, "Drydock was not found for this Run")
		}
		return drydock.Workspace{}, repository.DrydockSourceObservation{},
			repository.DrydockObservation{}, apperror.Normalize(err)
	}
	if workspace.Generation != expectedGeneration {
		return workspace, repository.DrydockSourceObservation{},
			repository.DrydockObservation{}, apperror.New(apperror.CodeConflict,
				"Drydock ownership generation changed")
	}
	if workspace.State == drydock.StateRecoveryRequired && !allowRecovery {
		return workspace, repository.DrydockSourceObservation{},
			repository.DrydockObservation{}, apperror.New(apperror.CodeFailedPrecondition,
				"Drydock requires operator recovery")
	}
	source, err := s.executor.InspectSource(ctx, binding.workspace.ID,
		binding.workspace.RootPath)
	if err != nil || source.Identity.Fingerprint() != workspace.Source.Fingerprint() {
		return workspace, source, repository.DrydockObservation{},
			apperror.New(apperror.CodeConflict,
				"Drydock source root, repository, branch, or base commit drifted")
	}
	observed, err := s.executor.Inspect(ctx, source.Identity.RootPath, source.Binding,
		workspace.Name)
	if err != nil {
		return workspace, source, observed, apperror.Normalize(err)
	}
	if err := s.validateObservation(ctx, workspace, observed); err != nil {
		return workspace, source, observed, apperror.Normalize(err)
	}
	return workspace, source, observed, nil
}

func (s *DrydockService) loadDrydockForCleanup(ctx context.Context, runID string,
	expectedGeneration int64,
) (drydock.Workspace, repository.DrydockSourceObservation,
	repository.DrydockObservation, error,
) {
	binding, err := s.loadRunBinding(ctx, runID, true)
	if err != nil {
		return drydock.Workspace{}, repository.DrydockSourceObservation{},
			repository.DrydockObservation{}, err
	}
	workspace, found, err := s.store.GetDrydockByRun(ctx, runID)
	if err != nil || !found {
		if err == nil {
			err = apperror.New(apperror.CodeNotFound, "Drydock was not found for this Run")
		}
		return drydock.Workspace{}, repository.DrydockSourceObservation{},
			repository.DrydockObservation{}, apperror.Normalize(err)
	}
	if workspace.Generation != expectedGeneration {
		return workspace, repository.DrydockSourceObservation{},
			repository.DrydockObservation{}, apperror.New(apperror.CodeConflict,
				"Drydock ownership generation changed")
	}
	source, err := s.executor.InspectSource(ctx, binding.workspace.ID,
		binding.workspace.RootPath)
	if err != nil || source.Identity.Fingerprint() != workspace.Source.Fingerprint() {
		return workspace, source, repository.DrydockObservation{},
			apperror.New(apperror.CodeConflict,
				"Drydock source root, repository, branch, or base commit drifted")
	}
	observed, err := s.executor.Inspect(ctx, source.Identity.RootPath, source.Binding,
		workspace.Name)
	if err != nil {
		return workspace, source, observed, apperror.Normalize(err)
	}
	if observed.Present {
		if err := s.validateObservation(ctx, workspace, observed); err != nil {
			return workspace, source, observed, apperror.Normalize(err)
		}
	}
	return workspace, source, observed, nil
}

func (s *DrydockService) validateObservation(ctx context.Context,
	workspace drydock.Workspace, observed repository.DrydockObservation,
) error {
	if !observed.Found || !observed.Present || observed.Prunable || observed.Locked ||
		observed.Path != workspace.Path || observed.Branch != workspace.Branch ||
		observed.Binding.CommonDirSHA256 != workspace.Source.CommonDirSHA256 {
		return apperror.New(apperror.CodeConflict,
			"Drydock root or branch identity changed")
	}
	if workspace.RootFingerprint != "" && observed.RootFingerprint != workspace.RootFingerprint {
		return apperror.New(apperror.CodeConflict, "Drydock root fingerprint changed")
	}
	if err := s.executor.VerifyBaseAncestry(ctx, observed.Path, workspace.BaseCommit,
		observed.Binding.Head); err != nil {
		return apperror.Wrap(apperror.CodeConflict, "Drydock base ancestry changed", err)
	}
	return nil
}

func (s *DrydockService) loadRunBinding(ctx context.Context, runID string,
	allowTerminal bool,
) (drydockRunBinding, error) {
	var value drydockRunBinding
	var err error
	if value.run, err = s.store.GetRun(ctx, strings.TrimSpace(runID)); err != nil {
		return value, apperror.Normalize(err)
	}
	if value.run.Terminal() && !allowTerminal {
		return value, apperror.New(apperror.CodeFailedPrecondition,
			"terminal Run cannot create a Drydock")
	}
	if value.mission, err = s.store.GetMission(ctx, value.run.MissionID); err != nil {
		return value, apperror.Normalize(err)
	}
	if value.mission.Profile != domain.ProfileCode || value.mission.WorkspaceID == "" ||
		value.run.SessionID == "" {
		return value, apperror.New(apperror.CodeFailedPrecondition,
			"Drydock requires a Code Run with an exact Workspace and Session")
	}
	if value.session, err = s.store.GetSession(ctx, value.run.SessionID); err != nil {
		return value, apperror.Normalize(err)
	}
	if value.workspace, err = s.store.GetWorkspaceByID(ctx,
		value.mission.WorkspaceID); err != nil {
		return value, apperror.Normalize(err)
	}
	if value.session.WorkspaceID != value.workspace.ID ||
		value.mission.Scope.WorkspaceID != value.workspace.ID {
		return value, apperror.New(apperror.CodeConflict,
			"Run, Mission, Session, and source Workspace binding changed")
	}
	return value, nil
}

func (s *DrydockService) captureCheckpoint(ctx context.Context,
	workspace drydock.Workspace, triggerReceiptID, title, requestedBy, phase,
	parentID string,
) (workspacecheckpoint.Checkpoint, error) {
	digest := runmutation.Fingerprint("drydock-checkpoint.v1", workspace.ID,
		triggerReceiptID, phase, parentID)
	snapshot, err := workspacecheckpoint.Capture(ctx, workspacecheckpoint.CaptureRequest{
		ID: "drydock-checkpoint-" + digest[:32], RunID: workspace.RunID,
		MissionID: workspace.MissionID, SessionID: workspace.SessionID,
		WorkspaceID: workspace.WorkspaceID, WorkspaceRoot: workspace.Path,
		CapabilityGeneration: drydock.Fingerprint("drydock-generation", workspace.ID,
			strconv.FormatInt(workspace.Generation, 10)),
		Trigger: workspacecheckpoint.TriggerManual, Phase: workspacecheckpoint.PhaseStandalone,
		TriggerReceiptID: triggerReceiptID, RequestedBy: requestedBy, Title: title,
		ParentCheckpointID: parentID, CreatedAt: s.now().UTC(),
	})
	if err != nil {
		return workspacecheckpoint.Checkpoint{}, err
	}
	checkpoint, _, err := s.store.CreateWorkspaceCheckpoint(ctx, snapshot)
	return checkpoint, err
}

func (s *DrydockService) transitionReceipt(workspace drydock.Workspace,
	beforeGeneration int64, operation drydock.Operation, operationDigest,
	requestFingerprint string, outcome drydock.Outcome, reason, summary,
	bindingBefore, bindingAfter, gitReceiptID, checkpointID, deliveryID string,
) drydock.Receipt {
	return drydock.Receipt{ID: drydockReceiptID(operationDigest),
		ProtocolVersion:    drydock.ReceiptProtocolVersion,
		OperationKeySHA256: operationDigest, RequestFingerprint: requestFingerprint,
		DrydockID: workspace.ID, RunID: workspace.RunID, Operation: operation,
		Outcome: outcome, GenerationBefore: beforeGeneration,
		GenerationAfter:      workspace.Generation,
		SourceIdentitySHA256: workspace.Source.Fingerprint(),
		RootFingerprint:      workspace.RootFingerprint,
		BindingBeforeSHA256:  bindingBefore, BindingAfterSHA256: bindingAfter,
		GitReceiptID: gitReceiptID, CheckpointID: checkpointID, DeliveryID: deliveryID,
		ReasonCode: reason, Summary: summary, GrantsProcessAuthority: false,
		CreatedAt: s.now().UTC()}
}

func (s *DrydockService) failCreate(ctx context.Context, workspace drydock.Workspace,
	source repository.DrydockSourceObservation, request DrydockCreateRequest,
	gitReceipt gitadvanced.Receipt, cause error,
) (drydock.Workspace, drydock.Receipt, error) {
	observed, inspectErr := s.executor.Inspect(ctx, source.Identity.RootPath,
		source.Binding, workspace.Name)
	beforeGeneration := workspace.Generation
	now := s.now().UTC()
	reason := "create_failed"
	if inspectErr == nil && observed.Present && observed.Path == workspace.Path &&
		observed.Branch == workspace.Branch && observed.Binding.Head == workspace.BaseCommit {
		workspace.RootFingerprint = observed.RootFingerprint
		workspace.ExpectedHead = observed.Binding.Head
		workspace.ExpectedBindingFingerprint = observed.Binding.Fingerprint()
		workspace.ManagedWorktreeID = gitReceipt.WorktreeID
		if workspace.ManagedWorktreeID == "" {
			workspace.ManagedWorktreeID = "gwt-" + gitadvanced.Fingerprint("worktree",
				workspace.CreatePreviewID)[:32]
		}
		workspace.State = drydock.StateRecoveryRequired
		workspace.RecoveryReason = reason
	} else if !observed.Found && inspectErr == nil {
		workspace.State = drydock.StateCleaned
		workspace.CleanedAt = &now
	} else {
		workspace.State = drydock.StateRecoveryRequired
		workspace.RecoveryReason = reason
	}
	workspace.Generation++
	workspace.UpdatedAt = now
	digest := drydockOperationDigest(drydock.OperationCreate, workspace.RunID,
		request.OperationKey)
	summary := "Drydock creation failed; uncertain or changed content was preserved"
	receipt := s.transitionReceipt(workspace, beforeGeneration, drydock.OperationCreate,
		digest, runmutation.Fingerprint("drydock-create-request.v1", workspace.RunID,
			workspace.Source.Fingerprint(), request.RequestedBy), drydock.OutcomeFailed,
		reason, summary, "", workspace.ExpectedBindingFingerprint, gitReceipt.ID, "", "")
	advanced, _, transitionErr := s.store.AdvanceDrydock(ctx, workspace,
		beforeGeneration, receipt)
	return advanced, receipt, errors.Join(apperror.Normalize(cause),
		apperror.Normalize(inspectErr), apperror.Normalize(transitionErr))
}

func (s *DrydockService) markRecovery(ctx context.Context, workspace drydock.Workspace,
	operation drydock.Operation, digest, requestedBy, requestFingerprint, reason, summary string,
	observed *repository.DrydockObservation,
) (drydock.Workspace, drydock.Receipt, error) {
	return s.markRecoveryWithGit(ctx, workspace, operation, digest, requestedBy,
		requestFingerprint, reason, summary, observed, "")
}

func (s *DrydockService) markRecoveryWithGit(ctx context.Context,
	workspace drydock.Workspace, operation drydock.Operation, digest, requestedBy,
	requestFingerprint, reason, summary string, observed *repository.DrydockObservation,
	gitReceiptID string,
) (drydock.Workspace, drydock.Receipt, error) {
	if strings.TrimSpace(requestedBy) == "" || !drydock.ValidDigest(requestFingerprint) {
		return drydock.Workspace{}, drydock.Receipt{}, apperror.New(
			apperror.CodeInvalidArgument, "Drydock recovery attribution is invalid")
	}
	beforeGeneration := workspace.Generation
	bindingBefore := workspace.ExpectedBindingFingerprint
	bindingAfter := ""
	if observed != nil && observed.Present && observed.Path == workspace.Path &&
		observed.Branch == workspace.Branch {
		// Never promote changed or uncertain content into the expected binding.
		// Only a preparing owner with no prior materialization evidence may adopt
		// an exact registered observation for later operator recovery.
		if workspace.RootFingerprint == "" && workspace.ExpectedHead == "" &&
			workspace.ExpectedBindingFingerprint == "" && workspace.ManagedWorktreeID == "" &&
			observed.Binding.CommonDirSHA256 == workspace.Source.CommonDirSHA256 {
			workspace.RootFingerprint = observed.RootFingerprint
			workspace.ExpectedHead = observed.Binding.Head
			workspace.ExpectedBindingFingerprint = observed.Binding.Fingerprint()
			workspace.ManagedWorktreeID = "gwt-" + gitadvanced.Fingerprint("worktree",
				workspace.CreatePreviewID)[:32]
		}
		bindingAfter = observed.Binding.Fingerprint()
	}
	workspace.State = drydock.StateRecoveryRequired
	workspace.RecoveryReason = reason
	workspace.Generation++
	workspace.UpdatedAt = s.now().UTC()
	receipt := s.transitionReceipt(workspace, beforeGeneration, operation, digest,
		requestFingerprint,
		drydock.OutcomePreserved, reason, summary, bindingBefore, bindingAfter,
		gitReceiptID, "", "")
	advanced, _, err := s.store.AdvanceDrydock(ctx, workspace, beforeGeneration, receipt)
	return advanced, receipt, apperror.Normalize(err)
}

func (s *DrydockService) reconcileOne(ctx context.Context,
	workspace drydock.Workspace,
) (drydock.Workspace, bool, error) {
	if workspace.State == drydock.StateCleaned || workspace.State == drydock.StateRecoveryRequired {
		return workspace, false, nil
	}
	binding, err := s.loadRunBinding(ctx, workspace.RunID, true)
	if err != nil {
		return s.reconcilePreserve(ctx, workspace, "run_binding_drift")
	}
	source, err := s.executor.InspectSource(ctx, binding.workspace.ID,
		binding.workspace.RootPath)
	if err != nil || source.Identity.Fingerprint() != workspace.Source.Fingerprint() {
		return s.reconcilePreserve(ctx, workspace, "source_identity_drift")
	}
	observed, inspectErr := s.executor.Inspect(ctx, source.Identity.RootPath,
		source.Binding, workspace.Name)
	if inspectErr != nil || !observed.Present {
		return s.reconcilePreserve(ctx, workspace, "worktree_identity_unavailable")
	}
	if err := s.validateObservation(ctx, workspace, observed); err != nil {
		return s.reconcilePreserveObserved(ctx, workspace, "worktree_identity_drift", observed)
	}
	if workspace.State == drydock.StatePreparing {
		if !observed.Clean || observed.Binding.Head != workspace.BaseCommit {
			return s.reconcilePreserveObserved(ctx, workspace,
				"interrupted_create_has_user_changes", observed)
		}
		digest := drydockOperationDigest(drydock.OperationRecover, workspace.RunID,
			"startup-create-"+strconv.FormatInt(workspace.Generation, 10))
		receiptID := drydockReceiptID(digest)
		checkpoint, captureErr := s.captureCheckpoint(ctx, workspace, receiptID,
			"Recovered Drydock baseline", "drydock_reconciler", "recover", "")
		if captureErr != nil {
			return s.reconcilePreserveObserved(ctx, workspace,
				"recovery_checkpoint_failed", observed)
		}
		beforeGeneration := workspace.Generation
		workspace.RootFingerprint = observed.RootFingerprint
		workspace.ExpectedHead = observed.Binding.Head
		workspace.ExpectedBindingFingerprint = observed.Binding.Fingerprint()
		workspace.ManagedWorktreeID = "gwt-" + gitadvanced.Fingerprint("worktree",
			workspace.CreatePreviewID)[:32]
		workspace.CreateGitReceiptID = "gar-" + gitadvanced.Fingerprint("receipt",
			workspace.CreatePreviewID)[:32]
		workspace.State = drydock.StateReady
		workspace.Generation++
		workspace.LastCheckpointID = checkpoint.ID
		workspace.UpdatedAt = s.now().UTC()
		receipt := s.transitionReceipt(workspace, beforeGeneration, drydock.OperationRecover,
			digest, runmutation.Fingerprint("drydock-reconcile.v1", workspace.ID,
				strconv.FormatInt(beforeGeneration, 10)), drydock.OutcomeSucceeded, "",
			"recovered an interrupted create only after exact clean identity verification",
			"", observed.Binding.Fingerprint(), workspace.CreateGitReceiptID,
			checkpoint.ID, "")
		workspace, _, err = s.store.AdvanceDrydock(ctx, workspace,
			beforeGeneration, receipt)
		return workspace, true, apperror.Normalize(err)
	}
	if observed.Binding.Fingerprint() != workspace.ExpectedBindingFingerprint {
		return s.reconcilePreserveObserved(ctx, workspace,
			"post_crash_workspace_change", observed)
	}
	return workspace, false, nil
}

func (s *DrydockService) reconcilePreserve(ctx context.Context,
	workspace drydock.Workspace, reason string,
) (drydock.Workspace, bool, error) {
	digest := drydockOperationDigest(drydock.OperationRecover, workspace.RunID,
		"startup-preserve-"+strconv.FormatInt(workspace.Generation, 10))
	updated, _, err := s.markRecovery(ctx, workspace, drydock.OperationRecover,
		digest, "drydock_reconciler", runmutation.Fingerprint("drydock-reconcile.v1",
			workspace.ID, strconv.FormatInt(workspace.Generation, 10), reason), reason,
		"startup recovery preserved a Drydock whose exact ownership could not be proved", nil)
	return updated, true, err
}

func (s *DrydockService) reconcilePreserveObserved(ctx context.Context,
	workspace drydock.Workspace, reason string, observed repository.DrydockObservation,
) (drydock.Workspace, bool, error) {
	digest := drydockOperationDigest(drydock.OperationRecover, workspace.RunID,
		"startup-preserve-"+strconv.FormatInt(workspace.Generation, 10))
	updated, _, err := s.markRecovery(ctx, workspace, drydock.OperationRecover,
		digest, "drydock_reconciler", runmutation.Fingerprint("drydock-reconcile.v1",
			workspace.ID, strconv.FormatInt(workspace.Generation, 10), reason), reason,
		"startup recovery preserved observed content instead of deleting it", &observed)
	return updated, true, err
}

func drydockTrustID(runID string, identity drydock.SourceIdentity) string {
	return "drydock-trust-" + drydock.Fingerprint("trust", runID,
		identity.Fingerprint())[:32]
}

func drydockOperationDigest(operation drydock.Operation, runID, operationKey string) string {
	return runmutation.OperationKeyDigest("drydock_lifecycle.v1."+string(operation),
		runID, operationKey)
}

func drydockReceiptID(operationDigest string) string {
	return "drydock-receipt-" + operationDigest[:32]
}

func normalizeDrydockActor(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "operator"
	}
	if len([]rune(value)) > 256 || strings.ContainsRune(value, 0) {
		return ""
	}
	return value
}
