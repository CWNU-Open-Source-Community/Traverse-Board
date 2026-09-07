package runner

import (
	"context"
	"encoding/json"
	"strings"

	"cyberagent-workbench/internal/commandruntimeadapter"
)

// ReadCommandRuntimeActivityTail returns a bounded process-local tail for an
// already authorized durable Job. The complete durable Job is required so a
// caller cannot turn this method into a process-wide Job-ID oracle. When this
// process no longer owns the Job, callers must fall back to the durable store.
func (m *CommandRuntimeManager) ReadCommandRuntimeActivityTail(ctx context.Context,
	job CommandRuntimeJob, maxBytes int,
) (CommandRuntimeJobSnapshot, CommandRuntimeOutputPage, bool, error) {
	if ctx == nil || ctx.Err() != nil || m == nil || strings.TrimSpace(job.ID) == "" {
		return CommandRuntimeJobSnapshot{}, CommandRuntimeOutputPage{}, false,
			ErrCommandRuntimeBoundary
	}
	if maxBytes == 0 {
		maxBytes = MaxCommandRuntimeOutputRead
	}
	if maxBytes < MinCommandRuntimeOutputRead || maxBytes > MaxCommandRuntimeOutputRead {
		return CommandRuntimeJobSnapshot{}, CommandRuntimeOutputPage{}, false,
			ErrCommandRuntimeBoundary
	}
	entry := m.entry(job.ID)
	if entry == nil {
		return CommandRuntimeJobSnapshot{}, CommandRuntimeOutputPage{}, false, nil
	}
	return entry.readBoundActivityTail(job, m.ownerID, m.ownerGeneration,
		m.adapter, maxBytes)
}

// ProjectCommandRuntimeStoredActivityTail reconstructs the terminal (or last
// heartbeat) ring tail from a validated durable Job. It is the fallback after
// process-local ownership ends; stdout/stderr fields remain only a legacy
// fallback because they are artifact-limit prefixes rather than true tails.
func ProjectCommandRuntimeStoredActivityTail(job CommandRuntimeJob, maxBytes int) (
	CommandRuntimeJobSnapshot, CommandRuntimeOutputPage, bool, error,
) {
	if maxBytes == 0 {
		maxBytes = MaxCommandRuntimeOutputRead
	}
	if maxBytes < MinCommandRuntimeOutputRead || maxBytes > MaxCommandRuntimeOutputRead ||
		job.Validate() != nil {
		return CommandRuntimeJobSnapshot{}, CommandRuntimeOutputPage{}, false,
			ErrCommandRuntimeBoundary
	}
	var frames []CommandRuntimeFrame
	if err := json.Unmarshal([]byte(job.OutputFramesJSON), &frames); err != nil {
		return CommandRuntimeJobSnapshot{}, CommandRuntimeOutputPage{}, false,
			ErrCommandRuntimeBoundary
	}
	if len(frames) == 0 {
		return ProjectCommandRuntimeJob(job), CommandRuntimeOutputPage{}, false, nil
	}
	cursor := job.OutputBaseCursor
	if job.OutputCursor > uint64(maxBytes) {
		candidate := job.OutputCursor - uint64(maxBytes)
		if candidate > cursor {
			cursor = candidate
		}
	}
	page := pageFromStoredJob(job, cursor, maxBytes)
	page.Dropped = page.Dropped || cursor > job.OutputBaseCursor || job.OutputBaseCursor > 0
	return ProjectCommandRuntimeJob(job), page, true, nil
}

func (e *commandRuntimeEntry) readBoundActivityTail(job CommandRuntimeJob,
	ownerID string, ownerGeneration int64, adapter commandruntimeadapter.Identity,
	maxBytes int,
) (CommandRuntimeJobSnapshot, CommandRuntimeOutputPage, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	record := &e.record
	if record.ID != job.ID || record.OperationDigest != job.OperationDigest ||
		record.RequestFingerprint != job.RequestFingerprint ||
		record.InvocationID != job.InvocationID || record.RunID != job.RunID ||
		record.MissionID != job.MissionID || record.SessionID != job.SessionID ||
		record.WorkspaceID != job.WorkspaceID || record.RootAgentID != job.RootAgentID ||
		record.WorkspaceRootSHA256 != job.WorkspaceRootSHA256 ||
		record.SpecFingerprint != job.SpecFingerprint ||
		record.OwnerID != ownerID || record.OwnerGeneration != ownerGeneration ||
		job.OwnerID != ownerID || job.OwnerGeneration != ownerGeneration ||
		!record.Adapter.SameBackend(adapter) || !job.Adapter.SameBackend(adapter) {
		return CommandRuntimeJobSnapshot{}, CommandRuntimeOutputPage{}, false,
			ErrCommandRuntimeBoundary
	}
	record.OutputCursor = e.ring.next
	record.OutputBaseCursor = e.ring.base
	cursor := e.ring.base
	if e.ring.next > uint64(maxBytes) {
		candidate := e.ring.next - uint64(maxBytes)
		if candidate > cursor {
			cursor = candidate
		}
	}
	page := e.ring.read(cursor, maxBytes)
	page.JobID = record.ID
	page.State = record.State
	page.ExitCode = cloneInt(record.ExitCode)
	page.TruncationReason = record.TruncationReason
	page.Dropped = page.Dropped || cursor > e.ring.base || e.ring.base > 0
	return ProjectCommandRuntimeJob(*record), page, true, nil
}
