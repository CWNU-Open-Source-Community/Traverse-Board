package application

import (
	"context"
	"net/url"

	"cyberagent-workbench/internal/artifact"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/standardcodedelivery"
)

// StandardCodeDeliveryOutputSource is a read-time link, never sealed report
// evidence. Reading the saved body will recheck the same Thread boundary.
type StandardCodeDeliveryOutputSource struct {
	JobID, ArtifactID, Status, ThreadID, ActivityRef, Reason string
}

type standardCodeDeliveryOutputStore interface {
	GetRunArtifactDescriptor(context.Context, string) (artifact.Descriptor, error)
	FindCommandRuntimeOutputActivity(context.Context, string, string) (string, string, bool, error)
}

func (s *ThreadActivityDetailService) StandardCodeDeliveryOutputSources(ctx context.Context,
	report standardcodedelivery.Report,
) []StandardCodeDeliveryOutputSource {
	var sources []StandardCodeDeliveryOutputSource
	reader, supported := s.store.(standardCodeDeliveryOutputStore)
	for _, verification := range report.Verifications {
		threadID, activityRef, found := "", "", false
		if supported {
			var err error
			threadID, activityRef, found, err = reader.FindCommandRuntimeOutputActivity(ctx,
				report.Binding.RunID, verification.JobID)
			found = found && err == nil
		}
		for _, expected := range verification.Artifacts {
			source := StandardCodeDeliveryOutputSource{JobID: verification.JobID,
				ArtifactID: expected.ID, Status: "metadata_only", Reason: "activity_source_unavailable"}
			if found {
				source.Reason = "artifact_binding_mismatch"
				call, callErr := s.store.GetThreadSupervisorToolCall(ctx, threadID, activityRef)
				job, jobErr := s.store.GetThreadCommandRuntimeJobMetadata(ctx, threadID, verification.JobID)
				descriptor, descriptorErr := reader.GetRunArtifactDescriptor(ctx, expected.ID)
				if callErr == nil && jobErr == nil && descriptorErr == nil &&
					call.RunID == report.Binding.RunID && job.RunID == report.Binding.RunID &&
					job.SessionID == report.Binding.SessionID && job.MissionID == report.Binding.MissionID &&
					job.WorkspaceID == report.Binding.SourceWorkspaceID &&
					descriptor.Validate() == nil && descriptor.RunID == job.RunID &&
					descriptor.SessionID == job.SessionID && descriptor.WorkspaceID == job.WorkspaceID &&
					descriptor.SourceID == job.ID &&
					descriptor.ID == expected.ID && string(descriptor.Stream) == expected.Stream &&
					descriptor.SHA256 == expected.SHA256 && descriptor.SizeBytes == expected.SizeBytes &&
					descriptor.Redacted == expected.Redacted && expected.URL == "/api/v1/artifacts/"+url.PathEscape(descriptor.ID) {
					source.Reason = "output_not_public"
					if job.State.Terminal() && job.StdinPolicy == runner.CommandRuntimeStdinClosed &&
						job.StdinWriteCount == 0 && job.Credentials == runner.CommandRuntimeCredentialsNone {
						if _, err := validateThreadActivityArtifactBinding(call, descriptor, job); err == nil {
							source.Status, source.Reason = "available", ""
							source.ThreadID, source.ActivityRef = threadID, activityRef
						}
					}
				}
			}
			sources = append(sources, source)
		}
	}
	return sources
}
