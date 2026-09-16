package domain

import "errors"

// StandardCodeContinuation names historical observations, never an inherited
// execution grant or verification result. Mutation references are normalized to
// the original observed file checkpoint, even after several execution epochs.
type StandardCodeContinuation struct {
	PredecessorRunID        string `json:"predecessor_run_id"`
	SnapshotVersion         int64  `json:"snapshot_version"`
	DrydockID               string `json:"drydock_id"`
	WorkspaceID             string `json:"workspace_id"`
	ConsecutiveReadRounds   int    `json:"consecutive_read_rounds"`
	MutationEpoch           int    `json:"mutation_epoch"`
	MutationRunID           string `json:"mutation_run_id,omitempty"`
	MutationSnapshotVersion int64  `json:"mutation_snapshot_version,omitempty"`
	MutationCheckpointID    string `json:"mutation_checkpoint_id,omitempty"`
}

func (c StandardCodeContinuation) Validate() error {
	if !ValidAgentID(c.PredecessorRunID) || !ValidAgentID(c.DrydockID) ||
		!ValidAgentID(c.WorkspaceID) || c.SnapshotVersion < 0 || c.ConsecutiveReadRounds < 0 || c.MutationEpoch < 0 {
		return errors.New("Standard Code continuation observation is invalid")
	}
	if c.MutationEpoch > 0 {
		if !ValidAgentID(c.MutationRunID) || c.MutationSnapshotVersion <= 0 || !ValidAgentID(c.MutationCheckpointID) {
			return errors.New("Standard Code continuation mutation source is incomplete")
		}
	} else if c.MutationRunID != "" || c.MutationSnapshotVersion != 0 || c.MutationCheckpointID != "" {
		return errors.New("Standard Code continuation has an unexpected mutation source")
	}
	return nil
}
