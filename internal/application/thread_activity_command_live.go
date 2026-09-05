package application

import (
	"context"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/runner"
)

// ThreadActivityCommandRuntimeSource is the narrow read-only bridge from a
// public Thread projection to the process-local Command Runtime output ring.
// The caller supplies a Thread-authorized durable Job, never a bare Job ID.
type ThreadActivityCommandRuntimeSource interface {
	ReadCommandRuntimeActivityTail(context.Context, runner.CommandRuntimeJob, int) (
		runner.CommandRuntimeJobSnapshot, runner.CommandRuntimeOutputPage, bool, error)
}

type threadActivityLiveCommand struct {
	snapshot runner.CommandRuntimeJobSnapshot
	page     runner.CommandRuntimeOutputPage
}

func (s *ThreadActivityDetailService) WithCommandRuntimeSource(
	source ThreadActivityCommandRuntimeSource,
) *ThreadActivityDetailService {
	if s != nil {
		s.commandRuntime = source
	}
	return s
}

func (s *CommandRuntimeService) ReadCommandRuntimeActivityTail(ctx context.Context,
	job runner.CommandRuntimeJob, maxBytes int,
) (runner.CommandRuntimeJobSnapshot, runner.CommandRuntimeOutputPage, bool, error) {
	if s == nil || s.manager == nil || !job.Adapter.SameBackend(s.adapter) {
		return runner.CommandRuntimeJobSnapshot{}, runner.CommandRuntimeOutputPage{}, false, nil
	}
	return s.manager.ReadCommandRuntimeActivityTail(ctx, job, maxBytes)
}

func (m *CommandRuntimeMultiplexer) ReadCommandRuntimeActivityTail(ctx context.Context,
	job runner.CommandRuntimeJob, maxBytes int,
) (runner.CommandRuntimeJobSnapshot, runner.CommandRuntimeOutputPage, bool, error) {
	if m == nil {
		return runner.CommandRuntimeJobSnapshot{}, runner.CommandRuntimeOutputPage{}, false,
			apperror.New(apperror.CodeFailedPrecondition,
				"Command Runtime activity sources are unavailable")
	}
	for _, adapter := range m.adapters {
		if job.Adapter.SameBackend(adapter.adapter) {
			return adapter.ReadCommandRuntimeActivityTail(ctx, job, maxBytes)
		}
	}
	return runner.CommandRuntimeJobSnapshot{}, runner.CommandRuntimeOutputPage{}, false, nil
}

func loadThreadActivityLiveCommands(ctx context.Context,
	source ThreadActivityCommandRuntimeSource, jobs []runner.CommandRuntimeJob,
) (map[string]threadActivityLiveCommand, error) {
	if len(jobs) == 0 {
		return nil, nil
	}
	result := make(map[string]threadActivityLiveCommand, len(jobs))
	for _, job := range jobs {
		// Later write_stdin plaintext is intentionally not durable. Until output
		// is redacted at ingestion against that plaintext, interactive Jobs are
		// not eligible for a public tail because the process may echo it.
		if job.StdinPolicy != runner.CommandRuntimeStdinClosed || job.StdinWriteCount > 0 {
			continue
		}
		var snapshot runner.CommandRuntimeJobSnapshot
		var page runner.CommandRuntimeOutputPage
		found := false
		if source != nil {
			var err error
			snapshot, page, found, err = source.ReadCommandRuntimeActivityTail(ctx, job,
				runner.MaxCommandRuntimeOutputRead)
			if err != nil {
				return nil, apperror.Wrap(apperror.CodeFailedPrecondition,
					"live Command Runtime activity could not be projected", err)
			}
		}
		if !found && strings.TrimSpace(job.OutputFramesJSON) != "" &&
			strings.TrimSpace(job.OutputFramesJSON) != "[]" {
			var err error
			snapshot, page, found, err = runner.ProjectCommandRuntimeStoredActivityTail(job,
				runner.MaxCommandRuntimeOutputRead)
			if err != nil {
				return nil, apperror.Wrap(apperror.CodeFailedPrecondition,
					"durable Command Runtime activity tail could not be projected", err)
			}
		}
		if !found {
			continue
		}
		if snapshot.ID != job.ID || page.JobID != job.ID {
			return nil, apperror.New(apperror.CodeFailedPrecondition,
				"live Command Runtime activity has an inconsistent Job binding")
		}
		result[job.ID] = threadActivityLiveCommand{snapshot: snapshot, page: page}
	}
	return result, nil
}

func threadActivityLiveOutput(live threadActivityLiveCommand) (string, string) {
	var stdout, stderr string
	for _, frame := range live.page.Frames {
		switch frame.Stream {
		case runner.CommandRuntimeStdout:
			stdout += frame.Text
		case runner.CommandRuntimeStderr:
			stderr += frame.Text
		}
	}
	return stdout, stderr
}

func activityLiveCommandForJob(job *runner.CommandRuntimeJob,
	values map[string]threadActivityLiveCommand,
) *threadActivityLiveCommand {
	if job == nil || values == nil {
		return nil
	}
	value, found := values[job.ID]
	if !found {
		return nil
	}
	return &value
}

func activityArtifactReferencesForJob(job *runner.CommandRuntimeJob,
	values map[string][]ThreadActivityArtifactReference,
) []ThreadActivityArtifactReference {
	if job == nil || values == nil {
		return nil
	}
	return values[job.ID]
}

var _ ThreadActivityCommandRuntimeSource = (*runner.CommandRuntimeManager)(nil)
var _ ThreadActivityCommandRuntimeSource = (*CommandRuntimeService)(nil)
var _ ThreadActivityCommandRuntimeSource = (*CommandRuntimeMultiplexer)(nil)
