package application

import (
	"context"
	"errors"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/drydock"
	"cyberagent-workbench/internal/workspacecheckpoint"
)

type drydockRestoreJournalStore interface {
	GetWorkspaceCheckpointTransactionByOperation(context.Context, string) (workspacecheckpoint.Transaction, bool, error)
	CreateWorkspaceCheckpointTransaction(context.Context, workspacecheckpoint.Transaction) (workspacecheckpoint.Transaction, bool, error)
	UpdateWorkspaceCheckpointTransaction(context.Context, workspacecheckpoint.Transaction) (workspacecheckpoint.Transaction, bool, error)
}

func (s *DrydockService) drydockRestoreJournal(ctx context.Context, digest string) (*workspacecheckpoint.Transaction, error) {
	store, ok := s.store.(drydockRestoreJournalStore)
	if !ok {
		return nil, apperror.New(apperror.CodeFailedPrecondition, "Drydock restore journal is unavailable")
	}
	transaction, found, err := store.GetWorkspaceCheckpointTransactionByOperation(ctx, digest)
	if err != nil || !found {
		return nil, apperror.Normalize(err)
	}
	return &transaction, nil
}

func (s *DrydockService) prepareDrydockRestore(ctx context.Context, request DrydockRewindRequest,
	operation drydock.Operation, digest string, workspace drydock.Workspace, before workspacecheckpoint.Snapshot,
	preview workspacecheckpoint.Preview,
) (*workspacecheckpoint.Transaction, error) {
	store, ok := s.store.(drydockRestoreJournalStore)
	if !ok {
		return nil, apperror.New(apperror.CodeFailedPrecondition, "Drydock restore journal is unavailable")
	}
	if stored, err := s.store.GetWorkspaceCheckpointSnapshot(ctx, before.Checkpoint.ID); err == nil {
		previous, actual := stored.Checkpoint, before.Checkpoint
		if previous.RunID != actual.RunID || previous.SessionID != actual.SessionID || previous.MissionID != actual.MissionID ||
			previous.WorkspaceID != actual.WorkspaceID || previous.TriggerReceiptID != actual.TriggerReceiptID ||
			previous.ParentCheckpointID != actual.ParentCheckpointID || previous.RootFingerprint != actual.RootFingerprint ||
			previous.RootPathSHA256 != actual.RootPathSHA256 || previous.BaseCommit != actual.BaseCommit ||
			previous.Branch != actual.Branch || previous.ManifestSHA256 != actual.ManifestSHA256 ||
			!drydockIndexProjectionEqual(stored, before) {
			return nil, apperror.New(apperror.CodeConflict, "Drydock restore changed after the original preflight; review a new request")
		}
		before = stored
	} else if apperror.CodeOf(apperror.Normalize(err)) != apperror.CodeNotFound {
		return nil, apperror.Normalize(err)
	}
	if _, _, err := s.store.CreateWorkspaceCheckpoint(ctx, before); err != nil {
		return nil, apperror.Normalize(err)
	}
	kind := workspacecheckpoint.TransactionRewind
	if operation == drydock.OperationUndo {
		kind = workspacecheckpoint.TransactionUndo
	}
	now := s.now().UTC()
	transaction := workspacecheckpoint.Transaction{ID: workspaceTransactionID(digest),
		ProtocolVersion: workspacecheckpoint.ProtocolVersion, OperationKeyDigest: digest,
		RequestFingerprint: drydockRewindRequestFingerprint(workspace.ID, operation, request),
		RunID:              request.RunID, WorkspaceID: workspace.WorkspaceID, Kind: kind,
		TriggerReceiptID: drydockReceiptID(digest), BeforeCheckpointID: before.Checkpoint.ID,
		ExpectedCurrentCheckpointID: workspace.LastCheckpointID, TargetCheckpointID: request.TargetCheckpointID,
		Status: workspacecheckpoint.TransactionPrepared, RecoveryLevel: preview.RecoveryLevel,
		ConflictJSON: "[]", CreatedAt: now, UpdatedAt: now}
	stored, _, err := store.CreateWorkspaceCheckpointTransaction(ctx, transaction)
	return &stored, apperror.Normalize(err)
}

// Keep the journal open until the lifecycle receipt is durable. That receipt
// and the physical cursor then prove which request completed after a lost reply.
func (s *DrydockService) finishDrydockRestoreJournal(ctx context.Context, digest string, receipt drydock.Receipt) error {
	transaction, err := s.drydockRestoreJournal(ctx, digest)
	if err != nil || transaction == nil || transaction.Status.Terminal() {
		return err
	}
	if transaction.TriggerReceiptID != receipt.ID || transaction.RunID != receipt.RunID {
		return apperror.New(apperror.CodeConflict, "Drydock restore journal and receipt differ")
	}
	now := s.now().UTC()
	transaction.Status = workspacecheckpoint.TransactionFailed
	transaction.ErrorCode = "drydock_restore_preserved"
	if receipt.Outcome == drydock.OutcomeSucceeded {
		transaction.Status = workspacecheckpoint.TransactionCompleted
		transaction.ErrorCode = ""
		transaction.AfterCheckpointID = receipt.CheckpointID
	}
	transaction.UpdatedAt, transaction.CompletedAt = now, &now
	_, _, err = s.store.(drydockRestoreJournalStore).UpdateWorkspaceCheckpointTransaction(ctx, *transaction)
	return apperror.Normalize(err)
}

