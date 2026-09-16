package application

import (
	"context"

	"cyberagent-workbench/internal/artifact"
	"cyberagent-workbench/internal/runner"
)

type commandSavedOutputStore interface {
	GetRunArtifact(context.Context, string) (artifact.Blob, error)
	ListRunArtifacts(context.Context, artifact.ListFilter) ([]artifact.Descriptor, error)
}

// A ring-window truncation does not imply lost saved output. Only a validated
// full artifact for every observed stream proves that distinction. The hashes
// cover sanitized UTF-8; pre-normalization CRLF/encoding byte counts cannot be
// compared to retained content length. Unknown reasons remain fail-closed.
func commandSavedOutputTruncated(ctx context.Context, state any, job runner.CommandRuntimeJob) bool {
	if job.TruncationReason == "" {
		return false
	}
	if job.TruncationReason != "inline_window" || !job.State.Terminal() ||
		job.StdinPolicy != runner.CommandRuntimeStdinClosed || job.StdinWriteCount != 0 ||
		job.Credentials != runner.CommandRuntimeCredentialsNone {
		return true
	}
	reader, ok := state.(commandSavedOutputStore)
	if !ok {
		return true
	}
	descriptors, err := reader.ListRunArtifacts(ctx, artifact.ListFilter{RunID: job.RunID, SourceID: job.ID, Limit: 2})
	if err != nil {
		return true
	}
	complete := map[artifact.Stream]bool{
		artifact.StreamStdout: job.StdoutObservedBytes == 0 && job.Stdout == "",
		artifact.StreamStderr: job.StderrObservedBytes == 0 && job.Stderr == "",
	}
	for _, descriptor := range descriptors {
		if !validThreadActivityArtifactDescriptor(descriptor, job) {
			return true
		}
		blob, err := reader.GetRunArtifact(ctx, descriptor.ID)
		if err != nil || blob.Validate() != nil || blob.Descriptor != descriptor {
			return true
		}
		content, digest := job.Stdout, job.StdoutSHA256
		if descriptor.Stream == artifact.StreamStderr {
			content, digest = job.Stderr, job.StderrSHA256
		}
		if descriptor.SHA256 != digest || artifact.Hash(content) != digest || blob.Content != content {
			return true
		}
		complete[descriptor.Stream] = true
	}
	return !complete[artifact.StreamStdout] || !complete[artifact.StreamStderr]
}

func (m *standardCodeSupervisorTurn) projectedCommandOutputTruncated(ctx context.Context,
	projected runner.CommandRuntimeJobSnapshot,
) bool {
	if projected.TruncationReason == "" {
		return false
	}
	if projected.TruncationReason != "inline_window" {
		return true
	}
	reader, ok := m.store.(interface {
		GetCommandRuntimeJob(context.Context, string) (runner.CommandRuntimeJob, error)
	})
	if !ok {
		return true
	}
	job, err := reader.GetCommandRuntimeJob(ctx, projected.ID)
	if err != nil || job.RunID != m.snapshot.RunID || job.SessionID != m.turn.Run.SessionID ||
		job.MissionID != m.turn.Run.MissionID || job.WorkspaceID != m.snapshot.WorkspaceID ||
		job.State != projected.State || job.TruncationReason != projected.TruncationReason ||
		job.StdoutSHA256 != projected.StdoutSHA256 || job.StderrSHA256 != projected.StderrSHA256 ||
		job.TreeReaped != projected.TreeReaped || job.ExitCode == nil || projected.ExitCode == nil ||
		*job.ExitCode != *projected.ExitCode || job.PermissionSnapshotID != m.snapshot.PermissionSnapshotID ||
		job.PermissionRevision != m.snapshot.PermissionRevision {
		return true
	}
	return commandSavedOutputTruncated(ctx, m.store, job)
}
