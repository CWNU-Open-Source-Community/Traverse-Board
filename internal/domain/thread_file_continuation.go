package domain

import (
	"cyberagent-workbench/internal/drydock"
	"time"
)

// ThreadDrydockBinding relates an execution epoch to the Thread's persistent
// working directory. The Drydock creator and all historical records stay intact.
// A binding is a directory identity, never an execution grant.
type ThreadDrydockBinding struct {
	RunID            string
	ThreadID         string
	DrydockID        string
	PredecessorRunID string
	CreatedAt        time.Time
}

// ThreadFileContinuation is an in-memory, read-checked publication input. No
// directory is copied and no unpublished filesystem operation needs recovery.
type ThreadFileContinuation struct {
	Binding              ThreadDrydockBinding
	ThreadVersion        int64
	PermissionSnapshotID string
	PermissionRevision   int64
	ModelPreference      *ThreadModelRoutePreference
	Workspace            drydock.Workspace
	Preset               StandardCodePresetOperation
}