func (s *DrydockService) preserveDrydockRestore(ctx context.Context, digest string, receipt drydock.Receipt, err error) error {
	if receipt.ID == "" {
		return err
	}
	return errors.Join(err, s.finishDrydockRestoreJournal(ctx, digest, receipt))
}

func drydockRestoreObservedIndex(expected, observed workspacecheckpoint.Snapshot) workspacecheckpoint.Snapshot {
	expected.Checkpoint.IndexSHA256 = observed.Checkpoint.IndexSHA256
	expected.Checkpoint.IndexBlobSHA256 = observed.Checkpoint.IndexBlobSHA256
	required := map[string]bool{expected.Checkpoint.IndexBlobSHA256: true}
	for _, entry := range expected.Entries {
		if entry.BlobSHA256 != "" {
			required[entry.BlobSHA256] = true
		}
	}
	all := append(append([]workspacecheckpoint.Blob{}, expected.Blobs...), observed.Blobs...)
	expected.Blobs = nil
	expected.Checkpoint.StoredBytes = 0
	for _, blob := range all {
		if required[blob.SHA256] {
			expected.Blobs = append(expected.Blobs, blob)
			expected.Checkpoint.StoredBytes += int64(len(blob.Content))
			delete(required, blob.SHA256)
		}
	}
	return expected
}

// A checkpoint retains the Run and Session that actually captured it. A
// successor may review that snapshot only through the immutable physical
// directory binding, never by substituting its current execution identity.
func (s *DrydockService) requireDrydockCheckpoint(ctx context.Context, workspace drydock.Workspace,
	checkpoint workspacecheckpoint.Checkpoint,
) error {
	owned, found, err := readRunFileDrydock(ctx, s.store, checkpoint.RunID)
	if err != nil {
		return apperror.Normalize(err)
	}
	if !found || owned.ID != workspace.ID || owned.WorkspaceID != workspace.WorkspaceID ||
		checkpoint.WorkspaceID != workspace.WorkspaceID || checkpoint.MissionID != workspace.MissionID ||
		checkpoint.RootFingerprint != workspace.RootFingerprint || checkpoint.Branch != workspace.Branch {
		return apperror.New(apperror.CodeFailedPrecondition, "Checkpoint does not belong to this exact conversation working directory")
	}
	run, err := s.store.GetRun(ctx, checkpoint.RunID)
	if err != nil {
		return apperror.Normalize(err)
	}
	if run.SessionID != checkpoint.SessionID || run.MissionID != checkpoint.MissionID {
		return apperror.New(apperror.CodeFailedPrecondition, "Checkpoint execution identity does not match its recorded Run")
	}
	return nil
}

func (s *DrydockService) skipTransferredDrydockReconciliation(ctx context.Context, workspace drydock.Workspace) (bool, error) {
	if pendingStore, ok := s.store.(interface {
		HasPendingDrydockCleanup(context.Context, string) (bool, error)
	}); ok {
		pending, err := pendingStore.HasPendingDrydockCleanup(ctx, workspace.ID)
		if err != nil || pending {
			return pending, apperror.Normalize(err)
		}
	}
	if store, ok := s.store.(interface {
		ListOpenWorkspaceCheckpointTransactions(context.Context, int) ([]workspacecheckpoint.Transaction, error)
	}); ok {
		transactions, err := store.ListOpenWorkspaceCheckpointTransactions(ctx, workspaceCheckpointListLimit)
		if err != nil {
			return false, apperror.Normalize(err)
		}
		for _, transaction := range transactions {
			if transaction.WorkspaceID == workspace.WorkspaceID {
				return true, nil
			}
		}
		if len(transactions) == workspaceCheckpointListLimit {
			return false, apperror.New(apperror.CodeResourceExhausted, "Workspace recovery journal exceeds the safe scan limit")
		}
	}
	if store, ok := s.store.(threadDrydockCleanupStore); ok {
		holder, found, err := store.GetDrydockHolder(ctx, workspace.ID)
		if err != nil {
			return false, apperror.Normalize(err)
		}
		if !found {
			return false, apperror.New(apperror.CodeConflict, "Working directory holder is unavailable during recovery")
		}
		return holder.RunID != workspace.RunID, nil
	}
	return false, nil
}
